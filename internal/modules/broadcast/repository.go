package broadcast

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

func (r *pgRepo) GetMessage(ctx context.Context, id uuid.UUID) (Message, error) {
	row, err := r.q.GetMessage(ctx, id)
	if err != nil {
		return Message{}, err
	}
	return Message{
		ID:              row.ID,
		ProjectID:       row.ProjectID,
		ProjectLegacyID: row.ProjectLegacyID,
		Caption:         row.Caption,
		MessageType:     row.MessageType,
		FileID:          row.FileID,
		ButtonType:      row.ButtonType,
		Buttons:         row.Buttons,
		Total:           row.Total,
		Done:            row.Done,
		Fail:            row.Fail,
		Status:          row.Status,
	}, nil
}

func (r *pgRepo) MarkMessageFailed(ctx context.Context, id uuid.UUID) error {
	return r.q.MarkMessageFailed(ctx, id)
}

func (r *pgRepo) HasUsersCollection(ctx context.Context, projectID uuid.UUID) (bool, error) {
	return r.q.HasUsersCollection(ctx, projectID)
}

func (r *pgRepo) GetProjectEnv(ctx context.Context, projectID uuid.UUID, key string) (string, error) {
	return r.q.GetProjectEnv(ctx, sqlc.GetProjectEnvParams{ProjectID: projectID, Key: key})
}

func (r *pgRepo) UpdateMessageTotal(ctx context.Context, id uuid.UUID, total int64) error {
	return r.q.UpdateMessageTotal(ctx, sqlc.UpdateMessageTotalParams{ID: id, Total: total})
}

func (r *pgRepo) UpdateMessageFileID(ctx context.Context, id uuid.UUID, fileID string) error {
	return r.q.UpdateMessageFileID(ctx, sqlc.UpdateMessageFileIDParams{ID: id, FileID: &fileID})
}

func (r *pgRepo) UpdateMessageResult(ctx context.Context, id uuid.UUID, done, failed int64, status string) error {
	return r.q.UpdateMessageResult(ctx, sqlc.UpdateMessageResultParams{ID: id, Done: done, Fail: failed, Status: status})
}
