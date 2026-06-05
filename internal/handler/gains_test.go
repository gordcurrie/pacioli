package handler_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/handler"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/session"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/gordcurrie/pacioli/internal/user"
	"github.com/gordcurrie/pacioli/web"
	"github.com/shopspring/decimal"
)

type testEnv struct {
	handler      *handler.Handler
	accounts     *sqlite.AccountStore
	securities   *sqlite.SecurityStore
	transactions *sqlite.TransactionStore
	userID       int64
	rawToken     string // session cookie value
}

// newRequest creates a test request with the test session cookie attached.
func (env *testEnv) newRequest(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.AddCookie(&http.Cookie{Name: "pacioli_session", Value: env.rawToken})
	return req
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	txStore := sqlite.NewTransactionStore(db)
	secStore := sqlite.NewSecurityStore(db)
	distStore := sqlite.NewDistributionStore(db)
	auditStore := sqlite.NewAuditStore(db)
	accountStore := sqlite.NewAccountStore(db)
	userStore := sqlite.NewUserStore(db, nil)
	sessionStore := sqlite.NewSessionStore(db)

	ctx := context.Background()

	// Create a test user with a known password hash (cost 4 for speed).
	userID, err := userStore.Create(ctx, &user.User{
		Email:        "test@example.com",
		PasswordHash: "$2a$04$testhashplaceholderxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", // dummy; not used in handler tests
		IsAdmin:      true,
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}

	// Create a fully-verified session.
	rawToken := "test-session-token-for-handler-tests"
	if err := sessionStore.Create(ctx, &session.Session{
		UserID:       userID,
		TokenHash:    sqlite.HashToken(rawToken),
		TOTPVerified: true,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create test session: %v", err)
	}

	acbSvc := service.NewACBService(txStore)
	gainsSvc := service.NewGainsService(txStore, secStore)
	rocSvc := service.NewROCService(txStore, distStore, secStore)

	h, err := handler.New(&handler.Config{
		Accounts:     accountStore,
		Securities:   secStore,
		Transactions: txStore,
		Audits:       auditStore,
		Users:        userStore,
		Sessions:     sessionStore,
		ACBSvc:       acbSvc,
		GainsSvc:     gainsSvc,
		ROCSvc:       rocSvc,
		Logger:       slog.Default(),
		TemplateFS:   web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return &testEnv{
		handler:      h,
		accounts:     accountStore,
		securities:   secStore,
		transactions: txStore,
		userID:       userID,
		rawToken:     rawToken,
	}
}


func TestGainsHandler_ShowGainsForYear(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/gains/2024", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Capital Gains") {
		t.Error("response body missing 'Capital Gains'")
	}
	if !strings.Contains(body, "2024") {
		t.Error("response body missing year '2024'")
	}
}

func TestGainsHandler_ShowGainsForYear_InvalidYear(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	for _, path := range []string{"/gains/abc", "/gains/1989", "/gains/2101"} {
		req := env.newRequest(http.MethodGet, path, http.NoBody)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("path %s: got %d want 404", path, rr.Code)
		}
	}
}

func TestGainsHandler_ListGains_RedirectsToCurrentYear(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/gains", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/gains/") {
		t.Errorf("redirect location %q should start with /gains/", loc)
	}
}

func TestGainsHandler_ExportCSV(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/gains/2024/export", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type: got %q want text/csv", ct)
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "capital-gains-2024.csv") {
		t.Errorf("Content-Disposition: got %q, want filename capital-gains-2024.csv", cd)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Date,Ticker") {
		t.Errorf("CSV missing header row, got: %s", body)
	}
}

func TestGainsHandler_PreviewROC(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/roc/2024", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "ROC Adjustments") {
		t.Error("response body missing 'ROC Adjustments'")
	}
}

func TestGainsHandler_ShowGainsDetail_InvalidArgs(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	cases := []struct {
		path string
		want int
	}{
		{"/gains/abc/1", http.StatusNotFound},
		{"/gains/1989/1", http.StatusNotFound},
		{"/gains/2101/1", http.StatusNotFound},
		{"/gains/2024/0", http.StatusNotFound},
		{"/gains/2024/-1", http.StatusNotFound},
		{"/gains/2024/abc", http.StatusNotFound},
	}
	for _, tc := range cases {
		req := env.newRequest(http.MethodGet, tc.path, http.NoBody)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Errorf("path %s: got %d want %d", tc.path, rr.Code, tc.want)
		}
	}
}

func TestGainsHandler_ShowGainsDetail_SecurityNotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/gains/2024/9999", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d want 404", rr.Code)
	}
}

func TestGainsHandler_ShowGainsDetail_NoDisposalsInYear(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	sec := &security.Security{Ticker: "HOLD", Exchange: "TSX", Name: "Hold Co", Type: security.TypeEquity, Currency: "CAD"}
	if err := env.securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/gains/2024/%d", sec.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "HOLD") {
		t.Error("response should contain security ticker")
	}
}

func TestGainsHandler_ShowGainsDetail_WithDisposals(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	acc := &account.Account{UserID: env.userID, Name: "Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	if err := env.accounts.Create(ctx, acc); err != nil {
		t.Fatalf("create account: %v", err)
	}
	sec := &security.Security{Ticker: "XYZ", Exchange: "TSX", Name: "XYZ Co", Type: security.TypeEquity, Currency: "CAD"}
	if err := env.securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}

	tradeDate := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	for _, typ := range []transaction.Type{transaction.TypeBuy, transaction.TypeSell} {
		tx := &transaction.Transaction{
			AccountID: acc.ID, SecurityID: sec.ID, Type: typ,
			TradeDate: tradeDate, SettledDate: tradeDate,
			Quantity: decimal.NewFromInt(10), PriceCAD: decimal.NewFromFloat(20),
			Source: transaction.SourceManual,
		}
		if err := env.transactions.Create(ctx, tx); err != nil {
			t.Fatalf("create tx: %v", err)
		}
		tradeDate = tradeDate.AddDate(0, 3, 0)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/gains/2024/%d", sec.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "XYZ") {
		t.Error("response missing ticker XYZ")
	}
	if !strings.Contains(body, "buy") {
		t.Error("response missing 'buy' row")
	}
	if !strings.Contains(body, "sell") {
		t.Error("response missing 'sell' row")
	}
}

func TestGainsHandler_ApplyROC_RedirectsAfterApply(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodPost, "/roc/2024", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/roc/2024" {
		t.Errorf("redirect location: got %q want /roc/2024", rr.Header().Get("Location"))
	}
}

// Verify unauthenticated requests redirect to /login.
func TestGainsHandler_RequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/gains/2024", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("redirect: got %q want /login", rr.Header().Get("Location"))
	}
}
