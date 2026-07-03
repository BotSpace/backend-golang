-- name: GetConfig :one
SELECT * FROM config WHERE key = sqlc.arg('key');

-- name: UpsertConfig :one
INSERT INTO config (key, value, updated_by)
VALUES (sqlc.arg('key'), sqlc.arg('value'), sqlc.narg('updated_by'))
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()
RETURNING *;
