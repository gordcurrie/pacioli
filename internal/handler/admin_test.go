package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/user"
	"golang.org/x/crypto/bcrypt"
)

// --- GET /admin/audit ---

func TestAdminAuditLog_Renders(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Seed a few audit entries directly so the page has data to display.
	for _, e := range []*audit.Entry{
		{UserID: env.userID, UserEmail: "test@example.com", Action: audit.ActionCreate, EntityType: audit.EntityAccount, EntityID: 1, Source: audit.SourceManual},
		{UserID: env.userID, UserEmail: "test@example.com", Action: audit.ActionUpdate, EntityType: audit.EntitySecurity, EntityID: 2, Source: audit.SourceManual, BeforeState: `{"id":2}`},
		{UserID: env.userID, UserEmail: "test@example.com", Action: audit.ActionDelete, EntityType: audit.EntityTransaction, EntityID: 3, Source: audit.SourceManual},
	} {
		if err := env.audits.Log(ctx, e); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/admin/audit", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200\nbody: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Audit Log") {
		t.Error("response should contain 'Audit Log' heading")
	}
	if !strings.Contains(body, "3 entries") {
		t.Errorf("response should show entry count, body excerpt: %s", body[:min(500, len(body))])
	}
}

func TestAdminAuditLog_InvalidUserID_Returns400(t *testing.T) {
	env := newTestEnv(t)
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	for _, bad := range []string{"abc", "-1", "0"} {
		req := env.newRequest(http.MethodGet, "/admin/audit?user_id="+bad, http.NoBody)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("user_id=%q: got %d want 400", bad, rr.Code)
		}
	}
}

func TestAdminAuditLog_OutOfRangePage_Redirects(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Single entry — only page 1 exists.
	if err := env.audits.Log(ctx, &audit.Entry{
		UserID: env.userID, UserEmail: "test@example.com", Action: audit.ActionCreate,
		EntityType: audit.EntityUser, EntityID: env.userID, Source: audit.SourceManual,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/admin/audit?page=999", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("got %d want 303", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/admin/audit" {
		t.Errorf("redirect location = %q, want /admin/audit (page 1)", loc)
	}
}

func TestAdminAuditLog_FilterByEntityType(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	if err := env.audits.Log(ctx, &audit.Entry{
		UserID: env.userID, UserEmail: "test@example.com", Action: audit.ActionCreate, EntityType: audit.EntityAccount, EntityID: 1, Source: audit.SourceManual,
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := env.audits.Log(ctx, &audit.Entry{
		UserID: env.userID, UserEmail: "test@example.com", Action: audit.ActionCreate, EntityType: audit.EntitySecurity, EntityID: 2, Source: audit.SourceManual,
	}); err != nil {
		t.Fatalf("seed security: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	req := env.newRequest(http.MethodGet, "/admin/audit?entity_type=account", http.NoBody)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 entry") {
		t.Errorf("filtered view should show 1 entry; body excerpt: %s", body[:min(500, len(body))])
	}
}

// --- Admin action audit writes ---

func TestAdminCreateUser_WritesAuditEntry(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	rr := postForm(mux, "/admin/users", url.Values{
		"email":    {"newuser@example.com"},
		"password": {"password12345"},
	}, &http.Cookie{Name: "pacioli_session", Value: env.rawToken})

	if rr.Code != http.StatusOK {
		t.Fatalf("create user: got %d\nbody: %s", rr.Code, rr.Body.String())
	}

	n, err := env.audits.Count(ctx, audit.ListFilter{Action: audit.ActionCreate, EntityType: audit.EntityUser})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("audit create entries = %d, want 1", n)
	}
}

func TestAdminDeleteUser_WritesAuditEntryWithSnapshot(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Create a second user to delete (can't delete self).
	hash, _ := bcrypt.GenerateFromPassword([]byte("password12345"), 4)
	targetID, err := env.users.Create(ctx, &user.User{
		Email:        "victim@example.com",
		PasswordHash: string(hash),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	path := "/admin/users/" + itoa(targetID) + "/delete"
	rr := postForm(mux, path, url.Values{}, &http.Cookie{Name: "pacioli_session", Value: env.rawToken})

	if rr.Code != http.StatusOK {
		t.Fatalf("delete user: got %d\nbody: %s", rr.Code, rr.Body.String())
	}

	entries, err := env.audits.List(ctx, audit.ListFilter{Action: audit.ActionDelete, EntityType: audit.EntityUser, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit delete entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.EntityID != targetID {
		t.Errorf("audit entity_id = %d, want %d", e.EntityID, targetID)
	}
	if e.BeforeState == "" {
		t.Error("audit delete entry should have a before-state snapshot")
	}
	if !strings.Contains(e.BeforeState, "victim@example.com") {
		t.Errorf("snapshot should contain deleted user email, got: %s", e.BeforeState)
	}
}

func TestAdminResetPassword_WritesAuditEntry(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpassword1"), 4)
	targetID, err := env.users.Create(ctx, &user.User{
		Email:        "target@example.com",
		PasswordHash: string(hash),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	mux := http.NewServeMux()
	env.handler.Routes(mux)

	path := "/admin/users/" + itoa(targetID) + "/reset-password"
	rr := postForm(mux, path, url.Values{
		"password": {"newpassword123"},
	}, &http.Cookie{Name: "pacioli_session", Value: env.rawToken})

	if rr.Code != http.StatusOK {
		t.Fatalf("reset password: got %d\nbody: %s", rr.Code, rr.Body.String())
	}

	entries, err := env.audits.List(ctx, audit.ListFilter{Action: audit.ActionUpdate, EntityType: audit.EntityUser, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit update entries = %d, want 1", len(entries))
	}
	if entries[0].EntityID != targetID {
		t.Errorf("audit entity_id = %d, want %d", entries[0].EntityID, targetID)
	}
	if entries[0].BeforeState == "" {
		t.Error("reset password audit entry should have a before-state snapshot")
	}
}

// --- Profile action audit writes ---

func TestUpdatePassword_WritesAuditEntry(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	env.handler.Routes(mux)

	rr := postForm(mux, "/profile/password", url.Values{
		"current_password": {env.password},
		"password":         {"newpassword123"},
		"confirm":          {"newpassword123"},
	}, &http.Cookie{Name: "pacioli_session", Value: env.rawToken})

	if rr.Code != http.StatusOK {
		t.Fatalf("update password: got %d\nbody: %s", rr.Code, rr.Body.String())
	}

	entries, err := env.audits.List(ctx, audit.ListFilter{Action: audit.ActionUpdate, EntityType: audit.EntityUser, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit update entries = %d, want 1", len(entries))
	}
	if entries[0].EntityID != env.userID {
		t.Errorf("audit entity_id = %d, want %d", entries[0].EntityID, env.userID)
	}
	if entries[0].BeforeState == "" {
		t.Error("password change audit entry should have a before-state snapshot")
	}
}

