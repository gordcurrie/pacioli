package sqlite_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/gordcurrie/pacioli/internal/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Seed two users so FK constraints on accounts/transactions are satisfied.
	// Tests that create accounts with UserID: 1 or UserID: 2 depend on these rows.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO users (email) VALUES ('user1@test.com'), ('user2@test.com')`,
	); err != nil {
		t.Fatalf("seed test users: %v", err)
	}
	return db
}
