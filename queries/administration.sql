-- name: AppendAuditLog :one
-- Records one admin action. actor_id/metadata/ip are all optional — a nil
-- actor means the action was system-initiated.
INSERT INTO audit_log (actor_id, action, target, metadata, ip)
VALUES (sqlc.narg('actor_id'), sqlc.arg('action'), sqlc.arg('target'), sqlc.narg('metadata'), sqlc.narg('ip'))
RETURNING *;

-- name: GetAuditLogByID :one
SELECT * FROM audit_log WHERE id = $1;

-- name: ListAuditLog :many
SELECT * FROM audit_log
WHERE (sqlc.narg('action')::text IS NULL OR action ILIKE '%' || sqlc.narg('action') || '%')
  AND (sqlc.narg('target')::text IS NULL OR target ILIKE '%' || sqlc.narg('target') || '%')
  AND (sqlc.narg('actor_id')::uuid IS NULL OR actor_id = sqlc.narg('actor_id'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAuditLog :one
SELECT COUNT(*) FROM audit_log
WHERE (sqlc.narg('action')::text IS NULL OR action ILIKE '%' || sqlc.narg('action') || '%')
  AND (sqlc.narg('target')::text IS NULL OR target ILIKE '%' || sqlc.narg('target') || '%')
  AND (sqlc.narg('actor_id')::uuid IS NULL OR actor_id = sqlc.narg('actor_id'));

-- Read-only aggregate queries backing the admin dashboard KPIs. These read
-- the users/projects tables directly (no cross-module Go import) per the
-- porting guide's SQL-scoping rule.

-- name: CountUsersTotal :one
SELECT COUNT(*) FROM users;

-- name: CountUsersCreatedSince :one
SELECT COUNT(*) FROM users WHERE created_at >= sqlc.arg('since');

-- name: CountProjectsTotal :one
SELECT COUNT(*) FROM projects WHERE deleted_at IS NULL;

-- name: DailyUserSignups :many
-- Growth chart: one row per calendar day (UTC) since `since`, newest last.
-- Cast to text (not DATE) to keep the sqlc-generated column a plain string.
SELECT TO_CHAR(created_at, 'YYYY-MM-DD') AS day, COUNT(*) AS count
FROM users
WHERE created_at >= sqlc.arg('since')
GROUP BY day
ORDER BY day;

-- ===================== richer dashboard KPIs (cross-module reads) =====================

-- name: CountUsersActive :one
SELECT COUNT(*) FROM users WHERE is_active = TRUE;

-- name: CountProjectsWithRelease :one
SELECT COUNT(DISTINCT project_id) FROM relase;

-- name: CountTemplatesTotal :one
SELECT COUNT(*) FROM template;

-- name: CountTemplateSubmissionsPending :one
SELECT COUNT(*) FROM template_submission_request WHERE status = 'pending';

-- name: CountBotWithdrawalsPending :one
SELECT COUNT(*) FROM payment_withdrawal_request WHERE status = 'pending';

-- name: CountTemplateWithdrawalsPending :one
SELECT COUNT(*) FROM template_withdrawal_request WHERE status = 'pending';

-- name: SumBotCommissionSince :one
-- Botspace's cut of paid bot payment orders (the payment_order.commission
-- column), used as the bot-side revenue component of the 30-day KPI.
SELECT COALESCE(SUM(commission), 0)::numeric AS total FROM payment_order
WHERE status = 'paid' AND deleted_at IS NULL AND paid_at >= sqlc.arg('since')::timestamptz;

-- name: SumTemplateCommissionSince :one
-- Botspace's 20% cut of confirmed template marketplace sales (author keeps
-- template.AuthorCommissionPercent = 80%, see internal/modules/template).
SELECT (COALESCE(SUM(amount), 0) * 0.20)::numeric AS total FROM template_order
WHERE payment_status = TRUE AND status = 'confirm' AND updated_at >= sqlc.arg('since')::timestamptz;

-- name: DailyPaidOrders :many
-- Growth chart: one row per calendar day (UTC) since `since`, count of bot
-- payment orders that settled paid that day.
SELECT TO_CHAR(paid_at, 'YYYY-MM-DD') AS day, COUNT(*) AS count
FROM payment_order
WHERE status = 'paid' AND deleted_at IS NULL AND paid_at >= sqlc.arg('since')::timestamptz
GROUP BY day
ORDER BY day;
