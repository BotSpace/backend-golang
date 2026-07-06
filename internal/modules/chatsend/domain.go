package chatsend

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	GetProjectEnv(ctx context.Context, projectID uuid.UUID, key string) (string, error)
	GetFilePath(ctx context.Context, fileID uuid.UUID) (string, error)
}
