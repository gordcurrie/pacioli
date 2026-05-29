package audit

import (
	"context"
	"time"
)

type Action string

const (
	ActionCreate Action = "create"
	ActionDelete Action = "delete"
)

type EntityType string

const (
	EntityAccount     EntityType = "account"
	EntitySecurity    EntityType = "security"
	EntityTransaction EntityType = "transaction"
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
	Action     Action
	EntityType EntityType
	EntityID   int64
	Source     Source
	Snapshot   string    // JSON of entity state before delete; empty on create
	ImportID   string    // batch tag for CSV/API imports (Phase 3/4); empty for manual entries
	CreatedAt  time.Time
}

type Store interface {
	Log(ctx context.Context, e *Entry) error
}
