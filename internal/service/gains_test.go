package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// mockTxStore is a minimal in-memory transaction.Store for testing.
type mockTxStore struct {
	nonRegistered map[int64][]*transaction.Transaction // securityID → txs
	allAccounts   map[int64][]*transaction.Transaction // securityID → txs (all accounts)
	sellsByUser   []*transaction.Transaction           // user-level sells by date range
}

func (m *mockTxStore) Create(_ context.Context, tx *transaction.Transaction) error { return nil }
func (m *mockTxStore) GetByID(_ context.Context, _ int64) (*transaction.Transaction, error) {
	return nil, nil
}
func (m *mockTxStore) ListByAccount(_ context.Context, _ int64) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *mockTxStore) ListBySecurityNonRegistered(_ context.Context, securityID, _ int64) ([]*transaction.Transaction, error) {
	return m.nonRegistered[securityID], nil
}
func (m *mockTxStore) ListNonRegisteredSellsByUser(_ context.Context, _ int64, from, to time.Time) ([]*transaction.Transaction, error) {
	var out []*transaction.Transaction
	for _, tx := range m.sellsByUser {
		if !tx.TradeDate.Before(from) && !tx.TradeDate.After(to) {
			out = append(out, tx)
		}
	}
	return out, nil
}
func (m *mockTxStore) ListBySecurityAllAccounts(_ context.Context, securityID, _ int64) ([]*transaction.Transaction, error) {
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
	secs map[int64]*security.Security
}

func (m *mockSecStore) Create(_ context.Context, _ *security.Security) error { return nil }
func (m *mockSecStore) GetByID(_ context.Context, id int64) (*security.Security, error) {
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
