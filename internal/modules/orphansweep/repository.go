package orphansweep

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

func (r *pgRepo) ListLiveEngineServiceIDs(ctx context.Context) ([]string, error) {
	rows, err := r.q.ListLiveEngineServiceIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, s := range rows {
		if s != nil && *s != "" {
			out = append(out, *s)
		}
	}
	return out, nil
}
