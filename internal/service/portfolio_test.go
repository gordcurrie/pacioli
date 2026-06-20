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

func pricePtr(s string) *decimal.Decimal {
	p, _ := decimal.NewFromString(s)
	return &p
}

// portfolioTxStore extends mockTxStore with ListDistinctAllSecurityIDsByUser control.
type portfolioTxStore struct {
	*mockTxStore
	distinctIDs    []int64
	errDistinctIDs error
}

func (m *portfolioTxStore) ListDistinctAllSecurityIDsByUser(_ context.Context, _ int64) ([]int64, error) {
	if m.errDistinctIDs != nil {
		return nil, m.errDistinctIDs
	}
	return m.distinctIDs, nil
}

func TestPortfolioService_Build_EmptyPositions(t *testing.T) {
	txStore := &portfolioTxStore{mockTxStore: &mockTxStore{}, distinctIDs: nil}
	secStore := &mockSecStore{secs: map[int64]*security.Security{}}
	acbSvc := service.NewACBService(txStore)
	svc := service.NewPortfolioService(txStore, secStore, acbSvc)

	positions, summary, err := svc.Build(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
	if !summary.TotalValueCAD.IsZero() {
		t.Error("expected zero total value")
	}
}

func TestPortfolioService_Build_RegisteredOnlyPosition(t *testing.T) {
	// Registered account: has shares, but non-reg ACB should be zero.
	sec := &security.Security{ID: 1, Ticker: "XEI.TO", Exchange: "TSX", Currency: "CAD", LastPriceCAD: pricePtr("25.00")}
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, Type: transaction.TypeBuy, TradeDate: time.Now(), Quantity: d("100"), PriceCAD: d("20"), CommissionCAD: d("0")},
	}
	txStore := &portfolioTxStore{
		mockTxStore: &mockTxStore{
			nonRegistered: map[int64][]*transaction.Transaction{}, // no non-reg txs
			allAccounts:   map[int64][]*transaction.Transaction{1: txs},
		},
		distinctIDs: []int64{1},
	}
	secStore := &mockSecStore{secs: map[int64]*security.Security{1: sec}}
	acbSvc := service.NewACBService(txStore)
	svc := service.NewPortfolioService(txStore, secStore, acbSvc)

	positions, summary, err := svc.Build(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}

	pos := positions[0]
	if pos.TotalShares.String() != "100" {
		t.Errorf("TotalShares: got %s, want 100", pos.TotalShares)
	}
	if !pos.NonRegShares.IsZero() {
		t.Errorf("NonRegShares should be zero for registered-only, got %s", pos.NonRegShares)
	}
	if !pos.NonRegACBTotal.IsZero() {
		t.Errorf("NonRegACBTotal should be zero, got %s", pos.NonRegACBTotal)
	}
	// Current value = 100 × $25
	if pos.CurrentValueCAD.String() != "2500" {
		t.Errorf("CurrentValueCAD: got %s, want 2500", pos.CurrentValueCAD)
	}
	// No non-reg shares → unrealized should be zero
	if !pos.UnrealizedGainCAD.IsZero() {
		t.Errorf("UnrealizedGainCAD should be zero for registered-only, got %s", pos.UnrealizedGainCAD)
	}
	// Summary: value populated, non-reg ACB zero
	if summary.TotalValueCAD.String() != "2500" {
		t.Errorf("summary TotalValueCAD: got %s, want 2500", summary.TotalValueCAD)
	}
	if !summary.TotalNonRegACB.IsZero() {
		t.Errorf("summary TotalNonRegACB should be zero, got %s", summary.TotalNonRegACB)
	}
}

func TestPortfolioService_Build_MixedRegNonReg(t *testing.T) {
	price := pricePtr("50.00")
	sec := &security.Security{ID: 1, Ticker: "SU.TO", Exchange: "TSX", Currency: "CAD", LastPriceCAD: price}
	regTx := &transaction.Transaction{ID: 1, SecurityID: 1, Type: transaction.TypeBuy, TradeDate: time.Now(), Quantity: d("50"), PriceCAD: d("40"), CommissionCAD: d("0")}
	nonRegTx := &transaction.Transaction{ID: 2, SecurityID: 1, Type: transaction.TypeBuy, TradeDate: time.Now(), Quantity: d("100"), PriceCAD: d("40"), CommissionCAD: d("0")}

	txStore := &portfolioTxStore{
		mockTxStore: &mockTxStore{
			nonRegistered: map[int64][]*transaction.Transaction{1: {nonRegTx}},
			allAccounts:   map[int64][]*transaction.Transaction{1: {regTx, nonRegTx}},
		},
		distinctIDs: []int64{1},
	}
	secStore := &mockSecStore{secs: map[int64]*security.Security{1: sec}}
	acbSvc := service.NewACBService(txStore)
	svc := service.NewPortfolioService(txStore, secStore, acbSvc)

	positions, _, err := svc.Build(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}

	pos := positions[0]
	// Total = 50 (reg) + 100 (non-reg) = 150
	if pos.TotalShares.String() != "150" {
		t.Errorf("TotalShares: got %s, want 150", pos.TotalShares)
	}
	// Non-reg = 100 shares @ $40 ACB
	if pos.NonRegShares.String() != "100" {
		t.Errorf("NonRegShares: got %s, want 100", pos.NonRegShares)
	}
	acbTotal := decimal.NewFromInt(100).Mul(decimal.NewFromFloat(40))
	if !pos.NonRegACBTotal.Equal(acbTotal) {
		t.Errorf("NonRegACBTotal: got %s, want %s", pos.NonRegACBTotal, acbTotal)
	}
	// CurrentValue = 150 × $50
	wantValue := decimal.NewFromInt(150).Mul(decimal.NewFromFloat(50))
	if !pos.CurrentValueCAD.Equal(wantValue) {
		t.Errorf("CurrentValueCAD: got %s, want %s", pos.CurrentValueCAD, wantValue)
	}
	// Unrealized = 100 × $50 - $4000 = $1000
	wantUnrealized := decimal.NewFromInt(100).Mul(decimal.NewFromFloat(50)).Sub(acbTotal)
	if !pos.UnrealizedGainCAD.Equal(wantUnrealized) {
		t.Errorf("UnrealizedGainCAD: got %s, want %s", pos.UnrealizedGainCAD, wantUnrealized)
	}
}

func TestPortfolioService_Build_NoPrice(t *testing.T) {
	// Security with no last_price_cad: HasPrice=false, CurrentValue=0.
	sec := &security.Security{ID: 1, Ticker: "XYZ.TO", Exchange: "TSX", Currency: "CAD", LastPriceCAD: nil}
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, Type: transaction.TypeBuy, TradeDate: time.Now(), Quantity: d("10"), PriceCAD: d("100"), CommissionCAD: d("0")},
	}
	txStore := &portfolioTxStore{
		mockTxStore: &mockTxStore{
			nonRegistered: map[int64][]*transaction.Transaction{1: txs},
			allAccounts:   map[int64][]*transaction.Transaction{1: txs},
		},
		distinctIDs: []int64{1},
	}
	secStore := &mockSecStore{secs: map[int64]*security.Security{1: sec}}
	acbSvc := service.NewACBService(txStore)
	svc := service.NewPortfolioService(txStore, secStore, acbSvc)

	positions, summary, err := svc.Build(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[0].HasPrice {
		t.Error("HasPrice should be false when no price set")
	}
	if !positions[0].CurrentValueCAD.IsZero() {
		t.Errorf("CurrentValueCAD should be zero without price, got %s", positions[0].CurrentValueCAD)
	}
	if !summary.TotalValueCAD.IsZero() {
		t.Error("summary TotalValueCAD should be zero without price")
	}
	// ACB still tracked
	if summary.TotalNonRegACB.String() != "1000" {
		t.Errorf("summary TotalNonRegACB: got %s, want 1000", summary.TotalNonRegACB)
	}
}

func TestPortfolioService_Build_ZeroSharesExcluded(t *testing.T) {
	// Bought and fully sold: net shares = 0, should not appear in positions.
	sec := &security.Security{ID: 1, Ticker: "SOLD.TO", Exchange: "TSX", Currency: "CAD"}
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, Type: transaction.TypeBuy, TradeDate: time.Now(), Quantity: d("50"), PriceCAD: d("10"), CommissionCAD: d("0")},
		{ID: 2, SecurityID: 1, Type: transaction.TypeSell, TradeDate: time.Now(), Quantity: d("50"), PriceCAD: d("12"), CommissionCAD: d("0")},
	}
	txStore := &portfolioTxStore{
		mockTxStore: &mockTxStore{
			nonRegistered: map[int64][]*transaction.Transaction{1: txs},
			allAccounts:   map[int64][]*transaction.Transaction{1: txs},
		},
		distinctIDs: []int64{1},
	}
	secStore := &mockSecStore{secs: map[int64]*security.Security{1: sec}}
	acbSvc := service.NewACBService(txStore)
	svc := service.NewPortfolioService(txStore, secStore, acbSvc)

	positions, _, err := svc.Build(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 0 {
		t.Errorf("expected 0 positions for fully sold security, got %d", len(positions))
	}
}
