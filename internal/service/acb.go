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
	adj := ComputeSuperficialAdjustments(securityID, txs, allTxs)
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

// ComputeSuperficialAdjustments returns a map of transactionID → ACB carry-forward amount.
// For each superficial loss sell in nonRegTxs, the denied loss is added to the ACB of
// the nearest replacement buy in nonRegTxs within 30 days after the sell.
//
// If the replacement buy is only in a registered account (present in allTxs but absent
// from nonRegTxs), the denied loss cannot be carried forward — the loss is permanently denied.
//
// Both slices must be sorted by trade_date ascending. Returns nil when there are no adjustments.
//
// Note: this handles the common case where the replacement buy occurs after the sell.
// Pre-sell replacement buys (within the 30-day prior window) are detected and flagged as
// superficial but the carry-forward is not applied retroactively in this implementation.
func ComputeSuperficialAdjustments(securityID int64, nonRegTxs, allTxs []*transaction.Transaction) map[int64]decimal.Decimal {
	if len(allTxs) == 0 {
		return nil
	}

	// Pass 1: compute ACBs without adjustments to determine gain/loss per sell.
	_, history := CalculateACBWithHistory(securityID, nonRegTxs, nil)

	var adj map[int64]decimal.Decimal

	for _, row := range history {
		if row.Tx.Type != transaction.TypeSell && row.Tx.Type != transaction.TypeTransferOut {
			continue
		}
		if row.PreTxShares.IsZero() {
			continue // NeedsReview row — ACB unknown, skip
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

		deniedLoss := gainLoss.Abs()
		windowEnd := row.Tx.TradeDate.AddDate(0, 0, 30)

		// Find the nearest replacement buy in non-registered txs within 30 days after the sell.
		for _, tx := range nonRegTxs {
			if !tx.TradeDate.After(row.Tx.TradeDate) {
				continue
			}
			if tx.TradeDate.After(windowEnd) {
				break
			}
			if tx.Type == transaction.TypeBuy || tx.Type == transaction.TypeTransferIn || tx.Type == transaction.TypeJournal {
				if adj == nil {
					adj = make(map[int64]decimal.Decimal)
				}
				adj[tx.ID] = adj[tx.ID].Add(deniedLoss)
				break
			}
		}
		// No non-registered replacement buy found → loss permanently denied, no carry-forward.
	}

	return adj
}
