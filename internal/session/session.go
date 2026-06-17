// Package session defines the Session type for browser authentication and the Store
// interface for persistence. Sessions are identified by a SHA-256 hash of the raw
// token stored in the user's cookie; the raw token never touches the database.
package session

import (
	"context"
	"time"
)

// Session represents an authenticated browser session identified by a hashed token.
type Session struct {
	ID           int64
	UserID       int64
	TokenHash    string
	TOTPVerified bool
	CreatedAt    time.Time
	ExpiresAt    time.Time
	LastSeenAt   time.Time
}

// Store defines persistence operations for user sessions.
type Store interface {
	Create(ctx context.Context, s *Session) error
	GetByTokenHash(ctx context.Context, hash string) (*Session, error)
	SetTOTPVerified(ctx context.Context, sessionID int64) error
	UpdateLastSeen(ctx context.Context, sessionID int64) error
	Delete(ctx context.Context, sessionID int64) error
	DeleteByUserID(ctx context.Context, userID int64) error
	DeleteExpired(ctx context.Context) error
}
