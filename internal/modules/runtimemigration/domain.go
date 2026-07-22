package runtimemigration

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	OwnerForService(ctx context.Context, projectID uuid.UUID, serviceID string, debug bool) (uuid.UUID, bool, error)
	ReplaceServiceID(ctx context.Context, projectID uuid.UUID, oldServiceID, newServiceID string, debug bool) (bool, error)
}

type Migrator interface {
	MigrateLegacyRuntimes(
		ctx context.Context,
		ownerForService func(context.Context, uuid.UUID, string, bool) (uuid.UUID, bool, error),
		replaceServiceID func(context.Context, uuid.UUID, string, string, bool) (bool, error),
	) (int, error)
}
