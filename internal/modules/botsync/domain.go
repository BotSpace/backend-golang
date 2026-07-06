package botsync

import (
	"context"

	"github.com/google/uuid"
)

type ProjectBot struct {
	ID           uuid.UUID
	Username     *string
	AvatarFileID *string
	BotToken     string
}

type Repository interface {
	ListProjectsWithReleaseToken(ctx context.Context) ([]ProjectBot, error)
	UpdateProjectBotInfo(ctx context.Context, id uuid.UUID, username, avatarFileID *string) error
}
