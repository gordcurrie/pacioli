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

type Store interface {
	Upsert(ctx context.Context, d *Distribution) error
	GetBySecurityYear(ctx context.Context, securityID int64, taxYear int) (*Distribution, error)
	ListBySecurity(ctx context.Context, securityID int64) ([]*Distribution, error)
	ListByTaxYear(ctx context.Context, taxYear int) ([]*Distribution, error)
}
