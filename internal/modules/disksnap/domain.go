package disksnap

import (
	"context"

	"github.com/google/uuid"
)

type ReleasedProject struct {
	ID       uuid.UUID
	LegacyID *int32
}

type Repository interface {
	ListReleasedProjectsForDisk(ctx context.Context) ([]ReleasedProject, error)
	SumProjectFileBytes(ctx context.Context, projectID uuid.UUID) (int64, error)
	InsertDiskSnapshot(ctx context.Context, projectID uuid.UUID, filesBytes, cmsBytes int64) error
}
