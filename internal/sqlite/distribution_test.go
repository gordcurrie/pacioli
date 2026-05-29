package sqlite_test

import (
	"context"
	"testing"

	"github.com/gordcurrie/pacioli/internal/distribution"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/shopspring/decimal"
)

func TestDistributionStore(t *testing.T) {
	db := newTestDB(t)
	securities := sqlite.NewSecurityStore(db)
	store := sqlite.NewDistributionStore(db)
	ctx := context.Background()

	sec := &security.Security{Ticker: "ZWC", Exchange: "TSX", Name: "BMO Canadian High Div Covered Call ETF", Type: security.TypeETF, Currency: "CAD"}
	if err := securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}

	t.Run("upsert and get", func(t *testing.T) {
		d := &distribution.Distribution{
			SecurityID:               sec.ID,
			TaxYear:                  2023,
			ROCPerUnit:               decimal.NewFromFloat(0.12),
			TotalDistributionPerUnit: decimal.NewFromFloat(0.85),
			RecordDate:               "2024-03-15",
			Source:                   "T3",
		}
		if err := store.Upsert(ctx, d); err != nil {
			t.Fatalf("Upsert: %v", err)
		}

		got, err := store.GetBySecurityYear(ctx, sec.ID, 2023)
		if err != nil {
			t.Fatalf("GetBySecurityYear: %v", err)
		}
		if !got.ROCPerUnit.Equal(d.ROCPerUnit) {
			t.Errorf("ROCPerUnit = %s, want %s", got.ROCPerUnit, d.ROCPerUnit)
		}
	})

	t.Run("upsert updates existing", func(t *testing.T) {
		updated := &distribution.Distribution{
			SecurityID:               sec.ID,
			TaxYear:                  2023,
			ROCPerUnit:               decimal.NewFromFloat(0.15),
			TotalDistributionPerUnit: decimal.NewFromFloat(0.90),
		}
		if err := store.Upsert(ctx, updated); err != nil {
			t.Fatalf("Upsert update: %v", err)
		}

		got, err := store.GetBySecurityYear(ctx, sec.ID, 2023)
		if err != nil {
			t.Fatalf("GetBySecurityYear after update: %v", err)
		}
		if !got.ROCPerUnit.Equal(decimal.NewFromFloat(0.15)) {
			t.Errorf("ROCPerUnit after update = %s, want 0.15", got.ROCPerUnit)
		}
	})

	t.Run("list by security", func(t *testing.T) {
		_ = store.Upsert(ctx, &distribution.Distribution{SecurityID: sec.ID, TaxYear: 2022, ROCPerUnit: decimal.NewFromFloat(0.10)})
		all, err := store.ListBySecurity(ctx, sec.ID)
		if err != nil {
			t.Fatalf("ListBySecurity: %v", err)
		}
		if len(all) < 2 {
			t.Errorf("expected at least 2 distributions, got %d", len(all))
		}
	})
}
