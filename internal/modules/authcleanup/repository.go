package authcleanup

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

func (r *pgRepo) DeleteExpiredRefreshTokens(ctx context.Context) (int64, error) {
	return r.q.DeleteExpiredRefreshTokens(ctx)
}

func (r *pgRepo) DeleteExpiredAuthbotCodes(ctx context.Context) (int64, error) {
	return r.q.DeleteExpiredAuthbotCodes(ctx)
}
