package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/questrade"
)

type QTokenStore struct {
	db *sql.DB
}

func NewQTokenStore(db *sql.DB) *QTokenStore {
	return &QTokenStore{db: db}
}

func (s *QTokenStore) Save(ctx context.Context, userID int64, t questrade.Token) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO questrade_tokens (user_id, access_token, refresh_token, api_server, expires_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		     access_token  = excluded.access_token,
		     refresh_token = excluded.refresh_token,
		     api_server    = excluded.api_server,
		     expires_at    = excluded.expires_at,
		     updated_at    = CURRENT_TIMESTAMP`,
		userID, t.AccessToken.Reveal(), t.RefreshToken.Reveal(), t.APIServer,
		t.ExpiresAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save questrade token: %w", err)
	}
	return nil
}

func (s *QTokenStore) Get(ctx context.Context, userID int64) (questrade.Token, error) {
	var accessToken, refreshToken, expiresAt string
	var t questrade.Token
	err := s.db.QueryRowContext(ctx,
		`SELECT access_token, refresh_token, api_server, expires_at
		 FROM questrade_tokens WHERE user_id = ?`, userID,
	).Scan(&accessToken, &refreshToken, &t.APIServer, &expiresAt)
	t.AccessToken = questrade.Secret(accessToken)
	t.RefreshToken = questrade.Secret(refreshToken)

	if errors.Is(err, sql.ErrNoRows) {
		return questrade.Token{}, errs.ErrNotFound
	}
	if err != nil {
		return questrade.Token{}, fmt.Errorf("get questrade token: %w", err)
	}
	var parseErr error
	t.ExpiresAt, parseErr = time.Parse(time.RFC3339, expiresAt)
	if parseErr != nil {
		return questrade.Token{}, fmt.Errorf("get questrade token: parse expires_at: %w", parseErr)
	}
	return t, nil
}

func (s *QTokenStore) Delete(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM questrade_tokens WHERE user_id = ?`, userID)
	return err
}
