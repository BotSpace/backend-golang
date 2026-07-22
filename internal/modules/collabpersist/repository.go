package collabpersist

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{pool: pool, queries: sqlcdb.New(pool)}
}

func (r *pgRepo) PersistOp(ctx context.Context, projectRef string, op map[string]any) (string, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := r.queries.WithTx(tx)
	projectID, flowRaw, err := lockProjectFlow(ctx, qtx, projectRef)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, fmt.Errorf("project not found for ref %q", projectRef)
		}
		return "", false, err
	}

	next, changed, err := applyOp(flowRaw, op)
	if err != nil {
		return "", false, err
	}
	if !changed {
		return projectID, false, tx.Commit(ctx)
	}

	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		return "", false, fmt.Errorf("parse project id: %w", err)
	}
	rows, err := qtx.PersistCollaborativeProjectFlow(ctx, sqlcdb.PersistCollaborativeProjectFlowParams{ID: projectUUID, Flow: next})
	if err != nil {
		return "", false, fmt.Errorf("update flow: %w", err)
	}
	if rows != 1 {
		return "", false, fmt.Errorf("update flow affected %d rows", rows)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit: %w", err)
	}
	return projectID, true, nil
}

func lockProjectFlow(ctx context.Context, q *sqlcdb.Queries, ref string) (string, []byte, error) {
	if n, err := strconv.ParseInt(ref, 10, 32); err == nil {
		legacyID := int32(n)
		row, err := q.LockCollaborativeProjectFlowByLegacyID(ctx, &legacyID)
		return row.ID.String(), row.Flow, err
	}
	id, err := uuid.Parse(ref)
	if err != nil {
		return "", nil, pgx.ErrNoRows
	}
	row, err := q.LockCollaborativeProjectFlowByID(ctx, id)
	return row.ID.String(), row.Flow, err
}

func (r *pgRepo) RuntimeSnapshot(ctx context.Context, projectID string) (string, json.RawMessage, error) {
	id, err := uuid.Parse(projectID)
	if err != nil {
		return "", nil, fmt.Errorf("parse project id: %w", err)
	}
	row, err := r.queries.GetCollaborativeRuntimeSnapshot(ctx, id)
	if err != nil {
		return "", nil, err
	}
	return row.DebugWebhookUuid.String(), row.Flow, nil
}
