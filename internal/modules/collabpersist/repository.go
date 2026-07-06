package collabpersist

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{pool: pool}
}

func (r *pgRepo) PersistOp(ctx context.Context, projectRef string, op map[string]any) (string, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	projectID, flowRaw, err := lockProjectFlow(ctx, tx, projectRef)
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

	if _, err := tx.Exec(ctx, `UPDATE projects SET flow = $2, updated_at = now() WHERE id = $1`, projectID, next); err != nil {
		return "", false, fmt.Errorf("update flow: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit: %w", err)
	}
	return projectID, true, nil
}

func lockProjectFlow(ctx context.Context, tx pgx.Tx, ref string) (string, []byte, error) {
	var id string
	var flow []byte
	if n, err := strconv.ParseInt(ref, 10, 32); err == nil {
		err := tx.QueryRow(ctx, `
SELECT id::text, flow FROM projects
WHERE legacy_id = $1 AND deleted_at IS NULL
FOR UPDATE
`, int32(n)).Scan(&id, &flow)
		return id, flow, err
	}

	err := tx.QueryRow(ctx, `
SELECT id::text, flow FROM projects
WHERE (id = $1::uuid OR uuid = $1::uuid) AND deleted_at IS NULL
FOR UPDATE
`, ref).Scan(&id, &flow)
	return id, flow, err
}
