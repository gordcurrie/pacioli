package sqlite_test

import (
	"context"
	"testing"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/sqlite"
)

func TestAccountStore(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	store := sqlite.NewAccountStore(db)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO users (email) VALUES ('test@test.com')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	t.Run("create and get", func(t *testing.T) {
		a := &account.Account{
			UserID:   1,
			Name:     "Margin Account",
			Type:     account.TypeMargin,
			Broker:   "Questrade",
			Currency: "CAD",
		}

		if err := store.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if a.ID == 0 {
			t.Fatal("expected non-zero ID after create")
		}
		if a.IsRegistered {
			t.Error("margin account should not be registered")
		}

		got, err := store.GetByID(ctx, a.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Name != a.Name {
			t.Errorf("Name = %q, want %q", got.Name, a.Name)
		}
		if got.Type != account.TypeMargin {
			t.Errorf("Type = %q, want %q", got.Type, account.TypeMargin)
		}
	})

	t.Run("list by user", func(t *testing.T) {
		tfsa := &account.Account{
			UserID:   1,
			Name:     "TFSA",
			Type:     account.TypeTFSA,
			Broker:   "Questrade",
			Currency: "CAD",
		}
		if err := store.Create(ctx, tfsa); err != nil {
			t.Fatalf("Create TFSA: %v", err)
		}
		if !tfsa.IsRegistered {
			t.Error("TFSA should be registered")
		}

		accounts, err := store.ListByUser(ctx, 1)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(accounts) < 2 {
			t.Errorf("expected at least 2 accounts, got %d", len(accounts))
		}
	})

	t.Run("delete", func(t *testing.T) {
		a := &account.Account{
			UserID:   1,
			Name:     "To Delete",
			Type:     account.TypeCash,
			Broker:   "Questrade",
			Currency: "CAD",
		}
		if err := store.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := store.Delete(ctx, a.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.GetByID(ctx, a.ID); err == nil {
			t.Error("expected error after delete, got nil")
		}
	})
}
