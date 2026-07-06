package broadcast

import (
	"context"

	"github.com/google/uuid"
)

type Message struct {
	ID              uuid.UUID
	ProjectID       uuid.UUID
	ProjectLegacyID *int32
	Caption         string
	MessageType     string
	FileID          *string
	ButtonType      string
	Buttons         []byte
	Total           int64
	Done            int64
	Fail            int64
	Status          string
}

type Repository interface {
	GetMessage(ctx context.Context, id uuid.UUID) (Message, error)
	MarkMessageFailed(ctx context.Context, id uuid.UUID) error
	HasUsersCollection(ctx context.Context, projectID uuid.UUID) (bool, error)
	GetProjectEnv(ctx context.Context, projectID uuid.UUID, key string) (string, error)
	UpdateMessageTotal(ctx context.Context, id uuid.UUID, total int64) error
	UpdateMessageFileID(ctx context.Context, id uuid.UUID, fileID string) error
	UpdateMessageResult(ctx context.Context, id uuid.UUID, done, failed int64, status string) error
}
