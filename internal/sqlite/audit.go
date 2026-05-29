package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/gordcurrie/pacioli/internal/audit"
)

type AuditStore struct {
	db *sql.DB
}

func NewAuditStore(db *sql.DB) *AuditStore {
	return &AuditStore{db: db}
}

func (s *AuditStore) Log(ctx context.Context, e *audit.Entry) error {
	var snapshot, importID *string
	if e.Snapshot != "" {
		snapshot = &e.Snapshot
	}
	if e.ImportID != "" {
		importID = &e.ImportID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, action, entity_type, entity_id, source, snapshot, import_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.UserID, string(e.Action), string(e.EntityType), e.EntityID,
		string(e.Source), snapshot, importID,
	)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	return nil
}
