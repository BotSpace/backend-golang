package execution

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

type pgRepo struct{ queries *sqlcdb.Queries }

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) Start(ctx context.Context, in StartInput) (*Execution, error) {
	row, err := r.queries.UpsertExecutionStarted(ctx, sqlcdb.UpsertExecutionStartedParams{
		ID: in.ID, ProjectID: in.ProjectID, ChatID: in.ChatID,
		TelegramUpdateID: in.TelegramUpdateID, RunID: in.RunID,
		TriggerType: in.TriggerType, RuntimeMode: in.RuntimeMode,
		FlowHash: in.FlowHash, EventTs: in.EventTS,
	})
	if err != nil {
		return nil, fmt.Errorf("start execution: %w", err)
	}
	out := executionFromSQLC(row)
	return &out, nil
}

func (r *pgRepo) Finish(ctx context.Context, in FinishInput) (*Execution, error) {
	eventTS := in.EventTS
	row, err := r.queries.FinishExecution(ctx, sqlcdb.FinishExecutionParams{
		ID: in.ID, Status: string(in.Status), Error: in.Error, EventTs: &eventTS,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finish execution: %w", err)
	}
	out := executionFromSQLC(row)
	return &out, nil
}

func (r *pgRepo) RecordNodeRun(ctx context.Context, in NodeRunInput) (*NodeRun, error) {
	row, err := r.queries.UpsertExecutionNodeRun(ctx, sqlcdb.UpsertExecutionNodeRunParams{
		ExecutionID: in.ExecutionID, NodeID: in.NodeID, NodeType: in.NodeType,
		Attempt: in.Attempt, Status: string(in.Status), Error: in.Error,
		Sequence: in.Sequence, EventTs: in.EventTS,
	})
	if err != nil {
		return nil, fmt.Errorf("record execution node run: %w", err)
	}
	if err := r.queries.MarkExecutionNode(ctx, sqlcdb.MarkExecutionNodeParams{
		ExecutionID: in.ExecutionID, NodeID: in.NodeID,
		NodeStatus: string(in.Status), Error: in.Error,
	}); err != nil {
		return nil, fmt.Errorf("mark execution current node: %w", err)
	}
	out := nodeRunFromSQLC(row)
	return &out, nil
}

func (r *pgRepo) ListForOwner(ctx context.Context, in ListFilter) ([]Execution, error) {
	rows, err := r.queries.ListExecutionsForOwner(ctx, sqlcdb.ListExecutionsForOwnerParams{
		ProjectID: in.ProjectID, OwnerID: in.OwnerID, StatusFilter: string(in.Status),
		RuntimeModeFilter: in.RuntimeMode,
		LimitCount:        in.Limit, OffsetCount: in.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	out := make([]Execution, len(rows))
	for i := range rows {
		out[i] = executionFromSQLC(rows[i])
	}
	return out, nil
}

func (r *pgRepo) GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Execution, error) {
	row, err := r.queries.GetExecutionForOwner(ctx, sqlcdb.GetExecutionForOwnerParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get execution: %w", err)
	}
	out := executionFromSQLC(row)
	return &out, nil
}

func (r *pgRepo) ListNodeRunsForOwner(ctx context.Context, executionID, ownerID uuid.UUID) ([]NodeRun, error) {
	rows, err := r.queries.ListExecutionNodeRunsForOwner(ctx, sqlcdb.ListExecutionNodeRunsForOwnerParams{
		ExecutionID: executionID, OwnerID: ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list execution node runs: %w", err)
	}
	out := make([]NodeRun, len(rows))
	for i := range rows {
		out[i] = nodeRunFromSQLC(rows[i])
	}
	return out, nil
}

func executionFromSQLC(row sqlcdb.Execution) Execution {
	return Execution{
		ID: row.ID, ProjectID: row.ProjectID, ReleaseID: row.ReleaseID,
		ChatID: row.ChatID, TelegramUpdateID: row.TelegramUpdateID, RunID: row.RunID,
		TriggerType: row.TriggerType, RuntimeMode: row.RuntimeMode,
		FlowHash: row.FlowHash, Status: Status(row.Status),
		CurrentNodeID: row.CurrentNodeID, Error: row.Error, StartedAt: row.StartedAt,
		FinishedAt: row.FinishedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func nodeRunFromSQLC(row sqlcdb.ExecutionNodeRun) NodeRun {
	return NodeRun{
		ID: row.ID, ExecutionID: row.ExecutionID, NodeID: row.NodeID,
		NodeType: row.NodeType, Attempt: row.Attempt, Status: Status(row.Status),
		Error: row.Error, Sequence: row.Sequence, StartedAt: row.StartedAt,
		FinishedAt: row.FinishedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
