package fx

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type Store interface {
	GetRate(ctx context.Context, date time.Time, fromCurrency, toCurrency string) (decimal.Decimal, error)
	StoreRate(ctx context.Context, date time.Time, fromCurrency, toCurrency string, rate decimal.Decimal, source string) error
}
