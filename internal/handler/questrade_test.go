package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/handler"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/session"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/gordcurrie/pacioli/internal/user"
	"github.com/gordcurrie/pacioli/web"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"
)

// fakeQTStore is an in-memory questrade.Store for testing.
type fakeQTStore struct {
	tokens map[int64]questrade.Token
}

func (f *fakeQTStore) Save(_ context.Context, userID int64, tok questrade.Token) error {
	f.tokens[userID] = tok
	return nil
}

func (f *fakeQTStore) Get(_ context.Context, userID int64) (questrade.Token, error) {
	tok, ok := f.tokens[userID]
	if !ok {
		return questrade.Token{}, errs.ErrNotFound
	}
	return tok, nil
}

func (f *fakeQTStore) Delete(_ context.Context, userID int64) error {
	delete(f.tokens, userID)
	return nil
}

// qtTestEnv is a test harness for Questrade handler tests.
type qtTestEnv struct {
	h            *handler.Handler
	accounts     *sqlite.AccountStore
	securities   *sqlite.SecurityStore
	transactions *sqlite.TransactionStore
	fxStore      *sqlite.FXStore
	userID       int64
	rawToken     string
}

func (e *qtTestEnv) newRequest(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.AddCookie(&http.Cookie{Name: "pacioli_session", Value: e.rawToken})
	return req
}

// newQTTestEnv builds a handler with QTTokens and BOCSvc wired to qtServer.
func newQTTestEnv(t *testing.T, qtServer *httptest.Server) *qtTestEnv {
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
	fxStore := sqlite.NewFXStore(db)

	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), 4)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	userID, err := userStore.Create(ctx, &user.User{
		Email:        "qt@example.com",
		PasswordHash: string(hash),
		IsAdmin:      true,
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}

	const rawToken = "qt-handler-test-session"
	if err := sessionStore.Create(ctx, &session.Session{
		UserID:       userID,
		TokenHash:    sqlite.HashToken(rawToken),
		TOTPVerified: true,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create test session: %v", err)
	}

	qtStore := &fakeQTStore{tokens: map[int64]questrade.Token{
		userID: {
			AccessToken:  questrade.Secret("fake-access"),
			RefreshToken: questrade.Secret("fake-refresh"),
			APIServer:    qtServer.URL,
			ExpiresAt:    time.Now().Add(24 * time.Hour),
		},
	}}

	bocSvc := service.NewBOCFetcher(fxStore)
	acbSvc := service.NewACBService(txStore)
	gainsSvc := service.NewGainsService(txStore, secStore)
	rocSvc := service.NewROCService(txStore, distStore, secStore)
	ngSvc := service.NewNGService(txStore, secStore)
	portfolioSvc := service.NewPortfolioService(txStore, secStore, acbSvc)
	yahooSvc := service.NewYahooFetcher(bocSvc)

	h, err := handler.New(&handler.Config{
		Accounts:     accountStore,
		Securities:   secStore,
		Transactions: txStore,
		Audits:       auditStore,
		Users:        userStore,
		Sessions:     sessionStore,
		QTTokens:     qtStore,
		BOCSvc:       bocSvc,
		ACBSvc:       acbSvc,
		GainsSvc:     gainsSvc,
		ROCSvc:       rocSvc,
		NGSvc:        ngSvc,
		PortfolioSvc: portfolioSvc,
		YahooSvc:     yahooSvc,
		Logger:       slog.Default(),
		TemplateFS:   web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return &qtTestEnv{
		h:            h,
		accounts:     accountStore,
		securities:   secStore,
		transactions: txStore,
		fxStore:      fxStore,
		userID:       userID,
		rawToken:     rawToken,
	}
}

// makeQTServer returns a fake Questrade API server returning the given activities slice.
// It returns empty symbol search results for all /symbols/search requests (auto-create fails).
func makeQTServer(t *testing.T, activities []map[string]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/activities"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"activities": activities})
		case strings.Contains(r.URL.Path, "/symbols/search"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"symbols": []interface{}{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// qtAct builds a Questrade activity JSON map. qty and price are decimal strings (e.g. "10", "34.50").
func qtAct(action, symbol, description, currency, qty, price string, date time.Time) map[string]interface{} {
	ds := date.Format(time.RFC3339)
	qtyD, _ := decimal.NewFromString(qty)
	priceD, _ := decimal.NewFromString(price)
	gross := qtyD.Mul(priceD).Neg().String()
	return map[string]interface{}{
		"tradeDate":      ds,
		"settlementDate": ds,
		"action":         action,
		"symbol":         symbol,
		"description":    description,
		"currency":       currency,
		"quantity":       json.Number(qty),
		"price":          json.Number(price),
		"grossAmount":    json.Number(gross),
		"commission":     json.Number("0"),
		"netAmount":      json.Number(gross),
		"type":           "Trades",
	}
}

// seedFXRate seeds a USD/CAD rate for date into the FX store.
func seedFXRate(t *testing.T, fxStore *sqlite.FXStore, date time.Time, rate string) {
	t.Helper()
	r, _ := decimal.NewFromString(rate)
	if err := fxStore.StoreRate(context.Background(), date, "USD", "CAD", r, "test"); err != nil {
		t.Fatalf("seed fx rate: %v", err)
	}
}

// doPreview fires POST /questrade/preview and returns the recorder.
func doPreview(t *testing.T, env *qtTestEnv, formVals url.Values) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	env.h.Routes(mux)
	body := strings.NewReader(formVals.Encode())
	req := env.newRequest(http.MethodPost, "/questrade/preview", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// baseForm returns common form values for the preview endpoint.
func baseForm(accID int64, date time.Time) url.Values {
	ds := date.Format(time.DateOnly)
	return url.Values{
		"qt_account":      {"QT12345"},
		"pacioli_account": {fmt.Sprint(accID)},
		"start_date":      {ds},
		"end_date":        {ds},
	}
}

// createTestAccount creates a CAD margin account owned by the test user.
func createTestAccount(t *testing.T, ctx context.Context, store *sqlite.AccountStore, userID int64) *account.Account {
	t.Helper()
	acc := &account.Account{
		UserID:   userID,
		Name:     "Test Margin",
		Type:     account.TypeMargin,
		Broker:   "Questrade",
		Currency: "CAD",
	}
	if err := store.Create(ctx, acc); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acc
}

// createTestSecurity creates a security and returns it.
func createTestSecurity(t *testing.T, ctx context.Context, store *sqlite.SecurityStore, ticker, currency string) *security.Security {
	t.Helper()
	sec := &security.Security{
		Ticker:   ticker,
		Name:     ticker + " Corp",
		Exchange: "TSX",
		Type:     security.TypeEquity,
		Currency: currency,
	}
	if err := store.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}
	return sec
}

// --- Tests ---

func TestQuestradePreview_CADSecurityBuy_OKRow(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	acts := []map[string]interface{}{
		qtAct("Buy", "XIU", "", "CAD", "10", "34.50", date),
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	createTestSecurity(t, ctx, env.securities, "XIU", "CAD")

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 ready") {
		t.Errorf("expected '1 ready' in response; body snippet: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "0 flagged") {
		t.Errorf("expected '0 flagged' in response")
	}
}

func TestQuestradePreview_USDSecurityBuy_OKRowWithFXRate(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	acts := []map[string]interface{}{
		qtAct("Buy", "MSFT", "", "USD", "5", "420.00", date),
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	createTestSecurity(t, ctx, env.securities, "MSFT", "USD")
	seedFXRate(t, env.fxStore, date, "1.3700")

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 ready") {
		t.Errorf("expected '1 ready'; got body snippet: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "1.37") {
		t.Errorf("expected FX rate '1.37' in response")
	}
}

// USD security TFI where Questrade reports currency=CAD (book value is CAD equivalent).
// Handler must divide extracted CAD price by BoC rate to get USD price.
func TestQuestradePreview_TFI_USDSecWithCADActivity_ConvertsByBoCRate(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	desc := "SPLV ETF CANACCORD GENUITY CORP. ACCOUNT TRANSFER BOOK VALUE 6850.00"
	acts := []map[string]interface{}{
		qtAct("TFI", "SPLV", desc, "CAD", "100", "0", date),
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	createTestSecurity(t, ctx, env.securities, "SPLV", "USD")
	seedFXRate(t, env.fxStore, date, "1.3700")

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 ready") {
		t.Errorf("expected '1 ready' for converted TFI; got body snippet: %s", body[:min(400, len(body))])
	}
	// FX rate must appear (USD activity → BoC rate shown)
	if !strings.Contains(body, "1.37") {
		t.Errorf("expected FX rate '1.37' in response for USD TFI")
	}
}


func TestQuestradePreview_TFI_NoBoCRate_Flags(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	desc := "SPLV ETF CANACCORD GENUITY CORP. ACCOUNT TRANSFER BOOK VALUE 6850.00"
	acts := []map[string]interface{}{
		qtAct("TFI", "SPLV", desc, "CAD", "100", "0", date),
	}

	// Fake BoC server that returns error.
	bocSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bocSrv.Close()

	qtSrv := makeQTServer(t, acts)

	// Build env manually so we can set bocSvc.BaseURL before constructing the handler.
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	txStore := sqlite.NewTransactionStore(db)
	secStore := sqlite.NewSecurityStore(db)
	distStore := sqlite.NewDistributionStore(db)
	auditStore := sqlite.NewAuditStore(db)
	accountStore := sqlite.NewAccountStore(db)
	userStore := sqlite.NewUserStore(db, nil)
	sessionStore := sqlite.NewSessionStore(db)
	fxStore := sqlite.NewFXStore(db)

	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), 4)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	userID, err := userStore.Create(ctx, &user.User{Email: "qt2@example.com", PasswordHash: string(hash), IsAdmin: true})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	const rawToken = "qt-noboc-session"
	if err := sessionStore.Create(ctx, &session.Session{
		UserID:       userID,
		TokenHash:    sqlite.HashToken(rawToken),
		TOTPVerified: true,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	bocSvc := service.NewBOCFetcher(fxStore)
	bocSvc.BaseURL = bocSrv.URL // force failure

	qtStore := &fakeQTStore{tokens: map[int64]questrade.Token{
		userID: {
			AccessToken:  questrade.Secret("fake-access"),
			RefreshToken: questrade.Secret("fake-refresh"),
			APIServer:    qtSrv.URL,
			ExpiresAt:    time.Now().Add(24 * time.Hour),
		},
	}}

	acbSvc := service.NewACBService(txStore)
	h, err := handler.New(&handler.Config{
		Accounts:     accountStore,
		Securities:   secStore,
		Transactions: txStore,
		Audits:       auditStore,
		Users:        userStore,
		Sessions:     sessionStore,
		QTTokens:     qtStore,
		BOCSvc:       bocSvc,
		ACBSvc:       acbSvc,
		GainsSvc:     service.NewGainsService(txStore, secStore),
		ROCSvc:       service.NewROCService(txStore, distStore, secStore),
		NGSvc:        service.NewNGService(txStore, secStore),
		PortfolioSvc: service.NewPortfolioService(txStore, secStore, acbSvc),
		YahooSvc:     service.NewYahooFetcher(bocSvc),
		Logger:       slog.Default(),
		TemplateFS:   web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	acc := &account.Account{UserID: userID, Name: "Test", Type: account.TypeMargin, Broker: "Questrade", Currency: "CAD"}
	if err := accountStore.Create(ctx, acc); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := secStore.Create(ctx, &security.Security{Ticker: "SPLV", Name: "SPLV", Exchange: "NYSE", Type: security.TypeEquity, Currency: "USD"}); err != nil {
		t.Fatalf("create security: %v", err)
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	form := url.Values{
		"qt_account":      {"QT12345"},
		"pacioli_account": {fmt.Sprint(acc.ID)},
		"start_date":      {date.Format(time.DateOnly)},
		"end_date":        {date.Format(time.DateOnly)},
	}
	req := httptest.NewRequest(http.MethodPost, "/questrade/preview", strings.NewReader(form.Encode()))
	req.AddCookie(&http.Cookie{Name: "pacioli_session", Value: rawToken})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 flagged") {
		t.Errorf("expected '1 flagged' when BoC rate unavailable; got body snippet: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "BoC USD/CAD rate unavailable") {
		t.Errorf("expected 'BoC USD/CAD rate unavailable' in flag message")
	}
}

func TestQuestradePreview_CurrencyMismatch_NonTFI_Flags(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	// Buy with activity.Currency=CAD but security is USD — not a TFI, so mismatch flags.
	acts := []map[string]interface{}{
		qtAct("Buy", "MSFT", "", "CAD", "5", "420.00", date),
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	createTestSecurity(t, ctx, env.securities, "MSFT", "USD")

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 flagged") {
		t.Errorf("expected '1 flagged' for currency mismatch")
	}
	if !strings.Contains(body, "currency mismatch") {
		t.Errorf("expected 'currency mismatch' in flag message; body snippet: %s", body[:min(400, len(body))])
	}
}

func TestQuestradePreview_AlreadyImported_VisibleSkip(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	acts := []map[string]interface{}{
		qtAct("Buy", "XIU", "", "CAD", "10", "34.50", date),
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	sec := createTestSecurity(t, ctx, env.securities, "XIU", "CAD")

	// Pre-create the transaction as already-imported from Questrade.
	priceCAD, _ := decimal.NewFromString("34.50")
	tx := &transaction.Transaction{
		AccountID:   acc.ID,
		SecurityID:  sec.ID,
		Type:        transaction.TypeBuy,
		TradeDate:   date,
		SettledDate: date,
		Quantity:    decimal.NewFromInt(10),
		PriceCAD:    priceCAD,
		Source:      transaction.SourceQuestrade,
	}
	if err := env.transactions.Create(ctx, tx); err != nil {
		t.Fatalf("create existing tx: %v", err)
	}

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 skipped") {
		t.Errorf("expected '1 skipped' for already-imported activity; body snippet: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "already imported") {
		t.Errorf("expected 'already imported' in status message")
	}
}

func TestQuestradePreview_SecurityNotFound_Flags(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	acts := []map[string]interface{}{
		qtAct("Buy", "UNKNOWN", "", "CAD", "10", "50.00", date),
	}
	// Server returns empty symbol search so auto-create fails.
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	// Do NOT create a security for "UNKNOWN".

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 flagged") {
		t.Errorf("expected '1 flagged' for unknown ticker; body snippet: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "security not found") {
		t.Errorf("expected 'security not found' in flag message")
	}
}

func TestQuestradePreview_TFI_NoBookValue_Flags_WithDescription(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	desc := "SAABY CANACCORD CAPITAL CORP. TRANSFER IN NO PRICE"
	acts := []map[string]interface{}{
		qtAct("TFI", "SAABY", desc, "USD", "50", "0", date),
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	createTestSecurity(t, ctx, env.securities, "SAABY", "USD")
	seedFXRate(t, env.fxStore, date, "1.3700")

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 flagged") {
		t.Errorf("expected '1 flagged' for TFI with no book value; body snippet: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "no book value") {
		t.Errorf("expected 'no book value' in flag message")
	}
	if !strings.Contains(body, "SAABY CANACCORD CAPITAL CORP") {
		t.Errorf("expected full description in flag message for diagnosability; body snippet: %s", body[:min(500, len(body))])
	}
}

func TestQuestradePreview_SkipAction_CountedNotShown(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	acts := []map[string]interface{}{
		qtAct("WDR", "", "", "CAD", "0", "0", date), // withdrawal — always skipped silently
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 skipped") {
		t.Errorf("expected '1 skipped' for WDR action; body snippet: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "0 ready") {
		t.Errorf("expected '0 ready' for WDR action; body snippet: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "0 flagged") {
		t.Errorf("expected '0 flagged' for WDR action; body snippet: %s", body[:min(300, len(body))])
	}
}

func TestQuestradePreview_ZeroQuantity_Flags(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	acts := []map[string]interface{}{
		qtAct("Buy", "XIU", "", "CAD", "0", "34.50", date), // zero qty
	}
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	createTestSecurity(t, ctx, env.securities, "XIU", "CAD")

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 flagged") {
		t.Errorf("expected '1 flagged' for zero-quantity buy; body snippet: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "zero quantity") {
		t.Errorf("expected 'zero quantity' in flag message")
	}
}

