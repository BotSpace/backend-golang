package runtimemigration

import (
	"context"

	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct{ queries *sqlcdb.Queries }

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) OwnerForService(ctx context.Context, projectID uuid.UUID, serviceID string, debug bool) (uuid.UUID, bool, error) {
	var ownerID uuid.UUID
	var err error
	if debug {
		ownerID, err = r.queries.GetActiveDebugRuntimeMigrationOwner(ctx, sqlcdb.GetActiveDebugRuntimeMigrationOwnerParams{ProjectID: projectID, ServiceID: &serviceID})
	} else {
		ownerID, err = r.queries.GetActiveReleaseRuntimeMigrationOwner(ctx, sqlcdb.GetActiveReleaseRuntimeMigrationOwnerParams{ProjectID: projectID, ServiceID: &serviceID})
	}
	if err == pgx.ErrNoRows {
		return uuid.Nil, false, nil
	}
	return ownerID, err == nil, err
}

func (r *pgRepo) ReplaceServiceID(ctx context.Context, projectID uuid.UUID, oldServiceID, newServiceID string, debug bool) (bool, error) {
	if debug {
		rows, err := r.queries.ReplaceLegacyDebugRuntimeServiceID(ctx, sqlcdb.ReplaceLegacyDebugRuntimeServiceIDParams{ProjectID: projectID, NewServiceID: &newServiceID, OldServiceID: &oldServiceID})
		return rows > 0, err
	}
	rows, err := r.queries.ReplaceLegacyReleaseRuntimeServiceID(ctx, sqlcdb.ReplaceLegacyReleaseRuntimeServiceIDParams{ProjectID: projectID, NewServiceID: &newServiceID, OldServiceID: &oldServiceID})
	return rows > 0, err
}

func (r *pgRepo) ResolveLegacyProject(ctx context.Context, legacyID int32) (uuid.UUID, uuid.UUID, error) {
	row, err := r.queries.ResolveLegacyRuntimeProject(ctx, &legacyID)
	return row.ID, row.OwnerID, err
}
