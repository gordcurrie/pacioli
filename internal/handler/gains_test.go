package handler_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gordcurrie/pacioli/internal/handler"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/web"
)

func newTestHandler(t *testing.T) *handler.Handler {
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

	acbSvc := service.NewACBService(txStore)
	gainsSvc := service.NewGainsService(txStore, secStore)
	rocSvc := service.NewROCService(txStore, distStore, secStore)

	h, err := handler.New(&handler.Config{
		Accounts:     accountStore,
		Securities:   secStore,
		Transactions: txStore,
		Audits:       auditStore,
		ACBSvc:       acbSvc,
		GainsSvc:     gainsSvc,
		ROCSvc:       rocSvc,
		UserID:       1,
		Logger:       slog.Default(),
		TemplateFS:   web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return h
}

func TestGainsHandler_ShowGainsForYear(t *testing.T) {
	h := newTestHandler(t)

	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/gains/2024", http.NoBody)
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
	h := newTestHandler(t)

	mux := http.NewServeMux()
	h.Routes(mux)

	for _, path := range []string{"/gains/abc", "/gains/1989", "/gains/2101"} {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("path %s: got %d want 404", path, rr.Code)
		}
	}
}

func TestGainsHandler_ListGains_RedirectsToCurrentYear(t *testing.T) {
	h := newTestHandler(t)

	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/gains", http.NoBody)
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
	h := newTestHandler(t)

	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/gains/2024/export", http.NoBody)
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
	// CSV header row should always be present
	body := rr.Body.String()
	if !strings.Contains(body, "Date,Ticker") {
		t.Errorf("CSV missing header row, got: %s", body)
	}
}

func TestGainsHandler_PreviewROC(t *testing.T) {
	h := newTestHandler(t)

	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/roc/2024", http.NoBody)
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

func TestGainsHandler_ApplyROC_RedirectsAfterApply(t *testing.T) {
	h := newTestHandler(t)

	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest(http.MethodPost, "/roc/2024", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/roc/2024" {
		t.Errorf("redirect location: got %q want /roc/2024", rr.Header().Get("Location"))
	}
}
