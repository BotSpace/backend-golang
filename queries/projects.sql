-- name: CreateProject :one
INSERT INTO projects (id, name, description, username, owner_id, flow, is_template)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1 AND deleted_at IS NULL;

-- name: GetProjectForOwner :one
SELECT * FROM projects WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: UpdateProject :one
UPDATE projects
SET name        = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    username    = COALESCE(sqlc.narg('username'), username),
    flow        = COALESCE(sqlc.narg('flow'), flow),
    updated_at  = NOW()
WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProject :execrows
UPDATE projects SET deleted_at = NOW()
WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: ListProjectsForOwner :many
SELECT * FROM projects
WHERE owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL AND is_template = false
  AND (sqlc.narg('search')::text IS NULL OR name ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountProjectsForOwner :one
SELECT COUNT(*) FROM projects
WHERE owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL AND is_template = false
  AND (sqlc.narg('search')::text IS NULL OR name ILIKE '%' || sqlc.narg('search') || '%');

-- ===================== admin-wide (cross-owner) =====================

-- name: ListProjectsAdmin :many
SELECT sqlc.embed(p), u.phone AS owner_phone,
       (SELECT COUNT(*) FROM relase r WHERE r.project_id = p.id) AS releases_count,
       COALESCE((SELECT r.status FROM relase r WHERE r.project_id = p.id ORDER BY r.version DESC LIMIT 1), '')::text AS release_status
FROM projects p
JOIN users u ON u.id = p.owner_id
WHERE p.deleted_at IS NULL
  AND (sqlc.narg('search')::text IS NULL OR p.name ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('is_template')::bool IS NULL OR p.is_template = sqlc.narg('is_template')::bool)
  AND (sqlc.narg('has_release')::bool IS NULL
       OR (EXISTS (SELECT 1 FROM relase r WHERE r.project_id = p.id)) = sqlc.narg('has_release')::bool)
  AND (sqlc.narg('created_after')::timestamptz IS NULL OR p.created_at >= sqlc.narg('created_after')::timestamptz)
  AND (sqlc.narg('created_before')::timestamptz IS NULL OR p.created_at <= sqlc.narg('created_before')::timestamptz)
ORDER BY
  CASE WHEN sqlc.narg('ordering')::text = 'name' THEN p.name END ASC,
  CASE WHEN sqlc.narg('ordering')::text = 'created_at' THEN p.created_at END ASC,
  p.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountProjectsAdmin :one
SELECT COUNT(*) FROM projects p
WHERE p.deleted_at IS NULL
  AND (sqlc.narg('search')::text IS NULL OR p.name ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('is_template')::bool IS NULL OR p.is_template = sqlc.narg('is_template')::bool)
  AND (sqlc.narg('has_release')::bool IS NULL
       OR (EXISTS (SELECT 1 FROM relase r WHERE r.project_id = p.id)) = sqlc.narg('has_release')::bool)
  AND (sqlc.narg('created_after')::timestamptz IS NULL OR p.created_at >= sqlc.narg('created_after')::timestamptz)
  AND (sqlc.narg('created_before')::timestamptz IS NULL OR p.created_at <= sqlc.narg('created_before')::timestamptz);

-- name: GetProjectAdmin :one
SELECT sqlc.embed(p), u.phone AS owner_phone, u.email AS owner_email
FROM projects p
JOIN users u ON u.id = p.owner_id
WHERE p.id = sqlc.arg('id') AND p.deleted_at IS NULL;

-- name: ListReleasesForProjectAdmin :many
SELECT * FROM relase WHERE project_id = sqlc.arg('project_id') ORDER BY version DESC;

-- name: AdminMonitoringProjects :many
-- Per-project resource snapshot for the admin monitoring page: latest
-- reported cpu/ram (resource table), latest disk usage snapshot
-- (project_resource_snapshot), and the most recent release's status.
-- cpu_cores/memory_bytes are nullable (no report yet); disk_bytes defaults to 0.
SELECT sqlc.embed(p), u.phone AS owner_phone,
       COALESCE((SELECT r.status FROM relase r WHERE r.project_id = p.id ORDER BY r.version DESC LIMIT 1), '')::text AS release_status,
       COALESCE((SELECT res.cpu FROM resource res WHERE res.project_id = p.id ORDER BY res.created_at DESC LIMIT 1), 0)::int AS cpu_cores,
       COALESCE((SELECT res.ram FROM resource res WHERE res.project_id = p.id ORDER BY res.created_at DESC LIMIT 1), 0)::int AS memory_bytes,
       COALESCE((SELECT snap.disk_bytes FROM project_resource_snapshot snap WHERE snap.project_id = p.id ORDER BY snap.created_at DESC LIMIT 1), 0)::bigint AS disk_bytes,
       COALESCE((SELECT res.net_rx + res.net_tx FROM resource res WHERE res.project_id = p.id ORDER BY res.created_at DESC LIMIT 1), 0)::bigint AS net_bytes
FROM projects p
JOIN users u ON u.id = p.owner_id
WHERE p.deleted_at IS NULL
  AND (sqlc.narg('search')::text IS NULL OR p.name ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY p.created_at DESC, p.id ASC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ProjectDiskHistory :many
-- Disk-usage time series from the hourly disksnap snapshots, for the monitoring
-- Disk chart. Prometheus has no filesystem metric for the engine pods (its disk
-- range query returns nothing), so the chart sources disk history from here.
SELECT created_at, disk_bytes FROM project_resource_snapshot
WHERE project_id = sqlc.arg('project_id') AND created_at >= sqlc.arg('since')
ORDER BY created_at;
