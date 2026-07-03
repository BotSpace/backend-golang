-- CMS domain (Cmsv2): schema definitions live here in Postgres; the actual
-- documents live in MongoDB, one database per project (projects.mongo_database),
-- one Mongo collection per `collection.slug`. See internal/modules/cms/mongo.go.
--
-- collection: one schema (like a "table") inside a project. `slug` is the
-- Mongo collection name (lowercase snake_case, immutable); `name` is the
-- human label shown in the UI. `system` collections (e.g. users/chat) cannot
-- be deleted. `schema_version` bumps on every field add/remove/update so
-- clients can detect a stale cached schema.
CREATE TABLE IF NOT EXISTS collection (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id     UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    slug           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    primary_field  TEXT NOT NULL DEFAULT '',
    system         BOOLEAN NOT NULL DEFAULT FALSE,
    schema_version INTEGER NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at     TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_collection_project_slug
    ON collection (project_id, slug) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_collection_project ON collection (project_id) WHERE deleted_at IS NULL;

-- field: one column of a collection's schema. `type` mirrors Django's
-- FieldKind. RELATION fields point at another collection via
-- relation_collection_id (ON DELETE SET NULL — a dangling relation just
-- degrades to a plain id, it never blocks the referenced collection's delete).
-- `default_value`/`validators` are JSON blobs interpreted by the usecase
-- validator (regex/min/max/choices/array_kind/...), matching Django's
-- services/validator.py so no separate rules table is needed (rule #6).
CREATE TABLE IF NOT EXISTS field (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collection_id           UUID NOT NULL REFERENCES collection (id) ON DELETE CASCADE,
    name                    TEXT NOT NULL,
    label                   TEXT NOT NULL DEFAULT '',
    type                    TEXT NOT NULL DEFAULT 'text' CHECK (type IN (
        'text', 'longtext', 'int', 'float', 'bool', 'datetime', 'date',
        'email', 'url', 'phone', 'enum', 'json', 'array', 'relation',
        'file', 'image', 'audio', 'video', 'geo'
    )),
    required                BOOLEAN NOT NULL DEFAULT FALSE,
    default_value           JSONB,
    validators              JSONB NOT NULL DEFAULT '{}'::jsonb,
    relation_collection_id  UUID REFERENCES collection (id) ON DELETE SET NULL,
    cascade                 TEXT NOT NULL DEFAULT 'restrict' CHECK (cascade IN ('cascade', 'restrict', 'set_null')),
    index_kind              TEXT NOT NULL DEFAULT 'none' CHECK (index_kind IN (
        'none', 'asc', 'desc', 'text', 'hashed', 'unique', 'ttl'
    )),
    sort_order              INTEGER NOT NULL DEFAULT 0,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_field_collection_name ON field (collection_id, name);
CREATE INDEX IF NOT EXISTS idx_field_collection ON field (collection_id);
CREATE INDEX IF NOT EXISTS idx_field_relation_collection ON field (relation_collection_id) WHERE relation_collection_id IS NOT NULL;

-- cms_export_task: async Excel export of a collection's documents. The worker
-- that walks Mongo and renders the .xlsx is out of scope here — this table
-- only tracks the job's lifecycle (progress/status/download_url).
CREATE TABLE IF NOT EXISTS cms_export_task (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collection_id UUID NOT NULL REFERENCES collection (id) ON DELETE CASCADE,
    owner_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'done', 'failed')),
    progress      SMALLINT NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    download_url  TEXT,
    error_message TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cms_export_task_collection ON cms_export_task (collection_id);
CREATE INDEX IF NOT EXISTS idx_cms_export_task_owner ON cms_export_task (owner_id);
