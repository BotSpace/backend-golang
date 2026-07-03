-- name: MintWebhookEndpoint :one
-- Owner-scoped get-or-create: a row is only inserted when the caller owns the
-- project (EXISTS gate below); a (project_id, node_id, chat_id) conflict just
-- returns the existing row. Zero rows back means "not found or not owned".
INSERT INTO webhook_endpoint (id, project_id, node_id, chat_id)
SELECT gen_random_uuid(), sqlc.arg('project_id'), sqlc.arg('node_id'), sqlc.arg('chat_id')
WHERE EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
ON CONFLICT (project_id, node_id, chat_id) DO UPDATE SET updated_at = NOW()
RETURNING *;

-- name: ResolveWebhookToken :one
-- Internal lookup for the webhook-hook forwarder: token -> node/chat routing
-- info plus the owning project's release/debug uuids.
SELECT
    we.id,
    we.token,
    we.project_id,
    we.node_id,
    we.chat_id,
    we.created_at,
    we.updated_at,
    p.release_webhook_uuid,
    p.debug_webhook_uuid
FROM webhook_endpoint we
JOIN projects p ON p.id = we.project_id
WHERE we.token = sqlc.arg('token') AND p.deleted_at IS NULL;

-- name: ListManagedBotsForOwner :many
SELECT * FROM managed_bot WHERE owner_id = sqlc.arg('owner_id') ORDER BY created_at DESC;

-- name: UpsertManagedBot :one
-- Keyed by the Telegram bot id (manager-bot webhook is idempotent per bot).
-- owner_id is intentionally overwritten on every call, including with NULL,
-- to mirror the Django update_or_create(defaults={"owner": owner, ...}).
INSERT INTO managed_bot (id, owner_id, tg_bot_id, username, name, token_encrypted)
VALUES (gen_random_uuid(), sqlc.narg('owner_id'), sqlc.arg('tg_bot_id'), sqlc.arg('username'), sqlc.arg('name'), sqlc.arg('token_encrypted'))
ON CONFLICT (tg_bot_id) DO UPDATE
SET owner_id        = sqlc.narg('owner_id'),
    username        = sqlc.arg('username'),
    name            = sqlc.arg('name'),
    token_encrypted = sqlc.arg('token_encrypted'),
    updated_at      = NOW()
RETURNING *;
