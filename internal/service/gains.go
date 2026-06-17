package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// GainsLine is a single taxable disposition line in a capital gains report.
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

// GainsReport aggregates all taxable dispositions for a calendar year.
type GainsReport struct {
	Year                 int
	Lines                []GainsLine
	NeedsReviewLines     []GainsLine
	TotalGains           decimal.Decimal
	TotalLosses          decimal.Decimal
	NetGain              decimal.Decimal
	SuperficialLossTotal decimal.Decimal
}

// GainsService computes CRA-compliant capital gains reports for a given tax year.
type GainsService struct {
	txStore  transaction.Store
	secStore security.Store
}

// NewGainsService constructs a GainsService backed by the given stores.
func NewGainsService(txStore transaction.Store, secStore security.Store) *GainsService {
	return &GainsService{txStore: txStore, secStore: secStore}
}

func isDisposalType(t transaction.Type) bool {
	// TypeFXConversion is the Norbert's Gambit give-leg: reduces share count in ACB
	// but is NOT a taxable disposal (gains SQL filters on 'sell','transfer_out' only).
	return t == transaction.TypeSell || t == transaction.TypeTransferOut || t == transaction.TypeFXConversion
}

// isTaxableDisposal reports whether a transaction type produces a reportable capital gain/loss.
// TypeFXConversion (NG give-leg) is excluded: it reduces ACB share count but is not a taxable event.
func isTaxableDisposal(t transaction.Type) bool {
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

	secIDs, err := s.txStore.ListDistinctNonRegisteredSecurityIDsByUser(ctx, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("gains calculate: %w", err)
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

		adj, denied, _ := ComputeSuperficialAdjustments(secID, txs, allTxs)
		_, history := CalculateACBWithHistory(secID, txs, adj)

		for _, row := range history {
			if !isTaxableDisposal(row.Tx.Type) || row.Tx.TradeDate.Year() != year {
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

			var deniedAmt decimal.Decimal
			if gainLoss.IsNegative() {
				if da, ok := denied[row.Tx.ID]; ok {
					line.IsSuperficialLoss = true
					deniedAmt = da
				}
			}

			if gainLoss.IsPositive() {
				report.TotalGains = report.TotalGains.Add(gainLoss)
			} else if gainLoss.IsNegative() {
				if claimable := gainLoss.Abs().Sub(deniedAmt); claimable.IsPositive() {
					report.TotalLosses = report.TotalLosses.Add(claimable)
				}
			}
			if line.IsSuperficialLoss {
				report.SuperficialLossTotal = report.SuperficialLossTotal.Add(deniedAmt)
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
	IsDisposal        bool
	NeedsReview       bool            // disposal with no prior share position — ACB unknown, GainLoss suppressed
	GainLoss          decimal.Decimal // zero for non-disposal rows and NeedsReview disposals
	IsSuperficialLoss bool            // loss is (fully or partially) denied under CRA superficial loss rules
	HasCarryForward   bool            // true when denied loss carries forward to a non-reg replacement; false for registered-only replacement
	DeniedAmt         decimal.Decimal // portion of the loss that is denied
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
		if isTaxableDisposal(tx.Type) && tx.TradeDate.Year() == year {
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
	adj, denied, withCarryFwd := ComputeSuperficialAdjustments(securityID, txs, allTxs)
	_, history := CalculateACBWithHistory(securityID, txs, adj)

	lastIdx := -1
	for i, row := range history {
		if isTaxableDisposal(row.Tx.Type) && row.Tx.TradeDate.Year() == year {
			lastIdx = i
		}
	}

	trimmed := history[:lastIdx+1]
	out := make([]GainsDetailRow, len(trimmed))
	for i, row := range trimmed {
		isDisposal := isTaxableDisposal(row.Tx.Type)
		needsReview := isDisposal && row.PreTxShares.IsZero()
		var gainLoss decimal.Decimal
		var isSuperficialLoss bool
		var hasCarryForward bool
		var deniedAmt decimal.Decimal
		if isDisposal && !needsReview {
			gainLoss = proceedsCAD(row.Tx).Sub(row.PreTxACBPerShare.Mul(row.Tx.Quantity))
			if da, ok := denied[row.Tx.ID]; ok {
				isSuperficialLoss = true
				deniedAmt = da
				_, hasCarryForward = withCarryFwd[row.Tx.ID]
			}
		}
		out[i] = GainsDetailRow{
			HistoryRow:        row,
			IsDisposal:        isDisposal,
			NeedsReview:       needsReview,
			GainLoss:          gainLoss,
			IsSuperficialLoss: isSuperficialLoss,
			HasCarryForward:   hasCarryForward,
			DeniedAmt:         deniedAmt,
		}
	}
	return sec, out, nil
}

