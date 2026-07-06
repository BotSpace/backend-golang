package debugsync

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ExpiredDebug struct {
	ID        uuid.UUID
	ServiceID *string
}

type Repository interface {
	ListExpiredRunningDebugs(ctx context.Context, cutoff time.Time) ([]ExpiredDebug, error)
	MarkDebugStopping(ctx context.Context, id uuid.UUID) error
}
