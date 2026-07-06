package releases

import (
	"context"

	sqlc "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	q *sqlc.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{q: sqlc.New(pool)}
}

func (r *pgRepo) MarkReleaseFailedByService(ctx context.Context, serviceID string) (int64, error) {
	return r.q.MarkReleaseFailedByService(ctx, &serviceID)
}

func (r *pgRepo) MarkDebugFailedByService(ctx context.Context, serviceID string) (int64, error) {
	return r.q.MarkDebugFailedByService(ctx, &serviceID)
}
