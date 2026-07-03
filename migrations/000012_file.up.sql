-- Uploaded files (FileModel) and resource-usage tracking (ResourceModel,
-- StorageResourceModel, ProjectResourceSnapshot, DebugRunLog). All rows are
-- project-scoped; ownership is enforced transitively through
-- projects.owner_id (see queries/file.sql), mirroring the message module.

-- file: metadata for one uploaded blob. The blob itself lives on local disk
-- under APP_MEDIA_DIR/file_path; uuid is the public lookup key (Django's
-- lookup_field="uuid", AllowAny retrieve), id is the internal PK.
CREATE TABLE IF NOT EXISTS file (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    uuid          UUID NOT NULL DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    file_path     TEXT NOT NULL,
    original_name TEXT NOT NULL DEFAULT '',
    filesize      BIGINT NOT NULL DEFAULT 0,
    filetype      TEXT NOT NULL DEFAULT 'file' CHECK (filetype IN ('file', 'photo', 'video', 'audio')),
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_file_uuid ON file (uuid);
CREATE INDEX IF NOT EXISTS idx_file_project ON file (project_id);
CREATE INDEX IF NOT EXISTS idx_file_user ON file (user_id);
CREATE INDEX IF NOT EXISTS idx_file_expires_at ON file (expires_at) WHERE expires_at IS NOT NULL;

-- resource: periodic host metrics reported for a project's running bot
-- (RAM/CPU/network). hostname disambiguates multiple pods/replicas so the
-- "latest per hostname" read pattern (see queries/file.sql) sums live
-- instances instead of double-counting stale history.
CREATE TABLE IF NOT EXISTS resource (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    hostname   TEXT NOT NULL,
    ram        INTEGER NOT NULL,
    cpu        INTEGER NOT NULL,
    net_rx     BIGINT NOT NULL,
    net_tx     BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_resource_project_created ON resource (project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_resource_hostname ON resource (hostname, created_at);

-- storageresource: periodic disk-usage snapshot per project, kept separate
-- from file.filesize sums so historical usage survives file deletion.
CREATE TABLE IF NOT EXISTS storageresource (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    storage    BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_storageresource_project_created ON storageresource (project_id, created_at);

-- project_resource_snapshot: hourly disk-breakdown snapshot (files vs CMS
-- documents) written by a periodic job; kept for history/charting. Schema
-- parity with Django's monitoring.ProjectResourceSnapshot — not yet wired to
-- an endpoint in this module.
CREATE TABLE IF NOT EXISTS project_resource_snapshot (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id       UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    disk_bytes       BIGINT NOT NULL DEFAULT 0,
    disk_files_bytes BIGINT NOT NULL DEFAULT 0,
    disk_cms_bytes   BIGINT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_snapshot_project_created ON project_resource_snapshot (project_id, created_at);

-- debug_run_log: one row per debug-bot run (GET /project/{id}/debug/), used
-- for run-trend charts. user_id is nullable (SET NULL) so a purged account
-- doesn't cascade-delete history. Schema parity with Django's
-- monitoring.DebugRunLog — not yet wired to an endpoint in this module.
CREATE TABLE IF NOT EXISTS debug_run_log (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_debug_run_log_project_created ON debug_run_log (project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_debug_run_log_created ON debug_run_log (created_at);
