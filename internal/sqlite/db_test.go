package sqlite_test

import (
	"context"
	"testing"

	"github.com/gordcurrie/pacioli/internal/sqlite"
)

func TestOpen(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
