package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/distribution"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type ROCPreviewRow struct {
	Security       *security.Security
	TaxYear        int
	ROCPerUnit     decimal.Decimal
	UnitsHeld      decimal.Decimal
	TotalROC       decimal.Decimal
	AlreadyApplied bool
	accountID      int64 // account used for auto-generated roc_adjustment transaction
}

type ROCService struct {
	txStore   transaction.Store
	distStore distribution.Store
	secStore  security.Store
}

func NewROCService(txStore transaction.Store, distStore distribution.Store, secStore security.Store) *ROCService {
	return &ROCService{txStore: txStore, distStore: distStore, secStore: secStore}
}

func (s *ROCService) PreviewROC(ctx context.Context, userID int64, taxYear int) ([]ROCPreviewRow, error) {
	yearEnd := time.Date(taxYear, 12, 31, 0, 0, 0, 0, time.UTC)

	dists, err := s.distStore.ListByTaxYear(ctx, taxYear)
	if err != nil {
		return nil, fmt.Errorf("roc preview: %w", err)
	}

	var rows []ROCPreviewRow
	for _, d := range dists {
		if !d.ROCPerUnit.IsPositive() {
			continue
		}

		sec, err := s.secStore.GetByID(ctx, d.SecurityID)
		if err != nil {
			return nil, fmt.Errorf("roc preview: get security %d: %w", d.SecurityID, err)
		}

		txs, err := s.txStore.ListBySecurityNonRegistered(ctx, d.SecurityID, userID)
		if err != nil {
			return nil, fmt.Errorf("roc preview: list txs for security %d: %w", d.SecurityID, err)
		}

		var upToYearEnd []*transaction.Transaction
		alreadyApplied := false
		var lastAccountID int64
		for _, tx := range txs {
			if !tx.TradeDate.After(yearEnd) {
				upToYearEnd = append(upToYearEnd, tx)
				if tx.Type != transaction.TypeROCAdjustment {
					lastAccountID = tx.AccountID
				}
			}
			if tx.Type == transaction.TypeROCAdjustment && tx.TradeDate.Year() == taxYear {
				alreadyApplied = true
			}
		}

		_, history := CalculateACBWithHistory(d.SecurityID, upToYearEnd)
		var sharesAtYearEnd decimal.Decimal
		if len(history) > 0 {
			sharesAtYearEnd = history[len(history)-1].RunningShares
		}

		rows = append(rows, ROCPreviewRow{
			Security:       sec,
			TaxYear:        taxYear,
			ROCPerUnit:     d.ROCPerUnit,
			UnitsHeld:      sharesAtYearEnd,
			TotalROC:       d.ROCPerUnit.Mul(sharesAtYearEnd),
			AlreadyApplied: alreadyApplied,
			accountID:      lastAccountID,
		})
	}
	return rows, nil
}

func (s *ROCService) ApplyROC(ctx context.Context, userID int64, taxYear int) error {
	rows, err := s.PreviewROC(ctx, userID, taxYear)
	if err != nil {
		return err
	}
	yearEnd := time.Date(taxYear, 12, 31, 0, 0, 0, 0, time.UTC)
	for _, row := range rows {
		if row.AlreadyApplied || !row.UnitsHeld.IsPositive() || row.accountID == 0 {
			continue
		}
		// T3 distribution data is always reported in CAD. Non-CAD securities
		// would require an FX rate to derive PriceCAD correctly; skip them
		// rather than silently treating a foreign-currency amount as CAD.
		if row.Security.Currency != "CAD" {
			continue
		}
		tx := &transaction.Transaction{
			AccountID:    row.accountID,
			SecurityID:   row.Security.ID,
			Type:         transaction.TypeROCAdjustment,
			TradeDate:    yearEnd,
			SettledDate:  yearEnd,
			Quantity:     row.UnitsHeld,
			PriceNative:  row.ROCPerUnit,
			PriceCAD:     row.ROCPerUnit,
			Source:       transaction.SourceManual,
			Notes:        fmt.Sprintf("ROC adjustment %d (auto-generated from T3 distribution data)", taxYear),
		}
		if err := s.txStore.Create(ctx, tx); err != nil {
			return fmt.Errorf("roc apply: create transaction for security %d: %w", row.Security.ID, err)
		}
	}
	return nil
}
