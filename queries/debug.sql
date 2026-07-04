-- Debug flow-error events: written by the Redis Stream consumer
-- (internal/shared/redisq + cmd/api's wiring of the "debug.error_event"
-- task), read by the owner-scoped GET /api/v1/debug/errors/ endpoint.

-- name: DebugResolveProjectByLegacyID :one
-- Mirrors Django's cms/views/_common.py resolve_project()'s integer-pk
-- branch: the engine's raw project ref is the Django legacy int pk.
SELECT id FROM projects WHERE legacy_id = sqlc.arg('legacy_id') AND deleted_at IS NULL;

-- name: DebugResolveProjectByUUID :one
-- The engine's PROJECT_ID env is THIS backend's projects.id (a uuid5 hash of
-- the legacy pk — orchestrator sets env["PROJECT_ID"]=spec.ProjectID), while
-- older/Django-origin events pass projects.uuid (the ETL-copied Django field).
-- Match EITHER column so both resolve — node/context events were failing with
-- project.not_found because only `uuid` was checked and the engine sends `id`.
SELECT id FROM projects WHERE (id = sqlc.arg('ref') OR uuid = sqlc.arg('ref')) AND deleted_at IS NULL LIMIT 1;

-- name: DebugInsertErrorEvent :one
INSERT INTO debug_error_event (id, project_id, chat_id, node_id, node_type, level, message, detail, event_ts)
VALUES (sqlc.arg('id'), sqlc.arg('project_id'), sqlc.arg('chat_id'), sqlc.arg('node_id'), sqlc.arg('node_type'),
        sqlc.arg('level'), sqlc.arg('message'), sqlc.arg('detail'), sqlc.narg('event_ts'))
RETURNING *;

-- name: DebugListErrorEventsForOwner :many
-- Owner-scoped (rule #4): a caller can only read errors for a project they
-- own, joined the same way every other module in this codebase does.
SELECT e.* FROM debug_error_event e
JOIN projects p ON p.id = e.project_id
WHERE e.project_id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
ORDER BY e.created_at DESC
LIMIT sqlc.arg('limit_count');

-- Debug per-node execution status events ("debug.node_event" redis stream
-- task, migration 000036) — write-only for now (no read API yet; add one
-- analogous to DebugListErrorEventsForOwner if a node-history view is
-- needed later).

-- name: DebugInsertNodeEvent :one
INSERT INTO debug_node_event (id, project_id, chat_id, node_id, node_type, status, error, run_id, seq, event_ts)
VALUES (sqlc.arg('id'), sqlc.arg('project_id'), sqlc.arg('chat_id'), sqlc.arg('node_id'), sqlc.arg('node_type'),
        sqlc.arg('status'), sqlc.arg('error'), sqlc.arg('run_id'), sqlc.arg('seq'), sqlc.narg('event_ts'))
RETURNING *;

-- name: DebugGetProjectOwner :one
-- Owner-scoping for the Redis-only context cache (debug.context_event has
-- no Postgres table to join owner_id against, unlike debug_error_event —
-- see ListContext in usecase.go).
SELECT owner_id FROM projects WHERE id = sqlc.arg('project_id') AND deleted_at IS NULL;
