package runtimemigration

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	OwnerForProject(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error)
	ReplaceServiceID(ctx context.Context, projectID uuid.UUID, oldServiceID, newServiceID string, debug bool) (bool, error)
}

type Migrator interface {
	MigrateLegacyRuntimes(
		ctx context.Context,
		ownerForProject func(context.Context, uuid.UUID) (uuid.UUID, error),
		replaceServiceID func(context.Context, uuid.UUID, string, string, bool) (bool, error),
	) (int, error)
}
