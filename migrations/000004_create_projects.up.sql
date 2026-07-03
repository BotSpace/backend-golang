-- Bot projects owned by a user. flow holds the constructor graph (JSON).
-- release/debug webhook UUIDs are the public deploy hooks the engine calls.
-- mongo_* are the per-project MongoDB credentials, provisioned lazily.
-- deleted_at implements soft delete (Django SoftDeleteModel).
CREATE TABLE IF NOT EXISTS projects (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    uuid                 UUID NOT NULL DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL,
    description          TEXT,
    username             TEXT,
    avatar_file_id       TEXT,
    owner_id             UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    flow                 JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_template          BOOLEAN NOT NULL DEFAULT FALSE,
    release_webhook_uuid UUID NOT NULL DEFAULT gen_random_uuid(),
    debug_webhook_uuid   UUID NOT NULL DEFAULT gen_random_uuid(),
    mongo_username       TEXT,
    mongo_password       TEXT,
    mongo_database       TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_projects_owner ON projects (owner_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_uuid ON projects (uuid);
