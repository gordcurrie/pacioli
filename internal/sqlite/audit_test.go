package sqlite_test

import (
	"context"
	"testing"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/sqlite"
)

func TestAuditStore(t *testing.T) {
	db := newTestDB(t)
	store := sqlite.NewAuditStore(db)
	ctx := context.Background()

	// user row already seeded by newTestDB (id=1)
t.Run("log create entry", func(t *testing.T) {
		e := &audit.Entry{
			UserID:     1,
			Action:     audit.ActionCreate,
			EntityType: audit.EntityAccount,
			EntityID:   42,
			Source:     audit.SourceManual,
		}
		if err := store.Log(ctx, e); err != nil {
			t.Fatalf("Log: %v", err)
		}

		var action, entityType, source string
		var snapshot, importID *string
		err := db.QueryRowContext(ctx,
			`SELECT action, entity_type, source, snapshot, import_id FROM audit_log WHERE entity_id=42`).
			Scan(&action, &entityType, &source, &snapshot, &importID)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if action != "create" {
			t.Errorf("action = %q, want create", action)
		}
		if entityType != "account" {
			t.Errorf("entity_type = %q, want account", entityType)
		}
		if source != "manual" {
			t.Errorf("source = %q, want manual", source)
		}
		if snapshot != nil {
			t.Errorf("snapshot should be NULL for create, got %q", *snapshot)
		}
		if importID != nil {
			t.Errorf("import_id should be NULL, got %q", *importID)
		}
	})

	t.Run("log update entry", func(t *testing.T) {
		e := &audit.Entry{
			UserID:     1,
			Action:     audit.ActionUpdate,
			EntityType: audit.EntitySecurity,
			EntityID:   77,
			Source:     audit.SourceManual,
		}
		if err := store.Log(ctx, e); err != nil {
			t.Fatalf("Log update: %v", err)
		}
		var action string
		if err := db.QueryRowContext(ctx,
			`SELECT action FROM audit_log WHERE entity_id=77`).Scan(&action); err != nil {
			t.Fatalf("query: %v", err)
		}
		if action != "update" {
			t.Errorf("action = %q, want update", action)
		}
	})

	t.Run("log delete entry with snapshot", func(t *testing.T) {
		snap := `{"id":99,"name":"old account"}`
		e := &audit.Entry{
			UserID:     1,
			Action:     audit.ActionDelete,
			EntityType: audit.EntityTransaction,
			EntityID:   99,
			Source:     audit.SourceCanaccordCSV,
			Snapshot:   snap,
		}
		if err := store.Log(ctx, e); err != nil {
			t.Fatalf("Log: %v", err)
		}

		var snapshot *string
		err := db.QueryRowContext(ctx,
			`SELECT snapshot FROM audit_log WHERE entity_id=99`).Scan(&snapshot)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if snapshot == nil {
			t.Fatal("snapshot should not be NULL for delete with snapshot")
		}
		if *snapshot != snap {
			t.Errorf("snapshot = %q, want %q", *snapshot, snap)
		}
	})

	t.Run("list all entries", func(t *testing.T) {
		entries, err := store.List(ctx, audit.ListFilter{Limit: 10})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) < 3 {
			t.Errorf("List returned %d entries, want >= 3", len(entries))
		}
		// newest first
		for i := 1; i < len(entries); i++ {
			if entries[i].CreatedAt.After(entries[i-1].CreatedAt) {
				t.Error("entries not ordered newest-first")
				break
			}
		}
		// UserEmail populated via JOIN
		for _, e := range entries {
			if e.UserEmail == "" {
				t.Errorf("entry %d has empty UserEmail", e.ID)
			}
		}
	})

	t.Run("list filtered by entity_type", func(t *testing.T) {
		entries, err := store.List(ctx, audit.ListFilter{EntityType: audit.EntityAccount, Limit: 10})
		if err != nil {
			t.Fatalf("List filtered: %v", err)
		}
		for _, e := range entries {
			if e.EntityType != audit.EntityAccount {
				t.Errorf("entry entity_type = %q, want account", e.EntityType)
			}
		}
	})

	t.Run("count all", func(t *testing.T) {
		n, err := store.Count(ctx, audit.ListFilter{})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n < 3 {
			t.Errorf("Count = %d, want >= 3", n)
		}
	})

	t.Run("count filtered", func(t *testing.T) {
		n, err := store.Count(ctx, audit.ListFilter{Action: audit.ActionDelete})
		if err != nil {
			t.Fatalf("Count filtered: %v", err)
		}
		if n != 1 {
			t.Errorf("Count(delete) = %d, want 1", n)
		}
	})
}
