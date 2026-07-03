-- Project runtime: relase (deployed versions), debug (one-to-one live debug
-- container) and enviroment (project env vars). Mirrors Django RelaseModel /
-- DebugModel / EnviromentModel. status mirrors engine.Status (Django
-- ContainerStatusEnum): pending, initializing, running, stopped, stopping,
-- failed, suspended.

CREATE TABLE IF NOT EXISTS relase (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    version    INTEGER NOT NULL,
    flow       JSONB NOT NULL DEFAULT '[]'::jsonb,
    service_id TEXT,
    status     TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'initializing', 'running', 'stopped', 'stopping', 'failed', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_relase_project ON relase (project_id);
CREATE INDEX IF NOT EXISTS idx_relase_project_status ON relase (project_id, status);

-- One live debug container per project (Django OneToOneField).
CREATE TABLE IF NOT EXISTS debug (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL UNIQUE REFERENCES projects (id) ON DELETE CASCADE,
    flow_hash  TEXT,
    service_id TEXT,
    status     TEXT NOT NULL DEFAULT 'stopped'
        CHECK (status IN ('pending', 'initializing', 'running', 'stopped', 'stopping', 'failed', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS enviroment (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    secret     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_enviroment_key_project UNIQUE (key, project_id)
);

CREATE INDEX IF NOT EXISTS idx_enviroment_project ON enviroment (project_id);
