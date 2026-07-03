-- name: CreateMessage :one
-- Ownership is enforced inline: the SELECT only produces a row (so INSERT
-- only happens) when the target project belongs to sqlc.arg('owner_id').
INSERT INTO message (id, project_id, caption, message_type, file_id, button_type, buttons)
SELECT sqlc.arg('id'),
       sqlc.arg('project_id'),
       sqlc.arg('caption'),
       sqlc.arg('message_type'),
       sqlc.narg('file_id'),
       sqlc.arg('button_type'),
       sqlc.arg('buttons')
WHERE EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
RETURNING *;

-- name: GetMessageForOwner :one
SELECT m.* FROM message m
WHERE m.id = sqlc.arg('id')
  AND EXISTS (
    SELECT 1 FROM projects p WHERE p.id = m.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
);

-- name: UpdateMessageForOwner :one
UPDATE message
SET caption      = COALESCE(sqlc.narg('caption'), caption),
    message_type = COALESCE(sqlc.narg('message_type'), message_type),
    file_id      = COALESCE(sqlc.narg('file_id'), file_id),
    button_type  = COALESCE(sqlc.narg('button_type'), button_type),
    buttons      = COALESCE(sqlc.narg('buttons'), buttons),
    updated_at   = NOW()
WHERE message.id = sqlc.arg('id')
  AND EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = message.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
RETURNING *;

-- name: DeleteMessageForOwner :execrows
DELETE FROM message
WHERE message.id = sqlc.arg('id')
  AND EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = message.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
);

-- name: ListMessagesForOwner :many
-- project_id narg lets the caller optionally scope to a single project;
-- when NULL, every message across all of the owner's projects is returned.
SELECT m.* FROM message m
JOIN projects p ON p.id = m.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR m.project_id = sqlc.narg('project_id'))
ORDER BY m.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountMessagesForOwner :one
SELECT COUNT(*) FROM message m
JOIN projects p ON p.id = m.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR m.project_id = sqlc.narg('project_id'));
