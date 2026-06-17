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
			UserEmail:  "user1@test.com",
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
			`SELECT action, entity_type, source, before_state, import_id FROM audit_log WHERE entity_id=42`).
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
			UserEmail:  "user1@test.com",
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
			UserEmail:  "user1@test.com",
			Action:     audit.ActionDelete,
			EntityType: audit.EntityTransaction,
			EntityID:   99,
			Source:     audit.SourceCanaccordCSV,
			BeforeState: snap,
		}
		if err := store.Log(ctx, e); err != nil {
			t.Fatalf("Log: %v", err)
		}

		var snapshot *string
		err := db.QueryRowContext(ctx,
			`SELECT before_state FROM audit_log WHERE entity_id=99`).Scan(&snapshot)
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
		// UserEmail populated from denormalized actor_email column
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

	t.Run("list filtered by action", func(t *testing.T) {
		entries, err := store.List(ctx, audit.ListFilter{Action: audit.ActionUpdate, Limit: 10})
		if err != nil {
			t.Fatalf("List(update): %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("List(update) returned %d entries, want 1", len(entries))
		}
		if entries[0].Action != audit.ActionUpdate {
			t.Errorf("entry action = %q, want update", entries[0].Action)
		}
	})

	t.Run("list filtered by user_id", func(t *testing.T) {
		entries, err := store.List(ctx, audit.ListFilter{UserID: 1, Limit: 10})
		if err != nil {
			t.Fatalf("List(user_id=1): %v", err)
		}
		for _, e := range entries {
			if e.UserID != 1 {
				t.Errorf("entry user_id = %d, want 1", e.UserID)
			}
		}
		if len(entries) < 3 {
			t.Errorf("List(user_id=1) returned %d entries, want >= 3", len(entries))
		}
	})

	t.Run("list pagination offset", func(t *testing.T) {
		all, err := store.List(ctx, audit.ListFilter{Limit: 10})
		if err != nil {
			t.Fatalf("List all: %v", err)
		}
		if len(all) < 2 {
			t.Skip("need at least 2 entries for pagination test")
		}
		page1, err := store.List(ctx, audit.ListFilter{Limit: 1, Offset: 0})
		if err != nil {
			t.Fatalf("List page1: %v", err)
		}
		page2, err := store.List(ctx, audit.ListFilter{Limit: 1, Offset: 1})
		if err != nil {
			t.Fatalf("List page2: %v", err)
		}
		if len(page1) != 1 || len(page2) != 1 {
			t.Fatalf("page1 len=%d, page2 len=%d; want 1 each", len(page1), len(page2))
		}
		if page1[0].ID == page2[0].ID {
			t.Errorf("page1 and page2 returned same entry id=%d", page1[0].ID)
		}
	})

	t.Run("page returns entries and total", func(t *testing.T) {
		entries, total, err := store.Page(ctx, audit.ListFilter{Limit: 10})
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		if total < 3 {
			t.Errorf("Page total = %d, want >= 3", total)
		}
		if len(entries) != total {
			t.Errorf("Page len(entries) = %d, want %d", len(entries), total)
		}
	})

	t.Run("page filtered matches count", func(t *testing.T) {
		entries, total, err := store.Page(ctx, audit.ListFilter{Action: audit.ActionCreate, Limit: 10})
		if err != nil {
			t.Fatalf("Page(create): %v", err)
		}
		if total != 1 {
			t.Errorf("Page(create) total = %d, want 1", total)
		}
		if len(entries) != 1 {
			t.Errorf("Page(create) entries = %d, want 1", len(entries))
		}
		if entries[0].Action != audit.ActionCreate {
			t.Errorf("entry action = %q, want create", entries[0].Action)
		}
	})

	t.Run("page total reflects all rows not just limit", func(t *testing.T) {
		// Total should be the unfiltered count even when Limit < total.
		_, total, err := store.Page(ctx, audit.ListFilter{Limit: 1})
		if err != nil {
			t.Fatalf("Page(limit=1): %v", err)
		}
		if total < 3 {
			t.Errorf("Page(limit=1) total = %d, want >= 3 (total of all rows, not just returned)", total)
		}
	})
}
