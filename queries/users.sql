-- name: CreateUser :one
-- Upsert-by-phone: re-registering an unvalidated phone updates its details
-- (mirrors Django update_or_create in UserService.create_user).
INSERT INTO users (id, phone, first_name, last_name, password_hash, role)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (phone) DO UPDATE
SET first_name    = EXCLUDED.first_name,
    last_name     = EXCLUDED.last_name,
    password_hash = EXCLUDED.password_hash,
    updated_at    = NOW()
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone = $1;

-- name: GetUserByEmail :one
-- email has no UNIQUE constraint (legacy schema); Google sign-in is the only
-- caller and only ever looks up its own verified address, so a stray
-- duplicate elsewhere just resolves to whichever row Postgres returns first.
SELECT * FROM users WHERE email = $1;

-- name: UpdateUserProfile :one
UPDATE users
SET first_name = COALESCE(sqlc.narg('first_name'), first_name),
    last_name  = COALESCE(sqlc.narg('last_name'), last_name),
    username   = COALESCE(sqlc.narg('username'), username),
    email      = COALESCE(sqlc.narg('email'), email),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: UpdateUserPassword :execrows
UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1;

-- name: MarkUserValidated :one
UPDATE users
SET validated_at = COALESCE(validated_at, NOW()), updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateUserRole :one
UPDATE users SET role = $2, updated_at = NOW() WHERE id = $1 RETURNING *;

-- name: AddUserBalance :one
-- Atomic balance delta (can be negative). Guarded so it never goes below 0.
UPDATE users
SET balance = balance + sqlc.arg('delta'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND balance + sqlc.arg('delta') >= 0
RETURNING *;

-- name: SetUserActive :one
-- Admin ban/unban toggle (is_active = FALSE blocks login, mirrors accounts.UserModel.is_active).
UPDATE users SET is_active = sqlc.arg('is_active'), updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users
WHERE (sqlc.narg('search')::text IS NULL
       OR phone ILIKE '%' || sqlc.narg('search') || '%'
       OR first_name ILIKE '%' || sqlc.narg('search') || '%'
       OR last_name ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountUsers :one
SELECT COUNT(*) FROM users
WHERE (sqlc.narg('search')::text IS NULL
       OR phone ILIKE '%' || sqlc.narg('search') || '%'
       OR first_name ILIKE '%' || sqlc.narg('search') || '%'
       OR last_name ILIKE '%' || sqlc.narg('search') || '%');

-- ===================== admin detail enrichment (cross-module reads) =====================

-- name: GetUserSubscriptionDetail :one
SELECT us.plan_id, us.status, us.started_at, us.expires_at, us.token_reset_at, us.bots_count,
       us.tokens_used_input, us.tokens_used_output,
       p.name AS plan_name, p.max_bots, p.max_tokens
FROM user_subscription us
JOIN plan p ON p.id = us.plan_id
WHERE us.user_id = sqlc.arg('user_id');

-- name: CountProjectsForUser :one
SELECT COUNT(*) FROM projects WHERE owner_id = sqlc.arg('user_id') AND deleted_at IS NULL;

-- name: ListProjectsForUserBrief :many
SELECT id, name, username, is_template, created_at FROM projects
WHERE owner_id = sqlc.arg('user_id') AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 20;

-- name: CountPurchasedTemplatesForUser :one
SELECT COUNT(*) FROM usertemplate WHERE user_id = sqlc.arg('user_id');

-- name: CountAuthoredTemplatesForUser :one
SELECT COUNT(*) FROM template WHERE author_id = sqlc.arg('user_id')::uuid;

-- name: AdminInsertBalanceHistory :one
-- Audit row for an admin-initiated balance adjustment (AddUserBalance above
-- is what actually moves the money). Written directly into payment's
-- `history` table by table name only — no import of package payment — the
-- same cross-module SQL-only convention internal/modules/administration uses
-- for its dashboard stats.
INSERT INTO history (id, user_id, amount, comment, type)
VALUES (sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('amount'), sqlc.arg('comment'), sqlc.arg('type'))
RETURNING *;
