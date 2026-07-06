package botsync

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

func (r *pgRepo) ListProjectsWithReleaseToken(ctx context.Context) ([]ProjectBot, error) {
	rows, err := r.q.ListProjectsWithReleaseToken(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectBot, 0, len(rows))
	for _, row := range rows {
		out = append(out, ProjectBot{
			ID:           row.ID,
			Username:     row.Username,
			AvatarFileID: row.AvatarFileID,
			BotToken:     row.BotToken,
		})
	}
	return out, nil
}

func (r *pgRepo) UpdateProjectBotInfo(ctx context.Context, id uuid.UUID, username, avatarFileID *string) error {
	return r.q.UpdateProjectBotInfo(ctx, sqlc.UpdateProjectBotInfoParams{
		ID:           id,
		Username:     username,
		AvatarFileID: avatarFileID,
	})
}
