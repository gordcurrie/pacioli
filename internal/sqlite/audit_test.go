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

	// seed user required by FK
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email) VALUES (1, 'test@test.com')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

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
}
