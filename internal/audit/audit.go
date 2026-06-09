package audit

import (
	"context"
	"time"
)

type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

type EntityType string

const (
	EntityAccount     EntityType = "account"
	EntitySecurity    EntityType = "security"
	EntityTransaction EntityType = "transaction"
	EntityUser        EntityType = "user"
)

type Source string

const (
	SourceManual       Source = "manual"
	SourceQuestrade    Source = "questrade"
	SourceCanaccordCSV Source = "canaccord_csv"
)

type Entry struct {
	ID         int64
	UserID     int64
	UserEmail  string // populated by List; empty when written via Log
	Action     Action
	EntityType EntityType
	EntityID   int64
	Source     Source
	Snapshot   string    // JSON of entity state before delete; empty on create
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

type Store interface {
	Log(ctx context.Context, e *Entry) error
	List(ctx context.Context, f ListFilter) ([]*Entry, error)
	Count(ctx context.Context, f ListFilter) (int, error)
}
