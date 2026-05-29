package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type UserStore struct {
	db *sql.DB
}

func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

// EnsureDefault creates the user if absent and returns their ID.
func (s *UserStore) EnsureDefault(ctx context.Context, email string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE email=?`, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := s.db.ExecContext(ctx, `INSERT INTO users (email) VALUES (?)`, email)
		if err != nil {
			return 0, fmt.Errorf("create default user: %w", err)
		}
		return res.LastInsertId()
	}
	if err != nil {
		return 0, fmt.Errorf("get default user: %w", err)
	}
	return id, nil
}
