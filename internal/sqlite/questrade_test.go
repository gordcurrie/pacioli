package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/sqlite"
)

func TestQTokenStore(t *testing.T) {
	db := newTestDB(t)
	s := sqlite.NewQTokenStore(db)
	ctx := context.Background()

	// ensure user exists
	userStore := sqlite.NewUserStore(db)
	userID, err := userStore.EnsureDefault(ctx, "test@example.com")
	if err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	// Get on empty store returns ErrNotFound
	_, err = s.Get(ctx, userID)
	if !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("empty Get: want ErrNotFound, got %v", err)
	}

	token := questrade.Token{
		AccessToken:  "access-abc",
		RefreshToken: "refresh-xyz",
		APIServer:    "https://api01.iq.questrade.com/",
		ExpiresAt:    time.Now().Add(30 * time.Minute).Truncate(time.Second),
	}

	if err := s.Save(ctx, userID, token); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Get(ctx, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != token.AccessToken {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, token.AccessToken)
	}
	if got.RefreshToken != token.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, token.RefreshToken)
	}
	if got.APIServer != token.APIServer {
		t.Errorf("APIServer = %q, want %q", got.APIServer, token.APIServer)
	}
	if !got.ExpiresAt.Equal(token.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, token.ExpiresAt)
	}

	// Upsert overwrites
	token2 := questrade.Token{
		AccessToken:  "access-new",
		RefreshToken: "refresh-new",
		APIServer:    "https://api02.iq.questrade.com/",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Truncate(time.Second),
	}
	if err := s.Save(ctx, userID, token2); err != nil {
		t.Fatalf("Save upsert: %v", err)
	}
	got2, err := s.Get(ctx, userID)
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got2.AccessToken != token2.AccessToken {
		t.Errorf("upsert AccessToken = %q, want %q", got2.AccessToken, token2.AccessToken)
	}

	// Delete removes token
	if err := s.Delete(ctx, userID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = s.Get(ctx, userID)
	if !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("after Delete: want ErrNotFound, got %v", err)
	}
}
