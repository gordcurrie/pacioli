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
	GetFirstUnconfigured(ctx context.Context) (*User, error) // first user with no password_hash
	// ConfigureUser atomically sets email, password_hash, and is_admin=1 in one UPDATE.
	// Used by setupSubmit to prevent partial-update races (e.g. password set but admin not granted).
	ConfigureUser(ctx context.Context, userID int64, email, passwordHash string) error
	Delete(ctx context.Context, userID int64) error
	UpdateEmail(ctx context.Context, userID int64, email string) error
	UpdatePassword(ctx context.Context, userID int64, hash string) error
	SetAdmin(ctx context.Context, userID int64, isAdmin bool) error
	UpdateTOTP(ctx context.Context, userID int64, secret string, enabled bool) error
	// EnableTOTPWithCodes atomically enables TOTP and replaces recovery codes in one transaction.
	EnableTOTPWithCodes(ctx context.Context, userID int64, secret string, codes []*RecoveryCode) error
	CountConfigured(ctx context.Context) (int, error) // COUNT WHERE password_hash IS NOT NULL

	// Recovery codes
	CreateRecoveryCodes(ctx context.Context, codes []*RecoveryCode) error
	ListRecoveryCodes(ctx context.Context, userID int64) ([]*RecoveryCode, error)
	MarkRecoveryCodeUsed(ctx context.Context, codeID int64) error
	DeleteRecoveryCodes(ctx context.Context, userID int64) error
}
