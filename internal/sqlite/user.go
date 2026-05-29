package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type UserStore struct {
	db *sql.DB
}

func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

// EnsureDefault creates the user if absent and returns their ID.
// INSERT OR IGNORE avoids a race between the SELECT and INSERT.
func (s *UserStore) EnsureDefault(ctx context.Context, email string) (int64, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO users (email) VALUES (?)`, email); err != nil {
		return 0, fmt.Errorf("ensure default user: %w", err)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE email=?`, email).Scan(&id); err != nil {
		return 0, fmt.Errorf("get default user: %w", err)
	}
	return id, nil
}
