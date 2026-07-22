-- name: GetRuntimeMigrationOwner :one
SELECT owner_id
FROM projects
WHERE id = sqlc.arg('project_id') AND deleted_at IS NULL;

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
