package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// mockTxStore is a minimal in-memory transaction.Store for testing.
type mockTxStore struct {
	nonRegistered    map[int64][]*transaction.Transaction // securityID → txs
	allAccounts      map[int64][]*transaction.Transaction // securityID → txs (all accounts)
	sellsByUser      []*transaction.Transaction           // user-level sells by date range
	errListDisposals error
	errListNonReg    error
	errListAll       error
	errCreate        error
}

func (m *mockTxStore) Create(_ context.Context, _ *transaction.Transaction) error {
	return m.errCreate
}
func (m *mockTxStore) GetByID(_ context.Context, _ int64) (*transaction.Transaction, error) {
	return nil, nil
}
func (m *mockTxStore) ListByAccount(_ context.Context, _ int64) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *mockTxStore) ListBySecurityNonRegistered(_ context.Context, securityID, _ int64) ([]*transaction.Transaction, error) {
	if m.errListNonReg != nil {
		return nil, m.errListNonReg
	}
	return m.nonRegistered[securityID], nil
}
func (m *mockTxStore) ListNonRegisteredDisposalsByUser(_ context.Context, _ int64, from, to time.Time) ([]*transaction.Transaction, error) {
	if m.errListDisposals != nil {
		return nil, m.errListDisposals
	}
	var out []*transaction.Transaction
	for _, tx := range m.sellsByUser {
		if !tx.TradeDate.Before(from) && !tx.TradeDate.After(to) {
			out = append(out, tx)
		}
	}
	return out, nil
}
func (m *mockTxStore) ListBySecurityAllAccounts(_ context.Context, securityID, _ int64) ([]*transaction.Transaction, error) {
	if m.errListAll != nil {
		return nil, m.errListAll
	}
	return m.allAccounts[securityID], nil
}
func (m *mockTxStore) ListByDateRange(_ context.Context, _ int64, _, _ time.Time) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *mockTxStore) Delete(_ context.Context, _ int64) error { return nil }
func (m *mockTxStore) UpdateFXRate(_ context.Context, _ int64, _ *decimal.Decimal, _, _ decimal.Decimal) error {
	return nil
}

// mockSecStore is a minimal in-memory security.Store for testing.
type mockSecStore struct {
	secs       map[int64]*security.Security
	errGetByID error
}

func (m *mockSecStore) Create(_ context.Context, _ *security.Security) error { return nil }
func (m *mockSecStore) GetByID(_ context.Context, id int64) (*security.Security, error) {
	if m.errGetByID != nil {
		return nil, m.errGetByID
	}
	return m.secs[id], nil
}
func (m *mockSecStore) GetByTickerExchange(_ context.Context, _, _ string) (*security.Security, error) {
	return nil, nil
}
func (m *mockSecStore) Search(_ context.Context, _ string) ([]*security.Security, error) { return nil, nil }
func (m *mockSecStore) ListAll(_ context.Context) ([]*security.Security, error)           { return nil, nil }
func (m *mockSecStore) Update(_ context.Context, _ *security.Security) error              { return nil }
func (m *mockSecStore) Delete(_ context.Context, _ int64) error                           { return nil }

func date(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

func TestGainsService_Gain(t *testing.T) {
	// Buy 100 @ $10, sell 50 @ $15 — gain = 50*15 - 50*10 = 250
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 1, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("50"), PriceCAD: d("15"), CommissionCAD: d("0")},
	}
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{1: txs},
		allAccounts:   map[int64][]*transaction.Transaction{1: txs},
		sellsByUser:   txs[1:2],
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{1: sec}})

	report, err := svc.Calculate(context.Background(), 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(report.Lines))
	}
	line := report.Lines[0]
	if !line.GainLoss.Equal(d("250")) {
		t.Errorf("gain/loss: got %s want 250", line.GainLoss)
	}
	if line.IsSuperficialLoss {
		t.Error("should not be superficial loss")
	}
	if !report.TotalGains.Equal(d("250")) {
		t.Errorf("total gains: got %s want 250", report.TotalGains)
	}
	if !report.TotalLosses.IsZero() {
		t.Errorf("total losses: got %s want 0", report.TotalLosses)
	}
	if !report.NetGain.Equal(d("250")) {
		t.Errorf("net gain: got %s want 250", report.NetGain)
	}
}

func TestGainsService_Loss(t *testing.T) {
	// Buy 100 @ $10, sell 100 @ $8 — loss = 100*8 - 100*10 = -200
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 2, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 2, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0")},
	}
	sec := &security.Security{ID: 2, Ticker: "ABC", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{2: txs},
		allAccounts:   map[int64][]*transaction.Transaction{2: txs},
		sellsByUser:   txs[1:2],
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{2: sec}})

	report, err := svc.Calculate(context.Background(), 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(report.Lines))
	}
	line := report.Lines[0]
	if !line.GainLoss.Equal(d("-200")) {
		t.Errorf("gain/loss: got %s want -200", line.GainLoss)
	}
	if !report.TotalLosses.Equal(d("200")) {
		t.Errorf("total losses: got %s want 200", report.TotalLosses)
	}
	if !report.NetGain.Equal(d("-200")) {
		t.Errorf("net gain: got %s want -200", report.NetGain)
	}
}

func TestGainsService_SuperficialLoss_RegisteredRepurchase(t *testing.T) {
	// Sell at loss in non-registered; buy back in registered within 30 days — superficial loss
	sellDate := date("2024-06-01")
	nonRegTxs := []*transaction.Transaction{
		{ID: 1, SecurityID: 3, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 3, Type: transaction.TypeSell, TradeDate: sellDate, Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0")},
	}
	// Registered account repurchase within 30 days
	regBuy := &transaction.Transaction{
		ID: 3, SecurityID: 3, Type: transaction.TypeBuy, TradeDate: date("2024-06-10"), Quantity: d("50"), PriceCAD: d("8.50"), CommissionCAD: d("0"),
	}
	allTxs := make([]*transaction.Transaction, len(nonRegTxs)+1)
	copy(allTxs, nonRegTxs)
	allTxs[len(nonRegTxs)] = regBuy
	sec := &security.Security{ID: 3, Ticker: "DEF", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{3: nonRegTxs},
		allAccounts:   map[int64][]*transaction.Transaction{3: allTxs},
		sellsByUser:   nonRegTxs[1:2],
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{3: sec}})

	report, err := svc.Calculate(context.Background(), 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(report.Lines))
	}
	if !report.Lines[0].IsSuperficialLoss {
		t.Error("expected superficial loss flag")
	}
	if !report.SuperficialLossTotal.Equal(d("200")) {
		t.Errorf("superficial loss total: got %s want 200", report.SuperficialLossTotal)
	}
}

func TestGainsService_NoSells(t *testing.T) {
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{},
		allAccounts:   map[int64][]*transaction.Transaction{},
		sellsByUser:   nil,
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{}})

	report, err := svc.Calculate(context.Background(), 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(report.Lines))
	}
	if !report.TotalGains.IsZero() || !report.TotalLosses.IsZero() {
		t.Error("expected zero totals with no sells")
	}
}

func TestGainsService_WithCommission(t *testing.T) {
	// Buy 100 @ $10 + $5 commission, sell 100 @ $12 - $5 commission
	// ACB = (100*10 + 5) / 100 = 10.05/share
	// Proceeds = 100*12 - 5 = 1195
	// ACB at sell = 100 * 10.05 = 1005
	// Gain = 1195 - 1005 = 190
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 4, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("5")},
		{ID: 2, SecurityID: 4, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("100"), PriceCAD: d("12"), CommissionCAD: d("5")},
	}
	sec := &security.Security{ID: 4, Ticker: "GHI", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{4: txs},
		allAccounts:   map[int64][]*transaction.Transaction{4: txs},
		sellsByUser:   txs[1:2],
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{4: sec}})

	report, err := svc.Calculate(context.Background(), 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(report.Lines))
	}
	expected := d("190")
	if !report.Lines[0].GainLoss.Equal(expected) {
		t.Errorf("gain/loss: got %s want %s", report.Lines[0].GainLoss, expected)
	}
}

func TestGainsService_HistoryForSecurity_Trimmed(t *testing.T) {
	// Buy Jan, sell Jun, buy Aug (future relative to sell) — history for 2024 should stop at the sell.
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 10, Type: transaction.TypeBuy, TradeDate: date("2024-01-10"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 10, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("50"), PriceCAD: d("15"), CommissionCAD: d("0")},
		{ID: 3, SecurityID: 10, Type: transaction.TypeBuy, TradeDate: date("2024-08-01"), Quantity: d("30"), PriceCAD: d("14"), CommissionCAD: d("0")},
	}
	sec := &security.Security{ID: 10, Ticker: "TRIM", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{10: txs},
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{10: sec}})

	gotSec, history, err := svc.HistoryForSecurity(context.Background(), 10, 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSec == nil || gotSec.Ticker != "TRIM" {
		t.Errorf("security: got %v", gotSec)
	}
	// Only the buy + sell rows; Aug buy excluded.
	if len(history) != 2 {
		t.Fatalf("expected 2 history rows, got %d", len(history))
	}
	if history[1].Tx.Type != transaction.TypeSell {
		t.Errorf("last row should be sell, got %s", history[1].Tx.Type)
	}
}

func TestGainsService_HistoryForSecurity_NoDisposalsInYear(t *testing.T) {
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 11, Type: transaction.TypeBuy, TradeDate: date("2024-01-10"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
	}
	sec := &security.Security{ID: 11, Ticker: "HOLD", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{11: txs},
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{11: sec}})

	gotSec, history, err := svc.HistoryForSecurity(context.Background(), 11, 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSec == nil {
		t.Fatal("expected security, got nil")
	}
	if history != nil {
		t.Errorf("expected nil history when no disposals in year, got %d rows", len(history))
	}
}

func TestGainsService_HistoryForSecurity_TrimmedAtLastDisposal(t *testing.T) {
	// Sell Jan, buy Feb, sell Mar, buy Apr — history for 2024 stops at Mar sell.
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 12, Type: transaction.TypeBuy, TradeDate: date("2023-12-01"), Quantity: d("200"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 12, Type: transaction.TypeSell, TradeDate: date("2024-01-15"), Quantity: d("50"), PriceCAD: d("12"), CommissionCAD: d("0")},
		{ID: 3, SecurityID: 12, Type: transaction.TypeBuy, TradeDate: date("2024-02-01"), Quantity: d("20"), PriceCAD: d("11"), CommissionCAD: d("0")},
		{ID: 4, SecurityID: 12, Type: transaction.TypeSell, TradeDate: date("2024-03-10"), Quantity: d("30"), PriceCAD: d("13"), CommissionCAD: d("0")},
		{ID: 5, SecurityID: 12, Type: transaction.TypeBuy, TradeDate: date("2024-04-01"), Quantity: d("10"), PriceCAD: d("12"), CommissionCAD: d("0")},
	}
	sec := &security.Security{ID: 12, Ticker: "MULTI", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{12: txs},
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{12: sec}})

	_, history, err := svc.HistoryForSecurity(context.Background(), 12, 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// expect: 2023 buy + Jan sell + Feb buy + Mar sell = 4 rows; Apr buy excluded
	if len(history) != 4 {
		t.Fatalf("expected 4 history rows, got %d", len(history))
	}
	last := history[len(history)-1]
	if last.Tx.Type != transaction.TypeSell || last.Tx.TradeDate.Month() != 3 {
		t.Errorf("last row should be Mar sell, got type=%s date=%s", last.Tx.Type, last.Tx.TradeDate.Format(time.DateOnly))
	}
}

func TestGainsService_HistoryForSecurity_RunningACBCorrect(t *testing.T) {
	// Buy 100 @ $10 (ACB = $10/share), sell 50 @ $15.
	// At sell row: PreTxACBPerShare = 10, RunningShares = 50, RunningACB = 500.
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 13, Type: transaction.TypeBuy, TradeDate: date("2024-01-10"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 13, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("50"), PriceCAD: d("15"), CommissionCAD: d("0")},
	}
	sec := &security.Security{ID: 13, Ticker: "ACB", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{13: txs},
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{13: sec}})

	_, history, err := svc.HistoryForSecurity(context.Background(), 13, 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(history))
	}
	sellRow := history[1]
	if !sellRow.RunningShares.Equal(d("50")) {
		t.Errorf("RunningShares after sell: got %s want 50", sellRow.RunningShares)
	}
	if !sellRow.RunningACB.Equal(d("500")) {
		t.Errorf("RunningACB after sell: got %s want 500", sellRow.RunningACB)
	}
	if !sellRow.PreTxACBPerShare.Equal(d("10")) {
		t.Errorf("PreTxACBPerShare at sell: got %s want 10", sellRow.PreTxACBPerShare)
	}
}

func TestGainsService_NeedsReview_NoACBBuy(t *testing.T) {
	// Norbert's Gambit scenario: sell of DLR.TO with no corresponding buy
	// (buy was DLR.U.TO → journalled via FXT, which is skipped by importer)
	sec := &security.Security{ID: 5, Ticker: "DLR.TO", Exchange: "TSX"}
	sellOnly := []*transaction.Transaction{
		{ID: 1, SecurityID: 5, Type: transaction.TypeSell, TradeDate: date("2024-03-25"),
			Quantity: d("1000"), PriceCAD: d("14.05"), CommissionCAD: d("0")},
	}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{5: sellOnly},
		allAccounts:   map[int64][]*transaction.Transaction{5: sellOnly},
		sellsByUser:   sellOnly,
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{5: sec}})

	report, err := svc.Calculate(context.Background(), 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Lines) != 0 {
		t.Errorf("expected 0 confirmed lines, got %d", len(report.Lines))
	}
	if len(report.NeedsReviewLines) != 1 {
		t.Fatalf("expected 1 NeedsReview line, got %d", len(report.NeedsReviewLines))
	}
	if !report.NeedsReviewLines[0].NeedsReview {
		t.Error("NeedsReview flag should be true")
	}
	if !report.TotalGains.IsZero() {
		t.Errorf("NeedsReview sells must not inflate totals: TotalGains=%s", report.TotalGains)
	}
}

func TestGainsService_SuperficialLoss_NonRegRepurchase_CarryForward(t *testing.T) {
	// Buy 100 @ $10, sell 100 @ $8 (loss=$200) then buy 50 @ $8.50 in same non-reg within 30 days.
	// Superficial loss: $200 denied loss carries forward to replacement buy's ACB.
	// Replacement buy cost: 50*8.50 = $425 + $200 carry-forward = $625 total ACB.
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 6, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 6, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0")},
		{ID: 3, SecurityID: 6, Type: transaction.TypeBuy, TradeDate: date("2024-06-10"), Quantity: d("50"), PriceCAD: d("8.50"), CommissionCAD: d("0")},
	}
	sec := &security.Security{ID: 6, Ticker: "XYZ", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{6: txs},
		allAccounts:   map[int64][]*transaction.Transaction{6: txs},
		sellsByUser:   txs[1:2],
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{6: sec}})

	report, err := svc.Calculate(context.Background(), 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Lines) != 1 {
		t.Fatalf("expected 1 gains line, got %d", len(report.Lines))
	}
	if !report.Lines[0].IsSuperficialLoss {
		t.Error("expected superficial loss flag on sell")
	}
	// Proportional denial: min(50, 100)/100 × 200 = 100.
	if !report.SuperficialLossTotal.Equal(d("100")) {
		t.Errorf("superficial loss total: got %s want 100", report.SuperficialLossTotal)
	}
	// Claimable portion: 200 − 100 = 100.
	if !report.TotalLosses.Equal(d("100")) {
		t.Errorf("total losses: got %s want 100", report.TotalLosses)
	}

	// Verify carry-forward applied to replacement buy via HistoryForSecurity.
	_, history, err := svc.HistoryForSecurity(context.Background(), 6, 1, 2024)
	if err != nil {
		t.Fatalf("HistoryForSecurity error: %v", err)
	}
	// history is trimmed to last disposal in 2024, which is the sell (ID=2); buy ID=3 is after
	if len(history) != 2 {
		t.Fatalf("expected 2 history rows (buy+sell), got %d", len(history))
	}
}

func TestComputeSuperficialAdjustments_CarryForward(t *testing.T) {
	// Same scenario as above but tested directly on the pure function.
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 7, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 7, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0")},
		{ID: 3, SecurityID: 7, Type: transaction.TypeBuy, TradeDate: date("2024-06-10"), Quantity: d("50"), PriceCAD: d("8.50"), CommissionCAD: d("0")},
	}

	// Proportional: min(50, 100)/100 × 200 = 100.
	adj, denied, _ := service.ComputeSuperficialAdjustments(7, txs, txs)
	if adj == nil {
		t.Fatal("expected non-nil adjustments map")
	}
	if !adj[3].Equal(d("100")) {
		t.Errorf("carry-forward on tx ID=3: got %s want 100", adj[3])
	}
	if !adj[1].IsZero() {
		t.Errorf("buy tx ID=1 should have no adjustment, got %s", adj[1])
	}
	if denied == nil {
		t.Fatal("expected non-nil denied map")
	}
	if !denied[2].Equal(d("100")) {
		t.Errorf("denied loss on sell ID=2: got %s want 100", denied[2])
	}

	// Pass 2: ACB with adjustments applied.
	_, history := service.CalculateACBWithHistory(7, txs, adj)
	// Row 2 (index 2) is the replacement buy (ID=3).
	if len(history) != 3 {
		t.Fatalf("expected 3 history rows, got %d", len(history))
	}
	replacementRow := history[2]
	if !replacementRow.SuperficialLossAdj.Equal(d("100")) {
		t.Errorf("SuperficialLossAdj on replacement row: got %s want 100", replacementRow.SuperficialLossAdj)
	}
	// ACB after replacement buy: 50*8.50 + 100 carry-forward = 525; ACB/share = 10.50
	if !replacementRow.RunningACB.Equal(d("525")) {
		t.Errorf("RunningACB after replacement buy: got %s want 525", replacementRow.RunningACB)
	}
	if !replacementRow.RunningACBPerShare.Equal(d("10.5")) {
		t.Errorf("RunningACBPerShare after replacement buy: got %s want 10.50", replacementRow.RunningACBPerShare)
	}
}

func TestComputeSuperficialAdjustments_RegisteredRepurchase_NoCarryForward(t *testing.T) {
	// Replacement buy is in registered account only — no carry-forward to non-reg.
	nonRegTxs := []*transaction.Transaction{
		{ID: 1, SecurityID: 8, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 8, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0")},
	}
	regBuy := &transaction.Transaction{
		ID: 3, SecurityID: 8, Type: transaction.TypeBuy, TradeDate: date("2024-06-10"), Quantity: d("50"), PriceCAD: d("8.50"), CommissionCAD: d("0"),
	}
	allTxs := make([]*transaction.Transaction, len(nonRegTxs)+1)
	copy(allTxs, nonRegTxs)
	allTxs[len(nonRegTxs)] = regBuy

	adj, denied, _ := service.ComputeSuperficialAdjustments(8, nonRegTxs, allTxs)
	if adj != nil {
		t.Errorf("expected nil adj when replacement is registered-only, got %v", adj)
	}
	if denied == nil {
		t.Fatal("expected non-nil denied map")
	}
	if !denied[2].Equal(d("200")) {
		t.Errorf("denied loss on sell (ID=2): got %s want 200", denied[2])
	}
}

func TestCalculateACBWithHistory_PreSellCarryForwardDeferred(t *testing.T) {
	// Non-reg: buy 100@$10 May-01, sell ALL 100@$8 May-20 (loss=$200, pre-sell replacement).
	// RRSP buy on May-30 satisfies checkSuperficialLoss (positive position at windowEnd)
	// but is not in nonRegTxs, so the post-sell search finds nothing.
	// Pre-sell fallback finds the May-01 buy → preSell=true, adj[sell.ID=2]=$200.
	// Non-reg rebuy at Jul-15 is outside the 30-day window so it's not a post-sell replacement.
	// Fix: the sell empties the pool, so the carry-forward is deferred to the Jul-15 buy.
	secID := int64(20)
	nonRegTxs := []*transaction.Transaction{
		{ID: 1, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-05-01"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: secID, Type: transaction.TypeSell, TradeDate: date("2024-05-20"), Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0")},
		{ID: 4, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-07-15"), Quantity: d("50"), PriceCAD: d("9"), CommissionCAD: d("0")},
	}
	rrspBuy := &transaction.Transaction{
		ID: 3, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-05-30"), Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0"),
	}
	allTxs := []*transaction.Transaction{nonRegTxs[0], nonRegTxs[1], rrspBuy, nonRegTxs[2]}

	adj, denied, _ := service.ComputeSuperficialAdjustments(secID, nonRegTxs, allTxs)
	if adj == nil {
		t.Fatal("expected non-nil adj")
	}
	if !adj[2].Equal(d("200")) {
		t.Errorf("carry-forward keyed to sell ID=2: got %s want 200", adj[2])
	}
	if denied == nil {
		t.Fatal("expected non-nil denied")
	}
	if !denied[2].Equal(d("200")) {
		t.Errorf("denied on sell ID=2: got %s want 200", denied[2])
	}

	_, history := service.CalculateACBWithHistory(secID, nonRegTxs, adj)
	if len(history) != 3 {
		t.Fatalf("expected 3 history rows, got %d", len(history))
	}

	sellRow := history[1]
	// Sell empties pool — carry-forward deferred, NOT applied here.
	if !sellRow.RunningShares.IsZero() {
		t.Errorf("RunningShares after sell-all: got %s want 0", sellRow.RunningShares)
	}
	if !sellRow.RunningACB.IsZero() {
		t.Errorf("RunningACB after sell-all: got %s want 0", sellRow.RunningACB)
	}
	if !sellRow.SuperficialLossAdj.IsZero() {
		t.Errorf("SuperficialLossAdj on sell row should be 0 (deferred): got %s", sellRow.SuperficialLossAdj)
	}

	nextBuyRow := history[2]
	// ACB = 50*9 + $200 deferred = $650; ACB/share = $13.
	if !nextBuyRow.RunningACB.Equal(d("650")) {
		t.Errorf("RunningACB after deferred carry-forward: got %s want 650", nextBuyRow.RunningACB)
	}
	if !nextBuyRow.SuperficialLossAdj.Equal(d("200")) {
		t.Errorf("SuperficialLossAdj on next buy (deferred): got %s want 200", nextBuyRow.SuperficialLossAdj)
	}
}

func TestComputeSuperficialAdjustments_PreSellCarryForward(t *testing.T) {
	// Buy 150 @ $10 on Day 1; sell 50 @ $8 on Day 20 (partial sell, loss=$100).
	// No post-sell buy; pre-sell buy (Day 1, qty=150) is the fallback.
	// Proportional: min(150,50)/50 × 100 = 100 — full denial since replacement qty > sold qty.
	//
	// G3 fix: carry-forward is keyed to the sell ID (not the pre-sell buy ID) so that
	// CalculateACBWithHistory applies it post-sell to the remaining pool, leaving prior
	// disposals unchanged.
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 9, Type: transaction.TypeBuy, TradeDate: date("2024-05-01"), Quantity: d("150"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 9, Type: transaction.TypeSell, TradeDate: date("2024-05-20"), Quantity: d("50"), PriceCAD: d("8"), CommissionCAD: d("0")},
	}
	adj, denied, _ := service.ComputeSuperficialAdjustments(9, txs, txs)
	if adj == nil {
		t.Fatal("expected non-nil adj for pre-sell carry-forward")
	}
	// carry-forward is on the sell ID (applied post-sell to remaining shares)
	if !adj[2].Equal(d("100")) {
		t.Errorf("carry-forward keyed to sell (ID=2): got %s want 100", adj[2])
	}
	// pre-sell buy gets no direct ACB adjustment
	if !adj[1].IsZero() {
		t.Errorf("pre-sell buy (ID=1) should have no adj, got %s", adj[1])
	}
	if denied == nil {
		t.Fatal("expected non-nil denied map")
	}
	if !denied[2].Equal(d("100")) {
		t.Errorf("denied loss on sell (ID=2): got %s want 100", denied[2])
	}

	// Verify CalculateACBWithHistory applies the carry-forward post-sell.
	// After sell: 100 shares remain at $10 ACB/share = $1000. Post-sell adj $100 → $1100 = $11/share.
	_, history := service.CalculateACBWithHistory(9, txs, adj)
	if len(history) != 2 {
		t.Fatalf("expected 2 history rows, got %d", len(history))
	}
	sellRow := history[1]
	if !sellRow.RunningACB.Equal(d("1100")) {
		t.Errorf("RunningACB after pre-sell carry-forward: got %s want 1100", sellRow.RunningACB)
	}
	if !sellRow.RunningACBPerShare.Equal(d("11")) {
		t.Errorf("RunningACBPerShare after pre-sell carry-forward: got %s want 11", sellRow.RunningACBPerShare)
	}
	if !sellRow.SuperficialLossAdj.Equal(d("100")) {
		t.Errorf("SuperficialLossAdj on sell row: got %s want 100", sellRow.SuperficialLossAdj)
	}
	// pre-sell buy row must be unchanged ($1500 ACB, $10/share)
	buyRow := history[0]
	if !buyRow.RunningACB.Equal(d("1500")) {
		t.Errorf("RunningACB for pre-sell buy should be unchanged: got %s want 1500", buyRow.RunningACB)
	}
}

// TestComputeSuperficialAdjustments_ExhaustedCapacity verifies that when two sells share one
// replacement buy and the first sell consumes all of its capacity, the second sell is not denied
// (the !available.IsPositive() continue branch in computeAdjDenied).
func TestComputeSuperficialAdjustments_ExhaustedCapacity(t *testing.T) {
	// Buy 100@$10; sell 30@$8 (loss=$60) and sell 30@$8 (loss=$60) on same day;
	// replacement buy 30@$9 after both sells. First sell exhausts the replacement's 30-share capacity;
	// second sell finds zero available capacity and is not denied.
	secID := int64(30)
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-01-01"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: secID, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("30"), PriceCAD: d("8"), CommissionCAD: d("0")},
		{ID: 3, SecurityID: secID, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("30"), PriceCAD: d("8"), CommissionCAD: d("0")},
		{ID: 4, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-06-10"), Quantity: d("30"), PriceCAD: d("9"), CommissionCAD: d("0")},
	}

	adj, denied, withCarryFwd := service.ComputeSuperficialAdjustments(secID, txs, txs)

	// First sell: full denial (replacement qty=30 == sold qty=30).
	if denied == nil {
		t.Fatal("expected non-nil denied map")
	}
	if !denied[2].Equal(d("60")) {
		t.Errorf("denied[sell1]: got %s want 60", denied[2])
	}
	// Second sell: replacement capacity exhausted — no denial.
	if !denied[3].IsZero() {
		t.Errorf("denied[sell2]: got %s want 0 (capacity exhausted)", denied[3])
	}
	// Carry-forward only for first sell.
	if adj == nil {
		t.Fatal("expected non-nil adj map")
	}
	if !adj[4].Equal(d("60")) {
		t.Errorf("adj[replacement]: got %s want 60", adj[4])
	}
	if _, ok := withCarryFwd[2]; !ok {
		t.Error("withCarryFwd should have sell ID=2")
	}
	if _, ok := withCarryFwd[3]; ok {
		t.Error("withCarryFwd should NOT have sell ID=3 (capacity exhausted)")
	}
}

// TestComputeSuperficialAdjustments_TypeTransferOut verifies that TypeTransferOut (a CRA deemed
// disposition) triggers the superficial loss rule, same as TypeSell.
func TestComputeSuperficialAdjustments_TypeTransferOut(t *testing.T) {
	// Buy 100@$10; transfer-out 50@$8 (loss=$100, deemed disposition);
	// buy back 50 within 30 days — superficial loss applies.
	secID := int64(31)
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: secID, Type: transaction.TypeTransferOut, TradeDate: date("2024-06-01"), Quantity: d("50"), PriceCAD: d("8"), CommissionCAD: d("0")},
		{ID: 3, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-06-10"), Quantity: d("50"), PriceCAD: d("8"), CommissionCAD: d("0")},
	}

	adj, denied, withCarryFwd := service.ComputeSuperficialAdjustments(secID, txs, txs)

	if denied == nil {
		t.Fatal("expected non-nil denied map")
	}
	// Proportional: min(50,50)/50 × 100 = 100 (full denial).
	if !denied[2].Equal(d("100")) {
		t.Errorf("denied[transfer-out]: got %s want 100", denied[2])
	}
	if adj == nil {
		t.Fatal("expected non-nil adj map")
	}
	if !adj[3].Equal(d("100")) {
		t.Errorf("adj[replacement buy]: got %s want 100", adj[3])
	}
	if _, ok := withCarryFwd[2]; !ok {
		t.Error("withCarryFwd should have transfer-out ID=2")
	}
}

// TestGainsService_HistoryForSecurity_RegisteredRepurchase_HasCarryForwardFalse verifies that
// when the replacement is registered-only, GainsDetailRow.HasCarryForward is false.
func TestGainsService_HistoryForSecurity_RegisteredRepurchase_HasCarryForwardFalse(t *testing.T) {
	secID := int64(40)
	nonRegTxs := []*transaction.Transaction{
		{ID: 1, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-01-15"), Quantity: d("100"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: secID, Type: transaction.TypeSell, TradeDate: date("2024-06-01"), Quantity: d("100"), PriceCAD: d("8"), CommissionCAD: d("0")},
	}
	regBuy := &transaction.Transaction{
		ID: 3, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-06-10"), Quantity: d("50"), PriceCAD: d("8.50"), CommissionCAD: d("0"),
	}
	allTxs := make([]*transaction.Transaction, len(nonRegTxs)+1)
	copy(allTxs, nonRegTxs)
	allTxs[len(nonRegTxs)] = regBuy
	sec := &security.Security{ID: secID, Ticker: "REG", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{secID: nonRegTxs},
		allAccounts:   map[int64][]*transaction.Transaction{secID: allTxs},
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{secID: sec}})

	_, history, err := svc.HistoryForSecurity(context.Background(), secID, 1, 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(history))
	}
	sellRow := history[1]
	if !sellRow.IsSuperficialLoss {
		t.Error("expected IsSuperficialLoss=true")
	}
	if sellRow.HasCarryForward {
		t.Error("HasCarryForward should be false for registered-only replacement")
	}
	if !sellRow.DeniedAmt.Equal(d("200")) {
		t.Errorf("DeniedAmt: got %s want 200", sellRow.DeniedAmt)
	}
}

// Error path tests for GainsService.Calculate.

func TestGainsService_Calculate_ListDisposalsError(t *testing.T) {
	store := &mockTxStore{errListDisposals: fmt.Errorf("db error")}
	svc := service.NewGainsService(store, &mockSecStore{})
	_, err := svc.Calculate(context.Background(), 1, 2024)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGainsService_Calculate_SecStoreError(t *testing.T) {
	sell := &transaction.Transaction{
		ID: 1, SecurityID: 50, Type: transaction.TypeSell, TradeDate: date("2024-06-01"),
		Quantity: d("10"), PriceCAD: d("10"), CommissionCAD: d("0"),
	}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{50: {sell}},
		allAccounts:   map[int64][]*transaction.Transaction{50: {sell}},
		sellsByUser:   []*transaction.Transaction{sell},
	}
	svc := service.NewGainsService(store, &mockSecStore{errGetByID: fmt.Errorf("sec not found")})
	_, err := svc.Calculate(context.Background(), 1, 2024)
	if err == nil {
		t.Fatal("expected error from secStore, got nil")
	}
}

func TestGainsService_Calculate_ListNonRegError(t *testing.T) {
	sell := &transaction.Transaction{
		ID: 1, SecurityID: 51, Type: transaction.TypeSell, TradeDate: date("2024-06-01"),
		Quantity: d("10"), PriceCAD: d("10"), CommissionCAD: d("0"),
	}
	sec := &security.Security{ID: 51, Ticker: "ERR", Exchange: "TSX"}
	store := &mockTxStore{
		sellsByUser:   []*transaction.Transaction{sell},
		errListNonReg: fmt.Errorf("list nonreg error"),
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{51: sec}})
	_, err := svc.Calculate(context.Background(), 1, 2024)
	if err == nil {
		t.Fatal("expected error from ListBySecurityNonRegistered, got nil")
	}
}

func TestGainsService_Calculate_ListAllError(t *testing.T) {
	sell := &transaction.Transaction{
		ID: 1, SecurityID: 52, Type: transaction.TypeSell, TradeDate: date("2024-06-01"),
		Quantity: d("10"), PriceCAD: d("10"), CommissionCAD: d("0"),
	}
	buy := &transaction.Transaction{
		ID: 2, SecurityID: 52, Type: transaction.TypeBuy, TradeDate: date("2024-01-01"),
		Quantity: d("10"), PriceCAD: d("10"), CommissionCAD: d("0"),
	}
	sec := &security.Security{ID: 52, Ticker: "ERR2", Exchange: "TSX"}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{52: {buy, sell}},
		sellsByUser:   []*transaction.Transaction{sell},
		errListAll:    fmt.Errorf("list all error"),
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{52: sec}})
	_, err := svc.Calculate(context.Background(), 1, 2024)
	if err == nil {
		t.Fatal("expected error from ListBySecurityAllAccounts, got nil")
	}
}

// Error path tests for GainsService.HistoryForSecurity.

func TestGainsService_HistoryForSecurity_SecStoreError(t *testing.T) {
	svc := service.NewGainsService(&mockTxStore{}, &mockSecStore{errGetByID: fmt.Errorf("not found")})
	_, _, err := svc.HistoryForSecurity(context.Background(), 99, 1, 2024)
	if err == nil {
		t.Fatal("expected error from secStore, got nil")
	}
}

func TestGainsService_HistoryForSecurity_ListNonRegError(t *testing.T) {
	sec := &security.Security{ID: 60, Ticker: "X", Exchange: "TSX"}
	store := &mockTxStore{errListNonReg: fmt.Errorf("list error")}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{60: sec}})
	_, _, err := svc.HistoryForSecurity(context.Background(), 60, 1, 2024)
	if err == nil {
		t.Fatal("expected error from ListBySecurityNonRegistered, got nil")
	}
}

func TestGainsService_HistoryForSecurity_ListAllError(t *testing.T) {
	secID := int64(61)
	sec := &security.Security{ID: secID, Ticker: "Y", Exchange: "TSX"}
	sell := &transaction.Transaction{
		ID: 1, SecurityID: secID, Type: transaction.TypeSell, TradeDate: date("2024-06-01"),
		Quantity: d("10"), PriceCAD: d("10"), CommissionCAD: d("0"),
	}
	buy := &transaction.Transaction{
		ID: 2, SecurityID: secID, Type: transaction.TypeBuy, TradeDate: date("2024-01-01"),
		Quantity: d("10"), PriceCAD: d("10"), CommissionCAD: d("0"),
	}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{secID: {buy, sell}},
		errListAll:    fmt.Errorf("all accts error"),
	}
	svc := service.NewGainsService(store, &mockSecStore{secs: map[int64]*security.Security{secID: sec}})
	_, _, err := svc.HistoryForSecurity(context.Background(), secID, 1, 2024)
	if err == nil {
		t.Fatal("expected error from ListBySecurityAllAccounts, got nil")
	}
}
