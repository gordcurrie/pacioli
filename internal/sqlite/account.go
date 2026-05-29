package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
)

type AccountStore struct {
	db *sql.DB
}

func NewAccountStore(db *sql.DB) *AccountStore {
	return &AccountStore{db: db}
}

func (r *AccountStore) Create(ctx context.Context, a *account.Account) error {
	a.IsRegistered = a.Type.IsRegistered()
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO accounts (user_id, name, type, broker, currency, account_number, is_registered)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.UserID, a.Name, string(a.Type), a.Broker, a.Currency, a.AccountNumber, boolToInt(a.IsRegistered),
	)
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get account id: %w", err)
	}
	a.ID = id
	return nil
}

func (r *AccountStore) GetByID(ctx context.Context, id int64) (*account.Account, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, type, broker, currency, account_number, is_registered, created_at
		 FROM accounts WHERE id = ?`, id)
	return scanAccount(row)
}

func (r *AccountStore) ListByUser(ctx context.Context, userID int64) ([]*account.Account, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, name, type, broker, currency, account_number, is_registered, created_at
		 FROM accounts WHERE user_id = ? ORDER BY name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var accounts []*account.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (r *AccountStore) Update(ctx context.Context, a *account.Account) error {
	a.IsRegistered = a.Type.IsRegistered()
	_, err := r.db.ExecContext(ctx,
		`UPDATE accounts SET name=?, type=?, broker=?, currency=?, account_number=?, is_registered=?
		 WHERE id=?`,
		a.Name, string(a.Type), a.Broker, a.Currency, a.AccountNumber, boolToInt(a.IsRegistered), a.ID,
	)
	return err
}

func (r *AccountStore) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=?`, id)
	return err
}

type accountScanner interface {
	Scan(dest ...any) error
}

func scanAccount(s accountScanner) (*account.Account, error) {
	var a account.Account
	var accountType string
	var isRegistered int
	var createdAt string

	err := s.Scan(
		&a.ID, &a.UserID, &a.Name, &accountType, &a.Broker,
		&a.Currency, &a.AccountNumber, &isRegistered, &createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan account: %w", err)
	}
	a.Type = account.Type(accountType)
	a.IsRegistered = isRegistered == 1
	a.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
	return &a, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
