-- debug_error_event: persisted flow/node errors emitted by the engine during
-- a debug (or release) run. Django never persisted these — it only
-- WebSocket-published them live (core/apps/api/views/debug.py
-- DebugNodeEventView/DebugContextEventView) — so there is no Django table to
-- port here; this is new, needed for GET /api/v1/debug/errors/ (agentv3 +
-- the constructor's debug error panel) to have something to read after the
-- engine restarts or nobody was connected to the WS at the time.
--
-- project_id is THIS backend's internal projects.id (uuid) — the redis
-- stream consumer resolves the engine's raw project ref (legacy Django int
-- OR Django `project.uuid`, see internal/modules/debug/usecase.go
-- ResolveProjectRef, mirroring Django's cms/views/_common.py resolve_project)
-- to this id before inserting, so every other query in this codebase can
-- keep joining through projects.id/owner_id as usual.
CREATE TABLE IF NOT EXISTS debug_error_event (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    chat_id    BIGINT NOT NULL DEFAULT 0,
    node_id    TEXT NOT NULL DEFAULT '',
    node_type  TEXT NOT NULL DEFAULT '',
    level      TEXT NOT NULL DEFAULT 'error',
    message    TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    event_ts   TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_debug_error_event_project_created
    ON debug_error_event (project_id, created_at DESC);
