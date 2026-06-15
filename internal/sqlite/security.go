package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/security"
)

type SecurityStore struct {
	db *sql.DB
}

func NewSecurityStore(db *sql.DB) *SecurityStore {
	return &SecurityStore{db: db}
}

func (r *SecurityStore) Create(ctx context.Context, s *security.Security) error {
	if s.Source == "" {
		s.Source = "manual"
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO securities (ticker, exchange, name, type, currency, source) VALUES (?, ?, ?, ?, ?, ?)`,
		s.Ticker, s.Exchange, s.Name, string(s.Type), s.Currency, s.Source,
	)
	if err != nil {
		return fmt.Errorf("create security: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get security id: %w", err)
	}
	s.ID = id
	return nil
}

func (r *SecurityStore) GetByID(ctx context.Context, id int64) (*security.Security, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, ticker, exchange, name, type, currency, source FROM securities WHERE id = ?`, id)
	return scanSecurity(row)
}

func (r *SecurityStore) GetByTickerExchange(ctx context.Context, ticker, exchange string) (*security.Security, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, ticker, exchange, name, type, currency, source FROM securities WHERE ticker=? AND exchange=?`,
		ticker, exchange)
	return scanSecurity(row)
}

func (r *SecurityStore) Search(ctx context.Context, query string) ([]*security.Security, error) {
	like := "%" + query + "%"
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, ticker, exchange, name, type, currency, source FROM securities
		 WHERE ticker LIKE ? OR name LIKE ? ORDER BY ticker LIMIT 50`,
		like, like)
	if err != nil {
		return nil, fmt.Errorf("search securities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSecurities(rows)
}

func (r *SecurityStore) Update(ctx context.Context, s *security.Security) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE securities SET ticker=?, exchange=?, name=?, type=?, currency=? WHERE id=?`,
		s.Ticker, s.Exchange, s.Name, string(s.Type), s.Currency, s.ID,
	)
	if err != nil {
		return fmt.Errorf("update security: %w", err)
	}
	return nil
}

func (r *SecurityStore) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM securities WHERE id=?`, id)
	if err != nil {
		if isFKConstraintErr(err) {
			return errs.ErrConstraint
		}
		return fmt.Errorf("delete security: %w", err)
	}
	return nil
}

func (r *SecurityStore) ListAll(ctx context.Context) ([]*security.Security, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, ticker, exchange, name, type, currency, source FROM securities ORDER BY ticker`)
	if err != nil {
		return nil, fmt.Errorf("list securities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSecurities(rows)
}

type securityScanner interface {
	Scan(dest ...any) error
}

func scanSecurity(s securityScanner) (*security.Security, error) {
	var sec security.Security
	var secType string
	if err := s.Scan(&sec.ID, &sec.Ticker, &sec.Exchange, &sec.Name, &secType, &sec.Currency, &sec.Source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errs.ErrNotFound
		}
		return nil, fmt.Errorf("scan security: %w", err)
	}
	sec.Type = security.Type(secType)
	return &sec, nil
}

func scanSecurities(rows *sql.Rows) ([]*security.Security, error) {
	var out []*security.Security
	for rows.Next() {
		s, err := scanSecurity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
