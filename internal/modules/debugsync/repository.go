package debugsync

import (
	"context"
	"time"

	sqlc "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	q *sqlc.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{q: sqlc.New(pool)}
}

func (r *pgRepo) ListExpiredRunningDebugs(ctx context.Context, cutoff time.Time) ([]ExpiredDebug, error) {
	rows, err := r.q.ListExpiredRunningDebugs(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	out := make([]ExpiredDebug, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExpiredDebug{ID: row.ID, ServiceID: row.ServiceID})
	}
	return out, nil
}

func (r *pgRepo) MarkDebugStopping(ctx context.Context, id uuid.UUID) error {
	return r.q.MarkDebugStopping(ctx, id)
}
