// Package fx defines the Store interface for caching foreign exchange rates.
// Rates are fetched from the Bank of Canada and cached to avoid repeated API calls.
package fx

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Store defines persistence operations for cached foreign exchange rates.
type Store interface {
	GetRate(ctx context.Context, date time.Time, fromCurrency, toCurrency string) (decimal.Decimal, error)
	StoreRate(ctx context.Context, date time.Time, fromCurrency, toCurrency string, rate decimal.Decimal, source string) error
}
