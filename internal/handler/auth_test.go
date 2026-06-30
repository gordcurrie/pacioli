package handler_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/handler"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/session"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/user"
	"github.com/gordcurrie/pacioli/web"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// newBlankTestEnv creates a handler with an empty DB — no users, no sessions.
// Used for testing setup flow and auth middleware before any accounts exist.
func newBlankTestEnv(t *testing.T) (*handler.Handler, *sqlite.UserStore) {
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

	acbSvc := service.NewACBService(txStore)
	gainsSvc := service.NewGainsService(txStore, secStore)
	rocSvc := service.NewROCService(txStore, distStore, secStore)
	portfolioSvc := service.NewPortfolioService(txStore, secStore, acbSvc)
	fxStore := sqlite.NewFXStore(db)
	bocSvc := service.NewBOCFetcher(fxStore)
	yahooSvc := service.NewYahooFetcher(bocSvc)

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
		PortfolioSvc: portfolioSvc,
		YahooSvc:     yahooSvc,
		Logger:       slog.Default(),
		TemplateFS:   web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return h, userStore
}

// postForm is a helper that sends a form POST request with optional cookie.
func postForm(mux http.Handler, path string, vals url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// --- Setup flow ---

func TestSetupPage_FirstRun(t *testing.T) {
	h, _ := newBlankTestEnv(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/setup", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("setup page: got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Create your admin account") {
		t.Error("setup page should show account creation form")
	}
}

func TestSetupPage_AlreadyConfigured_Redirects(t *testing.T) {
	env := newTestEnv(t) // has a configured user
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/setup", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("redirect = %q want /login", rr.Header().Get("Location"))
	}
}

func TestSetupSubmit_Success(t *testing.T) {
	h, userStore := newBlankTestEnv(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	rr := postForm(mux, "/setup", url.Values{
		"email":    {"admin@test.com"},
		"password": {"securepassword1"},
		"confirm":  {"securepassword1"},
	}, nil)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303\nbody: %s", rr.Code, rr.Body.String())
	}
	// Session cookie should be set.
	cookies := rr.Result().Cookies()
	var hasCookie bool
	for _, c := range cookies {
		if c.Name == "pacioli_session" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("setup should set session cookie")
	}

	// User should be created as admin.
	u, err := userStore.GetByEmail(context.Background(), "admin@test.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if !u.IsAdmin {
		t.Error("setup user should be admin")
	}
}

func TestSetupSubmit_PasswordMismatch(t *testing.T) {
	h, _ := newBlankTestEnv(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	rr := postForm(mux, "/setup", url.Values{
		"email":    {"admin@test.com"},
		"password": {"password1"},
		"confirm":  {"password2"},
	}, nil)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200 (re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "do not match") {
		t.Error("should show password mismatch error")
	}
}

func TestSetupSubmit_TooShort(t *testing.T) {
	h, _ := newBlankTestEnv(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	rr := postForm(mux, "/setup", url.Values{
		"email":    {"admin@test.com"},
		"password": {"short"},
		"confirm":  {"short"},
	}, nil)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "8 characters") {
		t.Error("should show minimum length error")
	}
}

// --- Login flow ---

func TestLoginPage(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/login", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Sign in") {
		t.Error("login page should contain 'Sign in'")
	}
}

func TestLoginSubmit_Success(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	rr := postForm(mux, "/login", url.Values{
		"email":    {"test@example.com"},
		"password": {env.password},
	}, nil)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303\nbody: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/" {
		t.Errorf("redirect = %q want /", rr.Header().Get("Location"))
	}
	var hasCookie bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == "pacioli_session" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("successful login should set session cookie")
	}
}

func TestLoginSubmit_WrongPassword(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	rr := postForm(mux, "/login", url.Values{
		"email":    {"test@example.com"},
		"password": {"wrong-password"},
	}, nil)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Invalid email or password") {
		t.Error("should show auth failure message")
	}
}

func TestLoginSubmit_UnknownEmail(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	rr := postForm(mux, "/login", url.Values{
		"email":    {"nobody@example.com"},
		"password": {"anything"},
	}, nil)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Invalid email or password") {
		t.Error("unknown email should show same error as wrong password (no enumeration)")
	}
}

// --- Logout ---

func TestLogout_ClearsCookieAndRedirects(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: env.rawToken}
	rr := postForm(mux, "/logout", nil, cookie)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("redirect = %q want /login", rr.Header().Get("Location"))
	}
	// Cookie should be cleared (MaxAge=-1).
	for _, c := range rr.Result().Cookies() {
		if c.Name == "pacioli_session" && c.MaxAge < 0 {
			return // found the clear cookie
		}
	}
	t.Error("logout should set MaxAge=-1 on session cookie")
}

func TestLogout_SessionDeletedFromDB(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: env.rawToken}
	postForm(mux, "/logout", nil, cookie)

	// Session should be gone.
	_, err := env.sessions.GetByTokenHash(context.Background(), sqlite.HashToken(env.rawToken))
	if err == nil {
		t.Error("session should be deleted after logout")
	}
}

// --- Auth middleware ---

func TestRequireAuth_NoCookie_RedirectsLogin(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/accounts", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("redirect = %q want /login", rr.Header().Get("Location"))
	}
}

func TestRequireAuth_ValidCookie_Passes(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/accounts", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
}

func TestRequireAuth_ExpiredSession_RedirectsLogin(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Create an expired session.
	expiredToken := "expired-raw-token"
	if err := env.sessions.Create(ctx, &session.Session{
		UserID:       env.userID,
		TokenHash:    sqlite.HashToken(expiredToken),
		TOTPVerified: true,
		ExpiresAt:    time.Now().Add(-time.Hour), // expired
	}); err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/accounts", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "pacioli_session", Value: expiredToken})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expired session: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("redirect = %q want /login", rr.Header().Get("Location"))
	}
}

func TestRequireAuth_TOTPPending_Redirects2FA(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Enable TOTP on the user (no real secret needed — just the flag).
	u, _ := env.users.GetByID(ctx, env.userID)
	_ = env.users.UpdatePassword(ctx, u.ID, u.PasswordHash) // no-op, just satisfy interface

	// Create a session with totp_verified=false for a TOTP-enabled user.
	// We'll hack this by creating the user with TOTP enabled via DB directly.
	// Simpler: create a second user with TOTP enabled via the store with a key.
	key := make([]byte, 32)
	db2, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()

	txStore2 := sqlite.NewTransactionStore(db2)
	secStore2 := sqlite.NewSecurityStore(db2)
	distStore2 := sqlite.NewDistributionStore(db2)
	auditStore2 := sqlite.NewAuditStore(db2)
	accountStore2 := sqlite.NewAccountStore(db2)
	userStore2 := sqlite.NewUserStore(db2, key)
	sessionStore2 := sqlite.NewSessionStore(db2)

	acbSvc2 := service.NewACBService(txStore2)
	gainsSvc2 := service.NewGainsService(txStore2, secStore2)
	rocSvc2 := service.NewROCService(txStore2, distStore2, secStore2)
	portfolioSvc2 := service.NewPortfolioService(txStore2, secStore2, acbSvc2)
	fxStore2 := sqlite.NewFXStore(db2)
	yahooSvc2 := service.NewYahooFetcher(service.NewBOCFetcher(fxStore2))

	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), 4)
	uid, _ := userStore2.Create(ctx, &user.User{Email: "totp@test.com", PasswordHash: string(hash)})

	// Generate a real TOTP key and enable it.
	totpKey, _ := totp.Generate(totp.GenerateOpts{Issuer: "Test", AccountName: "totp@test.com"})
	_ = userStore2.UpdateTOTP(ctx, uid, totpKey.Secret(), true)

	rawToken := "totp-pending-token"
	_ = sessionStore2.Create(ctx, &session.Session{
		UserID:       uid,
		TokenHash:    sqlite.HashToken(rawToken),
		TOTPVerified: false, // not yet verified
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	h2, err := handler.New(&handler.Config{
		Accounts: accountStore2, Securities: secStore2, Transactions: txStore2,
		Audits: auditStore2, Users: userStore2, Sessions: sessionStore2,
		ACBSvc: acbSvc2, GainsSvc: gainsSvc2, ROCSvc: rocSvc2,
		PortfolioSvc: portfolioSvc2, YahooSvc: yahooSvc2,
		Logger: slog.Default(), TemplateFS: web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	mux := http.NewServeMux()
	h2.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/accounts", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "pacioli_session", Value: rawToken})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("TOTP pending: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login/2fa" {
		t.Errorf("redirect = %q want /login/2fa", rr.Header().Get("Location"))
	}
}

func TestRequireAdmin_NonAdmin_Returns403(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Create a non-admin user with their own session.
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), 4)
	nonAdminID, _ := env.users.Create(ctx, &user.User{Email: "nonadmin@test.com", PasswordHash: string(hash), IsAdmin: false})
	nonAdminToken := "non-admin-raw-token"
	_ = env.sessions.Create(ctx, &session.Session{
		UserID:       nonAdminID,
		TokenHash:    sqlite.HashToken(nonAdminToken),
		TOTPVerified: true,
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/admin/users", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "pacioli_session", Value: nonAdminToken})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin /admin/users: got %d want 403", rr.Code)
	}
}

func TestRequireAdmin_Admin_Passes(t *testing.T) {
	env := newTestEnv(t) // env.userID is admin
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/admin/users", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("admin /admin/users: got %d want 200", rr.Code)
	}
}

// --- SetupGate ---

func TestSetupGate_NoUsers_RedirectsSetup(t *testing.T) {
	h, _ := newBlankTestEnv(t)

	mux := http.NewServeMux()
	h.Routes(mux)
	gated := h.SetupGate(mux)

	req := httptest.NewRequest(http.MethodGet, "/accounts", http.NoBody)
	rr := httptest.NewRecorder()
	gated.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/setup" {
		t.Errorf("redirect = %q want /setup", rr.Header().Get("Location"))
	}
}

func TestSetupGate_WithUsers_Passes(t *testing.T) {
	env := newTestEnv(t) // has a configured user

	mux := http.NewServeMux()
	env.handler.Routes(mux)
	gated := env.handler.SetupGate(mux)

	// /login should pass through (public route).
	req := httptest.NewRequest(http.MethodGet, "/login", http.NoBody)
	rr := httptest.NewRecorder()
	gated.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("/login with configured users: got %d want 200", rr.Code)
	}
}

// --- TOTP submit ---

func TestTOTPPage(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/login/2fa", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Two-factor") {
		t.Error("TOTP page should contain 'Two-factor'")
	}
}

func TestTOTPSubmit_ValidCode(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	txStore := sqlite.NewTransactionStore(db)
	secStore := sqlite.NewSecurityStore(db)
	distStore := sqlite.NewDistributionStore(db)
	auditStore := sqlite.NewAuditStore(db)
	accountStore := sqlite.NewAccountStore(db)
	userStore := sqlite.NewUserStore(db, key)
	sessionStore := sqlite.NewSessionStore(db)

	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), 4)
	uid, _ := userStore.Create(ctx, &user.User{Email: "totp@test.com", PasswordHash: string(hash)})

	totpKey, _ := totp.Generate(totp.GenerateOpts{Issuer: "Test", AccountName: "totp@test.com"})
	_ = userStore.UpdateTOTP(ctx, uid, totpKey.Secret(), true)

	rawToken := "pre-totp-raw"
	_ = sessionStore.Create(ctx, &session.Session{
		UserID:       uid,
		TokenHash:    sqlite.HashToken(rawToken),
		TOTPVerified: false,
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	acbSvcLocal := service.NewACBService(txStore)
	fxStoreLocal := sqlite.NewFXStore(db)
	bocSvcLocal := service.NewBOCFetcher(fxStoreLocal)
	h, err := handler.New(&handler.Config{
		Accounts: accountStore, Securities: secStore, Transactions: txStore,
		Audits: auditStore, Users: userStore, Sessions: sessionStore,
		ACBSvc:       acbSvcLocal,
		GainsSvc:     service.NewGainsService(txStore, secStore),
		ROCSvc:       service.NewROCService(txStore, distStore, secStore),
		PortfolioSvc: service.NewPortfolioService(txStore, secStore, acbSvcLocal),
		YahooSvc:     service.NewYahooFetcher(bocSvcLocal),
		EncKey:       key,
		Logger:       slog.Default(),
		TemplateFS:   web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	mux := http.NewServeMux()
	h.Routes(mux)

	validCode, _ := totp.GenerateCode(totpKey.Secret(), time.Now())
	cookie := &http.Cookie{Name: "pacioli_session", Value: rawToken}
	rr := postForm(mux, "/login/2fa", url.Values{"code": {validCode}}, cookie)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("valid TOTP: got %d want 303\nbody: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/" {
		t.Errorf("redirect = %q want /", rr.Header().Get("Location"))
	}

	// Session should be totp_verified.
	sess, _ := sessionStore.GetByTokenHash(ctx, sqlite.HashToken(rawToken))
	if !sess.TOTPVerified {
		t.Error("session should be TOTP verified after successful code")
	}
}

func TestTOTPSubmit_InvalidCode_RerendersError(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	txStore := sqlite.NewTransactionStore(db)
	secStore := sqlite.NewSecurityStore(db)
	distStore := sqlite.NewDistributionStore(db)
	auditStore := sqlite.NewAuditStore(db)
	accountStore := sqlite.NewAccountStore(db)
	userStore := sqlite.NewUserStore(db, key)
	sessionStore := sqlite.NewSessionStore(db)

	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), 4)
	uid, _ := userStore.Create(ctx, &user.User{Email: "totp2@test.com", PasswordHash: string(hash)})
	totpKey, _ := totp.Generate(totp.GenerateOpts{Issuer: "Test", AccountName: "totp2@test.com"})
	_ = userStore.UpdateTOTP(ctx, uid, totpKey.Secret(), true)

	rawToken := "pre-totp-invalid"
	_ = sessionStore.Create(ctx, &session.Session{
		UserID: uid, TokenHash: sqlite.HashToken(rawToken),
		TOTPVerified: false, ExpiresAt: time.Now().Add(time.Hour),
	})

	h, err := handler.New(&handler.Config{
		Accounts: accountStore, Securities: secStore, Transactions: txStore,
		Audits: auditStore, Users: userStore, Sessions: sessionStore,
		ACBSvc: service.NewACBService(txStore), GainsSvc: service.NewGainsService(txStore, secStore),
		ROCSvc: service.NewROCService(txStore, distStore, secStore),
		PortfolioSvc: service.NewPortfolioService(txStore, secStore, service.NewACBService(txStore)),
		YahooSvc: service.NewYahooFetcher(service.NewBOCFetcher(sqlite.NewFXStore(db))),
		EncKey: key, Logger: slog.Default(), TemplateFS: web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	mux := http.NewServeMux()
	h.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: rawToken}
	rr := postForm(mux, "/login/2fa", url.Values{"code": {"000000"}}, cookie)

	if rr.Code != http.StatusOK {
		t.Errorf("invalid TOTP: got %d want 200 (re-render)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Invalid code") {
		t.Error("should show 'Invalid code' error")
	}
}

// --- Admin handlers ---

func TestAdminCreateUser_Success(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: env.rawToken}
	rr := postForm(mux, "/admin/users", url.Values{
		"email":    {"newuser@test.com"},
		"password": {"somepassword123"},
	}, cookie)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "User created") {
		t.Error("should show 'User created' success message")
	}

	// User should exist.
	u, err := env.users.GetByEmail(context.Background(), "newuser@test.com")
	if err != nil {
		t.Fatalf("new user not found: %v", err)
	}
	if u.Email != "newuser@test.com" {
		t.Errorf("email = %q", u.Email)
	}
}

func TestAdminResetPassword_Success(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	// Create a user to reset.
	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpass"), 4)
	targetID, _ := env.users.Create(ctx, &user.User{Email: "target@test.com", PasswordHash: string(hash)})

	cookie := &http.Cookie{Name: "pacioli_session", Value: env.rawToken}
	rr := postForm(mux, "/admin/users/"+itoa(targetID)+"/reset-password", url.Values{
		"password": {"newpassword123"},
	}, cookie)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Password reset") {
		t.Error("should show 'Password reset' success message")
	}
}

// --- Profile handlers ---

func TestPasswordPage_Renders(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/profile/password", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
}

func TestUpdatePassword_WrongCurrent(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: env.rawToken}
	rr := postForm(mux, "/profile/password", url.Values{
		"current_password": {"wrong"},
		"password":         {"newpassword123"},
		"confirm":          {"newpassword123"},
	}, cookie)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "incorrect") {
		t.Error("should show incorrect password error")
	}
}

func TestUpdatePassword_Success(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: env.rawToken}
	rr := postForm(mux, "/profile/password", url.Values{
		"current_password": {env.password},
		"password":         {"newpassword123"},
		"confirm":          {"newpassword123"},
	}, cookie)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "updated") {
		t.Error("should show password updated success")
	}
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }

// totpHandlerEnv is a handler + stores backed by a DB with AES-256-GCM key and one TOTP-enabled user.
type totpHandlerEnv struct {
	h          *handler.Handler
	users      *sqlite.UserStore
	sessions   *sqlite.SessionStore
	userID     int64
	password   string
	totpSecret string
}

func newTOTPHandlerEnv(t *testing.T) *totpHandlerEnv {
	t.Helper()
	ctx := context.Background()
	key := make([]byte, 32)

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
	userStore := sqlite.NewUserStore(db, key)
	sessionStore := sqlite.NewSessionStore(db)

	const pw = "pw-totp-user"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), 4)
	uid, _ := userStore.Create(ctx, &user.User{Email: "totp-env@test.com", PasswordHash: string(hash)})

	totpKey, _ := totp.Generate(totp.GenerateOpts{Issuer: "Test", AccountName: "totp-env@test.com"})
	_ = userStore.UpdateTOTP(ctx, uid, totpKey.Secret(), true)

	acbSvc := service.NewACBService(txStore)
	h, err := handler.New(&handler.Config{
		Accounts: accountStore, Securities: secStore, Transactions: txStore,
		Audits: auditStore, Users: userStore, Sessions: sessionStore,
		ACBSvc:       acbSvc,
		GainsSvc:     service.NewGainsService(txStore, secStore),
		ROCSvc:       service.NewROCService(txStore, distStore, secStore),
		PortfolioSvc: service.NewPortfolioService(txStore, secStore, acbSvc),
		YahooSvc:     service.NewYahooFetcher(service.NewBOCFetcher(sqlite.NewFXStore(db))),
		EncKey:       key,
		Logger:       slog.Default(),
		TemplateFS:   web.Templates,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return &totpHandlerEnv{
		h: h, users: userStore, sessions: sessionStore,
		userID: uid, password: pw, totpSecret: totpKey.Secret(),
	}
}

// --- loginSubmit additional paths ---

func TestLoginSubmit_EmptyPasswordHash_RejectsWithSentinelTiming(t *testing.T) {
	h, userStore := newBlankTestEnv(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	h.Routes(mux)

	// User exists but has no password set (e.g. imported account pre-setup).
	if _, err := userStore.Create(ctx, &user.User{Email: "nohash@test.com"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rr := postForm(mux, "/login", url.Values{
		"email":    {"nohash@test.com"},
		"password": {"anything"},
	}, nil)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Invalid email or password") {
		t.Error("unconfigured account should show same error as wrong password")
	}
}

func TestLoginSubmit_TOTPEnabled_RedirectsTo2FA(t *testing.T) {
	env := newTOTPHandlerEnv(t)
	mux := http.NewServeMux()
	env.h.Routes(mux)

	rr := postForm(mux, "/login", url.Values{
		"email":    {"totp-env@test.com"},
		"password": {env.password},
	}, nil)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("TOTP login: got %d want 303\nbody: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/login/2fa" {
		t.Errorf("redirect = %q want /login/2fa", rr.Header().Get("Location"))
	}

	// Session must exist with totp_verified=false.
	var sessionCookie string
	for _, c := range rr.Result().Cookies() {
		if c.Name == "pacioli_session" {
			sessionCookie = c.Value
		}
	}
	if sessionCookie == "" {
		t.Fatal("no session cookie set")
	}
	sess, err := env.sessions.GetByTokenHash(context.Background(), sqlite.HashToken(sessionCookie))
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if sess.TOTPVerified {
		t.Error("session should have totp_verified=false after TOTP login")
	}
}

// --- totpSubmit additional paths ---

func TestTOTPSubmit_NoCookie_RedirectsLogin(t *testing.T) {
	h, _ := newBlankTestEnv(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	rr := postForm(mux, "/login/2fa", url.Values{"code": {"123456"}}, nil)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("no cookie: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("redirect = %q want /login", rr.Header().Get("Location"))
	}
}

func TestTOTPSubmit_ExpiredSession_RedirectsLogin(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	expiredToken := "expired-totp-session"
	if err := env.sessions.Create(ctx, &session.Session{
		UserID:       env.userID,
		TokenHash:    sqlite.HashToken(expiredToken),
		TOTPVerified: false,
		ExpiresAt:    time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: expiredToken}
	rr := postForm(mux, "/login/2fa", url.Values{"code": {"123456"}}, cookie)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expired session: got %d want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/login" {
		t.Errorf("redirect = %q want /login", rr.Header().Get("Location"))
	}
}

func TestTOTPSubmit_RecoveryCode_Success(t *testing.T) {
	env := newTOTPHandlerEnv(t)
	ctx := context.Background()

	const plainCode = "RCVR-TEST-0001"
	codeHash, _ := bcrypt.GenerateFromPassword([]byte(plainCode), 4)
	if err := env.users.CreateRecoveryCodes(ctx, []*user.RecoveryCode{
		{UserID: env.userID, Hash: string(codeHash)},
	}); err != nil {
		t.Fatalf("CreateRecoveryCodes: %v", err)
	}

	rawToken := "pre-totp-recovery"
	if err := env.sessions.Create(ctx, &session.Session{
		UserID:       env.userID,
		TokenHash:    sqlite.HashToken(rawToken),
		TOTPVerified: false,
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	mux := http.NewServeMux()
	env.h.Routes(mux)

	cookie := &http.Cookie{Name: "pacioli_session", Value: rawToken}
	rr := postForm(mux, "/login/2fa", url.Values{"code": {plainCode}}, cookie)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("recovery code: got %d want 303\nbody: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/" {
		t.Errorf("redirect = %q want /", rr.Header().Get("Location"))
	}

	sess, _ := env.sessions.GetByTokenHash(ctx, sqlite.HashToken(rawToken))
	if !sess.TOTPVerified {
		t.Error("session should be TOTP verified after valid recovery code")
	}

	// Recovery code must be consumed.
	remaining, _ := env.users.ListRecoveryCodes(ctx, env.userID)
	if len(remaining) != 0 {
		t.Errorf("recovery code should be marked used, got %d remaining", len(remaining))
	}
}

// --- setupSubmit additional paths ---

func TestSetupSubmit_ReuseUnconfiguredUser(t *testing.T) {
	h, userStore := newBlankTestEnv(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	h.Routes(mux)

	// Unconfigured user with a different email (simulates imported data with no auth set).
	unconfiguredID, err := userStore.Create(ctx, &user.User{Email: "imported@example.com"})
	if err != nil {
		t.Fatalf("create unconfigured user: %v", err)
	}

	rr := postForm(mux, "/setup", url.Values{
		"email":    {"admin@pacioli.com"},
		"password": {"securepassword1"},
		"confirm":  {"securepassword1"},
	}, nil)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303\nbody: %s", rr.Code, rr.Body.String())
	}

	// Original unconfigured row must be reused and fully configured.
	u, err := userStore.GetByID(ctx, unconfiguredID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if u.Email != "admin@pacioli.com" {
		t.Errorf("Email = %q, want admin@pacioli.com", u.Email)
	}
	if u.PasswordHash == "" {
		t.Error("PasswordHash should be set after setup")
	}
	if !u.IsAdmin {
		t.Error("reused user should be set as admin")
	}
}

func TestSetupSubmit_ConfigureExistingUnconfiguredEmailMatch(t *testing.T) {
	h, userStore := newBlankTestEnv(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	h.Routes(mux)

	// User with target email already exists but has no password.
	existingID, err := userStore.Create(ctx, &user.User{Email: "admin@pacioli.com"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	rr := postForm(mux, "/setup", url.Values{
		"email":    {"admin@pacioli.com"},
		"password": {"securepassword1"},
		"confirm":  {"securepassword1"},
	}, nil)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("got %d want 303\nbody: %s", rr.Code, rr.Body.String())
	}

	u, err := userStore.GetByID(ctx, existingID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if u.PasswordHash == "" {
		t.Error("PasswordHash should be set after setup")
	}
	if !u.IsAdmin {
		t.Error("existing user should be promoted to admin")
	}
}
