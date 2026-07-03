-- Module catalog (Django ModuleModel) — CI/self-hosted flow-engine plugins.
-- Modules are SHARED: one pod/endpoint per slug serves every project that
-- enables it (see project_module below). manifest_cache holds the last
-- `describe()`/`docs()` RPC result (nodes, credentialTypes, docs markdown,
-- and the parsed module.yaml `_source` config) so the constructor never has
-- to call the module pod directly.
CREATE TABLE IF NOT EXISTS module (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                TEXT NOT NULL,
    name                TEXT NOT NULL,
    description         TEXT,
    icon                TEXT,
    github_url          TEXT,
    image               TEXT NOT NULL DEFAULT '',
    port                INTEGER NOT NULL DEFAULT 8100,
    auth_token          TEXT,
    manifest_cache      JSONB,
    manifest_fetched_at TIMESTAMPTZ,
    active              BOOLEAN NOT NULL DEFAULT TRUE,
    is_default          BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_module_slug ON module (slug);
CREATE INDEX IF NOT EXISTS idx_module_active ON module (active);
CREATE INDEX IF NOT EXISTS idx_module_is_default ON module (is_default);

-- Per-project module activation (Django ProjectModuleModel). enabled=false
-- rows are kept (not deleted) so history/config survives a disable/re-enable
-- cycle. service_id/endpoint/status are filled in by the deploy usecase once
-- the orchestrator confirms the shared pod is up.
CREATE TABLE IF NOT EXISTS project_module (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    module_id  UUID NOT NULL REFERENCES module (id) ON DELETE CASCADE,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    config     JSONB NOT NULL DEFAULT '{}'::jsonb,
    service_id TEXT,
    endpoint   TEXT,
    status     TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_project_module ON project_module (project_id, module_id);
CREATE INDEX IF NOT EXISTS idx_project_module_project ON project_module (project_id) WHERE enabled = TRUE;
CREATE INDEX IF NOT EXISTS idx_project_module_module ON project_module (module_id) WHERE enabled = TRUE;
