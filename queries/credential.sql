-- name: CreateCredential :one
INSERT INTO credential (id, owner_id, type_key, name, data_encrypted)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetCredentialForOwner :one
SELECT * FROM credential WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: GetCredentialByID :one
-- Unscoped by owner — internal/engine reads only, guarded by X-Internal-Token
-- and an explicit owner check in the usecase layer.
SELECT * FROM credential WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCredentialByOwnerAndName :one
SELECT * FROM credential WHERE owner_id = $1 AND name = $2 AND deleted_at IS NULL;

-- name: UpdateCredential :one
UPDATE credential
SET name           = COALESCE(sqlc.narg('name'), name),
    data_encrypted = COALESCE(sqlc.narg('data_encrypted'), data_encrypted),
    updated_at     = NOW()
WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteCredential :execrows
UPDATE credential SET deleted_at = NOW()
WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: ListCredentialsForOwner :many
SELECT * FROM credential
WHERE owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL
  AND (sqlc.narg('type_key')::text IS NULL OR type_key = sqlc.narg('type_key'))
ORDER BY name
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountCredentialsForOwner :one
SELECT COUNT(*) FROM credential
WHERE owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL
  AND (sqlc.narg('type_key')::text IS NULL OR type_key = sqlc.narg('type_key'));

-- name: GetProjectOwnerForCredential :one
-- Cross-tenant guard for the internal resolve endpoints: derive a project's
-- owner without importing the project module (plain SQL join, per PORTING_GUIDE rule 4).
SELECT owner_id FROM projects WHERE id = $1 AND deleted_at IS NULL;
