-- name: GetActiveReleaseRuntimeMigrationOwner :one
SELECT p.owner_id
FROM projects p
JOIN relase r ON r.project_id = p.id
WHERE p.id = sqlc.arg('project_id') AND p.deleted_at IS NULL
  AND r.service_id = sqlc.arg('service_id')
  AND r.status NOT IN ('stopped', 'deleted')
LIMIT 1;

-- name: GetActiveDebugRuntimeMigrationOwner :one
SELECT p.owner_id
FROM projects p
JOIN debug d ON d.project_id = p.id
WHERE p.id = sqlc.arg('project_id') AND p.deleted_at IS NULL
  AND d.service_id = sqlc.arg('service_id')
  AND d.status NOT IN ('stopped', 'deleted')
LIMIT 1;

-- name: ReplaceLegacyReleaseRuntimeServiceID :execrows
UPDATE relase
SET service_id = sqlc.arg('new_service_id'), updated_at = NOW()
WHERE project_id = sqlc.arg('project_id')
  AND service_id = sqlc.arg('old_service_id')
  AND status NOT IN ('stopped', 'deleted');

-- name: ReplaceLegacyDebugRuntimeServiceID :execrows
UPDATE debug
SET service_id = sqlc.arg('new_service_id'), updated_at = NOW()
WHERE project_id = sqlc.arg('project_id')
  AND service_id = sqlc.arg('old_service_id')
  AND status NOT IN ('stopped', 'deleted');
