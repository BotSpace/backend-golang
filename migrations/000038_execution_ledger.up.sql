-- First-class workflow execution ledger. One row represents one inbound
-- Telegram update (or another trigger) evaluated against one immutable runtime
-- flow. Node attempts are upserted from engine lifecycle events, so Redis
-- Stream redelivery is idempotent.
CREATE TABLE IF NOT EXISTS execution (
    id                 UUID PRIMARY KEY,
    project_id         UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    release_id         UUID REFERENCES relase (id) ON DELETE SET NULL,
    chat_id            BIGINT NOT NULL DEFAULT 0,
    telegram_update_id BIGINT,
    run_id             BIGINT NOT NULL DEFAULT 0,
    trigger_type       TEXT NOT NULL DEFAULT 'telegram',
    runtime_mode       TEXT NOT NULL DEFAULT 'release'
        CHECK (runtime_mode IN ('debug', 'release', 'shared')),
    flow_hash          TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'waiting', 'succeeded', 'failed', 'cancelled')),
    current_node_id    TEXT NOT NULL DEFAULT '',
    error              TEXT NOT NULL DEFAULT '',
    started_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_execution_project_started
    ON execution (project_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_execution_project_chat_started
    ON execution (project_id, chat_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_execution_status_started
    ON execution (status, started_at)
    WHERE status IN ('running', 'waiting');
CREATE TABLE IF NOT EXISTS execution_node_run (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id  UUID NOT NULL REFERENCES execution (id) ON DELETE CASCADE,
    node_id       TEXT NOT NULL,
    node_type     TEXT NOT NULL DEFAULT '',
    attempt       INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    status        TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('waiting', 'running', 'succeeded', 'failed', 'cancelled')),
    input_data    JSONB,
    output_data   JSONB,
    error         TEXT NOT NULL DEFAULT '',
    sequence      BIGINT NOT NULL DEFAULT 0,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT execution_node_run_attempt_unique UNIQUE (execution_id, node_id, attempt)
);

CREATE INDEX IF NOT EXISTS idx_execution_node_run_execution_sequence
    ON execution_node_run (execution_id, sequence, started_at);
