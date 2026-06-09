package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/session"
)

type SessionStore struct {
	db *sql.DB
}

func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{db: db}
}

// HashToken returns the hex-encoded SHA-256 hash of a raw session token.
// The raw token is stored only in the cookie; only its hash lives in the DB.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *SessionStore) Create(ctx context.Context, sess *session.Session) error {
	totpVerified := 0
	if sess.TOTPVerified {
		totpVerified = 1
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, totp_verified, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		sess.UserID, sess.TokenHash, totpVerified, sess.ExpiresAt.UTC().UnixMilli(), now,
	)
	if err != nil {
		return fmt.Errorf("session create: %w", err)
	}
	return nil
}

func (s *SessionStore) GetByTokenHash(ctx context.Context, hash string) (*session.Session, error) {
	var sess session.Session
	var totpVerified int
	var expiresAtUnix, lastSeenAtUnix int64
	var createdAtStr string // created_at is DEFAULT CURRENT_TIMESTAMP (SQLite text)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, totp_verified, created_at, expires_at, last_seen_at FROM sessions WHERE token_hash=?`, hash,
	).Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &totpVerified, &createdAtStr, &expiresAtUnix, &lastSeenAtUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("session get: %w", err)
	}
	sess.TOTPVerified = totpVerified == 1
	sess.ExpiresAt = time.UnixMilli(expiresAtUnix).UTC()
	sess.LastSeenAt = time.UnixMilli(lastSeenAtUnix).UTC()
	// created_at is a SQLite DEFAULT CURRENT_TIMESTAMP text value.
	if t, err := time.Parse("2006-01-02 15:04:05", createdAtStr); err == nil {
		sess.CreatedAt = t.UTC()
	}
	return &sess, nil
}

func (s *SessionStore) SetTOTPVerified(ctx context.Context, sessionID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET totp_verified=1 WHERE id=?`, sessionID)
	if err != nil {
		return fmt.Errorf("session set totp verified: %w", err)
	}
	return nil
}

func (s *SessionStore) UpdateLastSeen(ctx context.Context, sessionID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at=? WHERE id=?`, time.Now().UnixMilli(), sessionID,
	)
	if err != nil {
		return fmt.Errorf("session update last seen: %w", err)
	}
	return nil
}

func (s *SessionStore) Delete(ctx context.Context, sessionID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, sessionID)
	if err != nil {
		return fmt.Errorf("session delete: %w", err)
	}
	return nil
}

func (s *SessionStore) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	if err != nil {
		return fmt.Errorf("session delete by user: %w", err)
	}
	return nil
}

func (s *SessionStore) DeleteExpired(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("session delete expired: %w", err)
	}
	return nil
}
