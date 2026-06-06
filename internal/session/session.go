package session

import (
	"context"
	"time"
)

type Session struct {
	ID           int64
	UserID       int64
	TokenHash    string
	TOTPVerified bool
	CreatedAt    time.Time
	ExpiresAt    time.Time
	LastSeenAt   time.Time
}

type Store interface {
	Create(ctx context.Context, s *Session) error
	GetByTokenHash(ctx context.Context, hash string) (*Session, error)
	SetTOTPVerified(ctx context.Context, sessionID int64) error
	UpdateLastSeen(ctx context.Context, sessionID int64) error
	Delete(ctx context.Context, sessionID int64) error
	DeleteByUserID(ctx context.Context, userID int64) error
	DeleteExpired(ctx context.Context) error
}
