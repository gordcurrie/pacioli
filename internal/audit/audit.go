// Package audit defines the immutable audit log Entry type and the Store interface.
// Every create, update, and delete in the application writes an Entry so admins
// can review the full change history via the audit log viewer.
package audit

import (
	"context"
	"time"
)

// Action describes what was done to an entity in an audit log entry.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

// EntityType identifies which domain entity an audit log entry refers to.
type EntityType string

const (
	EntityAccount     EntityType = "account"
	EntitySecurity    EntityType = "security"
	EntityTransaction EntityType = "transaction"
	EntityUser        EntityType = "user"
)

// Source identifies the originating data source for an audit log entry.
type Source string

const (
	SourceManual       Source = "manual"
	SourceQuestrade    Source = "questrade"
	SourceCanaccordCSV Source = "canaccord_csv"
)

// Entry is a single immutable record in the audit log.
type Entry struct {
	ID         int64
	UserID     int64
	UserEmail  string // actor email; written at Log time, read back by List/Page
	Action     Action
	EntityType EntityType
	EntityID   int64
	Source     Source
	BeforeState string // JSON of entity state before the operation; empty on create
	ImportID   string    // batch tag for CSV/API imports (Phase 3/4); empty for manual entries
	CreatedAt  time.Time
}

// ListFilter narrows results returned by List and Count.
// Zero values mean "no filter": empty string matches all, zero UserID matches all users.
type ListFilter struct {
	EntityType EntityType
	Action     Action
	UserID     int64
	Limit      int
	Offset     int
}

// Store defines persistence operations for audit log entries.
type Store interface {
	Log(ctx context.Context, e *Entry) error
	List(ctx context.Context, f ListFilter) ([]*Entry, error)
	Count(ctx context.Context, f ListFilter) (int, error)
	Page(ctx context.Context, f ListFilter) ([]*Entry, int, error)
}
