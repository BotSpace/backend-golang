package config

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

func (r *pgRepo) Get(ctx context.Context, key string) (*Entry, error) {
	row, err := r.queries.GetConfig(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query config: %w", err)
	}
	e := fromSQLC(row)
	return &e, nil
}

func (r *pgRepo) Upsert(ctx context.Context, key string, value json.RawMessage, updatedBy *uuid.UUID) (*Entry, error) {
	row, err := r.queries.UpsertConfig(ctx, sqlcdb.UpsertConfigParams{
		Key: key, Value: valueOrEmpty(value), UpdatedBy: updatedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert config: %w", err)
	}
	e := fromSQLC(row)
	return &e, nil
}

func fromSQLC(r sqlcdb.Config) Entry {
	return Entry{
		Key:       r.Key,
		Value:     valueOrEmpty(json.RawMessage(r.Value)),
		UpdatedBy: r.UpdatedBy,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func valueOrEmpty(v json.RawMessage) []byte {
	if len(v) == 0 {
		return []byte("{}")
	}
	return v
}
