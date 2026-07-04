package debug

import (
	"context"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) ResolveProjectByLegacyID(ctx context.Context, legacyID int32) (uuid.UUID, error) {
	id, err := r.queries.DebugResolveProjectByLegacyID(ctx, &legacyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, database.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve project by legacy id: %w", err)
	}
	return id, nil
}

func (r *pgRepo) ResolveProjectByUUID(ctx context.Context, projectUUID uuid.UUID) (uuid.UUID, error) {
	id, err := r.queries.DebugResolveProjectByUUID(ctx, projectUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, database.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve project by uuid: %w", err)
	}
	return id, nil
}

func (r *pgRepo) GetProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	ownerID, err := r.queries.DebugGetProjectOwner(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, database.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get project owner: %w", err)
	}
	return ownerID, nil
}

func (r *pgRepo) InsertErrorEvent(ctx context.Context, e ErrorEvent) (*ErrorEvent, error) {
	id := e.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row, err := r.queries.DebugInsertErrorEvent(ctx, sqlcdb.DebugInsertErrorEventParams{
		ID:        id,
		ProjectID: e.ProjectID,
		ChatID:    e.ChatID,
		NodeID:    e.NodeID,
		NodeType:  e.NodeType,
		Level:     e.Level,
		Message:   e.Message,
		Detail:    e.Detail,
		EventTs:   e.EventTS,
	})
	if err != nil {
		return nil, fmt.Errorf("insert debug error event: %w", err)
	}
	out := fromSQLC(row)
	return &out, nil
}

func (r *pgRepo) InsertNodeEvent(ctx context.Context, e NodeEvent) (*NodeEvent, error) {
	id := e.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row, err := r.queries.DebugInsertNodeEvent(ctx, sqlcdb.DebugInsertNodeEventParams{
		ID:        id,
		ProjectID: e.ProjectID,
		ChatID:    e.ChatID,
		NodeID:    e.NodeID,
		NodeType:  e.NodeType,
		Status:    e.Status,
		Error:     e.Error,
		RunID:     e.RunID,
		Seq:       e.Seq,
		EventTs:   e.EventTS,
	})
	if err != nil {
		return nil, fmt.Errorf("insert debug node event: %w", err)
	}
	out := nodeEventFromSQLC(row)
	return &out, nil
}

func (r *pgRepo) ListErrorEventsForOwner(ctx context.Context, ownerID, projectID uuid.UUID, limit int32) ([]ErrorEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.queries.DebugListErrorEventsForOwner(ctx, sqlcdb.DebugListErrorEventsForOwnerParams{
		ProjectID:  projectID,
		OwnerID:    ownerID,
		LimitCount: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list debug error events: %w", err)
	}
	out := make([]ErrorEvent, len(rows))
	for i, row := range rows {
		out[i] = fromSQLC(row)
	}
	return out, nil
}

func fromSQLC(r sqlcdb.DebugErrorEvent) ErrorEvent {
	return ErrorEvent{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		ChatID:    r.ChatID,
		NodeID:    r.NodeID,
		NodeType:  r.NodeType,
		Level:     r.Level,
		Message:   r.Message,
		Detail:    r.Detail,
		EventTS:   r.EventTs,
		CreatedAt: r.CreatedAt,
	}
}

func nodeEventFromSQLC(r sqlcdb.DebugNodeEvent) NodeEvent {
	return NodeEvent{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		ChatID:    r.ChatID,
		NodeID:    r.NodeID,
		NodeType:  r.NodeType,
		Status:    r.Status,
		Error:     r.Error,
		RunID:     r.RunID,
		Seq:       r.Seq,
		EventTS:   r.EventTs,
		CreatedAt: r.CreatedAt,
	}
}
