package message

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

func (r *pgRepo) Create(ctx context.Context, in CreateInput) (*Message, error) {
	row, err := r.queries.CreateMessage(ctx, sqlcdb.CreateMessageParams{
		ID:          uuid.New(),
		ProjectID:   in.ProjectID,
		OwnerID:     in.OwnerID,
		Caption:     in.Caption,
		MessageType: in.MessageType,
		FileID:      in.FileID,
		ButtonType:  in.ButtonType,
		Buttons:     buttonsOrEmpty(in.Buttons),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// INSERT ... SELECT ... WHERE EXISTS produced no row: the project
		// does not belong to the caller (or does not exist).
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("create message: %w", err)
	}
	m := fromSQLC(row)
	return &m, nil
}

func (r *pgRepo) GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Message, error) {
	return mapOne(r.queries.GetMessageForOwner(ctx, sqlcdb.GetMessageForOwnerParams{ID: id, OwnerID: ownerID}))
}

func (r *pgRepo) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Message, error) {
	return mapOne(r.queries.UpdateMessageForOwner(ctx, sqlcdb.UpdateMessageForOwnerParams{
		ID:          id,
		OwnerID:     ownerID,
		Caption:     in.Caption,
		MessageType: in.MessageType,
		FileID:      in.FileID,
		ButtonType:  in.ButtonType,
		Buttons:     in.Buttons, // nil -> COALESCE keeps existing
	}))
}

func (r *pgRepo) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	rows, err := r.queries.DeleteMessageForOwner(ctx, sqlcdb.DeleteMessageForOwnerParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) List(ctx context.Context, f ListFilter) ([]Message, error) {
	rows, err := r.queries.ListMessagesForOwner(ctx, sqlcdb.ListMessagesForOwnerParams{
		OwnerID:   f.OwnerID,
		ProjectID: f.ProjectID,
		Limit:     f.Limit,
		Offset:    f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	out := make([]Message, len(rows))
	for i, row := range rows {
		out[i] = fromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) Count(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error) {
	n, err := r.queries.CountMessagesForOwner(ctx, sqlcdb.CountMessagesForOwnerParams{
		OwnerID:   ownerID,
		ProjectID: projectID,
	})
	if err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return n, nil
}

func mapOne(row sqlcdb.Message, err error) (*Message, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query message: %w", err)
	}
	m := fromSQLC(row)
	return &m, nil
}

func fromSQLC(r sqlcdb.Message) Message {
	return Message{
		ID:          r.ID,
		ProjectID:   r.ProjectID,
		Caption:     r.Caption,
		MessageType: r.MessageType,
		FileID:      r.FileID,
		ButtonType:  r.ButtonType,
		Buttons:     json.RawMessage(r.Buttons),
		Total:       r.Total,
		Done:        r.Done,
		Fail:        r.Fail,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// buttonsOrEmpty renders a nil/empty buttons payload as an explicit JSON
// empty array, matching the column's NOT NULL DEFAULT '[]'::jsonb.
func buttonsOrEmpty(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}
