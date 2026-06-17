// Package distribution defines the Distribution type for annual T3 slip data and
// the Store interface for persistence. Return of Capital (ROC) amounts from T3
// distributions reduce a security's ACB; this data is entered manually per tax year.
package distribution

import (
	"context"

	"github.com/shopspring/decimal"
)

// Distribution records annual T3 slip data that affects ACB via Return of Capital.
type Distribution struct {
	ID                       int64
	SecurityID               int64
	TaxYear                  int
	ROCPerUnit               decimal.Decimal
	TotalDistributionPerUnit decimal.Decimal
	RecordDate               string
	Source                   string
	Notes                    string
}

// Store defines persistence operations for T3 distribution records.
type Store interface {
	Upsert(ctx context.Context, d *Distribution) error
	GetBySecurityYear(ctx context.Context, securityID int64, taxYear int) (*Distribution, error)
	ListBySecurity(ctx context.Context, securityID int64) ([]*Distribution, error)
	ListByTaxYear(ctx context.Context, taxYear int) ([]*Distribution, error)
}
