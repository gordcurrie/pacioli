package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/gordcurrie/pacioli/internal/audit"
)

// AuditStore is the SQLite implementation of audit.Store.
type AuditStore struct {
	db *sql.DB
}

// NewAuditStore constructs an AuditStore backed by db.
func NewAuditStore(db *sql.DB) *AuditStore {
	return &AuditStore{db: db}
}

func (s *AuditStore) Log(ctx context.Context, e *audit.Entry) error {
	var beforeState, importID *string
	if e.BeforeState != "" {
		beforeState = &e.BeforeState
	}
	if e.ImportID != "" {
		importID = &e.ImportID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, actor_email, action, entity_type, entity_id, source, before_state, import_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.UserID, e.UserEmail, string(e.Action), string(e.EntityType), e.EntityID,
		string(e.Source), beforeState, importID,
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
		SELECT al.id, al.user_id, al.actor_email,
		       al.action, al.entity_type, al.entity_id,
		       al.source, COALESCE(al.before_state,''), COALESCE(al.import_id,''),
		       al.created_at
		FROM audit_log al
		WHERE (? = '' OR al.entity_type = ?)
		  AND (? = '' OR al.action = ?)
		  AND (? = 0 OR al.user_id = ?)
		ORDER BY al.created_at DESC, al.id DESC
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
			&source, &e.BeforeState, &e.ImportID,
			&e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("audit list scan: %w", err)
		}
		e.Action = audit.Action(action)
		e.EntityType = audit.EntityType(entityType)
		e.Source = audit.Source(source)
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit list: %w", err)
	}
	return entries, nil
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

func (s *AuditStore) Page(ctx context.Context, f audit.ListFilter) ([]*audit.Entry, int, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("audit page: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var total int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM audit_log al
		WHERE (? = '' OR al.entity_type = ?)
		  AND (? = '' OR al.action = ?)
		  AND (? = 0 OR al.user_id = ?)`,
		string(f.EntityType), string(f.EntityType),
		string(f.Action), string(f.Action),
		f.UserID, f.UserID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit page count: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT al.id, al.user_id, al.actor_email,
		       al.action, al.entity_type, al.entity_id,
		       al.source, COALESCE(al.before_state,''), COALESCE(al.import_id,''),
		       al.created_at
		FROM audit_log al
		WHERE (? = '' OR al.entity_type = ?)
		  AND (? = '' OR al.action = ?)
		  AND (? = 0 OR al.user_id = ?)
		ORDER BY al.created_at DESC, al.id DESC
		LIMIT ? OFFSET ?`,
		string(f.EntityType), string(f.EntityType),
		string(f.Action), string(f.Action),
		f.UserID, f.UserID,
		limit, f.Offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("audit page list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []*audit.Entry
	for rows.Next() {
		var e audit.Entry
		var action, entityType, source string
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.UserEmail,
			&action, &entityType, &e.EntityID,
			&source, &e.BeforeState, &e.ImportID,
			&e.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("audit page scan: %w", err)
		}
		e.Action = audit.Action(action)
		e.EntityType = audit.EntityType(entityType)
		e.Source = audit.Source(source)
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("audit page: %w", err)
	}

	return entries, total, nil
}
