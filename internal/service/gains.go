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

func isDisposalType(t transaction.Type) bool {
	return t == transaction.TypeSell || t == transaction.TypeTransferOut
}

// proceedsCAD computes net disposal proceeds: qty × priceCAD − commissionCAD.
// Single source of truth for this formula so Calculate and HistoryForSecurity stay in sync.
func proceedsCAD(tx *transaction.Transaction) decimal.Decimal {
	return tx.Quantity.Mul(tx.PriceCAD).Sub(tx.CommissionCAD)
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
		allTxs, err := s.txStore.ListBySecurityAllAccounts(ctx, secID, userID)
		if err != nil {
			return nil, fmt.Errorf("gains calculate: list all txs for security %d: %w", secID, err)
		}

		adj := ComputeSuperficialAdjustments(secID, txs, allTxs)
		_, history := CalculateACBWithHistory(secID, txs, adj)

		for _, row := range history {
			if !isDisposalType(row.Tx.Type) || row.Tx.TradeDate.Year() != year {
				continue
			}

			proceeds := proceedsCAD(row.Tx)
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
				line.IsSuperficialLoss = checkSuperficialLoss(allTxs, row.Tx.TradeDate)
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
	IsDisposal  bool
	NeedsReview bool            // disposal with no prior share position — ACB unknown, GainLoss suppressed
	GainLoss    decimal.Decimal // zero for non-disposal rows and NeedsReview disposals
}

// HistoryForSecurity returns the ACB history for a security trimmed to all rows
// up to and including the last disposal in year. Returns nil history when no
// disposals exist for that year.
func (s *GainsService) HistoryForSecurity(ctx context.Context, securityID, userID int64, year int) (*security.Security, []GainsDetailRow, error) {
	sec, err := s.secStore.GetByID(ctx, securityID)
	if err != nil {
		return nil, nil, fmt.Errorf("history for security %d: get security: %w", securityID, err)
	}

	txs, err := s.txStore.ListBySecurityNonRegistered(ctx, securityID, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("history for security %d: list transactions: %w", securityID, err)
	}

	// Fast path: skip O(n) ACB computation when no disposals exist in the target year.
	hasDisposal := false
	for _, tx := range txs {
		if isDisposalType(tx.Type) && tx.TradeDate.Year() == year {
			hasDisposal = true
			break
		}
	}
	if !hasDisposal {
		return sec, nil, nil
	}

	allTxs, err := s.txStore.ListBySecurityAllAccounts(ctx, securityID, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("history for security %d: list all transactions: %w", securityID, err)
	}
	adj := ComputeSuperficialAdjustments(securityID, txs, allTxs)
	_, history := CalculateACBWithHistory(securityID, txs, adj)

	lastIdx := -1
	for i, row := range history {
		if isDisposalType(row.Tx.Type) && row.Tx.TradeDate.Year() == year {
			lastIdx = i
		}
	}
	if lastIdx == -1 {
		return sec, nil, nil
	}

	trimmed := history[:lastIdx+1]
	out := make([]GainsDetailRow, len(trimmed))
	for i, row := range trimmed {
		isDisposal := isDisposalType(row.Tx.Type)
		needsReview := isDisposal && row.PreTxShares.IsZero()
		var gainLoss decimal.Decimal
		if isDisposal && !needsReview {
			gainLoss = proceedsCAD(row.Tx).Sub(row.PreTxACBPerShare.Mul(row.Tx.Quantity))
		}
		out[i] = GainsDetailRow{HistoryRow: row, IsDisposal: isDisposal, NeedsReview: needsReview, GainLoss: gainLoss}
	}
	return sec, out, nil
}

