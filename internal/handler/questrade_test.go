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
func newQTTestEnv(t *testing.T, qtServer *httptest.Server, bocBaseURL ...string) *qtTestEnv {
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
	if len(bocBaseURL) > 0 {
		bocSvc.BaseURL = bocBaseURL[0]
	}
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

	bocSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bocSrv.Close()

	qtSrv := makeQTServer(t, acts)
	env := newQTTestEnv(t, qtSrv, bocSrv.URL)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)
	createTestSecurity(t, ctx, env.securities, "SPLV", "USD")

	rr := doPreview(t, env, baseForm(acc.ID, date))
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

func TestQuestradePreview_UnknownTicker_AutoCreatesPlaceholder(t *testing.T) {
	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	acts := []map[string]interface{}{
		qtAct("Buy", "PRIVALT", "", "CAD", "10", "50.00", date),
	}
	// Server returns empty symbol search results (ticker not on Questrade — e.g. private/alt).
	srv := makeQTServer(t, acts)
	env := newQTTestEnv(t, srv)
	ctx := context.Background()

	acc := createTestAccount(t, ctx, env.accounts, env.userID)

	rr := doPreview(t, env, baseForm(acc.ID, date))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 ready") {
		t.Errorf("expected '1 ready' — unknown ticker should auto-create placeholder; body snippet: %s", body[:min(400, len(body))])
	}
	// Placeholder security should exist in the DB with the ticker as name.
	sec, err := env.securities.GetByTickerExchange(ctx, "PRIVALT", "")
	if err != nil {
		t.Fatalf("placeholder security not created: %v", err)
	}
	if sec.Name != "PRIVALT" {
		t.Errorf("placeholder name: got %q want %q", sec.Name, "PRIVALT")
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

// --- Sync tests ---

// syncAccNum is the QT account number used across all sync tests.
const syncAccNum = "MARGIN123"

// makeQTSyncServer returns a fake Questrade API server for /questrade/sync tests.
// It exposes one Margin account (syncAccNum) and serves positions and symbol search
// results from the supplied maps. A nil symbol result returns empty symbols.
func makeQTSyncServer(
	t *testing.T,
	positions map[string][]map[string]interface{},
	symbolResults map[string][]map[string]interface{},
) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/accounts":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accounts": []map[string]interface{}{
					{"number": syncAccNum, "type": "Margin", "status": "Active"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/positions"):
			parts := strings.Split(r.URL.Path, "/")
			accNum := parts[len(parts)-2]
			poses := positions[accNum]
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"positions": poses})
		case strings.Contains(r.URL.Path, "/symbols/search"):
			ticker := r.URL.Query().Get("prefix")
			results := symbolResults[ticker]
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"symbols": results})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// qtPos builds a position JSON map for makeQTSyncServer.
func qtPos(symbol, currency string) map[string]interface{} {
	return map[string]interface{}{
		"symbol":       symbol,
		"currency":     currency,
		"openQuantity": json.Number("100"),
	}
}

// qtSym builds a symbol search result JSON map.
func qtSym(symbol, description, exchange, secType, currency string) map[string]interface{} {
	return map[string]interface{}{
		"symbol":          symbol,
		"description":     description,
		"listingExchange": exchange,
		"securityType":    secType,
		"currency":        currency,
	}
}

// doSync fires POST /questrade/sync and returns the recorder.
func doSync(t *testing.T, env *qtTestEnv) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	env.h.Routes(mux)
	req := env.newRequest(http.MethodPost, "/questrade/sync", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// assertSyncRedirect checks that the sync response is a 303 redirect and that the
// Location contains the expected sync_securities count.
func assertSyncRedirect(t *testing.T, rr *httptest.ResponseRecorder, wantSecurities int) {
	t.Helper()
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("sync: status got %d want 303", rr.Code)
	}
	loc := rr.Header().Get("Location")
	wantS := fmt.Sprintf("sync_securities=%d", wantSecurities)
	if !strings.Contains(loc, wantS) {
		t.Errorf("redirect %q: want %q", loc, wantS)
	}
}

// TestQuestradeSync_ExactMatch_PopulatesSecurityFields verifies that when the
// symbol search returns an exact-ticker match, the created security gets the
// correct exchange, name, type, and currency from the search result (not the
// position's default values).
func TestQuestradeSync_ExactMatch_PopulatesSecurityFields(t *testing.T) {
	ctx := context.Background()
	positions := map[string][]map[string]interface{}{
		syncAccNum: {qtPos("XIU", "CAD")},
	}
	symbolResults := map[string][]map[string]interface{}{
		"XIU": {qtSym("XIU", "iShares S&P/TSX 60 Index ETF", "TSX", "ETF", "CAD")},
	}
	srv := makeQTSyncServer(t, positions, symbolResults)
	env := newQTTestEnv(t, srv)

	rr := doSync(t, env)
	assertSyncRedirect(t, rr, 1)

	sec, err := env.securities.GetByTickerExchange(ctx, "XIU", "TSX")
	if err != nil {
		t.Fatalf("security not created: %v", err)
	}
	if sec.Exchange != "TSX" {
		t.Errorf("Exchange: got %q want %q", sec.Exchange, "TSX")
	}
	if sec.Name != "iShares S&P/TSX 60 Index ETF" {
		t.Errorf("Name: got %q want %q", sec.Name, "iShares S&P/TSX 60 Index ETF")
	}
	if sec.Currency != "CAD" {
		t.Errorf("Currency: got %q want %q", sec.Currency, "CAD")
	}
}

// TestQuestradeSync_NoExactMatch_CreatesPlaceholder verifies that when symbol search
// returns no exact-ticker match, sync creates a placeholder security (Name=ticker,
// Exchange="") rather than using a non-exact hit.
func TestQuestradeSync_NoExactMatch_CreatesPlaceholder(t *testing.T) {
	ctx := context.Background()
	positions := map[string][]map[string]interface{}{
		syncAccNum: {qtPos("OTCCO", "USD")},
	}
	// Search returns a result with a different symbol — no exact match.
	symbolResults := map[string][]map[string]interface{}{
		"OTCCO": {qtSym("OTCCO.U", "OTC Corp USD", "OTC", "Stock", "USD")},
	}
	srv := makeQTSyncServer(t, positions, symbolResults)
	env := newQTTestEnv(t, srv)

	rr := doSync(t, env)
	assertSyncRedirect(t, rr, 1)

	sec, err := env.securities.GetByTickerExchange(ctx, "OTCCO", "")
	if err != nil {
		t.Fatalf("placeholder security not created: %v", err)
	}
	if sec.Name != "OTCCO" {
		t.Errorf("placeholder Name: got %q want %q", sec.Name, "OTCCO")
	}
	if sec.Exchange != "" {
		t.Errorf("placeholder Exchange: got %q want empty", sec.Exchange)
	}
}

// TestQuestradeSync_ExistingExactExchange_NoDuplicate verifies that re-syncing an
// account where a security already exists (same ticker+exchange) does not create a
// duplicate — the secByTickerExchange guard skips creation.
func TestQuestradeSync_ExistingExactExchange_NoDuplicate(t *testing.T) {
	ctx := context.Background()
	positions := map[string][]map[string]interface{}{
		syncAccNum: {qtPos("XIU", "CAD")},
	}
	symbolResults := map[string][]map[string]interface{}{
		"XIU": {qtSym("XIU", "iShares S&P/TSX 60 Index ETF", "TSX", "ETF", "CAD")},
	}
	srv := makeQTSyncServer(t, positions, symbolResults)
	env := newQTTestEnv(t, srv)

	// Pre-create the security so it already exists at sync time.
	createTestSecurity(t, ctx, env.securities, "XIU", "CAD")

	rr := doSync(t, env)
	assertSyncRedirect(t, rr, 0)

	all, err := env.securities.ListAll(ctx)
	if err != nil {
		t.Fatalf("list securities: %v", err)
	}
	var count int
	for _, s := range all {
		if s.Ticker == "XIU" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("XIU security count: got %d want 1", count)
	}
}

// TestQuestradeSync_ExistingOTCSecurity_NoDuplicateViaExistingTickers verifies that
// re-syncing an OTC security (empty exchange) does not create a duplicate via the
// existingTickers guard, even though the ticker+"|" key would miss an existing entry
// with a different exchange.
func TestQuestradeSync_ExistingOTCSecurity_NoDuplicateViaExistingTickers(t *testing.T) {
	ctx := context.Background()
	positions := map[string][]map[string]interface{}{
		syncAccNum: {qtPos("OTCCO", "USD")},
	}
	// Empty symbol search → no exact match → Exchange stays "".
	symbolResults := map[string][]map[string]interface{}{}
	srv := makeQTSyncServer(t, positions, symbolResults)
	env := newQTTestEnv(t, srv)

	// Pre-create placeholder (as a prior sync would have).
	existing := &security.Security{
		Ticker:   "OTCCO",
		Name:     "OTCCO",
		Exchange: "",
		Type:     security.TypeEquity,
		Currency: "USD",
	}
	if err := env.securities.Create(ctx, existing); err != nil {
		t.Fatalf("pre-create security: %v", err)
	}

	rr := doSync(t, env)
	assertSyncRedirect(t, rr, 0)

	all, err := env.securities.ListAll(ctx)
	if err != nil {
		t.Fatalf("list securities: %v", err)
	}
	var count int
	for _, s := range all {
		if s.Ticker == "OTCCO" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("OTCCO security count: got %d want 1 (existingTickers guard failed)", count)
	}
}

