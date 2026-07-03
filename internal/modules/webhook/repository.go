package webhook

import (
	"context"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. sqlc handles SQL <-> Go mapping; this
// adapter only translates domain <-> persistence models and pgx errors into
// domain errors. The usecase never sees sqlc, so it stays trivially testable.
type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) MintEndpoint(ctx context.Context, in MintInput) (*WebhookEndpoint, error) {
	row, err := r.queries.MintWebhookEndpoint(ctx, sqlcdb.MintWebhookEndpointParams{
		ProjectID: in.ProjectID,
		OwnerID:   in.OwnerID,
		NodeID:    in.NodeID,
		ChatID:    in.ChatID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mint webhook endpoint: %w", err)
	}
	e := fromSQLCEndpoint(row)
	return &e, nil
}

func (r *pgRepo) ResolveToken(ctx context.Context, token uuid.UUID) (*ResolvedHook, error) {
	row, err := r.queries.ResolveWebhookToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve webhook token: %w", err)
	}
	return &ResolvedHook{
		Token:              row.Token,
		ProjectID:          row.ProjectID,
		NodeID:             row.NodeID,
		ChatID:             row.ChatID,
		ReleaseWebhookUUID: row.ReleaseWebhookUuid,
		DebugWebhookUUID:   row.DebugWebhookUuid,
	}, nil
}

func (r *pgRepo) ListManagedBotsForOwner(ctx context.Context, ownerID uuid.UUID) ([]ManagedBot, error) {
	rows, err := r.queries.ListManagedBotsForOwner(ctx, &ownerID)
	if err != nil {
		return nil, fmt.Errorf("list managed bots: %w", err)
	}
	out := make([]ManagedBot, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCBot(row)
	}
	return out, nil
}

func (r *pgRepo) UpsertManagedBot(ctx context.Context, in UpsertManagedBotInput) (*ManagedBot, error) {
	row, err := r.queries.UpsertManagedBot(ctx, sqlcdb.UpsertManagedBotParams{
		OwnerID:        in.OwnerID,
		TgBotID:        in.TgBotID,
		Username:       in.Username,
		Name:           in.Name,
		TokenEncrypted: in.TokenEncrypted,
	})
	if err != nil {
		return nil, fmt.Errorf("upsert managed bot: %w", err)
	}
	b := fromSQLCBot(row)
	return &b, nil
}

func fromSQLCEndpoint(r sqlcdb.WebhookEndpoint) WebhookEndpoint {
	return WebhookEndpoint{
		ID:        r.ID,
		Token:     r.Token,
		ProjectID: r.ProjectID,
		NodeID:    r.NodeID,
		ChatID:    r.ChatID,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func fromSQLCBot(r sqlcdb.ManagedBot) ManagedBot {
	return ManagedBot{
		ID:             r.ID,
		OwnerID:        r.OwnerID,
		TgBotID:        r.TgBotID,
		Username:       r.Username,
		Name:           r.Name,
		TokenEncrypted: r.TokenEncrypted,
		Used:           r.Used,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}
