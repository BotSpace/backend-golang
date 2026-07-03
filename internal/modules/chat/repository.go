package chat

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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

// ResolveProjectForOwner mirrors cms.usecase.resolveMongoTenant + Django's
// cms/views/_common.py resolve_project(): rawProjectID may be the Django
// legacy int pk (the common case — agentv3 and the constructor both still
// pass Django-style project ids here) or this table's `uuid` column. Either
// way ownership is checked in the same query (WHERE ... AND owner_id = $2),
// so a project that exists but belongs to someone else 404s exactly like a
// project that doesn't exist at all — matching Django's Http404 branch in
// ChatViewSet.get_collections().
func (r *pgRepo) ResolveProjectForOwner(ctx context.Context, ownerID uuid.UUID, rawProjectID string) (any, error) {
	if n, err := strconv.ParseInt(rawProjectID, 10, 32); err == nil {
		legacyID := int32(n)
		row, err := r.queries.ChatResolveProjectByLegacyIDForOwner(ctx, sqlcdb.ChatResolveProjectByLegacyIDForOwnerParams{
			LegacyID: &legacyID,
			OwnerID:  ownerID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("resolve project by legacy id: %w", err)
		}
		return tenantOf(row.ID, row.LegacyID), nil
	}

	parsed, err := uuid.Parse(rawProjectID)
	if err != nil {
		return nil, database.ErrNotFound
	}
	row, err := r.queries.ChatResolveProjectByUUIDForOwner(ctx, sqlcdb.ChatResolveProjectByUUIDForOwnerParams{
		Uuid:    parsed,
		OwnerID: ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve project by uuid: %w", err)
	}
	return tenantOf(row.ID, row.LegacyID), nil
}

// tenantOf mirrors cms.usecase.resolveMongoTenant: the Django legacy pk when
// present (documents written by engine-golang or the old Django backend are
// tagged with it), else the project's own UUID string.
func tenantOf(id uuid.UUID, legacyID *int32) any {
	if legacyID != nil {
		return int64(*legacyID)
	}
	return id.String()
}
