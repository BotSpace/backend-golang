package chatsend

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

func (r *pgRepo) GetProjectEnv(ctx context.Context, projectID uuid.UUID, key string) (string, error) {
	return r.q.GetProjectEnv(ctx, sqlc.GetProjectEnvParams{ProjectID: projectID, Key: key})
}

func (r *pgRepo) GetFilePath(ctx context.Context, fileID uuid.UUID) (string, error) {
	return r.q.GetFilePath(ctx, fileID)
}
