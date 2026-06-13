package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type txFixture struct {
	account  *account.Account
	security *security.Security
	tx       *transaction.Transaction
}

func seedCADTransaction(t *testing.T, env *testEnv) txFixture {
	t.Helper()
	ctx := context.Background()
	acc := &account.Account{UserID: env.userID, Name: "CAD Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	if err := env.accounts.Create(ctx, acc); err != nil {
		t.Fatalf("seedCADTransaction account: %v", err)
	}
	sec := &security.Security{Ticker: "ZCN", Exchange: "TSX", Name: "BMO CAD ETF", Type: security.TypeETF, Currency: "CAD"}
	if err := env.securities.Create(ctx, sec); err != nil {
		t.Fatalf("seedCADTransaction security: %v", err)
	}
	date := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	tx := &transaction.Transaction{
		AccountID: acc.ID, SecurityID: sec.ID, Type: transaction.TypeBuy,
		TradeDate: date, SettledDate: date,
		Quantity: decimal.NewFromInt(50), PriceCAD: decimal.NewFromFloat(15),
		Source: transaction.SourceManual,
	}
	if err := env.transactions.Create(ctx, tx); err != nil {
		t.Fatalf("seedCADTransaction tx: %v", err)
	}
	return txFixture{acc, sec, tx}
}

func seedUSDTransaction(t *testing.T, env *testEnv) txFixture {
	t.Helper()
	ctx := context.Background()
	acc := &account.Account{UserID: env.userID, Name: "USD Margin", Type: account.TypeMargin, Broker: "B", Currency: "USD"}
	if err := env.accounts.Create(ctx, acc); err != nil {
		t.Fatalf("seedUSDTransaction account: %v", err)
	}
	sec := &security.Security{Ticker: "SPY", Exchange: "ARCA", Name: "SPDR S&P 500", Type: security.TypeETF, Currency: "USD"}
	if err := env.securities.Create(ctx, sec); err != nil {
		t.Fatalf("seedUSDTransaction security: %v", err)
	}
	date := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	fxRate := decimal.NewFromFloat(1.35)
	priceNative := decimal.NewFromFloat(400.00)
	commNative := decimal.NewFromFloat(1.00)
	tx := &transaction.Transaction{
		AccountID:        acc.ID,
		SecurityID:       sec.ID,
		Type:             transaction.TypeBuy,
		TradeDate:        date,
		SettledDate:      date,
		Quantity:         decimal.NewFromInt(10),
		PriceNative:      priceNative,
		CommissionNative: commNative,
		FXRate:           &fxRate,
		PriceCAD:         priceNative.Mul(fxRate),
		CommissionCAD:    commNative.Mul(fxRate),
		Source:           transaction.SourceManual,
	}
	if err := env.transactions.Create(ctx, tx); err != nil {
		t.Fatalf("seedUSDTransaction tx: %v", err)
	}
	return txFixture{acc, sec, tx}
}

func TestTransactionHandler_ListTransactions_Empty(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/transactions", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestTransactionHandler_ListTransactions_WithData(t *testing.T) {
	env := newTestEnv(t)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/transactions", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), f.security.Ticker) {
		t.Errorf("response missing ticker %q", f.security.Ticker)
	}
}

func TestTransactionHandler_NewTransaction(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/transactions/new", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestTransactionHandler_CreateTransaction_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := fmt.Sprintf(
		"account_id=%d&security_id=%d&type=buy&trade_date=2024-06-01&settled_date=2024-06-03&quantity=100&price_native=10.00&commission_native=5.00&fx_rate=&notes=",
		f.account.ID, f.security.ID,
	)
	req := env.newRequest(http.MethodPost, "/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303; body: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/transactions" {
		t.Errorf("redirect: got %q want /transactions", rr.Header().Get("Location"))
	}
}

func TestTransactionHandler_CreateTransaction_USDRequiresFXRate(t *testing.T) {
	env := newTestEnv(t)
	f := seedUSDTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	// missing fx_rate for USD security
	body := fmt.Sprintf(
		"account_id=%d&security_id=%d&type=buy&trade_date=2024-06-01&settled_date=2024-06-03&quantity=10&price_native=400.00&commission_native=1.00&fx_rate=&notes=",
		f.account.ID, f.security.ID,
	)
	req := env.newRequest(http.MethodPost, "/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (form re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "FX rate required") {
		t.Errorf("response missing 'FX rate required': %s", rr.Body.String())
	}
}

func TestTransactionHandler_CreateTransaction_NoAccount(t *testing.T) {
	env := newTestEnv(t)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := fmt.Sprintf(
		"account_id=0&security_id=%d&type=buy&trade_date=2024-06-01&settled_date=2024-06-03&quantity=10&price_native=10.00&commission_native=0&fx_rate=",
		f.security.ID,
	)
	req := env.newRequest(http.MethodPost, "/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("no account: got %d want 200 (form re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "select an account") {
		t.Errorf("response missing 'select an account': %s", rr.Body.String())
	}
}

func TestTransactionHandler_CreateTransaction_InvalidQuantity(t *testing.T) {
	env := newTestEnv(t)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := fmt.Sprintf(
		"account_id=%d&security_id=%d&type=buy&trade_date=2024-06-01&settled_date=2024-06-03&quantity=-5&price_native=10.00&commission_native=0&fx_rate=",
		f.account.ID, f.security.ID,
	)
	req := env.newRequest(http.MethodPost, "/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("bad qty: got %d want 200 (form re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "quantity must be greater than zero") {
		t.Errorf("response missing qty validation error: %s", rr.Body.String())
	}
}

func TestTransactionHandler_CreateTransaction_OtherUserAccount(t *testing.T) {
	env := newTestEnv(t)
	otherID := seedOtherUser(t, env)
	otherAcc := seedAccount(t, env, otherID)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := fmt.Sprintf(
		"account_id=%d&security_id=%d&type=buy&trade_date=2024-06-01&settled_date=2024-06-03&quantity=10&price_native=10.00&commission_native=0&fx_rate=",
		otherAcc.ID, f.security.ID,
	)
	req := env.newRequest(http.MethodPost, "/transactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("cross-user account: got %d want 200 (form error)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid account") {
		t.Errorf("response missing 'invalid account': %s", rr.Body.String())
	}
}

func TestTransactionHandler_DeleteTransaction_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, fmt.Sprintf("/transactions/%d", f.tx.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestTransactionHandler_DeleteTransaction_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, "/transactions/9999", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestTransactionHandler_DeleteTransaction_OtherUserTransaction(t *testing.T) {
	env := newTestEnv(t)
	otherID := seedOtherUser(t, env)
	ctx := context.Background()
	otherAcc := &account.Account{UserID: otherID, Name: "Other Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	if err := env.accounts.Create(ctx, otherAcc); err != nil {
		t.Fatalf("create other account: %v", err)
	}
	sec := seedSecurity(t, env)
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	tx := &transaction.Transaction{
		AccountID: otherAcc.ID, SecurityID: sec.ID, Type: transaction.TypeBuy,
		TradeDate: date, SettledDate: date,
		Quantity: decimal.NewFromInt(10), PriceCAD: decimal.NewFromInt(5),
		Source: transaction.SourceManual,
	}
	if err := env.transactions.Create(ctx, tx); err != nil {
		t.Fatalf("create other tx: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, fmt.Sprintf("/transactions/%d", tx.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-user: got %d want 404", rr.Code)
	}
}

func TestTransactionHandler_EditTransactionFXForm_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	f := seedUSDTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/transactions/%d/fx/edit", f.tx.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "fx_rate") {
		t.Errorf("response missing fx_rate input: %s", rr.Body.String())
	}
}

func TestTransactionHandler_EditTransactionFXForm_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/transactions/9999/fx/edit", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestTransactionHandler_EditTransactionFXForm_OtherUser(t *testing.T) {
	env := newTestEnv(t)
	otherID := seedOtherUser(t, env)
	ctx := context.Background()
	otherAcc := &account.Account{UserID: otherID, Name: "Other USD", Type: account.TypeMargin, Broker: "B", Currency: "USD"}
	if err := env.accounts.Create(ctx, otherAcc); err != nil {
		t.Fatalf("create other account: %v", err)
	}
	sec := &security.Security{Ticker: "QQQ", Exchange: "ARCA", Name: "Invesco QQQ", Type: security.TypeETF, Currency: "USD"}
	if err := env.securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fxRate := decimal.NewFromFloat(1.35)
	tx := &transaction.Transaction{
		AccountID: otherAcc.ID, SecurityID: sec.ID, Type: transaction.TypeBuy,
		TradeDate: date, SettledDate: date,
		Quantity: decimal.NewFromInt(5), PriceNative: decimal.NewFromFloat(300),
		FXRate: &fxRate, PriceCAD: decimal.NewFromFloat(300).Mul(fxRate),
		Source: transaction.SourceManual,
	}
	if err := env.transactions.Create(ctx, tx); err != nil {
		t.Fatalf("create other tx: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/transactions/%d/fx/edit", tx.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-user: got %d want 404", rr.Code)
	}
}

func TestTransactionHandler_TransactionFXCell_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	f := seedUSDTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/transactions/%d/fx/cell", f.tx.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "1.3500") {
		t.Errorf("response missing fx rate '1.3500': %s", rr.Body.String())
	}
}

func TestTransactionHandler_TransactionFXCell_CADHasNoRate(t *testing.T) {
	env := newTestEnv(t)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/transactions/%d/fx/cell", f.tx.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	// CAD transactions render "—" (no rate)
	if !strings.Contains(rr.Body.String(), "—") {
		t.Errorf("CAD tx cell: expected em dash for no rate, got: %s", rr.Body.String())
	}
}

func TestTransactionHandler_UpdateTransactionFX_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	f := seedUSDTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("fx_rate=1.38")
	req := env.newRequest(http.MethodPost, fmt.Sprintf("/transactions/%d/fx", f.tx.ID), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "1.3800") {
		t.Errorf("response missing updated fx rate '1.3800': %s", rr.Body.String())
	}
}

func TestTransactionHandler_UpdateTransactionFX_CADTransaction(t *testing.T) {
	env := newTestEnv(t)
	f := seedCADTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("fx_rate=1.35")
	req := env.newRequest(http.MethodPost, fmt.Sprintf("/transactions/%d/fx", f.tx.ID), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("CAD tx fx update: got %d want 400", rr.Code)
	}
}

func TestTransactionHandler_UpdateTransactionFX_InvalidRate(t *testing.T) {
	env := newTestEnv(t)
	f := seedUSDTransaction(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("fx_rate=0")
	req := env.newRequest(http.MethodPost, fmt.Sprintf("/transactions/%d/fx", f.tx.ID), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("zero rate: got %d want 400", rr.Code)
	}
}

func TestTransactionHandler_RequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	paths := []struct{ method, path string }{
		{http.MethodGet, "/transactions"},
		{http.MethodGet, "/transactions/new"},
		{http.MethodPost, "/transactions"},
		{http.MethodDelete, "/transactions/1"},
		{http.MethodGet, "/transactions/1/fx/edit"},
		{http.MethodGet, "/transactions/1/fx/cell"},
		{http.MethodPost, "/transactions/1/fx"},
	}
	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, http.NoBody)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("%s %s: got %d want 303", p.method, p.path, rr.Code)
		}
		if rr.Header().Get("Location") != "/login" {
			t.Errorf("%s %s: redirect to %q want /login", p.method, p.path, rr.Header().Get("Location"))
		}
	}
}
