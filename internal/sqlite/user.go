package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/user"
)

type UserStore struct {
	db  *sql.DB
	key []byte // AES-256 key for TOTP secret encryption; nil = TOTP disabled
}

func NewUserStore(db *sql.DB, key []byte) *UserStore {
	return &UserStore{db: db, key: key}
}

func (s *UserStore) Create(ctx context.Context, u *user.User) (int64, error) {
	isAdmin := 0
	if u.IsAdmin {
		isAdmin = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, password_hash, is_admin) VALUES (?, NULLIF(?,''), ?)`,
		u.Email, u.PasswordHash, isAdmin,
	)
	if err != nil {
		return 0, fmt.Errorf("user create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("user create: last insert id: %w", err)
	}
	return id, nil
}

func (s *UserStore) GetByID(ctx context.Context, id int64) (*user.User, error) {
	u, err := s.scan(s.db.QueryRowContext(ctx,
		`SELECT id, email, COALESCE(password_hash,''), is_admin, COALESCE(totp_secret,''), totp_enabled, created_at FROM users WHERE id=?`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.ErrNotFound
	}
	return u, err
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*user.User, error) {
	u, err := s.scan(s.db.QueryRowContext(ctx,
		`SELECT id, email, COALESCE(password_hash,''), is_admin, COALESCE(totp_secret,''), totp_enabled, created_at FROM users WHERE email=?`, email,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.ErrNotFound
	}
	return u, err
}

func (s *UserStore) List(ctx context.Context) ([]*user.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, COALESCE(password_hash,''), is_admin, COALESCE(totp_secret,''), totp_enabled, created_at FROM users ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("user list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var users []*user.User
	for rows.Next() {
		u, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *UserStore) GetFirstUnconfigured(ctx context.Context) (*user.User, error) {
	u, err := s.scan(s.db.QueryRowContext(ctx,
		`SELECT id, email, COALESCE(password_hash,''), is_admin, COALESCE(totp_secret,''), totp_enabled, created_at FROM users WHERE password_hash IS NULL LIMIT 1`,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errs.ErrNotFound
	}
	return u, err
}

func (s *UserStore) ConfigureUser(ctx context.Context, userID int64, email, passwordHash string, isAdmin bool) error {
	adminInt := 0
	if isAdmin {
		adminInt = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET email=?, password_hash=?, is_admin=? WHERE id=?`,
		email, passwordHash, adminInt, userID,
	)
	if err != nil {
		return fmt.Errorf("user configure: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user configure: rows affected: %w", err)
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (s *UserStore) Delete(ctx context.Context, userID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, userID)
	if err != nil {
		if isFKConstraintErr(err) {
			return errs.ErrConstraint
		}
		return fmt.Errorf("user delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user delete: rows affected: %w", err)
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (s *UserStore) UpdateEmail(ctx context.Context, userID int64, email string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET email=? WHERE id=?`, email, userID)
	if err != nil {
		return fmt.Errorf("user update email: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user update email: rows affected: %w", err)
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (s *UserStore) UpdatePassword(ctx context.Context, userID int64, hash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash=? WHERE id=?`, hash, userID,
	)
	if err != nil {
		return fmt.Errorf("user update password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user update password: rows affected: %w", err)
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (s *UserStore) SetAdmin(ctx context.Context, userID int64, isAdmin bool) error {
	v := 0
	if isAdmin {
		v = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET is_admin=? WHERE id=?`, v, userID)
	if err != nil {
		return fmt.Errorf("user set admin: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user set admin: rows affected: %w", err)
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (s *UserStore) UpdateTOTP(ctx context.Context, userID int64, secret string, enabled bool) error {
	var encSecret string
	if enabled && secret != "" {
		if len(s.key) != 32 {
			return fmt.Errorf("user update totp: TOKEN_ENCRYPTION_KEY required for 2FA")
		}
		var err error
		encSecret, err = encrypt(s.key, secret)
		if err != nil {
			return fmt.Errorf("user update totp: %w", err)
		}
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret=NULLIF(?,''), totp_enabled=? WHERE id=?`,
		encSecret, enabledInt, userID,
	)
	if err != nil {
		return fmt.Errorf("user update totp: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user update totp: rows affected: %w", err)
	}
	if n == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// EnableTOTPWithCodes atomically enables TOTP and replaces recovery codes in one
// transaction, so a partial failure cannot leave the user with TOTP on but no codes.
func (s *UserStore) EnableTOTPWithCodes(ctx context.Context, userID int64, secret string, codes []*user.RecoveryCode) error {
	if len(s.key) != 32 {
		return fmt.Errorf("enable totp: TOKEN_ENCRYPTION_KEY required")
	}
	encSecret, err := encrypt(s.key, secret)
	if err != nil {
		return fmt.Errorf("enable totp: encrypt: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("enable totp: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET totp_secret=?, totp_enabled=1 WHERE id=?`, encSecret, userID,
	)
	if err != nil {
		return fmt.Errorf("enable totp: update user: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("enable totp: rows affected: %w", err)
	} else if n == 0 {
		return errs.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM recovery_codes WHERE user_id=?`, userID,
	); err != nil {
		return fmt.Errorf("enable totp: delete codes: %w", err)
	}
	for _, c := range codes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, c.Hash,
		); err != nil {
			return fmt.Errorf("enable totp: insert code: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("enable totp: commit: %w", err)
	}
	return nil
}

func (s *UserStore) CountConfigured(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE password_hash IS NOT NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("user count configured: %w", err)
	}
	return n, nil
}

func (s *UserStore) CreateRecoveryCodes(ctx context.Context, codes []*user.RecoveryCode) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("recovery codes create: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range codes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`,
			c.UserID, c.Hash,
		); err != nil {
			return fmt.Errorf("recovery code create: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("recovery codes create: commit: %w", err)
	}
	return nil
}

func (s *UserStore) ListRecoveryCodes(ctx context.Context, userID int64) ([]*user.RecoveryCode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, code_hash, used_at FROM recovery_codes WHERE user_id=? AND used_at IS NULL ORDER BY id`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("recovery codes list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var codes []*user.RecoveryCode
	for rows.Next() {
		var rc user.RecoveryCode
		var usedAt sql.NullTime
		if err := rows.Scan(&rc.ID, &rc.UserID, &rc.Hash, &usedAt); err != nil {
			return nil, fmt.Errorf("recovery codes scan: %w", err)
		}
		if usedAt.Valid {
			t := usedAt.Time
			rc.UsedAt = &t
		}
		codes = append(codes, &rc)
	}
	return codes, rows.Err()
}

func (s *UserStore) MarkRecoveryCodeUsed(ctx context.Context, codeID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE recovery_codes SET used_at=? WHERE id=? AND used_at IS NULL`, time.Now().UTC(), codeID,
	)
	if err != nil {
		return fmt.Errorf("recovery code mark used: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("recovery code mark used: rows affected: %w", err)
	}
	if n == 0 {
		return errs.ErrNotFound // code already used (concurrent request won the race)
	}
	return nil
}

func (s *UserStore) DeleteRecoveryCodes(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id=?`, userID)
	if err != nil {
		return fmt.Errorf("recovery codes delete: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func (s *UserStore) scan(row scanner) (*user.User, error) {
	var u user.User
	var isAdmin, totpEnabled int
	var encSecret string
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &isAdmin, &encSecret, &totpEnabled, &u.CreatedAt); err != nil {
		return nil, fmt.Errorf("user scan: %w", err)
	}
	u.IsAdmin = isAdmin == 1
	u.TOTPEnabled = totpEnabled == 1
	if encSecret != "" && len(s.key) == 32 {
		plain, err := decrypt(s.key, encSecret)
		if err != nil {
			return nil, fmt.Errorf("user scan: decrypt totp: %w", err)
		}
		u.TOTPSecret = plain
	}
	return &u, nil
}
