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
	// SuperficialLossAdj is the denied-loss carry-forward applied on this row.
	// Buy rows: added to buy cost (post-sell replacement, or deferred pre-sell carry-forward
	// when a prior sell-all emptied the pool and couldn't apply the adjustment in-place).
	// Sell rows: added to remaining pool (pre-sell replacement); zero when pool went to zero
	// and the carry-forward was deferred to the next buy instead.
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
	adj, _, _ := ComputeSuperficialAdjustments(securityID, txs, allTxs)
	r, _ := CalculateACBWithHistory(securityID, txs, adj)
	return r, nil
}

// isAcquisitionType reports whether t acquires shares (buy, transfer-in, or journal).
// Single source of truth — used by ACB computation, replacement search, and window checks.
func isAcquisitionType(t transaction.Type) bool {
	return t == transaction.TypeBuy || t == transaction.TypeTransferIn || t == transaction.TypeJournal
}

// superficialLossWindow returns the ±30-day window [start, end] around sellDate per CRA rules.
func superficialLossWindow(sellDate time.Time) (start, end time.Time) {
	return sellDate.AddDate(0, 0, -30), sellDate.AddDate(0, 0, 30)
}

// CalculateACBWithHistory computes ACB and per-row running totals from an
// ordered (trade_date asc) transaction slice. adj maps transaction ID to an
// additional ACB adjustment (superficial loss carry-forward); nil means no adjustments.
func CalculateACBWithHistory(securityID int64, txs []*transaction.Transaction, adj map[int64]decimal.Decimal) (*ACBResult, []HistoryRow) {
	r := &ACBResult{SecurityID: securityID}
	rows := make([]HistoryRow, 0, len(txs))
	var pendingAdj decimal.Decimal // pre-sell carry-forward deferred when a sell emptied the pool
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

		switch {
		case isAcquisitionType(tx.Type):
			if pendingAdj.IsPositive() {
				superficialLossAdj = superficialLossAdj.Add(pendingAdj)
				pendingAdj = decimal.Zero
			}
			cost := tx.Quantity.Mul(tx.PriceCAD).Add(tx.CommissionCAD).Add(superficialLossAdj)
			r.TotalACB = r.TotalACB.Add(cost)
			r.Shares = r.Shares.Add(tx.Quantity)
		case isDisposalType(tx.Type):
			if r.Shares.IsPositive() {
				acbPerShare := r.TotalACB.Div(r.Shares)
				r.TotalACB = r.TotalACB.Sub(acbPerShare.Mul(tx.Quantity))
			}
			r.Shares = r.Shares.Sub(tx.Quantity)
			if !r.Shares.IsPositive() {
				r.Shares = decimal.Zero
				r.TotalACB = decimal.Zero
				if superficialLossAdj.IsPositive() {
					// Pre-sell carry-forward can't apply to an empty pool — defer to next buy.
					pendingAdj = pendingAdj.Add(superficialLossAdj)
					superficialLossAdj = decimal.Zero
				}
			} else if superficialLossAdj.IsPositive() {
				// Pre-sell carry-forward: add denied loss to pool of remaining shares.
				r.TotalACB = r.TotalACB.Add(superficialLossAdj)
			}
		case tx.Type == transaction.TypeROCAdjustment:
			r.TotalACB = r.TotalACB.Sub(tx.Quantity.Mul(tx.PriceCAD))
			if r.TotalACB.IsNegative() {
				r.TotalACB = decimal.Zero
			}
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
	windowStart, windowEnd := superficialLossWindow(sellDate)

	hasWindowBuy := false
	for _, tx := range allTxs {
		if tx.TradeDate.After(windowEnd) {
			break
		}
		if !isAcquisitionType(tx.Type) {
			continue
		}
		if !tx.TradeDate.Before(windowStart) {
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
		switch {
		case isAcquisitionType(tx.Type):
			shares = shares.Add(tx.Quantity)
		case isDisposalType(tx.Type):
			shares = shares.Sub(tx.Quantity)
		}
	}
	return shares.IsPositive()
}

// computeAdjDenied is the inner single-pass computation used by ComputeSuperficialAdjustments.
// priorAdj applies carry-forwards from a previous pass so ACBs reflect earlier adjustments.
// withCarryFwd maps sell IDs to present for sells that produced a non-registered carry-forward.
func computeAdjDenied(securityID int64, nonRegTxs, allTxs []*transaction.Transaction, priorAdj map[int64]decimal.Decimal) (adj, denied map[int64]decimal.Decimal, withCarryFwd map[int64]struct{}) {
	_, history := CalculateACBWithHistory(securityID, nonRegTxs, priorAdj)

	allocated := make(map[int64]decimal.Decimal) // replacement capacity already used across sells

	for histIdx, row := range history {
		if !isDisposalType(row.Tx.Type) {
			continue
		}
		if row.PreTxShares.IsZero() {
			continue
		}

		proceeds := proceedsCAD(row.Tx)
		acbAtSell := row.PreTxACBPerShare.Mul(row.Tx.Quantity)
		gainLoss := proceeds.Sub(acbAtSell)

		if !gainLoss.IsNegative() {
			continue
		}
		if !checkSuperficialLoss(allTxs, row.Tx.TradeDate) {
			continue
		}

		windowStart, windowEnd := superficialLossWindow(row.Tx.TradeDate)

		// Post-sell search: same-day acquisitions count as replacements per CRA.
		// Start at histIdx (the sell itself) — nonRegTxs[histIdx] == row.Tx, which is not an
		// acquisition type, so it's skipped without an extra date check.
		var replacementTx *transaction.Transaction
		preSell := false
		for i := histIdx; i < len(nonRegTxs); i++ {
			tx := nonRegTxs[i]
			if tx.TradeDate.After(windowEnd) {
				break
			}
			if isAcquisitionType(tx.Type) {
				replacementTx = tx
				break
			}
		}

		// Pre-sell fallback: nearest acquisition strictly before the sell within the 30-day window.
		// Start from the sell's slice position and scan backward — O(window-size), not O(n).
		if replacementTx == nil {
			for i := histIdx - 1; i >= 0; i-- {
				tx := nonRegTxs[i]
				if tx.TradeDate.Before(windowStart) {
					break
				}
				if isAcquisitionType(tx.Type) {
					replacementTx = tx
					preSell = true
					break
				}
			}
		}

		soldQty := row.Tx.Quantity
		var proportionalLoss decimal.Decimal

		if replacementTx != nil {
			// G4: cap against remaining unallocated capacity on the replacement buy.
			// When multiple sells share the same replacement, the total allocated qty across
			// them cannot exceed the replacement's total quantity (CRA proportionality rule).
			used := allocated[replacementTx.ID]
			available := replacementTx.Quantity.Sub(used)
			if !available.IsPositive() {
				// Replacement capacity exhausted — this sell has no effective replacement.
				continue
			}
			effectiveQty := available
			if soldQty.LessThan(effectiveQty) {
				effectiveQty = soldQty
			}
			proportionalLoss = gainLoss.Abs().Mul(effectiveQty).Div(soldQty)
			allocated[replacementTx.ID] = used.Add(effectiveQty)
		} else {
			proportionalLoss = gainLoss.Abs() // registered-only replacement: full denial, no carry-forward
		}

		if denied == nil {
			denied = make(map[int64]decimal.Decimal)
		}
		denied[row.Tx.ID] = denied[row.Tx.ID].Add(proportionalLoss)

		if replacementTx != nil {
			if adj == nil {
				adj = make(map[int64]decimal.Decimal)
			}
			if withCarryFwd == nil {
				withCarryFwd = make(map[int64]struct{})
			}
			withCarryFwd[row.Tx.ID] = struct{}{}
			if preSell {
				// G3: carry-forward keyed to the sell ID rather than the pre-sell buy ID.
				// CalculateACBWithHistory applies it to the pool after the sell completes,
				// so prior disposals processed before this sell are not retroactively changed.
				adj[row.Tx.ID] = adj[row.Tx.ID].Add(proportionalLoss)
			} else {
				adj[replacementTx.ID] = adj[replacementTx.ID].Add(proportionalLoss)
			}
		}
	}

	return adj, denied, withCarryFwd
}

// ComputeSuperficialAdjustments returns three maps:
//   - adj:          transactionID → carry-forward amount applied by CalculateACBWithHistory.
//   - denied:       transactionID → denied loss amount for superficial-loss disposals.
//   - withCarryFwd: sell IDs present when a non-registered carry-forward was created.
//                   Absent (nil or key missing) when the replacement was registered-only.
//
// Denial is proportional: effectiveReplQty/soldQty × totalLoss, where effectiveReplQty is
// the remaining unallocated capacity of the replacement buy (capped so multiple sells sharing
// the same replacement don't over-allocate it). Post-sell replacement (same-day included) is
// preferred; falls back to the nearest pre-sell acquisition within 30 days. Registered-only
// replacement yields full denial with no carry-forward. Both TypeSell and TypeTransferOut
// trigger the rule (both are CRA deemed dispositions).
//
// Three internal passes ensure adj and denied are mutually consistent with the ACBs callers
// see when they run CalculateACBWithHistory(txs, adj): pass 1 builds adj from raw ACBs;
// pass 2 refines adj using pass-1-adjusted ACBs (handles cascading pre-sell carry-forwards);
// pass 3 recomputes denied using pass-2 adj so denied matches the ACBs callers will see.
//
// Both slices must be sorted by trade_date ascending. Returns nil, nil, nil when there are no
// superficial losses.
func ComputeSuperficialAdjustments(securityID int64, nonRegTxs, allTxs []*transaction.Transaction) (adj, denied map[int64]decimal.Decimal, withCarryFwd map[int64]struct{}) {
	if len(allTxs) == 0 {
		return nil, nil, nil
	}
	// Pass 1: build adj from raw ACBs.
	adj, _, _ = computeAdjDenied(securityID, nonRegTxs, allTxs, nil)
	// Pass 2: refine adj using pass-1-adjusted ACBs so cascading pre-sell carry-forwards
	// are reflected in subsequent sells before their denial amounts are calculated.
	adj, _, _ = computeAdjDenied(securityID, nonRegTxs, allTxs, adj)
	// Pass 3: recompute denied with pass-2 adj applied — denied now matches the ACBs
	// callers will see when they call CalculateACBWithHistory(txs, adj).
	_, denied, withCarryFwd = computeAdjDenied(securityID, nonRegTxs, allTxs, adj)
	return adj, denied, withCarryFwd
}
