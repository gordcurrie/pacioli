package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/user"
)

func TestUserStore_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)
	ctx := context.Background()

	id, err := s.Create(ctx, &user.User{Email: "alice@example.com", PasswordHash: "hash", IsAdmin: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", got.Email)
	}
	if !got.IsAdmin {
		t.Error("expected IsAdmin=true")
	}
	if got.PasswordHash != "hash" {
		t.Errorf("PasswordHash = %q, want hash", got.PasswordHash)
	}
}

func TestUserStore_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)

	_, err := s.GetByID(context.Background(), 9999)
	if !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestUserStore_GetByEmail(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)
	ctx := context.Background()

	if _, err := s.Create(ctx, &user.User{Email: "bob@example.com", PasswordHash: "h"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetByEmail(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Email != "bob@example.com" {
		t.Errorf("Email = %q", got.Email)
	}
}

func TestUserStore_GetByEmail_NotFound(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)

	_, err := s.GetByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestUserStore_List(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)
	ctx := context.Background()

	for _, email := range []string{"a@x.com", "b@x.com", "c@x.com"} {
		if _, err := s.Create(ctx, &user.User{Email: email, PasswordHash: "h"}); err != nil {
			t.Fatalf("Create %s: %v", email, err)
		}
	}

	users, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// newTestDB seeds 2 users; 3 more created above — just check >= 3
	if len(users) < 3 {
		t.Errorf("List returned %d users, want at least 3", len(users))
	}
}

func TestUserStore_UpdatePassword(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)
	ctx := context.Background()

	id, _ := s.Create(ctx, &user.User{Email: "pw@x.com", PasswordHash: "old"})
	if err := s.UpdatePassword(ctx, id, "new-hash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	got, _ := s.GetByID(ctx, id)
	if got.PasswordHash != "new-hash" {
		t.Errorf("PasswordHash = %q, want new-hash", got.PasswordHash)
	}
}

func TestUserStore_SetAdmin(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)
	ctx := context.Background()

	id, _ := s.Create(ctx, &user.User{Email: "adm@x.com", PasswordHash: "h", IsAdmin: false})
	if err := s.SetAdmin(ctx, id, true); err != nil {
		t.Fatalf("SetAdmin: %v", err)
	}

	got, _ := s.GetByID(ctx, id)
	if !got.IsAdmin {
		t.Error("expected IsAdmin=true after SetAdmin")
	}

	if err := s.SetAdmin(ctx, id, false); err != nil {
		t.Fatalf("SetAdmin false: %v", err)
	}
	got, _ = s.GetByID(ctx, id)
	if got.IsAdmin {
		t.Error("expected IsAdmin=false after SetAdmin(false)")
	}
}

func TestUserStore_CountConfigured(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)
	ctx := context.Background()

	n, err := s.CountConfigured(ctx)
	if err != nil {
		t.Fatalf("CountConfigured: %v", err)
	}
	// newTestDB seeds 2 users with NULL password_hash; CountConfigured should return 0
	if n != 0 {
		t.Errorf("initial count = %d, want 0", n)
	}

	id, _ := s.Create(ctx, &user.User{Email: "c@x.com", PasswordHash: "hash"})
	n, _ = s.CountConfigured(ctx)
	if n != 1 {
		t.Errorf("after create: count = %d, want 1", n)
	}

	// Create one without password — should not count.
	if _, err := s.Create(ctx, &user.User{Email: "nopass@x.com"}); err != nil {
		t.Fatalf("Create no-pass: %v", err)
	}
	n, _ = s.CountConfigured(ctx)
	if n != 1 {
		t.Errorf("with no-pass user: count = %d, want 1", n)
	}

	// Update password — now counts.
	if err := s.UpdatePassword(ctx, id, "new"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	n, _ = s.CountConfigured(ctx)
	if n != 1 {
		t.Errorf("after update: count = %d, want 1", n)
	}
}

func TestUserStore_RecoveryCodes(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil)
	ctx := context.Background()

	id, _ := s.Create(ctx, &user.User{Email: "rc@x.com", PasswordHash: "h"})

	codes := []*user.RecoveryCode{
		{UserID: id, Hash: "hash1"},
		{UserID: id, Hash: "hash2"},
		{UserID: id, Hash: "hash3"},
	}
	if err := s.CreateRecoveryCodes(ctx, codes); err != nil {
		t.Fatalf("CreateRecoveryCodes: %v", err)
	}

	listed, err := s.ListRecoveryCodes(ctx, id)
	if err != nil {
		t.Fatalf("ListRecoveryCodes: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed %d codes, want 3", len(listed))
	}
	for _, c := range listed {
		if c.UsedAt != nil {
			t.Error("codes should not be used yet")
		}
	}

	// Mark one used — ListRecoveryCodes only returns unused codes, so count drops to 2.
	if err := s.MarkRecoveryCodeUsed(ctx, listed[0].ID); err != nil {
		t.Fatalf("MarkRecoveryCodeUsed: %v", err)
	}
	listed2, _ := s.ListRecoveryCodes(ctx, id)
	if len(listed2) != 2 {
		t.Errorf("after marking one used: %d codes, want 2", len(listed2))
	}
	for _, c := range listed2 {
		if c.UsedAt != nil {
			t.Error("ListRecoveryCodes should not return used codes")
		}
	}

	// Delete all.
	if err := s.DeleteRecoveryCodes(ctx, id); err != nil {
		t.Fatalf("DeleteRecoveryCodes: %v", err)
	}
	listed3, _ := s.ListRecoveryCodes(ctx, id)
	if len(listed3) != 0 {
		t.Errorf("after delete: %d codes remain, want 0", len(listed3))
	}
}

func TestUserStore_UpdateTOTP_RequiresKey(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, nil) // nil key
	ctx := context.Background()

	id, _ := s.Create(ctx, &user.User{Email: "totp@x.com", PasswordHash: "h"})

	err := s.UpdateTOTP(ctx, id, "secret", true)
	if err == nil {
		t.Error("expected error when UpdateTOTP called without encryption key")
	}
}

func TestUserStore_UpdateTOTP_WithKey(t *testing.T) {
	key := make([]byte, 32)
	db := newTestDB(t)
	s := sqlite.NewUserStore(db, key)
	ctx := context.Background()

	id, _ := s.Create(ctx, &user.User{Email: "totp2@x.com", PasswordHash: "h"})

	if err := s.UpdateTOTP(ctx, id, "MYSECRET", true); err != nil {
		t.Fatalf("UpdateTOTP: %v", err)
	}

	got, _ := s.GetByID(ctx, id)
	if !got.TOTPEnabled {
		t.Error("expected TOTPEnabled=true")
	}
	if got.TOTPSecret != "MYSECRET" {
		t.Errorf("TOTPSecret = %q, want MYSECRET", got.TOTPSecret)
	}

	// Disable.
	if err := s.UpdateTOTP(ctx, id, "", false); err != nil {
		t.Fatalf("UpdateTOTP disable: %v", err)
	}
	got, _ = s.GetByID(ctx, id)
	if got.TOTPEnabled {
		t.Error("expected TOTPEnabled=false after disable")
	}
	if got.TOTPSecret != "" {
		t.Errorf("TOTPSecret should be empty after disable, got %q", got.TOTPSecret)
	}
}

