package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/shopspring/decimal"
)

type FXStore struct {
	db *sql.DB
}

func NewFXStore(db *sql.DB) *FXStore {
	return &FXStore{db: db}
}

func (r *FXStore) GetRate(ctx context.Context, date time.Time, fromCurrency, toCurrency string) (decimal.Decimal, error) {
	var rateStr string
	err := r.db.QueryRowContext(ctx,
		`SELECT rate FROM fx_rates WHERE date=? AND from_currency=? AND to_currency=?`,
		date.Format(time.DateOnly), fromCurrency, toCurrency,
	).Scan(&rateStr)

	if errors.Is(err, sql.ErrNoRows) {
		return decimal.Zero, errs.ErrNotFound
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("get fx rate: %w", err)
	}

	rate, err := decimal.NewFromString(rateStr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse fx rate: %w", err)
	}
	return rate, nil
}

func (r *FXStore) StoreRate(ctx context.Context, date time.Time, fromCurrency, toCurrency string, rate decimal.Decimal, source string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO fx_rates (date, from_currency, to_currency, rate, source)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(date, from_currency, to_currency) DO UPDATE SET rate=excluded.rate, source=excluded.source`,
		date.Format(time.DateOnly), fromCurrency, toCurrency, rate.String(), source,
	)
	return err
}
