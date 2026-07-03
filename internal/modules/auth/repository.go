package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RefreshStore persists issued refresh tokens by their jti so they can be
// rotated and revoked (stateless JWTs alone cannot be invalidated early).
type RefreshStore interface {
	Save(ctx context.Context, jti, userID uuid.UUID, expiresAt time.Time) error
	IsActive(ctx context.Context, jti uuid.UUID) (bool, error)
	Revoke(ctx context.Context, jti uuid.UUID) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
	// DeleteExpired purges rows whose expires_at has passed (revoked or not —
	// a revoked-but-not-yet-expired row is left alone, it's still evidence a
	// token was rotated/logged-out). Wires the previously-generated but
	// never-called sqlcdb.DeleteExpiredRefreshTokens query into the
	// scheduler; called periodically, not from any HTTP handler.
	DeleteExpired(ctx context.Context) error
}

type pgRefreshStore struct {
	queries *sqlcdb.Queries
}

func NewRefreshStore(pool *pgxpool.Pool) RefreshStore {
	return &pgRefreshStore{queries: sqlcdb.New(pool)}
}

func (s *pgRefreshStore) Save(ctx context.Context, jti, userID uuid.UUID, expiresAt time.Time) error {
	err := s.queries.CreateRefreshToken(ctx, sqlcdb.CreateRefreshTokenParams{
		ID:        jti,
		UserID:    userID,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return fmt.Errorf("save refresh token: %w", err)
	}
	return nil
}

// IsActive reports whether the jti exists, is not revoked, and is not expired.
func (s *pgRefreshStore) IsActive(ctx context.Context, jti uuid.UUID) (bool, error) {
	row, err := s.queries.GetRefreshToken(ctx, jti)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get refresh token: %w", err)
	}
	if row.RevokedAt != nil || time.Now().After(row.ExpiresAt) {
		return false, nil
	}
	return true, nil
}

func (s *pgRefreshStore) Revoke(ctx context.Context, jti uuid.UUID) error {
	if _, err := s.queries.RevokeRefreshToken(ctx, jti); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

func (s *pgRefreshStore) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	if err := s.queries.RevokeAllUserRefreshTokens(ctx, userID); err != nil {
		return fmt.Errorf("revoke user refresh tokens: %w", err)
	}
	return nil
}

func (s *pgRefreshStore) DeleteExpired(ctx context.Context) error {
	if err := s.queries.DeleteExpiredRefreshTokens(ctx); err != nil {
		return fmt.Errorf("delete expired refresh tokens: %w", err)
	}
	return nil
}
