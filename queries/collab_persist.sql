-- name: LockCollaborativeProjectFlowByLegacyID :one
SELECT id, debug_webhook_uuid, flow
FROM projects
WHERE legacy_id = sqlc.arg('legacy_id') AND deleted_at IS NULL
FOR UPDATE;

-- name: LockCollaborativeProjectFlowByID :one
SELECT id, debug_webhook_uuid, flow
FROM projects
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
FOR UPDATE;

-- name: PersistCollaborativeProjectFlow :execrows
UPDATE projects
SET flow = sqlc.arg('flow'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: GetCollaborativeRuntimeSnapshot :one
SELECT debug_webhook_uuid, flow
FROM projects
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;
