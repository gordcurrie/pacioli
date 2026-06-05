package user

import (
	"context"
	"time"
)

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	IsAdmin      bool
	TOTPSecret   string // AES-256-GCM encrypted; empty = 2FA not enabled
	TOTPEnabled  bool
	CreatedAt    time.Time
}

type RecoveryCode struct {
	ID     int64
	UserID int64
	Hash   string     // bcrypt hash of the plain code
	UsedAt *time.Time // nil = not yet used
}

type Store interface {
	Create(ctx context.Context, u *User) (int64, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context) ([]*User, error)
	UpdatePassword(ctx context.Context, userID int64, hash string) error
	SetAdmin(ctx context.Context, userID int64, isAdmin bool) error
	UpdateTOTP(ctx context.Context, userID int64, secret string, enabled bool) error
	CountConfigured(ctx context.Context) (int, error) // COUNT WHERE password_hash IS NOT NULL

	// Recovery codes
	CreateRecoveryCodes(ctx context.Context, codes []*RecoveryCode) error
	ListRecoveryCodes(ctx context.Context, userID int64) ([]*RecoveryCode, error)
	MarkRecoveryCodeUsed(ctx context.Context, codeID int64) error
	DeleteRecoveryCodes(ctx context.Context, userID int64) error
}
