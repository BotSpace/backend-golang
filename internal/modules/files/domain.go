package files

import (
	"context"

	"github.com/google/uuid"
)

type ExpiredFile struct {
	ID       uuid.UUID
	FilePath string
}

type Repository interface {
	ListExpiredFiles(ctx context.Context) ([]ExpiredFile, error)
	DeleteFile(ctx context.Context, id uuid.UUID) error
}
