package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/session"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/user"
)

func newTestUserAndSession(t *testing.T, userStore *sqlite.UserStore, sessionStore *sqlite.SessionStore) (userID int64, rawToken string) { //nolint:unparam // userID intentionally unused by some callers; rawToken is always used
	t.Helper()
	ctx := context.Background()
	userID, err := userStore.Create(ctx, &user.User{
		Email: "sess-test@example.com", PasswordHash: "x", IsAdmin: false,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw := "raw-token-abc"
	if err := sessionStore.Create(ctx, &session.Session{
		UserID:       userID,
		TokenHash:    sqlite.HashToken(raw),
		TOTPVerified: false,
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return userID, raw
}

func TestSessionStore_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	us := sqlite.NewUserStore(db, nil)
	ss := sqlite.NewSessionStore(db)
	ctx := context.Background()

	_, raw := newTestUserAndSession(t, us, ss)

	got, err := ss.GetByTokenHash(ctx, sqlite.HashToken(raw))
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if got.TOTPVerified {
		t.Error("expected TOTPVerified=false")
	}
}

func TestSessionStore_GetByTokenHash_NotFound(t *testing.T) {
	db := newTestDB(t)
	ss := sqlite.NewSessionStore(db)

	_, err := ss.GetByTokenHash(context.Background(), sqlite.HashToken("nonexistent"))
	if !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSessionStore_SetTOTPVerified(t *testing.T) {
	db := newTestDB(t)
	us := sqlite.NewUserStore(db, nil)
	ss := sqlite.NewSessionStore(db)
	ctx := context.Background()

	_, raw := newTestUserAndSession(t, us, ss)

	sess, _ := ss.GetByTokenHash(ctx, sqlite.HashToken(raw))
	if err := ss.SetTOTPVerified(ctx, sess.ID); err != nil {
		t.Fatalf("SetTOTPVerified: %v", err)
	}

	got, _ := ss.GetByTokenHash(ctx, sqlite.HashToken(raw))
	if !got.TOTPVerified {
		t.Error("expected TOTPVerified=true after SetTOTPVerified")
	}
}

func TestSessionStore_UpdateLastSeen(t *testing.T) {
	db := newTestDB(t)
	us := sqlite.NewUserStore(db, nil)
	ss := sqlite.NewSessionStore(db)
	ctx := context.Background()

	_, raw := newTestUserAndSession(t, us, ss)
	sess, _ := ss.GetByTokenHash(ctx, sqlite.HashToken(raw))
	before := sess.LastSeenAt

	time.Sleep(50 * time.Millisecond)
	if err := ss.UpdateLastSeen(ctx, sess.ID); err != nil {
		t.Fatalf("UpdateLastSeen: %v", err)
	}

	got, _ := ss.GetByTokenHash(ctx, sqlite.HashToken(raw))
	if !got.LastSeenAt.After(before) {
		t.Error("last_seen_at should be updated")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	db := newTestDB(t)
	us := sqlite.NewUserStore(db, nil)
	ss := sqlite.NewSessionStore(db)
	ctx := context.Background()

	_, raw := newTestUserAndSession(t, us, ss)
	sess, _ := ss.GetByTokenHash(ctx, sqlite.HashToken(raw))

	if err := ss.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := ss.GetByTokenHash(ctx, sqlite.HashToken(raw))
	if !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("after Delete: want ErrNotFound, got %v", err)
	}
}

func TestSessionStore_DeleteExpired(t *testing.T) {
	db := newTestDB(t)
	us := sqlite.NewUserStore(db, nil)
	ss := sqlite.NewSessionStore(db)
	ctx := context.Background()

	userID, err := us.Create(ctx, &user.User{Email: "exp@example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	rawExpired := "expired-token"
	rawValid := "valid-token"

	if err := ss.Create(ctx, &session.Session{
		UserID: userID, TokenHash: sqlite.HashToken(rawExpired), ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if err := ss.Create(ctx, &session.Session{
		UserID: userID, TokenHash: sqlite.HashToken(rawValid), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create valid session: %v", err)
	}

	if err := ss.DeleteExpired(ctx); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}

	_, err = ss.GetByTokenHash(ctx, sqlite.HashToken(rawExpired))
	if !errors.Is(err, errs.ErrNotFound) {
		t.Error("expired session should be deleted")
	}
	if _, err := ss.GetByTokenHash(ctx, sqlite.HashToken(rawValid)); err != nil {
		t.Errorf("valid session should still exist: %v", err)
	}
}
