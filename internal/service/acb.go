package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type ACBResult struct {
	SecurityID  int64
	Shares      decimal.Decimal
	TotalACB    decimal.Decimal
	ACBPerShare decimal.Decimal
}

type HistoryRow struct {
	Tx                 *transaction.Transaction
	PreTxShares        decimal.Decimal // shares held before this transaction was applied
	PreTxACBPerShare   decimal.Decimal // ACB/share before this transaction was applied
	RunningShares      decimal.Decimal
	RunningACB         decimal.Decimal
	RunningACBPerShare decimal.Decimal
	// SuperficialLossAdj is the denied-loss amount added to this row's ACB as a
	// superficial loss carry-forward. Non-zero only on buy/transfer-in rows that
	// received a carry-forward from a preceding superficial loss sell.
	SuperficialLossAdj decimal.Decimal
}

type ACBService struct {
	txStore transaction.Store
}

func NewACBService(txStore transaction.Store) *ACBService {
	return &ACBService{txStore: txStore}
}

func (s *ACBService) Calculate(ctx context.Context, securityID, userID int64) (*ACBResult, error) {
	txs, err := s.txStore.ListBySecurityNonRegistered(ctx, securityID, userID)
	if err != nil {
		return nil, fmt.Errorf("acb calculate: %w", err)
	}
	allTxs, err := s.txStore.ListBySecurityAllAccounts(ctx, securityID, userID)
	if err != nil {
		return nil, fmt.Errorf("acb calculate: all accounts: %w", err)
	}
	adj, _ := ComputeSuperficialAdjustments(securityID, txs, allTxs)
	r, _ := CalculateACBWithHistory(securityID, txs, adj)
	return r, nil
}

// CalculateACBWithHistory computes ACB and per-row running totals from an
// ordered (trade_date asc) transaction slice. adj maps transaction ID to an
// additional ACB adjustment (superficial loss carry-forward); nil means no adjustments.
func CalculateACBWithHistory(securityID int64, txs []*transaction.Transaction, adj map[int64]decimal.Decimal) (*ACBResult, []HistoryRow) {
	r := &ACBResult{SecurityID: securityID}
	rows := make([]HistoryRow, 0, len(txs))
	for _, tx := range txs {
		preTxShares := r.Shares
		var preTxACBPerShare decimal.Decimal
		if r.Shares.IsPositive() {
			preTxACBPerShare = r.TotalACB.Div(r.Shares)
		}

		var superficialLossAdj decimal.Decimal
		if adj != nil {
			superficialLossAdj = adj[tx.ID]
		}

		switch tx.Type {
		case transaction.TypeBuy, transaction.TypeTransferIn, transaction.TypeJournal:
			cost := tx.Quantity.Mul(tx.PriceCAD).Add(tx.CommissionCAD).Add(superficialLossAdj)
			r.TotalACB = r.TotalACB.Add(cost)
			r.Shares = r.Shares.Add(tx.Quantity)
		case transaction.TypeSell, transaction.TypeTransferOut:
			if r.Shares.IsPositive() {
				acbPerShare := r.TotalACB.Div(r.Shares)
				r.TotalACB = r.TotalACB.Sub(acbPerShare.Mul(tx.Quantity))
			}
			r.Shares = r.Shares.Sub(tx.Quantity)
			if !r.Shares.IsPositive() {
				r.Shares = decimal.Zero
				r.TotalACB = decimal.Zero
			}
		case transaction.TypeROCAdjustment:
			r.TotalACB = r.TotalACB.Sub(tx.Quantity.Mul(tx.PriceCAD))
			if r.TotalACB.IsNegative() {
				r.TotalACB = decimal.Zero
			}
		case transaction.TypeDividend, transaction.TypeFXConversion:
			// no ACB impact
		}

		var perShare decimal.Decimal
		if r.Shares.IsPositive() {
			perShare = r.TotalACB.Div(r.Shares)
		}
		rows = append(rows, HistoryRow{
			Tx:                 tx,
			PreTxShares:        preTxShares,
			PreTxACBPerShare:   preTxACBPerShare,
			RunningShares:      r.Shares,
			RunningACB:         r.TotalACB,
			RunningACBPerShare: perShare,
			SuperficialLossAdj: superficialLossAdj,
		})
	}
	if r.Shares.IsPositive() {
		r.ACBPerShare = r.TotalACB.Div(r.Shares)
	}
	return r, rows
}

// CalculateACB computes ACB from an ordered (trade_date asc) transaction slice.
// Exported so it can be tested without a store.
func CalculateACB(securityID int64, txs []*transaction.Transaction) *ACBResult {
	r, _ := CalculateACBWithHistory(securityID, txs, nil)
	return r
}

// checkSuperficialLoss returns true when a sell at sellDate is a superficial loss per CRA rules:
// there must be an acquisition within the ±30-day window AND a positive net position
// across all accounts at the end of that window.
// allTxs must be sorted by trade_date ascending and include transactions from all accounts.
func checkSuperficialLoss(allTxs []*transaction.Transaction, sellDate time.Time) bool {
	windowStart := sellDate.AddDate(0, 0, -30)
	windowEnd := sellDate.AddDate(0, 0, 30)

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
		return false
	}

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
	return shares.IsPositive()
}

// ComputeSuperficialAdjustments returns two maps:
//   - adj: transactionID → ACB carry-forward amount for replacement buys
//   - denied: transactionID → denied loss amount for superficial loss sells
//
// The denied amount is proportional: min(replacementQty, soldQty)/soldQty × totalLoss.
// Post-sell replacement (same-day included) is preferred; falls back to the nearest
// pre-sell buy within 30 days. When no non-reg replacement exists (registered-only),
// the full loss is denied with no carry-forward.
//
// Both slices must be sorted by trade_date ascending. Returns nil, nil when there are
// no superficial losses.
func ComputeSuperficialAdjustments(securityID int64, nonRegTxs, allTxs []*transaction.Transaction) (adj, denied map[int64]decimal.Decimal) {
	if len(allTxs) == 0 {
		return nil, nil
	}

	_, history := CalculateACBWithHistory(securityID, nonRegTxs, nil)

	for _, row := range history {
		if row.Tx.Type != transaction.TypeSell && row.Tx.Type != transaction.TypeTransferOut {
			continue
		}
		if row.PreTxShares.IsZero() {
			continue
		}

		proceeds := row.Tx.Quantity.Mul(row.Tx.PriceCAD).Sub(row.Tx.CommissionCAD)
		acbAtSell := row.PreTxACBPerShare.Mul(row.Tx.Quantity)
		gainLoss := proceeds.Sub(acbAtSell)

		if !gainLoss.IsNegative() {
			continue
		}
		if !checkSuperficialLoss(allTxs, row.Tx.TradeDate) {
			continue
		}

		windowStart := row.Tx.TradeDate.AddDate(0, 0, -30)
		windowEnd := row.Tx.TradeDate.AddDate(0, 0, 30)

		// Post-sell search: same-day counts (use Before not After so equal dates pass).
		var replacementTx *transaction.Transaction
		for _, tx := range nonRegTxs {
			if tx.TradeDate.Before(row.Tx.TradeDate) {
				continue
			}
			if tx.TradeDate.After(windowEnd) {
				break
			}
			if tx.Type == transaction.TypeBuy || tx.Type == transaction.TypeTransferIn || tx.Type == transaction.TypeJournal {
				replacementTx = tx
				break
			}
		}

		// Pre-sell fallback: nearest buy strictly before the sell within 30 days.
		if replacementTx == nil {
			for i := len(nonRegTxs) - 1; i >= 0; i-- {
				tx := nonRegTxs[i]
				if !tx.TradeDate.Before(row.Tx.TradeDate) {
					continue
				}
				if tx.TradeDate.Before(windowStart) {
					break
				}
				if tx.Type == transaction.TypeBuy || tx.Type == transaction.TypeTransferIn || tx.Type == transaction.TypeJournal {
					replacementTx = tx
					break
				}
			}
		}

		soldQty := row.Tx.Quantity
		var proportionalLoss decimal.Decimal
		if replacementTx != nil {
			replQty := replacementTx.Quantity
			if replQty.GreaterThan(soldQty) {
				replQty = soldQty
			}
			proportionalLoss = gainLoss.Abs().Mul(replQty).Div(soldQty)
		} else {
			proportionalLoss = gainLoss.Abs()
		}

		if denied == nil {
			denied = make(map[int64]decimal.Decimal)
		}
		denied[row.Tx.ID] = denied[row.Tx.ID].Add(proportionalLoss)

		if replacementTx != nil {
			if adj == nil {
				adj = make(map[int64]decimal.Decimal)
			}
			adj[replacementTx.ID] = adj[replacementTx.ID].Add(proportionalLoss)
		}
	}

	return adj, denied
}
