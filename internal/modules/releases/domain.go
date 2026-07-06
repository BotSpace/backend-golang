package releases

import "context"

type Repository interface {
	MarkReleaseFailedByService(ctx context.Context, serviceID string) (int64, error)
	MarkDebugFailedByService(ctx context.Context, serviceID string) (int64, error)
}
