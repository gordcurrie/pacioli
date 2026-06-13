package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gordcurrie/pacioli/internal/security"
)

func seedSecurity(t *testing.T, env *testEnv) *security.Security {
	t.Helper()
	s := &security.Security{
		Ticker:   "BCE",
		Exchange: "TSX",
		Name:     "BCE Inc",
		Type:     security.TypeEquity,
		Currency: "CAD",
	}
	if err := env.securities.Create(context.Background(), s); err != nil {
		t.Fatalf("seedSecurity: %v", err)
	}
	return s
}

func TestSecurityHandler_ListSecurities_Empty(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestSecurityHandler_ListSecurities_WithData(t *testing.T) {
	env := newTestEnv(t)
	s := seedSecurity(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), s.Ticker) {
		t.Errorf("response missing ticker %q", s.Ticker)
	}
}

func TestSecurityHandler_ListSecurities_WithQuery(t *testing.T) {
	env := newTestEnv(t)
	s := seedSecurity(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities?q=BCE", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), s.Ticker) {
		t.Errorf("search response missing ticker %q", s.Ticker)
	}
}

func TestSecurityHandler_NewSecurity(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities/new", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestSecurityHandler_CreateSecurity_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("ticker=RY&exchange=TSX&name=Royal+Bank&type=equity&currency=CAD")
	req := env.newRequest(http.MethodPost, "/securities", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303; body: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/securities" {
		t.Errorf("redirect: got %q want /securities", rr.Header().Get("Location"))
	}
}

func TestSecurityHandler_CreateSecurity_InvalidType(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("ticker=XX&exchange=TSX&name=XX&type=badtype&currency=CAD")
	req := env.newRequest(http.MethodPost, "/securities", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("invalid type: got %d want 200 (form re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid security type") {
		t.Error("response missing validation error 'invalid security type'")
	}
}

func TestSecurityHandler_CreateSecurity_InvalidCurrency(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("ticker=XX&exchange=TSX&name=XX&type=equity&currency=EUR")
	req := env.newRequest(http.MethodPost, "/securities", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("invalid currency: got %d want 200 (form re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid currency") {
		t.Error("response missing validation error 'invalid currency'")
	}
}

func TestSecurityHandler_EditSecurity_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	s := seedSecurity(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/securities/%d/edit", s.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), s.Ticker) {
		t.Errorf("response missing ticker %q", s.Ticker)
	}
}

func TestSecurityHandler_EditSecurity_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities/9999/edit", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestSecurityHandler_UpdateSecurity_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	s := seedSecurity(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("ticker=BCE&exchange=TSX&name=BCE+Inc+Updated&type=equity&currency=CAD")
	req := env.newRequest(http.MethodPost, fmt.Sprintf("/securities/%d", s.ID), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303; body: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/securities" {
		t.Errorf("redirect: got %q want /securities", rr.Header().Get("Location"))
	}
}

func TestSecurityHandler_UpdateSecurity_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("ticker=X&exchange=TSX&name=X&type=equity&currency=CAD")
	req := env.newRequest(http.MethodPost, "/securities/9999", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestSecurityHandler_UpdateSecurity_InvalidType(t *testing.T) {
	env := newTestEnv(t)
	s := seedSecurity(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("ticker=BCE&exchange=TSX&name=BCE&type=badtype&currency=CAD")
	req := env.newRequest(http.MethodPost, fmt.Sprintf("/securities/%d", s.ID), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("invalid type: got %d want 200 (form re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid security type") {
		t.Error("response missing validation error 'invalid security type'")
	}
}

func TestSecurityHandler_DeleteSecurity_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	s := seedSecurity(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, fmt.Sprintf("/securities/%d", s.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestSecurityHandler_DeleteSecurity_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, "/securities/9999", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestSecurityHandler_DeleteSecurity_HasTransactions(t *testing.T) {
	env := newTestEnv(t)
	sec := seedACBFixture(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, fmt.Sprintf("/securities/%d", sec.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status: got %d want 409 (security has transactions)", rr.Code)
	}
}

func TestSecurityHandler_SearchSecurities_NoQuery(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities/search", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("empty query: expected empty body, got %q", rr.Body.String())
	}
}

func TestSecurityHandler_SearchSecurities_WithResults(t *testing.T) {
	env := newTestEnv(t)
	s := seedSecurity(t, env)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities/search?security_search_input=BCE", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), s.Ticker) {
		t.Errorf("response missing ticker %q", s.Ticker)
	}
}

func TestSecurityHandler_SearchSecurities_NoResults(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/securities/search?security_search_input=ZZZNOTEXIST", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No results") {
		t.Errorf("response missing 'No results': %s", rr.Body.String())
	}
}

func TestSecurityHandler_RequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	paths := []struct{ method, path string }{
		{http.MethodGet, "/securities"},
		{http.MethodGet, "/securities/new"},
		{http.MethodPost, "/securities"},
		{http.MethodGet, "/securities/1/edit"},
		{http.MethodPost, "/securities/1"},
		{http.MethodDelete, "/securities/1"},
		{http.MethodGet, "/securities/search"},
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
