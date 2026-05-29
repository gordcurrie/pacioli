package sqlite_test

import (
	"context"
	"testing"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/sqlite"
)

func TestSecurityStore(t *testing.T) {
	store := sqlite.NewSecurityStore(newTestDB(t))
	ctx := context.Background()

	t.Run("create and get by id", func(t *testing.T) {
		s := &security.Security{
			Ticker:   "VFV",
			Exchange: "TSX",
			Name:     "Vanguard S&P 500 Index ETF",
			Type:     security.TypeETF,
			Currency: "CAD",
		}
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if s.ID == 0 {
			t.Fatal("expected non-zero ID after create")
		}

		got, err := store.GetByID(ctx, s.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Ticker != s.Ticker {
			t.Errorf("Ticker = %q, want %q", got.Ticker, s.Ticker)
		}
		if got.Type != security.TypeETF {
			t.Errorf("Type = %q, want %q", got.Type, security.TypeETF)
		}
	})

	t.Run("get by ticker and exchange", func(t *testing.T) {
		s := &security.Security{
			Ticker:   "XEQT",
			Exchange: "TSX",
			Name:     "iShares Core Equity ETF Portfolio",
			Type:     security.TypeETF,
			Currency: "CAD",
		}
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create: %v", err)
		}

		got, err := store.GetByTickerExchange(ctx, "XEQT", "TSX")
		if err != nil {
			t.Fatalf("GetByTickerExchange: %v", err)
		}
		if got.ID != s.ID {
			t.Errorf("ID = %d, want %d", got.ID, s.ID)
		}
	})

	t.Run("search", func(t *testing.T) {
		results, err := store.Search(ctx, "Vanguard")
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least one search result")
		}
	})

	t.Run("list all", func(t *testing.T) {
		all, err := store.ListAll(ctx)
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(all) < 2 {
			t.Errorf("expected at least 2 securities, got %d", len(all))
		}
	})
}
