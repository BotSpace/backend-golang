// Package authcleanup — janitors for backend-golang's auth/authbot modules:
// expired refresh_tokens and unredeemed authbot_codes. Neither ever had a
// Django beat job (refresh_tokens is a backend-golang-only table; Django's
// authbot.tasks.authbot.clear_old_codes existed but was never wired into
// CELERY_BEAT_SCHEDULE). Moved here from backend-golang's in-process
// scheduler (internal/server/scheduler.go) — periodic jobs belong in the
// worker, not the API process.
package authcleanup

import (
	"context"

	"go.uber.org/zap"

	"github.com/botspace/backend/internal/shared/database"
)

type Usecase struct {
	Repo Repository
	Log  *zap.Logger
}

// RunRefreshTokens purges refresh_tokens rows whose expires_at has passed
// (revoked or not — a revoked-but-not-yet-expired row is left alone, it's
// still evidence a token was rotated/logged-out).
func (u *Usecase) RunRefreshTokens(ctx context.Context) {
	n, err := u.Repo.DeleteExpiredRefreshTokens(ctx)
	if err != nil {
		if database.IsUndefinedTable(err) {
			u.Log.Warn("cleanup_expired_refresh_tokens skipped: table does not exist")
			return
		}
		u.Log.Error("cleanup_expired_refresh_tokens", zap.Error(err))
		return
	}
	if n > 0 {
		u.Log.Info("cleanup_expired_refresh_tokens done", zap.Int64("deleted", n))
	}
}

// RunAuthbotCodes purges authbot_codes rows nobody ever redeemed.
func (u *Usecase) RunAuthbotCodes(ctx context.Context) {
	n, err := u.Repo.DeleteExpiredAuthbotCodes(ctx)
	if err != nil {
		if database.IsUndefinedTable(err) {
			u.Log.Warn("cleanup_expired_authbot_codes skipped: table does not exist")
			return
		}
		u.Log.Error("cleanup_expired_authbot_codes", zap.Error(err))
		return
	}
	if n > 0 {
		u.Log.Info("cleanup_expired_authbot_codes done", zap.Int64("deleted", n))
	}
}
