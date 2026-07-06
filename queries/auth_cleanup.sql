-- Net-new Go-side janitors that never existed in Django (checked
-- core/apps/api/tasks/*.py + CELERY_BEAT_SCHEDULE + SIMPLE_JWT settings —
-- backend-golang's internal/server/scheduler.go doc comments have the full
-- reasoning). Moved here from backend-golang because periodic jobs belong in
-- the worker, not the API process.

-- name: ExpireActiveSubscriptions :many
-- active -> grace_period once expires_at passes.
UPDATE user_subscription
SET status = 'grace_period', updated_at = now()
WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at < now()
RETURNING id;

-- name: SuspendGracePeriodSubscriptions :many
-- grace_period -> suspended a further 3 days after expires_at (matches
-- PlanStatus.GRACE_PERIOD's "3 kun" comment, backend-golang enums/plan.py).
UPDATE user_subscription
SET status = 'suspended', updated_at = now()
WHERE status = 'grace_period' AND expires_at IS NOT NULL AND expires_at < now() - INTERVAL '3 days'
RETURNING id;

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens WHERE expires_at < now();

-- name: DeleteExpiredAuthbotCodes :execrows
-- Codes nobody ever redeemed.
DELETE FROM authbot_codes WHERE expires_at < now();
