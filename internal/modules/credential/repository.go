package credential

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

type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) Create(ctx context.Context, in CreateInput) (*Credential, error) {
	row, err := r.queries.CreateCredential(ctx, sqlcdb.CreateCredentialParams{
		ID:            uuid.New(),
		OwnerID:       in.OwnerID,
		TypeKey:       in.TypeKey,
		Name:          in.Name,
		DataEncrypted: in.DataEncrypted,
	})
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}
	c := fromSQLC(row)
	return &c, nil
}

func (r *pgRepo) GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Credential, error) {
	return mapOne(r.queries.GetCredentialForOwner(ctx, sqlcdb.GetCredentialForOwnerParams{ID: id, OwnerID: ownerID}))
}

// GetByID is unscoped by owner — internal/engine reads only.
func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*Credential, error) {
	return mapOne(r.queries.GetCredentialByID(ctx, id))
}

func (r *pgRepo) GetByOwnerAndName(ctx context.Context, ownerID uuid.UUID, name string) (*Credential, error) {
	return mapOne(r.queries.GetCredentialByOwnerAndName(ctx, sqlcdb.GetCredentialByOwnerAndNameParams{
		OwnerID: ownerID,
		Name:    name,
	}))
}

func (r *pgRepo) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Credential, error) {
	return mapOne(r.queries.UpdateCredential(ctx, sqlcdb.UpdateCredentialParams{
		ID:            id,
		OwnerID:       ownerID,
		Name:          in.Name,
		DataEncrypted: in.DataEncrypted,
	}))
}

func (r *pgRepo) SoftDelete(ctx context.Context, id, ownerID uuid.UUID) error {
	rows, err := r.queries.SoftDeleteCredential(ctx, sqlcdb.SoftDeleteCredentialParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("soft delete credential: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) List(ctx context.Context, f ListFilter) ([]Credential, error) {
	rows, err := r.queries.ListCredentialsForOwner(ctx, sqlcdb.ListCredentialsForOwnerParams{
		OwnerID: f.OwnerID,
		TypeKey: strPtr(f.TypeKey),
		Limit:   f.Limit,
		Offset:  f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	out := make([]Credential, len(rows))
	for i, row := range rows {
		out[i] = fromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) Count(ctx context.Context, ownerID uuid.UUID, typeKey string) (int64, error) {
	n, err := r.queries.CountCredentialsForOwner(ctx, sqlcdb.CountCredentialsForOwnerParams{
		OwnerID: ownerID,
		TypeKey: strPtr(typeKey),
	})
	if err != nil {
		return 0, fmt.Errorf("count credentials: %w", err)
	}
	return n, nil
}

func (r *pgRepo) GetProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	ownerID, err := r.queries.GetProjectOwnerForCredential(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, database.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get project owner: %w", err)
	}
	return ownerID, nil
}

func mapOne(row sqlcdb.Credential, err error) (*Credential, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query credential: %w", err)
	}
	c := fromSQLC(row)
	return &c, nil
}

func fromSQLC(r sqlcdb.Credential) Credential {
	return Credential{
		ID:            r.ID,
		OwnerID:       r.OwnerID,
		TypeKey:       r.TypeKey,
		Name:          r.Name,
		DataEncrypted: r.DataEncrypted,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
