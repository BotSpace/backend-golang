package authbot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. sqlc handles SQL <-> Go mapping; this
// adapter only translates domain <-> persistence models and pgx errors into
// domain errors, per the module template in user/repository.go.
type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

const uniqueViolation = "23505"

func (r *pgRepo) UpsertBotUser(ctx context.Context, in IssueCodeInput) (*BotUser, error) {
	row, err := r.queries.UpsertAuthbotBotUser(ctx, sqlcdb.UpsertAuthbotBotUserParams{
		TelegramID: in.TelegramID,
		Phone:      in.Phone,
		FirstName:  strPtr(in.FirstName),
		LastName:   strPtr(in.LastName),
		Username:   strPtr(in.Username),
	})
	if err != nil {
		return nil, fmt.Errorf("upsert bot user: %w", err)
	}
	bu := botUserFromSQLC(row)
	return &bu, nil
}

// UpsertCode returns ErrCodeCollision when codeValue is already in use by a
// different (still-active) bot user's code — the caller should generate a
// new value and retry.
func (r *pgRepo) UpsertCode(ctx context.Context, botUserID uuid.UUID, codeValue string, expiresAt time.Time) (*Code, error) {
	row, err := r.queries.UpsertAuthbotCode(ctx, sqlcdb.UpsertAuthbotCodeParams{
		BotUserID: botUserID,
		Code:      codeValue,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return nil, ErrCodeCollision
		}
		return nil, fmt.Errorf("upsert code: %w", err)
	}
	c := codeFromSQLC(row)
	return &c, nil
}

func (r *pgRepo) SetCodeMessageID(ctx context.Context, codeID uuid.UUID, messageID int64) error {
	rows, err := r.queries.SetAuthbotCodeMessageID(ctx, sqlcdb.SetAuthbotCodeMessageIDParams{
		ID:        codeID,
		MessageID: &messageID,
	})
	if err != nil {
		return fmt.Errorf("set code message id: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) GetCodeWithBotUser(ctx context.Context, value string) (*Code, *BotUser, error) {
	row, err := r.queries.GetAuthbotCodeWithBotUser(ctx, value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, database.ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get code with bot user: %w", err)
	}
	code := &Code{
		ID:        row.CodeID,
		Value:     row.CodeValue,
		MessageID: row.CodeMessageID,
		BotUserID: row.BotUserID,
		ExpiresAt: row.CodeExpiresAt,
		CreatedAt: row.CodeCreatedAt,
		UpdatedAt: row.CodeUpdatedAt,
	}
	botUser := &BotUser{
		ID:         row.BotUserID,
		TelegramID: row.TelegramID,
		Phone:      row.Phone,
		FirstName:  deref(row.FirstName),
		LastName:   deref(row.LastName),
		Username:   deref(row.Username),
		CreatedAt:  row.BotUserCreatedAt,
		UpdatedAt:  row.BotUserUpdatedAt,
	}
	return code, botUser, nil
}

func (r *pgRepo) DeleteCode(ctx context.Context, codeID uuid.UUID) error {
	rows, err := r.queries.DeleteAuthbotCode(ctx, codeID)
	if err != nil {
		return fmt.Errorf("delete code: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func botUserFromSQLC(r sqlcdb.AuthbotBotUser) BotUser {
	return BotUser{
		ID:         r.ID,
		TelegramID: r.TelegramID,
		Phone:      r.Phone,
		FirstName:  deref(r.FirstName),
		LastName:   deref(r.LastName),
		Username:   deref(r.Username),
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

func codeFromSQLC(r sqlcdb.AuthbotCode) Code {
	return Code{
		ID:        r.ID,
		Value:     r.Code,
		MessageID: r.MessageID,
		BotUserID: r.BotUserID,
		ExpiresAt: r.ExpiresAt,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
