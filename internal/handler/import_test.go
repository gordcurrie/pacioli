package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/broker"
	"github.com/gordcurrie/pacioli/internal/security"
)

// --- helpers ---

// newImportTestEnv builds a minimal handler env for import tests (no QT integration).
func newImportTestEnv(t *testing.T) *qtTestEnv {
	t.Helper()
	// Use a dummy QT server; the import endpoints never call Questrade.
	dummy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(dummy.Close)
	return newQTTestEnv(t, dummy)
}

// canaccordCSV builds a minimal Canaccord Genuity CSV with a header row followed
// by the provided data rows. Each row is a slice of 10 column values:
// [Date, Type, Description, Settlement, Quantity, Price, Amount, Account, ClientName, ClientID]
func canaccordCSV(rows [][]string) string {
	var b strings.Builder
	b.WriteString("Date,Type,Description,Settlement,Quantity,Price,Amount,Account,Client Name,Client ID\n")
	for _, r := range rows {
		for i, v := range r {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(v)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// canaccordRow returns a Canaccord CSV data row for a Buy transaction.
// date format: "1/2/2006 12:00:00 AM"
func canaccordRow(txType, desc, accountNo, qty, price, amount string, date time.Time) []string {
	// Canaccord format is "M/D/YYYY H:MM:SS AM/PM" (12-hour). Append a fixed
	// midnight time literal to avoid Go's format parser mangling "12".
	ds := date.Format("1/2/2006") + " 12:00:00 AM"
	return []string{ds, txType, desc, ds, qty, price, amount, accountNo, "Test User", "TST001"}
}

// doImportPreview fires POST /import/preview with a multipart form containing
// the broker name and CSV content, then returns the recorder.
func doImportPreview(t *testing.T, env *qtTestEnv, brokerName, csvContent string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("broker", brokerName)
	fw, err := mw.CreateFormFile("csv_file", "test.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = io.WriteString(fw, csvContent)
	_ = mw.Close()

	mux := http.NewServeMux()
	env.h.Routes(mux)
	req := env.newRequest("/import/preview", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// doImportCommit fires POST /import/commit with the broker name and a JSON commit_rows payload.
func doImportCommit(t *testing.T, env *qtTestEnv, brokerName string, rows []broker.CommitRow) *httptest.ResponseRecorder {
	t.Helper()
	commitJSON, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal commit rows: %v", err)
	}
	form := url.Values{
		"broker":      {brokerName},
		"commit_rows": {string(commitJSON)},
	}
	mux := http.NewServeMux()
	env.h.Routes(mux)
	req := env.newRequest("/import/commit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// assertPreviewCounts checks TotalOK / TotalSkip / TotalFlag summary strings.
func assertPreviewCounts(t *testing.T, rr *httptest.ResponseRecorder, wantOK, wantSkip, wantFlag int) {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	checks := []struct {
		want string
	}{
		{fmt.Sprintf("%d ready", wantOK)},
		{fmt.Sprintf("%d skipped", wantSkip)},
		{fmt.Sprintf("%d flagged", wantFlag)},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.want) {
			t.Errorf("expected %q in preview body; snippet: %.300s", c.want, body)
		}
	}
}

// createImportAccount creates a Canaccord margin account with number "ACC001".
func createImportAccount(t *testing.T, ctx context.Context, store interface {
	Create(context.Context, *account.Account) error
}, userID int64) *account.Account {
	t.Helper()
	acc := &account.Account{
		UserID:        userID,
		Name:          "Import Test ACC001",
		Type:          account.TypeMargin,
		Broker:        "Canaccord Genuity",
		Currency:      "CAD",
		AccountNumber: "ACC001",
	}
	if err := store.Create(ctx, acc); err != nil {
		t.Fatalf("create import account: %v", err)
	}
	return acc
}

// createImportSecurity creates a security with a specific name for import matching.
func createImportSecurity(t *testing.T, ctx context.Context, store interface {
	Create(context.Context, *security.Security) error
}, name, ticker, currency string) *security.Security {
	t.Helper()
	sec := &security.Security{
		Name:     name,
		Ticker:   ticker,
		Exchange: "TSX",
		Type:     security.TypeEquity,
		Currency: currency,
	}
	if err := store.Create(ctx, sec); err != nil {
		t.Fatalf("create import security: %v", err)
	}
	return sec
}

// --- preview tests ---

func TestImportPreview_ValidBuyRow_OKRow(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	createImportAccount(t, ctx, env.accounts, env.userID)
	createImportSecurity(t, ctx, env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	row := canaccordRow("Buy", "INVESCO CANADN ETF TRD #1234", "ACC001", "100.00000", "19.70", "-1970.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 1, 0, 0)
}

func TestImportPreview_SkipRow_CountedNotShown(t *testing.T) {
	env := newImportTestEnv(t)
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	row := canaccordRow("Fee-Based Account Fee", "FEE", "ACC001", "0", "0", "-25.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 0, 1, 0)
}

func TestImportPreview_AccountNotFound_Flags(t *testing.T) {
	env := newImportTestEnv(t)
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	// No account created — ACC999 is unknown.
	createImportSecurity(t, context.Background(), env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	row := canaccordRow("Buy", "INVESCO CANADN ETF TRD #1234", "ACC999", "100.00000", "19.70", "-1970.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 0, 0, 1)
	if !strings.Contains(rr.Body.String(), "account not found") {
		t.Errorf("expected 'account not found' in flag message")
	}
}

func TestImportPreview_AmbiguousAccount_Flags(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	// Two accounts with the same account number — ambiguous.
	for _, name := range []string{"Margin A", "Margin B"} {
		if err := env.accounts.Create(ctx, &account.Account{
			UserID:        env.userID,
			Name:          name,
			Type:          account.TypeMargin,
			Currency:      "CAD",
			AccountNumber: "DUP001",
		}); err != nil {
			t.Fatalf("create account: %v", err)
		}
	}
	createImportSecurity(t, ctx, env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	row := canaccordRow("Buy", "INVESCO CANADN ETF TRD #1234", "DUP001", "100.00000", "19.70", "-1970.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 0, 0, 1)
	if !strings.Contains(rr.Body.String(), "ambiguous account") {
		t.Errorf("expected 'ambiguous account' in flag message; got snippet: %.300s", rr.Body.String())
	}
}

func TestImportPreview_SecurityNotFound_Flags(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	createImportAccount(t, ctx, env.accounts, env.userID)
	// No security with this name.

	row := canaccordRow("Buy", "UNKNOWN SECURITY TRD #1234", "ACC001", "100.00000", "19.70", "-1970.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 0, 0, 1)
	if !strings.Contains(rr.Body.String(), "security not found") {
		t.Errorf("expected 'security not found' in flag message")
	}
}

func TestImportPreview_SecurityFoundViaSearch_OKRow(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	createImportAccount(t, ctx, env.accounts, env.userID)
	// Security name in DB differs from CSV description — search finds it.
	createImportSecurity(t, ctx, env.securities, "VANGUARD S&P 500 ETF", "VOO", "CAD")

	// CSV description resolves to "VANGUARD S&P 500 ETF" after name extraction.
	row := canaccordRow("Buy", "VANGUARD S&P 500 ETF TRD #5678", "ACC001", "50.00000", "200.00", "-10000.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 1, 0, 0)
}

func TestImportPreview_AmbiguousSecurity_Flags(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	createImportAccount(t, ctx, env.accounts, env.userID)
	// Two securities with the same name.
	for _, ticker := range []string{"XIU", "XIU2"} {
		if err := env.securities.Create(ctx, &security.Security{
			Name:     "ISHARES S&P TSX ETF",
			Ticker:   ticker,
			Exchange: "TSX",
			Type:     security.TypeEquity,
			Currency: "CAD",
		}); err != nil {
			t.Fatalf("create security: %v", err)
		}
	}

	row := canaccordRow("Buy", "ISHARES S&P TSX ETF TRD #1234", "ACC001", "100.00000", "30.00", "-3000.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 0, 0, 1)
	if !strings.Contains(rr.Body.String(), "ambiguous security") {
		t.Errorf("expected 'ambiguous security' in flag message; got snippet: %.300s", rr.Body.String())
	}
}

func TestImportPreview_NonCADSecurity_Flags(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	createImportAccount(t, ctx, env.accounts, env.userID)
	createImportSecurity(t, ctx, env.securities, "MICROSOFT CORP", "MSFT", "USD")

	row := canaccordRow("Buy", "MICROSOFT CORP TRD #1234", "ACC001", "10.00000", "420.00", "-4200.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 0, 0, 1)
	if !strings.Contains(rr.Body.String(), "non-CAD security") {
		t.Errorf("expected 'non-CAD security' in flag message")
	}
}

func TestImportPreview_ZeroQuantity_Flags(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	createImportAccount(t, ctx, env.accounts, env.userID)
	createImportSecurity(t, ctx, env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	row := canaccordRow("Buy", "INVESCO CANADN ETF TRD #1234", "ACC001", "0", "19.70", "0", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 0, 0, 1)
	if !strings.Contains(rr.Body.String(), "zero or negative quantity") {
		t.Errorf("expected 'zero or negative quantity' in flag message")
	}
}

func TestImportPreview_UnknownBroker_RendersError(t *testing.T) {
	env := newImportTestEnv(t)
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	row := canaccordRow("Buy", "INVESCO CANADN ETF TRD #1234", "ACC001", "100", "19.70", "-1970.00", date)
	csv := canaccordCSV([][]string{row})
	rr := doImportPreview(t, env, "Unknown Broker XYZ", csv)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unknown broker profile") {
		t.Errorf("expected 'unknown broker profile' error message")
	}
}

func TestImportPreview_MixedRows_CorrectCounts(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	createImportAccount(t, ctx, env.accounts, env.userID)
	createImportSecurity(t, ctx, env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	rows := [][]string{
		canaccordRow("Buy", "INVESCO CANADN ETF TRD #1", "ACC001", "100", "19.70", "-1970.00", date),    // ok
		canaccordRow("Fee-Based Account Fee", "FEE", "ACC001", "0", "0", "-25.00", date),               // skip
		canaccordRow("Buy", "UNKNOWN SECURITY TRD #2", "ACC001", "50", "10.00", "-500.00", date),       // flag: sec not found
	}
	csv := canaccordCSV(rows)
	rr := doImportPreview(t, env, "Canaccord Genuity", csv)

	assertPreviewCounts(t, rr, 1, 1, 1)
}

// --- commit tests ---

func TestImportCommit_ValidRow_CreatesTransaction(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	acc := createImportAccount(t, ctx, env.accounts, env.userID)
	sec := createImportSecurity(t, ctx, env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	rows := []broker.CommitRow{{
		TradeDate:   date.Format("2006-01-02"),
		SettledDate: date.Format("2006-01-02"),
		AccountID:   acc.ID,
		SecurityID:  sec.ID,
		TxType:      "buy",
		Quantity:    "100",
		Price:       "19.70",
		Commission:  "0",
	}}

	rr := doImportCommit(t, env, "Canaccord Genuity", rows)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("commit status: got %d want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/transactions" {
		t.Errorf("redirect: got %q want /transactions", loc)
	}

	txs, err := env.transactions.ListByAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("transaction count: got %d want 1", len(txs))
	}
	if txs[0].SecurityID != sec.ID {
		t.Errorf("SecurityID: got %d want %d", txs[0].SecurityID, sec.ID)
	}
}

func TestImportCommit_UnknownBroker_RedirectsToImport(t *testing.T) {
	env := newImportTestEnv(t)

	rr := doImportCommit(t, env, "Fake Broker", []broker.CommitRow{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/import" {
		t.Errorf("redirect: got %q want /import", loc)
	}
}

func TestImportCommit_EmptyRows_RedirectsToImport(t *testing.T) {
	env := newImportTestEnv(t)

	rr := doImportCommit(t, env, "Canaccord Genuity", []broker.CommitRow{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/import" {
		t.Errorf("redirect: got %q want /import", loc)
	}
}

func TestImportCommit_AccountNotOwned_SkipsRow(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	sec := createImportSecurity(t, ctx, env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	// Account ID 99999 does not belong to this user.
	rows := []broker.CommitRow{{
		TradeDate:   date.Format("2006-01-02"),
		SettledDate: date.Format("2006-01-02"),
		AccountID:   99999,
		SecurityID:  sec.ID,
		TxType:      "buy",
		Quantity:    "100",
		Price:       "19.70",
		Commission:  "0",
	}}

	rr := doImportCommit(t, env, "Canaccord Genuity", rows)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", rr.Code)
	}

	// No transactions should have been created.
	txs, err := env.transactions.ListByAccount(ctx, 99999)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("expected 0 transactions for unowned account, got %d", len(txs))
	}
}

func TestImportCommit_NonCADSecurity_SkipsRow(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	acc := createImportAccount(t, ctx, env.accounts, env.userID)
	sec := createImportSecurity(t, ctx, env.securities, "MICROSOFT CORP", "MSFT", "USD")

	rows := []broker.CommitRow{{
		TradeDate:   date.Format("2006-01-02"),
		SettledDate: date.Format("2006-01-02"),
		AccountID:   acc.ID,
		SecurityID:  sec.ID,
		TxType:      "buy",
		Quantity:    "10",
		Price:       "420.00",
		Commission:  "0",
	}}

	rr := doImportCommit(t, env, "Canaccord Genuity", rows)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", rr.Code)
	}

	txs, err := env.transactions.ListByAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("expected 0 transactions for non-CAD security, got %d", len(txs))
	}
}

func TestImportCommit_InvalidTxType_SkipsRow(t *testing.T) {
	env := newImportTestEnv(t)
	ctx := context.Background()
	date := time.Date(2023, 2, 7, 0, 0, 0, 0, time.UTC)

	acc := createImportAccount(t, ctx, env.accounts, env.userID)
	sec := createImportSecurity(t, ctx, env.securities, "INVESCO CANADN ETF", "INVESCO", "CAD")

	rows := []broker.CommitRow{{
		TradeDate:   date.Format("2006-01-02"),
		SettledDate: date.Format("2006-01-02"),
		AccountID:   acc.ID,
		SecurityID:  sec.ID,
		TxType:      "transfer", // not in validImportTypes
		Quantity:    "100",
		Price:       "19.70",
		Commission:  "0",
	}}

	rr := doImportCommit(t, env, "Canaccord Genuity", rows)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status: got %d want 303", rr.Code)
	}

	txs, err := env.transactions.ListByAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != 0 {
		t.Errorf("expected 0 transactions for invalid tx type, got %d", len(txs))
	}
}
