// Package debug persists flow/node error events emitted by the engine
// during a debug or release run (previously WebSocket-only in Django, never
// stored — see migrations/000034_debug_error_event.up.sql) and exposes them
// read-only, owner-scoped, at GET /api/v1/debug/errors/.
//
// Writes arrive over the Redis Stream consumer (internal/shared/redisq),
// not HTTP — see docs/redis-stream-events.md for the "debug.error_event"
// contract this module's Usecase.RecordError implements.
package debug

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ErrorEvent is one recorded flow/node failure.
type ErrorEvent struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	ChatID    int64
	NodeID    string
	NodeType  string
	Level     string
	Message   string
	Detail    string
	EventTS   *time.Time
	CreatedAt time.Time
}

// RecordErrorInput is the write-side payload, keyed by the engine's raw
// project reference (see Usecase.ResolveProjectRef) rather than this
// backend's internal project id — the engine only knows the Django legacy
// pk or `project.uuid`, never this table's uuid5-derived `projects.id`.
type RecordErrorInput struct {
	ProjectRef string // legacy int pk (as string) OR Django project.uuid
	ChatID     int64
	NodeID     string
	NodeType   string
	Level      string
	Message    string
	Detail     string
	EventTS    *time.Time
}

// NodeEvent is one recorded per-node execution status ("debug.node_event").
type NodeEvent struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	ChatID    int64
	NodeID    string
	NodeType  string
	Status    string
	Error     string
	RunID     int64
	Seq       int64
	EventTS   *time.Time
	CreatedAt time.Time
}

// RecordNodeEventInput is the write-side payload for "debug.node_event".
type RecordNodeEventInput struct {
	ProjectRef string
	ChatID     int64
	NodeID     string
	NodeType   string
	Status     string
	Error      string
	RunID      int64
	Seq        int64
	EventTS    *time.Time
}

// RecordContextEventInput is the write-side payload for
// "debug.context_event" — cached (not persisted to Postgres), see
// Usecase.RecordContext.
type RecordContextEventInput struct {
	ProjectRef string
	ChatID     int64
	Variables  json.RawMessage
	EventTS    *time.Time
}

// Repository is the Postgres persistence port.
type Repository interface {
	// ResolveProjectByLegacyID/ResolveProjectByUUID mirror Django's
	// resolve_project(): try the value as a Django legacy int pk first,
	// then as `project.uuid`. Returns database.ErrNotFound when no project
	// matches either form.
	ResolveProjectByLegacyID(ctx context.Context, legacyID int32) (uuid.UUID, error)
	ResolveProjectByUUID(ctx context.Context, projectUUID uuid.UUID) (uuid.UUID, error)

	InsertErrorEvent(ctx context.Context, e ErrorEvent) (*ErrorEvent, error)
	// ListErrorEventsForOwner is owner-scoped (rule #4): projectID must
	// belong to ownerID or the result is empty, never another owner's data.
	ListErrorEventsForOwner(ctx context.Context, ownerID, projectID uuid.UUID, limit int32) ([]ErrorEvent, error)

	InsertNodeEvent(ctx context.Context, e NodeEvent) (*NodeEvent, error)
}

// ContextCache is the (optional) latest-snapshot cache port for
// "debug.context_event" — mirrors Django's Redis-cache-only behavior (never
// a growing Postgres table, see docs/redis-stream-events.md). Nil-able: a
// backend without Redis wired just no-ops the write (see NewUsecase).
type ContextCache interface {
	// SetContext caches variables for (projectID, chatID) with a TTL,
	// overwriting any previous snapshot.
	SetContext(ctx context.Context, projectID uuid.UUID, chatID int64, variables json.RawMessage, ttl time.Duration) error
}
