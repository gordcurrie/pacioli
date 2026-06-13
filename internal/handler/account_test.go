package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/user"
	"golang.org/x/crypto/bcrypt"
)

func seedAccount(t *testing.T, env *testEnv, userID int64) *account.Account {
	t.Helper()
	a := &account.Account{
		UserID:   userID,
		Name:     "Margin Account",
		Type:     account.TypeMargin,
		Broker:   "TD",
		Currency: "CAD",
	}
	if err := env.accounts.Create(context.Background(), a); err != nil {
		t.Fatalf("seedAccount: %v", err)
	}
	return a
}

func seedOtherUser(t *testing.T, env *testEnv) int64 {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("other-pass"), 4)
	if err != nil {
		t.Fatalf("bcrypt other user: %v", err)
	}
	id, err := env.users.Create(context.Background(), &user.User{
		Email:        "other@example.com",
		PasswordHash: string(hash),
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	return id
}

func TestAccountHandler_ListAccounts_Empty(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/accounts", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestAccountHandler_ListAccounts_WithAccounts(t *testing.T) {
	env := newTestEnv(t)
	a := seedAccount(t, env, env.userID)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/accounts", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), a.Name) {
		t.Errorf("response missing account name %q", a.Name)
	}
}

func TestAccountHandler_NewAccount(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/accounts/new", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestAccountHandler_CreateAccount_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("name=Test+TFSA&type=tfsa&broker=TD&currency=CAD&account_number=T123")
	req := env.newRequest(http.MethodPost, "/accounts", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/accounts" {
		t.Errorf("redirect: got %q want /accounts", rr.Header().Get("Location"))
	}
}

func TestAccountHandler_EditAccount_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	a := seedAccount(t, env, env.userID)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/accounts/%d/edit", a.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), a.Name) {
		t.Errorf("response missing account name %q", a.Name)
	}
}

func TestAccountHandler_EditAccount_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/accounts/9999/edit", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestAccountHandler_EditAccount_OtherUserAccount(t *testing.T) {
	env := newTestEnv(t)
	otherID := seedOtherUser(t, env)
	a := seedAccount(t, env, otherID)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, fmt.Sprintf("/accounts/%d/edit", a.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-user: got %d want 404", rr.Code)
	}
}

func TestAccountHandler_UpdateAccount_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	a := seedAccount(t, env, env.userID)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("name=Updated+Name&type=cash&broker=RBC&currency=CAD&account_number=")
	req := env.newRequest(http.MethodPost, fmt.Sprintf("/accounts/%d", a.ID), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/accounts" {
		t.Errorf("redirect: got %q want /accounts", rr.Header().Get("Location"))
	}
}

func TestAccountHandler_UpdateAccount_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("name=X&type=cash&broker=B&currency=CAD&account_number=")
	req := env.newRequest(http.MethodPost, "/accounts/9999", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestAccountHandler_UpdateAccount_OtherUserAccount(t *testing.T) {
	env := newTestEnv(t)
	otherID := seedOtherUser(t, env)
	a := seedAccount(t, env, otherID)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	body := strings.NewReader("name=Hijacked&type=cash&broker=B&currency=CAD&account_number=")
	req := env.newRequest(http.MethodPost, fmt.Sprintf("/accounts/%d", a.ID), body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-user: got %d want 404", rr.Code)
	}
}

func TestAccountHandler_DeleteAccount_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	a := seedAccount(t, env, env.userID)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, fmt.Sprintf("/accounts/%d", a.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
}

func TestAccountHandler_DeleteAccount_NotFound(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, "/accounts/9999", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestAccountHandler_DeleteAccount_OtherUserAccount(t *testing.T) {
	env := newTestEnv(t)
	otherID := seedOtherUser(t, env)
	a := seedAccount(t, env, otherID)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, fmt.Sprintf("/accounts/%d", a.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-user: got %d want 404", rr.Code)
	}
}

func TestAccountHandler_DeleteAccount_HasTransactions(t *testing.T) {
	env := newTestEnv(t)
	seedACBFixture(t, env)
	// seedACBFixture creates an account owned by env.userID and seeds a buy transaction
	// Retrieve that account for deletion
	accounts, err := env.accounts.ListByUser(context.Background(), env.userID)
	if err != nil || len(accounts) == 0 {
		t.Fatalf("no accounts found: %v", err)
	}
	a := accounts[0]
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodDelete, fmt.Sprintf("/accounts/%d", a.ID), http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status: got %d want 409 (account has transactions)", rr.Code)
	}
}

func TestAccountHandler_RequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	paths := []struct{ method, path string }{
		{http.MethodGet, "/accounts"},
		{http.MethodGet, "/accounts/new"},
		{http.MethodPost, "/accounts"},
		{http.MethodGet, "/accounts/1/edit"},
		{http.MethodPost, "/accounts/1"},
		{http.MethodDelete, "/accounts/1"},
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
