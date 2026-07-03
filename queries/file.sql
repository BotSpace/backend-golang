-- name: CreateFile :one
-- Ownership is enforced inline: the SELECT only produces a row (so INSERT
-- only happens) when the target project belongs to sqlc.arg('owner_id').
INSERT INTO file (id, uuid, project_id, user_id, file_path, original_name, filesize, filetype, expires_at)
SELECT sqlc.arg('id'), sqlc.arg('uuid'), sqlc.arg('project_id'), sqlc.arg('owner_id'), sqlc.arg('file_path'),
       sqlc.arg('original_name'), sqlc.arg('filesize'), sqlc.arg('filetype'), sqlc.narg('expires_at')
WHERE EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
RETURNING *;

-- name: GetFileByUUID :one
-- Intentionally public (no ownership check) — mirrors Django's AllowAny
-- retrieve, since file URLs are embedded in bot messages/CMS content that
-- end users open unauthenticated.
SELECT * FROM file WHERE file.uuid = sqlc.arg('uuid');

-- name: GetFileByUUIDForOwner :one
SELECT f.* FROM file f
JOIN projects p ON p.id = f.project_id
WHERE f.uuid = sqlc.arg('uuid') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: UpdateFileByUUIDForOwner :one
UPDATE file
SET filetype   = COALESCE(sqlc.narg('filetype'), filetype),
    updated_at = NOW()
WHERE file.uuid = sqlc.arg('uuid')
  AND EXISTS (
    SELECT 1 FROM projects p WHERE p.id = file.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  )
RETURNING *;

-- name: DeleteFileByUUIDForOwner :one
-- Returns the deleted row (not :execrows) so the caller can unlink the blob
-- on disk using its file_path.
DELETE FROM file
WHERE file.uuid = sqlc.arg('uuid')
  AND EXISTS (
    SELECT 1 FROM projects p WHERE p.id = file.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  )
RETURNING *;

-- name: ListFiles :many
-- project_id/filetype nargs let the caller optionally narrow the listing;
-- NULL means "no filter" on that dimension.
SELECT f.* FROM file f
JOIN projects p ON p.id = f.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR f.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('filetype')::text IS NULL OR f.filetype = sqlc.narg('filetype'))
ORDER BY f.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountFiles :one
SELECT COUNT(*) FROM file f
JOIN projects p ON p.id = f.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR f.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('filetype')::text IS NULL OR f.filetype = sqlc.narg('filetype'));

-- name: FileProjectOwnedByOwner :one
-- Lets the "used" usecase 404 on a project the caller does not own instead
-- of silently returning zeroed totals.
SELECT EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
);

-- name: CreateResource :one
INSERT INTO resource (id, project_id, user_id, hostname, ram, cpu, net_rx, net_tx)
SELECT sqlc.arg('id'), sqlc.arg('project_id'), sqlc.arg('owner_id'), sqlc.arg('hostname'),
       sqlc.arg('ram'), sqlc.arg('cpu'), sqlc.arg('net_rx'), sqlc.arg('net_tx')
WHERE EXISTS (
    SELECT 1 FROM projects p WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
RETURNING *;

-- name: GetResourceForOwner :one
SELECT r.* FROM resource r
JOIN projects p ON p.id = r.project_id
WHERE r.id = sqlc.arg('id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: ListResources :many
SELECT r.* FROM resource r
JOIN projects p ON p.id = r.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR r.project_id = sqlc.narg('project_id'))
ORDER BY r.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountResources :one
SELECT COUNT(*) FROM resource r
JOIN projects p ON p.id = r.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR r.project_id = sqlc.narg('project_id'));

-- ── /used aggregation ───────────────────────────────────────────────────────
-- Every query below accepts an optional project_id narg: NULL scopes across
-- every project the owner has ("user" mode in Django's UsedService), a value
-- scopes to that one project ("project" mode). Ownership is always enforced
-- through the projects join/EXISTS, never trusted from the caller.

-- name: SumFileSizeForOwner :one
SELECT COALESCE(SUM(f.filesize), 0)::bigint AS total
FROM file f
JOIN projects p ON p.id = f.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR f.project_id = sqlc.narg('project_id'));

-- name: LatestResourceByHostnameForOwner :many
-- One row per distinct hostname (its most recent report within the window),
-- so summing RAM/CPU/network across the result never double-counts a host
-- that reported multiple times.
SELECT DISTINCT ON (r.hostname) r.*
FROM resource r
JOIN projects p ON p.id = r.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR r.project_id = sqlc.narg('project_id'))
  AND r.created_at >= sqlc.arg('since')
ORDER BY r.hostname, r.created_at DESC;

-- name: ResourceHourlyForOwner :many
-- Hourly RAM/CPU average and network peak, for the usage chart.
SELECT date_trunc('hour', r.created_at)::timestamptz AS hour,
       COALESCE(AVG(r.ram), 0)::float8 AS ram,
       COALESCE(AVG(r.cpu), 0)::float8 AS cpu,
       COALESCE(MAX(r.net_rx), 0)::bigint AS net_rx,
       COALESCE(MAX(r.net_tx), 0)::bigint AS net_tx
FROM resource r
JOIN projects p ON p.id = r.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR r.project_id = sqlc.narg('project_id'))
  AND r.created_at >= sqlc.arg('since')
GROUP BY hour
ORDER BY hour;

-- name: StorageHourlyForOwner :many
-- Hourly storage peak, for the usage chart.
SELECT date_trunc('hour', s.created_at)::timestamptz AS hour,
       COALESCE(MAX(s.storage), 0)::bigint AS storage
FROM storageresource s
JOIN projects p ON p.id = s.project_id
WHERE p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR s.project_id = sqlc.narg('project_id'))
  AND s.created_at >= sqlc.arg('since')
GROUP BY hour
ORDER BY hour;
