package server

import (
	"context"
	"time"

	"github.com/botspace/backend/internal/modules/auth"
	"github.com/botspace/backend/internal/modules/authbot"
	"github.com/botspace/backend/internal/modules/file"
	"github.com/botspace/backend/internal/modules/plan"
	"github.com/botspace/backend/internal/modules/release"
	"github.com/botspace/backend/internal/shared/scheduler"
)

const (
	engineReconcileInterval    = 2 * time.Minute
	fileCleanupInterval        = 5 * time.Minute
	subscriptionExpiryInterval = time.Hour
	tokenCleanupInterval       = time.Hour
)

// startScheduler wires the Celery-beat-parity jobs enumerated in
// config/conf/celery.py's CELERY_BEAT_SCHEDULE (Django) into
// internal/shared/scheduler's in-process ticker. Every job here is a plain
// usecase call — nothing scheduler-specific — so testing a job means testing
// the usecase method directly (see e.g. release.TestReconcileAll,
// plan.TestExpireSubscriptions, file.TestCleanupExpired).
//
// Jobs intentionally NOT ported (checked against core/apps/api/tasks/*.py +
// CELERY_BEAT_SCHEDULE):
//   - sync_bot (hourly Telegram bot photo/username refresh) — cosmetic, not
//     correctness.
//   - sync_debug (stop debug containers idle >30min) — resource/cost hygiene,
//     not data correctness; candidate for a future job but left out of this
//     pass (arguably belongs next to engine-golang's own lifecycle code).
//   - snapshot_project_disk (hourly disk-usage snapshot) — monitoring/chart
//     data, not correctness.
//   - sync_storage — already disabled in Django's own beat schedule.
//   - payment.process_resource_usage — Django-side placeholder with no logic
//     (core/apps/payment/tasks/payment.py), nothing to port.
//   - template ingest — no such Celery task exists in Django (checked
//     core/apps/template: no tasks/ dir, nothing in the beat schedule).
func (s *Server) startScheduler(
	ctx context.Context,
	authUC *auth.Usecase,
	authbotUC *authbot.Usecase,
	planUC plan.Usecase,
	fileUC file.Usecase,
	releaseUC release.Usecase,
) {
	jobs := []scheduler.Job{
		{
			// Ports Django's cleanup_failed_releases (core/apps/api/tasks/release.py,
			// every 10min there); run tighter here since this job doubles as
			// the "bots hang on launching" fix's system-wide sweep, not just
			// crash cleanup — see release.Usecase.ReconcileAll.
			Name:     "release.reconcile",
			Interval: engineReconcileInterval,
			Run: func(ctx context.Context) error {
				_, _, err := releaseUC.ReconcileAll(ctx)
				return err
			},
		},
		{
			// Ports cleanup_expired_files (core/apps/api/tasks/file.py), same
			// 5-minute cadence.
			Name:     "file.cleanup_expired",
			Interval: fileCleanupInterval,
			Run: func(ctx context.Context) error {
				_, err := fileUC.CleanupExpired(ctx)
				return err
			},
		},
		{
			// Net-new: Django never actually transitioned subscription status
			// on expiry (see queries/plan.sql's ExpireActiveSubscriptions doc
			// comment) — this keeps the status column truthful going forward.
			Name:     "plan.expire_subscriptions",
			Interval: subscriptionExpiryInterval,
			Run: func(ctx context.Context) error {
				_, _, err := planUC.ExpireSubscriptions(ctx)
				return err
			},
		},
		{
			// Wires the previously-generated-but-never-called
			// sqlcdb.DeleteExpiredRefreshTokens query.
			Name:     "auth.cleanup_expired_refresh_tokens",
			Interval: tokenCleanupInterval,
			Run:      authUC.CleanupExpiredRefreshTokens,
		},
		{
			// Wires the previously-generated-but-never-called
			// sqlcdb.DeleteExpiredAuthbotCodes query (Django's own
			// clear_old_codes task exists but was never added to the beat
			// schedule either — core/apps/authbot/tasks/authbot.py).
			Name:     "authbot.cleanup_expired_codes",
			Interval: tokenCleanupInterval,
			Run: func(ctx context.Context) error {
				_, err := authbotUC.CleanupExpiredCodes(ctx)
				return err
			},
		},
	}
	scheduler.Start(ctx, s.log, jobs)
}
