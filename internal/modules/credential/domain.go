package credential

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Credential is an owner-scoped encrypted secret, usable across all of the
// owner's projects. DataEncrypted is opaque here — see crypto.Cipher for the
// AES-256-GCM encrypt/decrypt used by the usecase layer.
type Credential struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	TypeKey       string
	Name          string
	DataEncrypted string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreateInput is the persistence-layer create payload — data is already
// encrypted by the usecase before it reaches the repository.
type CreateInput struct {
	OwnerID       uuid.UUID
	TypeKey       string
	Name          string
	DataEncrypted string
}

// UpdateInput carries optional fields; nil pointers are left unchanged.
type UpdateInput struct {
	Name          *string
	DataEncrypted *string
}

// ListFilter narrows and paginates an owner's credential listing.
type ListFilter struct {
	OwnerID uuid.UUID
	TypeKey string
	Limit   int32
	Offset  int32
}

type OptionItem struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type OptionsInput struct {
	OwnerID      uuid.UUID
	Resource     string
	CredentialID uuid.UUID
	Params       map[string]string
}

// Repository is the persistence port; all reads/writes are owner-scoped so a
// user can never touch another user's credentials, except the two internal
// lookups used by the engine (GetByID, GetProjectOwner) which are guarded by
// the internal-token middleware and an explicit owner check in the usecase.
type Repository interface {
	Create(ctx context.Context, in CreateInput) (*Credential, error)
	GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Credential, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Credential, error)
	GetByOwnerAndName(ctx context.Context, ownerID uuid.UUID, name string) (*Credential, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Credential, error)
	SoftDelete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Credential, error)
	Count(ctx context.Context, ownerID uuid.UUID, typeKey string) (int64, error)
	// GetProjectOwner resolves a project's owner id without importing the
	// project module — used by the internal by-name lookup for the
	// cross-tenant guard. Returns database.ErrNotFound if the project is
	// missing or soft-deleted.
	GetProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error)
}
