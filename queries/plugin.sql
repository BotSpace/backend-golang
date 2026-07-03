-- Module catalog -------------------------------------------------------

-- name: CreateModule :one
INSERT INTO module (id, slug, name, description, icon, github_url, image, port, auth_token, active, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetModuleBySlug :one
SELECT * FROM module WHERE slug = $1;

-- name: GetModuleByID :one
SELECT * FROM module WHERE id = $1;

-- name: UpdateModule :one
UPDATE module
SET name        = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    icon        = COALESCE(sqlc.narg('icon'), icon),
    github_url  = COALESCE(sqlc.narg('github_url'), github_url),
    image       = COALESCE(sqlc.narg('image'), image),
    port        = COALESCE(sqlc.narg('port'), port),
    auth_token  = COALESCE(sqlc.narg('auth_token'), auth_token),
    active      = COALESCE(sqlc.narg('active'), active),
    is_default  = COALESCE(sqlc.narg('is_default'), is_default),
    updated_at  = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: DeleteModule :execrows
DELETE FROM module WHERE id = $1;

-- name: ListModules :many
SELECT * FROM module
WHERE (sqlc.narg('search')::text IS NULL
       OR name ILIKE '%' || sqlc.narg('search') || '%'
       OR slug ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('active')::bool IS NULL OR active = sqlc.narg('active'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountModules :one
SELECT COUNT(*) FROM module
WHERE (sqlc.narg('search')::text IS NULL
       OR name ILIKE '%' || sqlc.narg('search') || '%'
       OR slug ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('active')::bool IS NULL OR active = sqlc.narg('active'));

-- name: ListModulesWithGithubURL :many
-- Small, admin-curated catalog — loaded whole and matched in Go (github
-- webhook -> module lookup by normalized repo URL).
SELECT * FROM module WHERE github_url IS NOT NULL AND github_url <> '';

-- name: UpdateModuleManifest :one
UPDATE module
SET manifest_cache = sqlc.arg('manifest_cache'), manifest_fetched_at = NOW(), updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- Project <-> module links ----------------------------------------------

-- name: GetProjectModuleForOwner :one
SELECT * FROM project_module
WHERE project_id = sqlc.arg('project_id') AND module_id = sqlc.arg('module_id')
  AND EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  );

-- name: EnsureProjectModuleForOwner :one
-- Idempotent enable: inserts a fresh row (status='pending') the first time,
-- or just touches updated_at if the pair already exists. The SELECT ...
-- WHERE EXISTS guard scopes the insert to the caller's own project — a
-- mismatched owner yields zero rows (pgx.ErrNoRows -> ErrNotFound).
INSERT INTO project_module (id, project_id, module_id, status)
SELECT gen_random_uuid(), sqlc.arg('project_id'), sqlc.arg('module_id'), 'pending'
WHERE EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
ON CONFLICT (project_id, module_id) DO UPDATE SET updated_at = NOW()
RETURNING *;

-- name: UpdateProjectModuleDeployState :one
-- Called after the orchestrator confirms the shared pod (or a self-hosted
-- endpoint) is reachable; id was already owner-validated by the ensure step.
UPDATE project_module
SET enabled    = TRUE,
    service_id = sqlc.arg('service_id'),
    endpoint   = sqlc.arg('endpoint'),
    status     = sqlc.arg('status'),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: SetProjectModuleDisabledForOwner :one
UPDATE project_module
SET enabled = FALSE, status = 'stopped', updated_at = NOW()
WHERE project_id = sqlc.arg('project_id') AND module_id = sqlc.arg('module_id')
  AND EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  )
RETURNING *;

-- name: ListProjectModulesForOwner :many
SELECT * FROM project_module
WHERE project_id = sqlc.arg('project_id')
  AND EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  )
ORDER BY created_at DESC;

-- name: ListEnabledProjectModulesForOwner :many
-- Feeds module-manifests / node-manifests / modules-for-engine — all
-- owner-scoped project-detail reads.
SELECT * FROM project_module
WHERE project_id = sqlc.arg('project_id') AND enabled = TRUE
  AND EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  )
ORDER BY created_at DESC;

-- name: CountEnabledProjectModulesExcluding :one
-- "Is any other project still using this shared module?" — guards teardown
-- of the shared pod on disable (last user only).
SELECT COUNT(*) FROM project_module
WHERE module_id = sqlc.arg('module_id') AND enabled = TRUE AND project_id <> sqlc.arg('exclude_project_id');

-- name: BulkMarkProjectModulesRunning :execrows
-- Redeploy: refresh service_id/endpoint/status for every project currently
-- using this shared module.
UPDATE project_module
SET service_id = sqlc.arg('service_id'), endpoint = sqlc.arg('endpoint'), status = 'running', updated_at = NOW()
WHERE module_id = sqlc.arg('module_id') AND enabled = TRUE;
