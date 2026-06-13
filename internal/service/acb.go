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
	// pendingAdj holds a pre-sell carry-forward that couldn't be applied because a sell emptied
	// the pool. It is applied to the next acquisition. If no acquisition follows in this dataset
	// (e.g. the buy is in a later import batch), pendingAdj is silently lost — a known limitation
	// at import boundaries. Returning it from this function and threading it through callers
	// would fix this but is out of scope for now.
	var pendingAdj decimal.Decimal
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
// TypeFXConversion (Norbert's Gambit give-leg) is treated as a disposal for net position purposes;
// reverse Norbert's acquisitions will be under-counted as a result, but that is the less harmful
// direction (may miss a superficial loss, not create a false one). A direction field on Transaction
// would eliminate the ambiguity.
// checkSuperficialLoss returns (isSuperf, netSharesAtWindowEnd).
// isSuperf is true when the sell qualifies as a superficial loss: there is an acquisition
// within the ±30-day window AND the net position at windowEnd is positive.
// netSharesAtWindowEnd is the running net share count at the end of the window across all
// accounts — used by the caller to cap the effective replacement quantity per CRA rules.
func checkSuperficialLoss(allTxs []*transaction.Transaction, sellDate time.Time) (bool, decimal.Decimal) {
	windowStart, windowEnd := superficialLossWindow(sellDate)

	var shares decimal.Decimal
	hasWindowBuy := false
	for _, tx := range allTxs {
		if tx.TradeDate.After(windowEnd) {
			break
		}
		switch {
		case isAcquisitionType(tx.Type):
			shares = shares.Add(tx.Quantity)
			if !tx.TradeDate.Before(windowStart) {
				hasWindowBuy = true
			}
		case isDisposalType(tx.Type) || tx.Type == transaction.TypeFXConversion:
			shares = shares.Sub(tx.Quantity)
		}
	}
	return hasWindowBuy && shares.IsPositive(), shares
}

// replCandidate is a replacement acquisition found in the superficial-loss window.
type replCandidate struct {
	tx      *transaction.Transaction
	preSell bool // true when the acquisition precedes the sell date
	nonReg  bool // true when the acquisition is in a non-registered account (carry-forward eligible)
}

// computeAdjDenied is the inner single-pass computation used by ComputeSuperficialAdjustments.
// priorAdj applies carry-forwards from a previous pass so ACBs reflect earlier adjustments.
// withCarryFwd maps sell IDs to present for sells that produced a non-registered carry-forward.
func computeAdjDenied(securityID int64, nonRegTxs, allTxs []*transaction.Transaction, priorAdj map[int64]decimal.Decimal) (adj, denied map[int64]decimal.Decimal, withCarryFwd map[int64]struct{}) {
	_, history := CalculateACBWithHistory(securityID, nonRegTxs, priorAdj)

	// Index non-reg transactions for O(1) carry-forward eligibility checks.
	nonRegByID := make(map[int64]struct{}, len(nonRegTxs))
	for _, tx := range nonRegTxs {
		nonRegByID[tx.ID] = struct{}{}
	}

	allocated := make(map[int64]decimal.Decimal) // replacement capacity already used across sells

	// replFloor is a moving lower-bound index into allTxs (sorted trade_date asc).
	// Sells are processed in ascending date order so windowStart is non-decreasing;
	// replFloor only advances, keeping the per-sell window scan O(window-size).
	replFloor := 0

	for _, row := range history {
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
		isSuperf, netAtEnd := checkSuperficialLoss(allTxs, row.Tx.TradeDate)
		if !isSuperf {
			continue
		}

		windowStart, windowEnd := superficialLossWindow(row.Tx.TradeDate)
		soldQty := row.Tx.Quantity

		// Advance replFloor to the first allTxs entry on or after windowStart.
		// Sells are in ascending date order so windowStart is non-decreasing;
		// replFloor only moves forward across the entire outer loop.
		for replFloor < len(allTxs) && allTxs[replFloor].TradeDate.Before(windowStart) {
			replFloor++
		}

		// Collect ALL acquisitions in [windowStart, windowEnd] from allTxs (all accounts).
		// allTxs is sorted trade_date asc; break early once past windowEnd.
		// Pre-sell and post-sell replacements both contribute to denial per CRA.
		// Non-reg buys receive carry-forward; registered buys count for denial only.
		var repls []replCandidate
		for i := replFloor; i < len(allTxs); i++ {
			tx := allTxs[i]
			if tx.TradeDate.After(windowEnd) {
				break
			}
			if tx.ID == row.Tx.ID {
				continue // skip the sell itself
			}
			if !isAcquisitionType(tx.Type) {
				continue
			}
			_, isNonReg := nonRegByID[tx.ID]
			// preSell is true when the acquisition is processed before the sell in
			// CalculateACBWithHistory, which walks txs in (trade_date, id) order.
			// Same-date buys with a lower ID precede the sell in that walk and must
			// be treated as pre-sell so the preSellPool cap applies correctly.
			preSell := tx.TradeDate.Before(row.Tx.TradeDate) ||
				(!tx.TradeDate.After(row.Tx.TradeDate) && tx.ID < row.Tx.ID)
			repls = append(repls, replCandidate{
				tx:      tx,
				preSell: preSell,
				nonReg:  isNonReg,
			})
		}

		// Compute per-replacement contributions in a single pass, respecting cross-sell allocation.
		type replContrib struct {
			tx      *transaction.Transaction
			contrib decimal.Decimal
			preSell bool
			nonReg  bool
		}
		var contribs []replContrib
		// Cap effective replacement at the net position at windowEnd per CRA rules.
		// Prevents over-denial when window acquisitions exceed the final held position
		// (e.g. pre-sell buy 100, sell all 100, post-sell buy 1 → only 1/100 denied).
		remaining := soldQty
		if netAtEnd.LessThan(remaining) {
			remaining = netAtEnd
		}
		// preSellPool tracks remaining carry-forward capacity for pre-sell replacements.
		// Pre-sell shares can only carry forward to shares still in the pool after the sell.
		// If the sell empties the pool, pre-sell buys count toward denial but produce no
		// carry-forward; without this cap their adj would be keyed to the sell ID and deferred
		// via pendingAdj to the next acquisition, which may be outside the ±30-day window.
		preSellPool := row.PreTxShares.Sub(soldQty)
		if preSellPool.IsNegative() {
			preSellPool = decimal.Zero
		}
		for _, r := range repls {
			avail := r.tx.Quantity.Sub(allocated[r.tx.ID])
			if r.preSell && preSellPool.LessThan(avail) {
				avail = preSellPool
			}
			if !avail.IsPositive() {
				continue
			}
			contrib := avail
			if remaining.LessThan(contrib) {
				contrib = remaining
			}
			if r.preSell {
				preSellPool = preSellPool.Sub(contrib)
			}
			contribs = append(contribs, replContrib{tx: r.tx, contrib: contrib, preSell: r.preSell, nonReg: r.nonReg})
			remaining = remaining.Sub(contrib)
			if !remaining.IsPositive() {
				break
			}
		}

		if len(contribs) == 0 {
			continue // all replacement capacity exhausted by earlier sells
		}

		var totalEffective decimal.Decimal
		for _, c := range contribs {
			totalEffective = totalEffective.Add(c.contrib)
		}
		proportionalLoss := gainLoss.Abs().Mul(totalEffective).Div(soldQty)

		if denied == nil {
			denied = make(map[int64]decimal.Decimal)
		}
		denied[row.Tx.ID] = denied[row.Tx.ID].Add(proportionalLoss)

		// Distribute carry-forward to non-reg buys only; update allocated for all.
		// Pre-sell non-reg: carry-forward keyed to sell ID — CalculateACBWithHistory applies it
		// to the remaining pool after the sell (not retroactively to the pre-sell buy itself).
		// Post-sell non-reg: carry-forward keyed to the buy ID — added to that buy's cost.
		hasNonRegRepl := false
		for _, c := range contribs {
			allocated[c.tx.ID] = allocated[c.tx.ID].Add(c.contrib)
			if !c.nonReg {
				continue
			}
			carryFwd := gainLoss.Abs().Mul(c.contrib).Div(soldQty)
			if adj == nil {
				adj = make(map[int64]decimal.Decimal)
			}
			if c.preSell {
				adj[row.Tx.ID] = adj[row.Tx.ID].Add(carryFwd)
			} else {
				adj[c.tx.ID] = adj[c.tx.ID].Add(carryFwd)
			}
			hasNonRegRepl = true
		}
		if hasNonRegRepl {
			if withCarryFwd == nil {
				withCarryFwd = make(map[int64]struct{})
			}
			withCarryFwd[row.Tx.ID] = struct{}{}
		}
	}

	return adj, denied, withCarryFwd
}

// adjEqual reports whether two adj maps are identical (same keys, same values).
func adjEqual(a, b map[int64]decimal.Decimal) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || !v.Equal(bv) {
			return false
		}
	}
	return true
}

// ComputeSuperficialAdjustments returns three maps:
//   - adj:          transactionID → carry-forward amount applied by CalculateACBWithHistory.
//   - denied:       transactionID → denied loss amount for superficial-loss disposals.
//   - withCarryFwd: sell IDs present when a non-registered carry-forward was created.
//                   Absent (nil or key missing) when the replacement was registered-only.
//
// Denial is proportional: effectiveReplQty/soldQty × totalLoss. Replacement acquisitions
// are collected from allTxs (all accounts) across the full ±30-day window; pre-sell and
// post-sell buys both contribute. Non-registered buys receive a carry-forward adjustment;
// registered-only replacements trigger denial without carry-forward. Multiple sells sharing
// a replacement buy allocate capacity FIFO by sell date to prevent over-allocation.
//
// Internally, computeAdjDenied is called repeatedly until adj stabilises (cascading pre-sell
// carry-forwards can shift ACBs that affect whether a later sell is a loss). A final call
// recomputes denied against the stable adj so callers see consistent values.
//
// Both slices must be sorted by trade_date ascending. Returns nil, nil, nil when allTxs is empty.
func ComputeSuperficialAdjustments(securityID int64, nonRegTxs, allTxs []*transaction.Transaction) (adj, denied map[int64]decimal.Decimal, withCarryFwd map[int64]struct{}) {
	if len(allTxs) == 0 {
		return nil, nil, nil
	}
	adj, _, _ = computeAdjDenied(securityID, nonRegTxs, allTxs, nil)
	// Iterate until adj stabilises — handles cascading pre-sell carry-forwards where one
	// sell's adjustment shifts the ACB of a later sell. Cap at 9 refinements to bound cost
	// on pathological inputs; in practice 1-2 iterations are sufficient.
	for range 9 {
		next, _, _ := computeAdjDenied(securityID, nonRegTxs, allTxs, adj)
		if adjEqual(adj, next) {
			break
		}
		adj = next
	}
	_, denied, withCarryFwd = computeAdjDenied(securityID, nonRegTxs, allTxs, adj)
	return adj, denied, withCarryFwd
}
