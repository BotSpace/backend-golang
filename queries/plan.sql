-- name: ListActivePlans :many
SELECT * FROM plan WHERE is_active = true ORDER BY sort_order ASC;

-- name: GetPlanByID :one
SELECT * FROM plan WHERE id = $1;

-- name: GetActivePlanByID :one
-- Used by subscription upgrade: a target plan must be active to buy into it.
SELECT * FROM plan WHERE id = $1 AND is_active = true;

-- name: GetPlanByName :one
SELECT * FROM plan WHERE name = $1;

-- ===================== admin CRUD =====================

-- name: ListAllPlans :many
-- Admin listing: every plan regardless of is_active (the public ListActivePlans
-- above only returns active ones).
SELECT * FROM plan ORDER BY sort_order ASC, created_at ASC;

-- name: CreatePlan :one
INSERT INTO plan
    (name, price, description, is_top, is_active, sort_order, max_bots, max_tokens,
     max_output_tokens, max_input_tokens, cached_token_rate, features)
VALUES (sqlc.arg('name'), sqlc.arg('price'), sqlc.narg('description'), sqlc.arg('is_top'), sqlc.arg('is_active'),
        sqlc.arg('sort_order'), sqlc.arg('max_bots'), sqlc.arg('max_tokens'), sqlc.arg('max_output_tokens'),
        sqlc.arg('max_input_tokens'), sqlc.arg('cached_token_rate'), sqlc.arg('features'))
RETURNING *;

-- name: UpdatePlanAdmin :one
UPDATE plan
SET name              = COALESCE(sqlc.narg('name'), name),
    price             = COALESCE(sqlc.narg('price'), price),
    description       = COALESCE(sqlc.narg('description'), description),
    is_top            = COALESCE(sqlc.narg('is_top'), is_top),
    is_active         = COALESCE(sqlc.narg('is_active'), is_active),
    sort_order        = COALESCE(sqlc.narg('sort_order'), sort_order),
    max_bots          = COALESCE(sqlc.narg('max_bots'), max_bots),
    max_tokens        = COALESCE(sqlc.narg('max_tokens'), max_tokens),
    cached_token_rate = COALESCE(sqlc.narg('cached_token_rate'), cached_token_rate),
    features          = COALESCE(sqlc.narg('features'), features),
    updated_at        = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: DeletePlan :execrows
DELETE FROM plan WHERE id = sqlc.arg('id');

-- name: GetSubscriptionByUserID :one
SELECT * FROM user_subscription WHERE user_id = $1;

-- name: CreateSubscription :one
-- Get-or-create in one round trip: ON CONFLICT DO UPDATE with a no-op SET
-- (column set to itself) always returns a row via RETURNING, whether it was
-- just inserted or already existed, without touching an existing row's plan.
INSERT INTO user_subscription (id, user_id, plan_id, status)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE
SET updated_at = user_subscription.updated_at
RETURNING *;

-- name: UpdateSubscriptionPlan :one
UPDATE user_subscription
SET plan_id    = sqlc.arg('plan_id'),
    status     = sqlc.arg('status'),
    expires_at = sqlc.arg('expires_at'),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: IncrementSubscriptionUsage :execrows
UPDATE user_subscription
SET tokens_used_input  = tokens_used_input + sqlc.arg('input_delta'),
    tokens_used_output = tokens_used_output + sqlc.arg('output_delta'),
    updated_at         = NOW()
WHERE id = sqlc.arg('id');
