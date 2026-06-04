package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type GainsLine struct {
	Security          *security.Security
	TradeDate         time.Time
	Shares            decimal.Decimal
	ProceedsCAD       decimal.Decimal
	ACBAtSell         decimal.Decimal
	GainLoss          decimal.Decimal
	IsSuperficialLoss bool
	// NeedsReview is set when no shares were held at the time of this sell (PreTxShares==0),
	// indicating missing buy/transfer history (e.g. Norbert's Gambit FXT journal not yet
	// imported, or a buy recorded after this sell in the transaction log). These lines are
	// excluded from totals — the user must supply the correct ACB before they're valid.
	NeedsReview bool
}

type GainsReport struct {
	Year                 int
	Lines                []GainsLine
	NeedsReviewLines     []GainsLine
	TotalGains           decimal.Decimal
	TotalLosses          decimal.Decimal
	NetGain              decimal.Decimal
	SuperficialLossTotal decimal.Decimal
}

type GainsService struct {
	txStore  transaction.Store
	secStore security.Store
}

func NewGainsService(txStore transaction.Store, secStore security.Store) *GainsService {
	return &GainsService{txStore: txStore, secStore: secStore}
}

func (s *GainsService) Calculate(ctx context.Context, userID int64, year int) (*GainsReport, error) {
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)

	sells, err := s.txStore.ListNonRegisteredDisposalsByUser(ctx, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("gains calculate: %w", err)
	}

	// collect distinct security IDs in first-seen order to keep report stable
	seen := make(map[int64]struct{})
	var secIDs []int64
	for _, tx := range sells {
		if _, ok := seen[tx.SecurityID]; !ok {
			seen[tx.SecurityID] = struct{}{}
			secIDs = append(secIDs, tx.SecurityID)
		}
	}

	report := &GainsReport{Year: year}

	for _, secID := range secIDs {
		sec, err := s.secStore.GetByID(ctx, secID)
		if err != nil {
			return nil, fmt.Errorf("gains calculate: get security %d: %w", secID, err)
		}

		txs, err := s.txStore.ListBySecurityNonRegistered(ctx, secID, userID)
		if err != nil {
			return nil, fmt.Errorf("gains calculate: list txs for security %d: %w", secID, err)
		}

		_, history := CalculateACBWithHistory(secID, txs)

		for _, row := range history {
			isDisposal := row.Tx.Type == transaction.TypeSell || row.Tx.Type == transaction.TypeTransferOut
			if !isDisposal || row.Tx.TradeDate.Year() != year {
				continue
			}

			proceeds := row.Tx.Quantity.Mul(row.Tx.PriceCAD).Sub(row.Tx.CommissionCAD)
			acbAtSell := row.PreTxACBPerShare.Mul(row.Tx.Quantity)
			gainLoss := proceeds.Sub(acbAtSell)

			line := GainsLine{
				Security:    sec,
				TradeDate:   row.Tx.TradeDate,
				Shares:      row.Tx.Quantity,
				ProceedsCAD: proceeds,
				ACBAtSell:   acbAtSell,
				GainLoss:    gainLoss,
				NeedsReview: row.PreTxShares.IsZero(),
			}

			if line.NeedsReview {
				report.NeedsReviewLines = append(report.NeedsReviewLines, line)
				continue
			}

			if gainLoss.IsNegative() {
				superficial, err := s.isSuperficialLoss(ctx, secID, userID, row.Tx.TradeDate)
				if err != nil {
					return nil, fmt.Errorf("gains calculate: superficial loss check: %w", err)
				}
				line.IsSuperficialLoss = superficial
			}

			if gainLoss.IsPositive() {
				report.TotalGains = report.TotalGains.Add(gainLoss)
			} else if gainLoss.IsNegative() {
				report.TotalLosses = report.TotalLosses.Add(gainLoss.Abs())
			}
			if line.IsSuperficialLoss {
				report.SuperficialLossTotal = report.SuperficialLossTotal.Add(gainLoss.Abs())
			}

			report.Lines = append(report.Lines, line)
		}
	}

	report.NetGain = report.TotalGains.Sub(report.TotalLosses)
	return report, nil
}

// GainsDetailRow wraps HistoryRow with pre-computed display fields for the
// disposition detail page.
type GainsDetailRow struct {
	HistoryRow
	IsDisposal bool
	GainLoss   decimal.Decimal // non-zero only for disposal rows
}

// HistoryForSecurity returns the ACB history for a security trimmed to all rows
// up to and including the last disposal in year. Returns nil history when no
// disposals exist for that year.
func (s *GainsService) HistoryForSecurity(ctx context.Context, securityID, userID int64, year int) (*security.Security, []GainsDetailRow, error) {
	sec, err := s.secStore.GetByID(ctx, securityID)
	if err != nil {
		return nil, nil, fmt.Errorf("history for security %d: %w", securityID, err)
	}

	txs, err := s.txStore.ListBySecurityNonRegistered(ctx, securityID, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("history for security %d: %w", securityID, err)
	}

	_, history := CalculateACBWithHistory(securityID, txs)

	lastIdx := -1
	for i, row := range history {
		isDisposal := row.Tx.Type == transaction.TypeSell || row.Tx.Type == transaction.TypeTransferOut
		if isDisposal && row.Tx.TradeDate.Year() == year {
			lastIdx = i
		}
	}
	if lastIdx == -1 {
		return sec, nil, nil
	}

	trimmed := history[:lastIdx+1]
	out := make([]GainsDetailRow, len(trimmed))
	for i, row := range trimmed {
		isDisposal := row.Tx.Type == transaction.TypeSell || row.Tx.Type == transaction.TypeTransferOut
		var gainLoss decimal.Decimal
		if isDisposal {
			proceeds := row.Tx.Quantity.Mul(row.Tx.PriceCAD).Sub(row.Tx.CommissionCAD)
			acbAtSell := row.PreTxACBPerShare.Mul(row.Tx.Quantity)
			gainLoss = proceeds.Sub(acbAtSell)
		}
		out[i] = GainsDetailRow{HistoryRow: row, IsDisposal: isDisposal, GainLoss: gainLoss}
	}
	return sec, out, nil
}

func (s *GainsService) isSuperficialLoss(ctx context.Context, securityID, userID int64, sellDate time.Time) (bool, error) {
	allTxs, err := s.txStore.ListBySecurityAllAccounts(ctx, securityID, userID)
	if err != nil {
		return false, err
	}
	windowStart := sellDate.AddDate(0, 0, -30)
	windowEnd := sellDate.AddDate(0, 0, 30)

	// CRA requires both: (1) an acquisition within the ±30-day window AND
	// (2) a positive position at the end of the 30-day period after the sale.
	hasWindowBuy := false
	for _, tx := range allTxs {
		if tx.Type != transaction.TypeBuy && tx.Type != transaction.TypeTransferIn && tx.Type != transaction.TypeJournal {
			continue
		}
		if !tx.TradeDate.Before(windowStart) && !tx.TradeDate.After(windowEnd) {
			hasWindowBuy = true
			break
		}
	}
	if !hasWindowBuy {
		return false, nil
	}

	// Compute net position across all accounts at end of window.
	var shares decimal.Decimal
	for _, tx := range allTxs {
		if tx.TradeDate.After(windowEnd) {
			break
		}
		switch tx.Type {
		case transaction.TypeBuy, transaction.TypeTransferIn, transaction.TypeJournal:
			shares = shares.Add(tx.Quantity)
		case transaction.TypeSell, transaction.TypeTransferOut:
			shares = shares.Sub(tx.Quantity)
		case transaction.TypeDividend, transaction.TypeROCAdjustment, transaction.TypeFXConversion:
			// no share count impact
		}
	}
	return shares.IsPositive(), nil
}
