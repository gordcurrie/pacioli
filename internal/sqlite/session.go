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

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *SessionStore) Create(ctx context.Context, sess *session.Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, totp_verified, expires_at) VALUES (?, ?, ?, ?)`,
		sess.UserID, sess.TokenHash, sess.TOTPVerified, sess.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("session create: %w", err)
	}
	return nil
}

func (s *SessionStore) GetByTokenHash(ctx context.Context, hash string) (*session.Session, error) {
	var sess session.Session
	var totpVerified int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, totp_verified, created_at, expires_at, last_seen_at FROM sessions WHERE token_hash=?`, hash,
	).Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &totpVerified, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("session get: %w", err)
	}
	sess.TOTPVerified = totpVerified == 1
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
		`UPDATE sessions SET last_seen_at=? WHERE id=?`, time.Now().UTC(), sessionID,
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

func (s *SessionStore) DeleteExpired(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("session delete expired: %w", err)
	}
	return nil
}
