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

// QTokenStore is the SQLite implementation of questrade.Store; tokens are AES-256-GCM encrypted at rest.
type QTokenStore struct {
	db  *sql.DB
	key []byte // 32-byte AES-256 key
}

// NewQTokenStore constructs a QTokenStore backed by db, encrypting tokens with key.
func NewQTokenStore(db *sql.DB, key []byte) *QTokenStore {
	return &QTokenStore{db: db, key: key}
}

func (s *QTokenStore) Save(ctx context.Context, userID int64, t questrade.Token) error {
	encAccess, err := Encrypt(s.key, t.AccessToken.Reveal())
	if err != nil {
		return fmt.Errorf("save questrade token: %w", err)
	}
	encRefresh, err := Encrypt(s.key, t.RefreshToken.Reveal())
	if err != nil {
		return fmt.Errorf("save questrade token: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO questrade_tokens (user_id, access_token, refresh_token, api_server, expires_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		     access_token  = excluded.access_token,
		     refresh_token = excluded.refresh_token,
		     api_server    = excluded.api_server,
		     expires_at    = excluded.expires_at,
		     updated_at    = CURRENT_TIMESTAMP`,
		userID, encAccess, encRefresh, t.APIServer,
		t.ExpiresAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save questrade token: %w", err)
	}
	return nil
}

func (s *QTokenStore) Get(ctx context.Context, userID int64) (questrade.Token, error) {
	var encAccess, encRefresh, expiresAt string
	var t questrade.Token
	err := s.db.QueryRowContext(ctx,
		`SELECT access_token, refresh_token, api_server, expires_at
		 FROM questrade_tokens WHERE user_id = ?`, userID,
	).Scan(&encAccess, &encRefresh, &t.APIServer, &expiresAt)

	if errors.Is(err, sql.ErrNoRows) {
		return questrade.Token{}, errs.ErrNotFound
	}
	if err != nil {
		return questrade.Token{}, fmt.Errorf("get questrade token: %w", err)
	}
	accessToken, err := Decrypt(s.key, encAccess)
	if err != nil {
		return questrade.Token{}, fmt.Errorf("get questrade token: %w", err)
	}
	refreshToken, err := Decrypt(s.key, encRefresh)
	if err != nil {
		return questrade.Token{}, fmt.Errorf("get questrade token: %w", err)
	}
	t.AccessToken = questrade.Secret(accessToken)
	t.RefreshToken = questrade.Secret(refreshToken)
	t.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return questrade.Token{}, fmt.Errorf("get questrade token: parse expires_at: %w", err)
	}
	return t, nil
}

func (s *QTokenStore) Delete(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM questrade_tokens WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete questrade token: %w", err)
	}
	return nil
}
