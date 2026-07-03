-- Release: creating a row copies the project's current draft flow (owner-scoped
-- via the projects join, so a mismatched owner yields zero rows -> ErrNotFound).
-- name: CreateRelease :one
INSERT INTO relase (id, project_id, version, flow, status)
SELECT sqlc.arg('id'), p.id, sqlc.arg('version'), p.flow, 'pending'
FROM projects p
WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
RETURNING *;

-- name: GetReleaseForOwner :one
SELECT r.*
FROM relase r
JOIN projects p ON p.id = r.project_id
WHERE r.id = sqlc.arg('id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: ListReleasesForProject :many
SELECT r.*
FROM relase r
JOIN projects p ON p.id = r.project_id
WHERE r.project_id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
ORDER BY r.version DESC;

-- name: LatestReleaseVersion :one
SELECT COALESCE(MAX(r.version), 0)::int
FROM relase r
JOIN projects p ON p.id = r.project_id
WHERE r.project_id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: UpdateReleaseDeployResult :one
UPDATE relase
SET service_id = sqlc.narg('service_id'), status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: UpdateReleaseStatusForOwner :one
UPDATE relase r
SET status = sqlc.arg('status'), updated_at = NOW()
FROM projects p
WHERE r.id = sqlc.arg('id') AND r.project_id = p.id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
RETURNING r.*;

-- Cleanup rule (Django RelaseSignal): stop every previous release still
-- RUNNING/INIT for the project, excluding the freshly created one. Rows are
-- returned so the caller can call engine.Stop on each service_id before/along
-- with flipping status to stopped.
-- name: StopActiveReleasesExcept :many
UPDATE relase
SET status = 'stopped', updated_at = NOW()
WHERE project_id = sqlc.arg('project_id')
  AND id != sqlc.arg('exclude_id')
  AND status IN ('initializing', 'running')
RETURNING *;

-- Owner-scoped list of every release's service_id that still has a live k8s
-- deployment (any status — Stop is idempotent so an already-stopped/failed
-- row's stale service_id is harmless to also pass through) for a project.
-- Used by project.Usecase.Delete to tear down every release's Deployment
-- (+ its -env Secret/-flow ConfigMap/bot-<uuid> Service) BEFORE the project
-- disappears behind deleted_at — after that point GetDebugForOwner/
-- ListReleasesForProject can no longer see the project's rows at all.
-- name: ListReleaseServiceIDsForOwner :many
SELECT r.service_id
FROM relase r
JOIN projects p ON p.id = r.project_id
WHERE r.project_id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
  AND r.service_id IS NOT NULL AND r.service_id <> '';

-- Debug (one row per project). Upsert mirrors Django's get_or_create.
-- name: GetDebugForOwner :one
SELECT d.*
FROM debug d
JOIN projects p ON p.id = d.project_id
WHERE d.project_id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: GetOrCreateDebugForOwner :one
INSERT INTO debug (id, project_id)
SELECT sqlc.arg('id'), p.id
FROM projects p
WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
ON CONFLICT (project_id) DO UPDATE SET updated_at = debug.updated_at
RETURNING *;

-- name: UpdateDebugDeployResult :one
UPDATE debug
SET service_id = sqlc.narg('service_id'), status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: UpdateDebugStatusForOwner :one
UPDATE debug d
SET status = sqlc.arg('status'), updated_at = NOW()
FROM projects p
WHERE d.id = sqlc.arg('id') AND d.project_id = p.id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
RETURNING d.*;

-- Enviroment: create/update is an upsert on (key, project_id), owner-scoped
-- through a CTE so a foreign project silently yields zero rows -> ErrNotFound.
-- name: UpsertEnviromentForOwner :one
WITH proj AS (
    SELECT projects.id FROM projects WHERE projects.id = sqlc.arg('project_id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL
)
INSERT INTO enviroment (id, project_id, user_id, key, value, secret)
SELECT sqlc.arg('id'), proj.id, sqlc.arg('user_id'), sqlc.arg('key'), sqlc.arg('value'), sqlc.arg('secret')
FROM proj
ON CONFLICT (key, project_id) DO UPDATE
    SET value = EXCLUDED.value, secret = EXCLUDED.secret, user_id = EXCLUDED.user_id, updated_at = NOW()
RETURNING *;

-- name: ListEnviromentsForOwner :many
SELECT e.*
FROM enviroment e
JOIN projects p ON p.id = e.project_id
WHERE e.project_id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
ORDER BY e.key;

-- name: GetEnviromentForOwner :one
SELECT e.*
FROM enviroment e
JOIN projects p ON p.id = e.project_id
WHERE e.key = sqlc.arg('key') AND e.project_id = sqlc.arg('project_id')
  AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: DeleteEnviromentForOwner :execrows
DELETE FROM enviroment e
USING projects p
WHERE e.project_id = p.id AND e.key = sqlc.arg('key') AND e.project_id = sqlc.arg('project_id')
  AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- Internal (not owner-checked): reads every KEY=VALUE pair for a project to
-- build the engine deploy env. Callers must have already verified ownership
-- (e.g. inside a release/debug deploy that just loaded the project row).
-- name: ListEnviromentsByProject :many
SELECT * FROM enviroment WHERE project_id = sqlc.arg('project_id') ORDER BY key;

-- Owner-scoped read of the project's current draft flow — used to deploy a
-- debug container (which, unlike a release, always runs the live draft).
-- name: GetProjectFlowForOwner :one
SELECT flow FROM projects WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL;

-- Owner-scoped read of the project's stable webhook UUIDs — the engine
-- orchestrator names the "bot-<uuid>" Service after these so Telegram
-- webhook routing survives redeploys (see internal/shared/engine/kubernetes.go).
-- name: GetProjectWebhookUUIDsForOwner :one
SELECT release_webhook_uuid, debug_webhook_uuid FROM projects
WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL;
