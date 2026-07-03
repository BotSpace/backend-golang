-- debug_node_event: persisted per-node execution status events emitted by
-- the engine during a debug (or release) run — the "debug.node_event" Redis
-- Stream task (docs/redis-stream-events.md). Mirrors debug_error_event
-- (migration 000034) byte-for-byte; Django never persisted these either
-- (WebSocket-only, core/apps/api/views/debug.py DebugNodeEventView), so
-- there's no legacy table to port. run_id/seq let a reader reconstruct node
-- execution order within one flow run without relying on created_at
-- ordering (redelivery can insert rows out of wall-clock order).
CREATE TABLE IF NOT EXISTS debug_node_event (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    chat_id    BIGINT NOT NULL DEFAULT 0,
    node_id    TEXT NOT NULL DEFAULT '',
    node_type  TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT '',
    error      TEXT NOT NULL DEFAULT '',
    run_id     BIGINT NOT NULL DEFAULT 0,
    seq        BIGINT NOT NULL DEFAULT 0,
    event_ts   TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_debug_node_event_project_created
    ON debug_node_event (project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_debug_node_event_run
    ON debug_node_event (project_id, run_id, seq);
