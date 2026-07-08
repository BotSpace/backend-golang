-- name: ListExpiredRunningDebugs :many
SELECT d.id, d.service_id
FROM debug d
JOIN projects p ON p.id = d.project_id
WHERE d.status = 'running'
  AND d.updated_at <= $1
  AND p.is_template = false
  AND p.deleted_at IS NULL;

-- name: MarkDebugStopping :exec
UPDATE debug SET status = 'stopping', updated_at = now() WHERE id = $1;

-- name: ListExpiredFiles :many
SELECT id, file_path FROM file WHERE expires_at IS NOT NULL AND expires_at < now();

-- name: DeleteFile :exec
DELETE FROM file WHERE id = $1;

-- name: MarkReleaseFailedByService :execrows
UPDATE relase SET status = 'failed' WHERE service_id = $1;

-- name: MarkDebugFailedByService :execrows
UPDATE debug SET status = 'failed' WHERE service_id = $1;

-- name: ListLiveEngineServiceIDs :many
-- Orphan-sweep support: every release/debug service_id whose DB row is NOT in a
-- definitively-terminal state, i.e. the deployment SHOULD still exist. The
-- orphan sweep deletes any engine-managed Deployment in the namespace whose
-- name is absent from this set (its owning row was reaped/deleted, so the
-- Deployment + its -env/-flow/bot-<uuid> resources leaked).
SELECT service_id FROM relase
  WHERE service_id IS NOT NULL AND service_id <> ''
    AND status NOT IN ('stopped', 'deleted')
UNION
SELECT service_id FROM debug
  WHERE service_id IS NOT NULL AND service_id <> ''
    AND status NOT IN ('stopped', 'deleted');

-- name: ListProjectsWithReleaseToken :many
SELECT p.id, p.username, p.avatar_file_id, e.value AS bot_token
FROM projects p
JOIN enviroment e ON e.project_id = p.id AND e.key = 'RELEASE_BOT_TOKEN'
WHERE p.deleted_at IS NULL;

-- name: UpdateProjectBotInfo :exec
UPDATE projects SET username = $2, avatar_file_id = $3 WHERE id = $1;

-- name: ListReleasedProjectIDs :many
-- Reliz QILINGAN yoki debug ishlatilgan loyihalar — tokensiz (debug-only)
-- botlarning ham disk charti chiqishi kerak.
SELECT DISTINCT p.id
FROM projects p
LEFT JOIN relase r ON r.project_id = p.id
LEFT JOIN debug d ON d.project_id = p.id
WHERE p.deleted_at IS NULL
  AND (r.id IS NOT NULL OR d.id IS NOT NULL);

-- name: SumProjectFileBytes :one
SELECT COALESCE(SUM(filesize), 0)::bigint FROM file WHERE project_id = $1;

-- name: InsertDiskSnapshot :exec
INSERT INTO project_resource_snapshot (project_id, disk_bytes, disk_files_bytes, disk_cms_bytes, created_at)
VALUES ($1, $2, $3, $4, now());

-- name: GetMessage :one
SELECT m.id, m.project_id, p.legacy_id AS project_legacy_id,
       m.caption, m.message_type, m.file_id, m.button_type, m.buttons,
       m.total, m.done, m.fail, m.status
FROM message m
JOIN projects p ON p.id = m.project_id
WHERE m.id = $1;

-- name: UpdateMessageTotal :exec
UPDATE message SET total = $2 WHERE id = $1;

-- name: UpdateMessageFileID :exec
UPDATE message SET file_id = $2 WHERE id = $1;

-- name: UpdateMessageResult :exec
UPDATE message SET done = $2, fail = $3, status = $4 WHERE id = $1;

-- name: MarkMessageFailed :exec
UPDATE message SET status = 'failed' WHERE id = $1;

-- name: HasUsersCollection :one
SELECT EXISTS (SELECT 1 FROM collection WHERE project_id = $1 AND slug = 'users' AND deleted_at IS NULL);

-- name: GetProjectEnv :one
SELECT value FROM enviroment WHERE project_id = $1 AND key = $2;

-- name: GetFilePath :one
SELECT file_path FROM file WHERE id = $1;

-- name: ListReleasedProjectsForDisk :many
SELECT DISTINCT p.id, p.legacy_id
FROM projects p
LEFT JOIN relase r ON r.project_id = p.id
LEFT JOIN debug d ON d.project_id = p.id
WHERE p.deleted_at IS NULL
  AND (r.id IS NOT NULL OR d.id IS NOT NULL);
