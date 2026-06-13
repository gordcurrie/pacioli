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

func seedACBFixture(t *testing.T, env *testEnv) *security.Security {
	t.Helper()
	ctx := context.Background()
	acc := &account.Account{UserID: env.userID, Name: "Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	if err := env.accounts.Create(ctx, acc); err != nil {
		t.Fatalf("create account: %v", err)
	}
	sec := &security.Security{Ticker: "XYZ", Exchange: "TSX", Name: "XYZ Co", Type: security.TypeEquity, Currency: "CAD"}
	if err := env.securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	tx := &transaction.Transaction{
		AccountID: acc.ID, SecurityID: sec.ID, Type: transaction.TypeBuy,
		TradeDate: date, SettledDate: date,
		Quantity: decimal.NewFromInt(100), PriceCAD: decimal.NewFromInt(10),
		Source: transaction.SourceManual,
	}
	if err := env.transactions.Create(ctx, tx); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	return sec
}

func TestACBHandler_Index_Empty(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestACBHandler_Index_WithPositions(t *testing.T) {
	env := newTestEnv(t)
	sec := seedACBFixture(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), sec.Ticker) {
		t.Errorf("response missing ticker %q", sec.Ticker)
	}
}

func TestACBHandler_ListACB(t *testing.T) {
	env := newTestEnv(t)
	sec := seedACBFixture(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/acb", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), sec.Ticker) {
		t.Errorf("response missing ticker %q", sec.Ticker)
	}
}

func TestACBHandler_ShowACB_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	sec := seedACBFixture(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/acb/%d", sec.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), sec.Ticker) {
		t.Errorf("response missing ticker %q", sec.Ticker)
	}
}

func TestACBHandler_ShowACB_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/acb/9999", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestACBHandler_ShowACB_BadID(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/acb/notanid", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestACBHandler_RequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	for _, path := range []string{"/", "/acb", "/acb/1"} {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("path %s: got %d want 303", path, rr.Code)
		}
		if rr.Header().Get("Location") != "/login" {
			t.Errorf("path %s: redirect to %q want /login", path, rr.Header().Get("Location"))
		}
	}
}
