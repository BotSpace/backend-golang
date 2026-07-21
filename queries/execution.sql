-- name: UpsertExecutionStarted :one
INSERT INTO execution (
    id, project_id, chat_id, telegram_update_id, run_id, trigger_type, runtime_mode,
    flow_hash, status, started_at
)
VALUES (
    sqlc.arg('id'), sqlc.arg('project_id'), sqlc.arg('chat_id'),
    sqlc.narg('telegram_update_id'), sqlc.arg('run_id'), sqlc.arg('trigger_type'), sqlc.arg('runtime_mode'),
    sqlc.arg('flow_hash'), 'running', sqlc.arg('event_ts')
)
ON CONFLICT (id) DO UPDATE SET
    telegram_update_id = COALESCE(EXCLUDED.telegram_update_id, execution.telegram_update_id),
    trigger_type = EXCLUDED.trigger_type,
    runtime_mode = EXCLUDED.runtime_mode,
    flow_hash = CASE WHEN EXCLUDED.flow_hash = '' THEN execution.flow_hash ELSE EXCLUDED.flow_hash END,
    updated_at = NOW()
RETURNING *;

-- name: FinishExecution :one
UPDATE execution
SET status = CASE
        -- A late completion event must never hide a node failure already stored.
        WHEN execution.status = 'failed' AND sqlc.arg('status')::text <> 'failed' THEN execution.status
        ELSE sqlc.arg('status')::text
    END,
    error = CASE WHEN sqlc.arg('error')::text = '' THEN execution.error ELSE sqlc.arg('error')::text END,
    finished_at = CASE WHEN sqlc.arg('status')::text = 'waiting' THEN NULL ELSE sqlc.arg('event_ts') END,
    updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: UpsertExecutionNodeRun :one
INSERT INTO execution_node_run (
    execution_id, node_id, node_type, attempt, status, error, sequence,
    started_at, finished_at
)
VALUES (
    sqlc.arg('execution_id'), sqlc.arg('node_id'), sqlc.arg('node_type'),
    sqlc.arg('attempt'), sqlc.arg('status'), sqlc.arg('error'), sqlc.arg('sequence'),
    sqlc.arg('event_ts'),
    CASE WHEN sqlc.arg('status') IN ('succeeded', 'failed', 'cancelled') THEN sqlc.arg('event_ts') ELSE NULL END
)
ON CONFLICT (execution_id, node_id, attempt) DO UPDATE SET
    node_type = EXCLUDED.node_type,
    status = CASE
        WHEN EXCLUDED.sequence >= execution_node_run.sequence THEN EXCLUDED.status
        ELSE execution_node_run.status
    END,
    error = CASE
        WHEN EXCLUDED.sequence < execution_node_run.sequence OR EXCLUDED.error = '' THEN execution_node_run.error
        ELSE EXCLUDED.error
    END,
    sequence = GREATEST(execution_node_run.sequence, EXCLUDED.sequence),
    finished_at = CASE
        WHEN EXCLUDED.sequence >= execution_node_run.sequence
          AND EXCLUDED.status IN ('succeeded', 'failed', 'cancelled') THEN EXCLUDED.finished_at
        ELSE execution_node_run.finished_at
    END,
    updated_at = NOW()
RETURNING *;

-- name: MarkExecutionNode :exec
UPDATE execution
SET current_node_id = sqlc.arg('node_id'),
    status = CASE WHEN sqlc.arg('node_status')::text = 'failed' THEN 'failed' ELSE status END,
    error = CASE WHEN sqlc.arg('error')::text = '' THEN error ELSE sqlc.arg('error')::text END,
    updated_at = NOW()
WHERE id = sqlc.arg('execution_id');

-- name: ListExecutionsForOwner :many
SELECT e.*
FROM execution e
JOIN projects p ON p.id = e.project_id
WHERE e.project_id = sqlc.arg('project_id')
  AND p.owner_id = sqlc.arg('owner_id')
  AND p.deleted_at IS NULL
  AND (sqlc.arg('status_filter')::text = '' OR e.status = sqlc.arg('status_filter')::text)
  AND (sqlc.arg('runtime_mode_filter')::text = '' OR e.runtime_mode = sqlc.arg('runtime_mode_filter')::text)
ORDER BY e.started_at DESC
LIMIT sqlc.arg('limit_count') OFFSET sqlc.arg('offset_count');

-- name: GetExecutionForOwner :one
SELECT e.*
FROM execution e
JOIN projects p ON p.id = e.project_id
WHERE e.id = sqlc.arg('id')
  AND p.owner_id = sqlc.arg('owner_id')
  AND p.deleted_at IS NULL;

-- name: ListExecutionNodeRunsForOwner :many
SELECT nr.*
FROM execution_node_run nr
JOIN execution e ON e.id = nr.execution_id
JOIN projects p ON p.id = e.project_id
WHERE nr.execution_id = sqlc.arg('execution_id')
  AND p.owner_id = sqlc.arg('owner_id')
  AND p.deleted_at IS NULL
ORDER BY nr.sequence, nr.started_at, nr.node_id, nr.attempt;
