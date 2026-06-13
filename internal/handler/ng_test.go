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

func seedNGPair(t *testing.T, env *testEnv) (giveID, recvID int64) {
	t.Helper()
	ctx := context.Background()

	acc := &account.Account{UserID: env.userID, Name: "Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	if err := env.accounts.Create(ctx, acc); err != nil {
		t.Fatalf("create account: %v", err)
	}

	dlr := &security.Security{Ticker: "DLR", Exchange: "TSX", Name: "DLR ETF", Type: security.TypeETF, Currency: "CAD"}
	if err := env.securities.Create(ctx, dlr); err != nil {
		t.Fatalf("create DLR security: %v", err)
	}
	dlru := &security.Security{Ticker: "DLR.U", Exchange: "TSX", Name: "DLR.U ETF", Type: security.TypeETF, Currency: "USD"}
	if err := env.securities.Create(ctx, dlru); err != nil {
		t.Fatalf("create DLR.U security: %v", err)
	}

	date := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	qty := decimal.NewFromInt(200)
	price := decimal.NewFromInt(10)

	give := &transaction.Transaction{
		AccountID: acc.ID, SecurityID: dlr.ID, Type: transaction.TypeTransferOut,
		TradeDate: date, SettledDate: date,
		Quantity: qty, PriceCAD: price,
		Source: transaction.SourceManual,
	}
	if err := env.transactions.Create(ctx, give); err != nil {
		t.Fatalf("create give tx: %v", err)
	}

	recv := &transaction.Transaction{
		AccountID: acc.ID, SecurityID: dlru.ID, Type: transaction.TypeJournal,
		TradeDate: date, SettledDate: date,
		Quantity: qty, PriceCAD: price,
		Source: transaction.SourceManual,
	}
	if err := env.transactions.Create(ctx, recv); err != nil {
		t.Fatalf("create recv tx: %v", err)
	}
	return give.ID, recv.ID
}

func TestNGHandler_Preview_NoPairs(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/questrade/ng", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No unlinked Norbert") {
		t.Error("expected 'No unlinked Norbert' in response body")
	}
}

func TestNGHandler_Preview_WithPairs(t *testing.T) {
	env := newTestEnv(t)
	seedNGPair(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/questrade/ng", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Found") {
		t.Error("expected 'Found' in response body")
	}
	if !strings.Contains(body, "Link 1 Pair") {
		t.Error("expected 'Link 1 Pair' confirm button")
	}
}

func TestNGHandler_Link_NoPairs(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodPost, "/questrade/ng", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/questrade?ng=0" {
		t.Errorf("redirect: got %q want /questrade?ng=0", rr.Header().Get("Location"))
	}
}

func TestNGHandler_Link_WithPairs(t *testing.T) {
	env := newTestEnv(t)
	giveID, recvID := seedNGPair(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader(fmt.Sprintf("give_id=%d&recv_id=%d", giveID, recvID))
	req := env.newRequest(http.MethodPost, "/questrade/ng", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/questrade?ng=1" {
		t.Errorf("redirect: got %q want /questrade?ng=1", rr.Header().Get("Location"))
	}
}

func TestNGHandler_Link_IgnoresUnreviewedPairs(t *testing.T) {
	env := newTestEnv(t)
	seedNGPair(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	// Submit with pair IDs that don't match the seeded pair → nothing linked.
	body := strings.NewReader("give_id=9999&recv_id=9998")
	req := env.newRequest(http.MethodPost, "/questrade/ng", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/questrade?ng=0" {
		t.Errorf("redirect: got %q want /questrade?ng=0", rr.Header().Get("Location"))
	}
}

func TestNGHandler_RequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/questrade/ng"},
		{http.MethodPost, "/questrade/ng"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, http.NoBody)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("%s %s: got %d want 303", tc.method, tc.path, rr.Code)
		}
		if rr.Header().Get("Location") != "/login" {
			t.Errorf("%s %s: redirect got %q want /login", tc.method, tc.path, rr.Header().Get("Location"))
		}
	}
}
