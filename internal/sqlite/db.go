// Package sqlite provides SQLite implementations of every domain Store interface.
// Each store (AccountStore, TransactionStore, etc.) wraps a *sql.DB and satisfies
// its matching interface from the corresponding domain package. Schema migrations are
// embedded and run automatically on startup via Open.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens a SQLite database at dsn, sets a busy timeout and WAL mode, and runs all pending migrations.
func Open(dsn string) (*sql.DB, error) {
	// Embed per-connection PRAGMAs in the DSN so every new pool connection has
	// them regardless of SetMaxOpenConns. PRAGMA foreign_keys and busy_timeout are
	// per-connection in SQLite; without DSN params, pool expansion silently drops them.
	// journal_mode=WAL is file-persisted and does not need to be here.
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("sqlite", dsn+sep+"_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite doesn't support concurrent writes

	if _, err := db.ExecContext(context.Background(), `PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), `PRAGMA journal_mode = WAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	return nil
}
