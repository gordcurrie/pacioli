package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/distribution"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// mockDistStore is a minimal in-memory distribution.Store for testing.
type mockDistStore struct {
	byYear           map[int][]*distribution.Distribution
	bySecurity       map[int64][]*distribution.Distribution
	errListByTaxYear error
}

func (m *mockDistStore) Upsert(_ context.Context, _ *distribution.Distribution) error { return nil }
func (m *mockDistStore) GetBySecurityYear(_ context.Context, _ int64, _ int) (*distribution.Distribution, error) {
	return nil, nil
}
func (m *mockDistStore) ListBySecurity(_ context.Context, secID int64) ([]*distribution.Distribution, error) {
	return m.bySecurity[secID], nil
}
func (m *mockDistStore) ListByTaxYear(_ context.Context, taxYear int) ([]*distribution.Distribution, error) {
	if m.errListByTaxYear != nil {
		return nil, m.errListByTaxYear
	}
	return m.byYear[taxYear], nil
}

func TestROCService_PreviewROC_NoDistributions(t *testing.T) {
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{},
		allAccounts:   map[int64][]*transaction.Transaction{},
	}
	distStore := &mockDistStore{byYear: map[int][]*distribution.Distribution{}}
	svc := service.NewROCService(store, distStore, &mockSecStore{secs: map[int64]*security.Security{}})

	rows, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestROCService_PreviewROC_ZeroROC_Skipped(t *testing.T) {
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX"}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.Zero}},
		},
	}
	store := &mockTxStore{nonRegistered: map[int64][]*transaction.Transaction{}}
	svc := service.NewROCService(store, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})

	rows, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("zero ROC distribution should be skipped, got %d rows", len(rows))
	}
}

func TestROCService_PreviewROC_PendingRow(t *testing.T) {
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX"}
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
			TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(10), CommissionCAD: decimal.Zero},
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.15)}},
		},
	}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{1: txs},
	}
	svc := service.NewROCService(store, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})

	rows, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.AlreadyApplied {
		t.Error("expected AlreadyApplied=false")
	}
	if !row.UnitsHeld.Equal(decimal.NewFromInt(100)) {
		t.Errorf("units held: got %s want 100", row.UnitsHeld)
	}
	expectedROC := decimal.NewFromFloat(0.15).Mul(decimal.NewFromInt(100))
	if !row.TotalROC.Equal(expectedROC) {
		t.Errorf("total ROC: got %s want %s", row.TotalROC, expectedROC)
	}
}

func TestROCService_PreviewROC_AlreadyApplied(t *testing.T) {
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX"}
	yearEnd := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
			TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(10), CommissionCAD: decimal.Zero},
		{ID: 2, SecurityID: 1, AccountID: 10, Type: transaction.TypeROCAdjustment,
			TradeDate: yearEnd, Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(0.15), CommissionCAD: decimal.Zero},
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.15)}},
		},
	}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{1: txs},
	}
	svc := service.NewROCService(store, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})

	rows, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].AlreadyApplied {
		t.Error("expected AlreadyApplied=true")
	}
}

func TestROCService_PreviewROC_NoSharesHeld(t *testing.T) {
	// Bought and sold all shares before year-end — units held = 0
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX"}
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
			TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(10), CommissionCAD: decimal.Zero},
		{ID: 2, SecurityID: 1, AccountID: 10, Type: transaction.TypeSell,
			TradeDate: date("2023-06-01"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(12), CommissionCAD: decimal.Zero},
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.15)}},
		},
	}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{1: txs},
	}
	svc := service.NewROCService(store, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})

	rows, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].UnitsHeld.IsZero() {
		t.Errorf("expected 0 units held after full sell, got %s", rows[0].UnitsHeld)
	}
	if !rows[0].TotalROC.IsZero() {
		t.Errorf("expected 0 total ROC with no holdings, got %s", rows[0].TotalROC)
	}
}

func TestROCService_ApplyROC_CreatesTransaction(t *testing.T) {
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX", Currency: "CAD"}
	var created []*transaction.Transaction

	// custom store that captures Create calls
	captureTxStore := &capturingTxStore{
		mockTxStore: &mockTxStore{
			nonRegistered: map[int64][]*transaction.Transaction{
				1: {
					{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
						TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
						PriceCAD: decimal.NewFromFloat(10), CommissionCAD: decimal.Zero},
				},
			},
		},
		created: &created,
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.15)}},
		},
	}
	svc := service.NewROCService(captureTxStore, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})

	if err := svc.ApplyROC(context.Background(), 1, 2023); err != nil {
		t.Fatalf("ApplyROC: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("expected 1 transaction created, got %d", len(created))
	}
	tx := created[0]
	if tx.Type != transaction.TypeROCAdjustment {
		t.Errorf("type: got %s want roc_adjustment", tx.Type)
	}
	if !tx.Quantity.Equal(decimal.NewFromInt(100)) {
		t.Errorf("quantity: got %s want 100", tx.Quantity)
	}
	if tx.AccountID != 10 {
		t.Errorf("accountID: got %d want 10", tx.AccountID)
	}
}

// capturingTxStore wraps mockTxStore and records Create calls.
type capturingTxStore struct {
	*mockTxStore
	created *[]*transaction.Transaction
}

func (c *capturingTxStore) Create(_ context.Context, tx *transaction.Transaction) error {
	*c.created = append(*c.created, tx)
	return nil
}

func TestROCService_PreviewROC_ListByTaxYearError(t *testing.T) {
	distStore := &mockDistStore{errListByTaxYear: fmt.Errorf("db error")}
	svc := service.NewROCService(&mockTxStore{}, distStore, &mockSecStore{})
	_, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err == nil {
		t.Fatal("expected error from ListByTaxYear, got nil")
	}
}

func TestROCService_PreviewROC_SecStoreError(t *testing.T) {
	// Distribution with positive ROC, security has transactions, but secStore.GetByID fails.
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
			TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(10), CommissionCAD: decimal.Zero},
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.10)}},
		},
	}
	store := &mockTxStore{nonRegistered: map[int64][]*transaction.Transaction{1: txs}}
	svc := service.NewROCService(store, distStore, &mockSecStore{errGetByID: fmt.Errorf("sec not found")})
	_, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err == nil {
		t.Fatal("expected error from secStore.GetByID, got nil")
	}
}

func TestROCService_PreviewROC_ListNonRegTxsError(t *testing.T) {
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.10)}},
		},
	}
	store := &mockTxStore{errListNonReg: fmt.Errorf("list error")}
	svc := service.NewROCService(store, distStore, &mockSecStore{})
	_, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err == nil {
		t.Fatal("expected error from ListBySecurityNonRegistered, got nil")
	}
}

func TestROCService_PreviewROC_NoTxsForSecurity_Skipped(t *testing.T) {
	// Positive ROCPerUnit but no transactions for the security — row should be skipped.
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.10)}},
		},
	}
	// security 1 not in nonRegistered — returns nil (len=0)
	store := &mockTxStore{nonRegistered: map[int64][]*transaction.Transaction{}}
	svc := service.NewROCService(store, distStore, &mockSecStore{secs: map[int64]*security.Security{}})
	rows, err := svc.PreviewROC(context.Background(), 1, 2023)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows when security has no txs, got %d", len(rows))
	}
}

func TestROCService_ApplyROC_PreviewError(t *testing.T) {
	distStore := &mockDistStore{errListByTaxYear: fmt.Errorf("db error")}
	svc := service.NewROCService(&mockTxStore{}, distStore, &mockSecStore{})
	err := svc.ApplyROC(context.Background(), 1, 2023)
	if err == nil {
		t.Fatal("expected error propagated from PreviewROC, got nil")
	}
}

func TestROCService_ApplyROC_AlreadyApplied_Skipped(t *testing.T) {
	// AlreadyApplied row — no transaction should be created.
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX", Currency: "CAD"}
	yearEnd := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
			TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(10), CommissionCAD: decimal.Zero},
		{ID: 2, SecurityID: 1, AccountID: 10, Type: transaction.TypeROCAdjustment,
			TradeDate: yearEnd, Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(0.15), CommissionCAD: decimal.Zero},
	}
	var created []*transaction.Transaction
	capStore := &capturingTxStore{
		mockTxStore: &mockTxStore{nonRegistered: map[int64][]*transaction.Transaction{1: txs}},
		created:     &created,
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.15)}},
		},
	}
	svc := service.NewROCService(capStore, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})
	if err := svc.ApplyROC(context.Background(), 1, 2023); err != nil {
		t.Fatalf("ApplyROC: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected 0 transactions for AlreadyApplied row, got %d", len(created))
	}
}

func TestROCService_ApplyROC_NonCAD_Skipped(t *testing.T) {
	// Currency != "CAD" — no transaction should be created.
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "NYSE", Currency: "USD"}
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
			TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(13), CommissionCAD: decimal.Zero},
	}
	var created []*transaction.Transaction
	capStore := &capturingTxStore{
		mockTxStore: &mockTxStore{nonRegistered: map[int64][]*transaction.Transaction{1: txs}},
		created:     &created,
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.10)}},
		},
	}
	svc := service.NewROCService(capStore, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})
	if err := svc.ApplyROC(context.Background(), 1, 2023); err != nil {
		t.Fatalf("ApplyROC: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("expected 0 transactions for non-CAD security, got %d", len(created))
	}
}

func TestROCService_ApplyROC_CreateError(t *testing.T) {
	sec := &security.Security{ID: 1, Ticker: "XYZ", Exchange: "TSX", Currency: "CAD"}
	txs := []*transaction.Transaction{
		{ID: 1, SecurityID: 1, AccountID: 10, Type: transaction.TypeBuy,
			TradeDate: date("2023-01-15"), Quantity: decimal.NewFromInt(100),
			PriceCAD: decimal.NewFromFloat(10), CommissionCAD: decimal.Zero},
	}
	store := &mockTxStore{
		nonRegistered: map[int64][]*transaction.Transaction{1: txs},
		errCreate:     fmt.Errorf("create failed"),
	}
	distStore := &mockDistStore{
		byYear: map[int][]*distribution.Distribution{
			2023: {{SecurityID: 1, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.15)}},
		},
	}
	svc := service.NewROCService(store, distStore, &mockSecStore{secs: map[int64]*security.Security{1: sec}})
	err := svc.ApplyROC(context.Background(), 1, 2023)
	if err == nil {
		t.Fatal("expected error from Create, got nil")
	}
}
