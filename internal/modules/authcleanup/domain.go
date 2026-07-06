package authcleanup

import "context"

type Repository interface {
	DeleteExpiredRefreshTokens(ctx context.Context) (int64, error)
	DeleteExpiredAuthbotCodes(ctx context.Context) (int64, error)
}
