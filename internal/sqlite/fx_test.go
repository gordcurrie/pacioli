package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/shopspring/decimal"
)

func TestFXStore(t *testing.T) {
	store := sqlite.NewFXStore(newTestDB(t))
	ctx := context.Background()

	date := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	rate := decimal.NewFromFloat(1.3542)

	t.Run("store and get rate", func(t *testing.T) {
		if err := store.StoreRate(ctx, date, "USD", "CAD", rate, "boc"); err != nil {
			t.Fatalf("StoreRate: %v", err)
		}

		got, err := store.GetRate(ctx, date, "USD", "CAD")
		if err != nil {
			t.Fatalf("GetRate: %v", err)
		}
		if !got.Equal(rate) {
			t.Errorf("rate = %s, want %s", got, rate)
		}
	})

	t.Run("upsert overwrites existing rate", func(t *testing.T) {
		updated := decimal.NewFromFloat(1.3600)
		if err := store.StoreRate(ctx, date, "USD", "CAD", updated, "manual"); err != nil {
			t.Fatalf("StoreRate update: %v", err)
		}
		got, err := store.GetRate(ctx, date, "USD", "CAD")
		if err != nil {
			t.Fatalf("GetRate after update: %v", err)
		}
		if !got.Equal(updated) {
			t.Errorf("updated rate = %s, want %s", got, updated)
		}
	})

	t.Run("missing rate returns ErrNotFound", func(t *testing.T) {
		_, err := store.GetRate(ctx, date, "GBP", "CAD")
		if !errors.Is(err, errs.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}
