package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This file ports core/apps/accounts/models/api_token.py's PersonalAccessToken
// + views/api_token.py's APITokenViewSet/TokenExchangeView: a user-issued
// long-lived "bsp_" token that MCP clients (Claude Code etc.) exchange for a
// normal short-lived JWT access+refresh pair, since MCP transports generally
// can't run the phone/OTP login flow interactively.
//
// NOTE: the personal_access_token table was NOT populated by the ETL (see
// migrations/000035's header comment) — every Django-issued "bsp_" token
// predating this port is invalid here; users must mint new ones via
// POST /auth/api-tokens/.

// PersonalAccessToken is the persisted row — TokenHash/Prefix only, the raw
// token is returned once (on create) and never stored.
type PersonalAccessToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	TokenHash  string
	Prefix     string
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

// PATStore is the persistence port for personal access tokens.
type PATStore interface {
	Create(ctx context.Context, userID uuid.UUID, name, tokenHash, prefix string) (*PersonalAccessToken, error)
	ListForUser(ctx context.Context, userID uuid.UUID) ([]PersonalAccessToken, error)
	CountForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	GetByHash(ctx context.Context, tokenHash string) (*PersonalAccessToken, error)
	Touch(ctx context.Context, id uuid.UUID) error
	// DeleteForUser reports database.ErrNotFound (via the caller checking
	// rows==0) when id doesn't exist or belongs to another user.
	DeleteForUser(ctx context.Context, id, userID uuid.UUID) error
}

type pgPATStore struct {
	queries *sqlcdb.Queries
}

func NewPATStore(pool *pgxpool.Pool) PATStore {
	return &pgPATStore{queries: sqlcdb.New(pool)}
}

func (s *pgPATStore) Create(ctx context.Context, userID uuid.UUID, name, tokenHash, prefix string) (*PersonalAccessToken, error) {
	row, err := s.queries.CreatePersonalAccessToken(ctx, sqlcdb.CreatePersonalAccessTokenParams{
		ID: uuid.New(), UserID: userID, Name: name, TokenHash: tokenHash, Prefix: prefix,
	})
	if err != nil {
		return nil, fmt.Errorf("create personal access token: %w", err)
	}
	p := patFromSQLC(row)
	return &p, nil
}

func (s *pgPATStore) ListForUser(ctx context.Context, userID uuid.UUID) ([]PersonalAccessToken, error) {
	rows, err := s.queries.ListPersonalAccessTokensForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list personal access tokens: %w", err)
	}
	out := make([]PersonalAccessToken, len(rows))
	for i, r := range rows {
		out[i] = patFromSQLC(r)
	}
	return out, nil
}

func (s *pgPATStore) CountForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := s.queries.CountPersonalAccessTokensForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count personal access tokens: %w", err)
	}
	return n, nil
}

func (s *pgPATStore) GetByHash(ctx context.Context, tokenHash string) (*PersonalAccessToken, error) {
	row, err := s.queries.GetPersonalAccessTokenByHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get personal access token: %w", err)
	}
	p := patFromSQLC(row)
	return &p, nil
}

func (s *pgPATStore) Touch(ctx context.Context, id uuid.UUID) error {
	if err := s.queries.TouchPersonalAccessToken(ctx, id); err != nil {
		return fmt.Errorf("touch personal access token: %w", err)
	}
	return nil
}

func (s *pgPATStore) DeleteForUser(ctx context.Context, id, userID uuid.UUID) error {
	rows, err := s.queries.DeletePersonalAccessTokenForUser(ctx, sqlcdb.DeletePersonalAccessTokenForUserParams{
		ID: id, UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("delete personal access token: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func patFromSQLC(r sqlcdb.PersonalAccessToken) PersonalAccessToken {
	return PersonalAccessToken{
		ID: r.ID, UserID: r.UserID, Name: r.Name, TokenHash: r.TokenHash, Prefix: r.Prefix,
		LastUsedAt: r.LastUsedAt, CreatedAt: r.CreatedAt,
	}
}

// generatePAT mirrors _generate_token(): "bsp_" + 32 random bytes as 64 hex
// chars.
func generatePAT() (raw string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return "bsp_" + hex.EncodeToString(b), nil
}

// hashPAT mirrors _hash_token(): sha256 hex digest of the raw token.
func hashPAT(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
