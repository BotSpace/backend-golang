package subscriptions

import "context"

type Repository interface {
	ExpireActiveSubscriptions(ctx context.Context) (int, error)
	SuspendGracePeriodSubscriptions(ctx context.Context) (int, error)
}
