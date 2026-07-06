package subscriptions

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

func (r *pgRepo) ExpireActiveSubscriptions(ctx context.Context) (int, error) {
	rows, err := r.q.ExpireActiveSubscriptions(ctx)
	return len(rows), err
}

func (r *pgRepo) SuspendGracePeriodSubscriptions(ctx context.Context) (int, error) {
	rows, err := r.q.SuspendGracePeriodSubscriptions(ctx)
	return len(rows), err
}
