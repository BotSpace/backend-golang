package files

import (
	"context"

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

func (r *pgRepo) ListExpiredFiles(ctx context.Context) ([]ExpiredFile, error) {
	rows, err := r.q.ListExpiredFiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ExpiredFile, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExpiredFile{ID: row.ID, FilePath: row.FilePath})
	}
	return out, nil
}

func (r *pgRepo) DeleteFile(ctx context.Context, id uuid.UUID) error {
	return r.q.DeleteFile(ctx, id)
}
