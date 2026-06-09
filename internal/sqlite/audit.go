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

func (s *AuditStore) List(ctx context.Context, f audit.ListFilter) ([]*audit.Entry, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT al.id, al.user_id, COALESCE(u.email, '(deleted)'),
		       al.action, al.entity_type, al.entity_id,
		       al.source, COALESCE(al.snapshot,''), COALESCE(al.import_id,''),
		       al.created_at
		FROM audit_log al
		LEFT JOIN users u ON al.user_id = u.id
		WHERE (? = '' OR al.entity_type = ?)
		  AND (? = '' OR al.action = ?)
		  AND (? = 0 OR al.user_id = ?)
		ORDER BY al.created_at DESC
		LIMIT ? OFFSET ?`,
		string(f.EntityType), string(f.EntityType),
		string(f.Action), string(f.Action),
		f.UserID, f.UserID,
		f.Limit, f.Offset,
	)
	if err != nil {
		return nil, fmt.Errorf("audit list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []*audit.Entry
	for rows.Next() {
		var e audit.Entry
		var action, entityType, source string
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.UserEmail,
			&action, &entityType, &e.EntityID,
			&source, &e.Snapshot, &e.ImportID,
			&e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("audit list scan: %w", err)
		}
		e.Action = audit.Action(action)
		e.EntityType = audit.EntityType(entityType)
		e.Source = audit.Source(source)
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

func (s *AuditStore) Count(ctx context.Context, f audit.ListFilter) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM audit_log al
		WHERE (? = '' OR al.entity_type = ?)
		  AND (? = '' OR al.action = ?)
		  AND (? = 0 OR al.user_id = ?)`,
		string(f.EntityType), string(f.EntityType),
		string(f.Action), string(f.Action),
		f.UserID, f.UserID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("audit count: %w", err)
	}
	return n, nil
}
