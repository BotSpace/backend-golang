package runtimemigration

import (
	"context"

	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct{ queries *sqlcdb.Queries }

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) OwnerForProject(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	return r.queries.GetRuntimeMigrationOwner(ctx, projectID)
}

func (r *pgRepo) ReplaceServiceID(ctx context.Context, projectID uuid.UUID, oldServiceID, newServiceID string, debug bool) (bool, error) {
	if debug {
		rows, err := r.queries.ReplaceLegacyDebugRuntimeServiceID(ctx, sqlcdb.ReplaceLegacyDebugRuntimeServiceIDParams{ProjectID: projectID, NewServiceID: &newServiceID, OldServiceID: &oldServiceID})
		return rows > 0, err
	}
	rows, err := r.queries.ReplaceLegacyReleaseRuntimeServiceID(ctx, sqlcdb.ReplaceLegacyReleaseRuntimeServiceIDParams{ProjectID: projectID, NewServiceID: &newServiceID, OldServiceID: &oldServiceID})
	return rows > 0, err
}
