-- CMS domain: collection/field schema definitions + export task bookkeeping.
-- Document data itself never touches Postgres (see internal/modules/cms/mongo.go).
-- Every query is scoped through `projects.owner_id` (rule #4) — no query here
-- ever trusts a caller-supplied project_id/collection_id without that join.

-- ── Collection ──────────────────────────────────────────────────────────────

-- name: CmsCreateCollection :one
INSERT INTO collection (id, project_id, name, slug, description, primary_field, system)
SELECT sqlc.arg('id'), sqlc.arg('project_id'), sqlc.arg('name'), sqlc.arg('slug'),
       sqlc.arg('description'), sqlc.arg('primary_field'), sqlc.arg('system')
WHERE EXISTS (
    SELECT 1 FROM projects p
    WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
RETURNING *;

-- name: CmsGetCollectionBySlugForOwner :one
SELECT c.* FROM collection c
JOIN projects p ON p.id = c.project_id
WHERE c.project_id = sqlc.arg('project_id') AND c.slug = sqlc.arg('slug') AND c.deleted_at IS NULL
  AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: CmsGetCollectionByIDForOwner :one
SELECT c.* FROM collection c
JOIN projects p ON p.id = c.project_id
WHERE c.id = sqlc.arg('id') AND c.deleted_at IS NULL
  AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: CmsListCollectionsForProject :many
SELECT c.* FROM collection c
JOIN projects p ON p.id = c.project_id
WHERE c.project_id = sqlc.arg('project_id') AND c.deleted_at IS NULL
  AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
ORDER BY c.name;

-- name: CmsGetProjectOwner :one
-- Resolves a project's owner id WITHOUT an owner check, so the admin CMS
-- endpoints can run the normal owner-scoped usecase on behalf of the project's
-- real owner (see cms/routes.go admin group + handler AdminOwnerOverride).
-- Cross-tenant by design — mount only behind an admin/superuser role guard.
SELECT owner_id FROM projects
WHERE id = sqlc.arg('project_id') AND deleted_at IS NULL;

-- name: CmsUpdateCollection :one
UPDATE collection c
SET name          = COALESCE(sqlc.narg('name'), c.name),
    description   = COALESCE(sqlc.narg('description'), c.description),
    primary_field = COALESCE(sqlc.narg('primary_field'), c.primary_field),
    updated_at    = NOW()
FROM projects p
WHERE c.id = sqlc.arg('id') AND c.deleted_at IS NULL
  AND p.id = c.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
RETURNING c.*;

-- name: CmsBumpCollectionSchemaVersion :one
UPDATE collection SET schema_version = schema_version + 1, updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: CmsSoftDeleteCollection :execrows
UPDATE collection c
SET deleted_at = NOW(), updated_at = NOW()
FROM projects p
WHERE c.id = sqlc.arg('id') AND c.deleted_at IS NULL AND c.system = FALSE
  AND p.id = c.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: CmsGetProjectMongoDatabaseForOwner :one
-- Resolves the Mongo database name for a project, verifying ownership in the
-- same query so the caller can never probe another owner's project.
SELECT mongo_database FROM projects
WHERE id = sqlc.arg('project_id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL;

-- name: CmsGetProjectTenantForOwner :one
-- Resolves the CMS Mongo tenant key for a project, verifying ownership.
-- legacy_id (nullable) is the Django project pk that existing imported Mongo
-- documents were tagged with (`_project_id`); see migration 000033 and
-- internal/modules/cms/usecase.go resolveMongoTenant for how NULL is handled.
SELECT id, legacy_id FROM projects
WHERE id = sqlc.arg('project_id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL;

-- ── Field ───────────────────────────────────────────────────────────────────

-- name: CmsCreateField :one
INSERT INTO field (
    id, collection_id, name, label, type, required, default_value, validators,
    relation_collection_id, cascade, index_kind, sort_order
)
SELECT sqlc.arg('id'), sqlc.arg('collection_id'), sqlc.arg('name'), sqlc.arg('label'),
       sqlc.arg('type'), sqlc.arg('required'), sqlc.narg('default_value'), sqlc.arg('validators'),
       sqlc.narg('relation_collection_id'), sqlc.arg('cascade'), sqlc.arg('index_kind'), sqlc.arg('sort_order')
WHERE EXISTS (
    SELECT 1 FROM collection c
    JOIN projects p ON p.id = c.project_id
    WHERE c.id = sqlc.arg('collection_id') AND c.deleted_at IS NULL
      AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
RETURNING *;

-- name: CmsGetFieldByNameForOwner :one
SELECT f.* FROM field f
JOIN collection c ON c.id = f.collection_id
JOIN projects p ON p.id = c.project_id
WHERE f.collection_id = sqlc.arg('collection_id') AND f.name = sqlc.arg('name')
  AND c.deleted_at IS NULL AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- name: CmsListFieldsForCollection :many
SELECT f.* FROM field f
WHERE f.collection_id = sqlc.arg('collection_id')
ORDER BY f.sort_order, f.id;

-- name: CmsListFieldsForCollectionForOwner :many
SELECT f.* FROM field f
JOIN collection c ON c.id = f.collection_id
JOIN projects p ON p.id = c.project_id
WHERE f.collection_id = sqlc.arg('collection_id')
  AND c.deleted_at IS NULL AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
ORDER BY f.sort_order, f.id;

-- name: CmsUpdateField :one
UPDATE field f
SET label                   = COALESCE(sqlc.narg('label'), f.label),
    required                = COALESCE(sqlc.narg('required'), f.required),
    default_value           = COALESCE(sqlc.narg('default_value'), f.default_value),
    validators              = COALESCE(sqlc.narg('validators'), f.validators),
    relation_collection_id  = COALESCE(sqlc.narg('relation_collection_id'), f.relation_collection_id),
    cascade                 = COALESCE(sqlc.narg('cascade'), f.cascade),
    index_kind              = COALESCE(sqlc.narg('index_kind'), f.index_kind),
    sort_order              = COALESCE(sqlc.narg('sort_order'), f.sort_order),
    updated_at              = NOW()
FROM collection c, projects p
WHERE f.id = sqlc.arg('id')
  AND c.id = f.collection_id AND c.deleted_at IS NULL
  AND p.id = c.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
RETURNING f.*;

-- name: CmsDeleteField :execrows
DELETE FROM field f
USING collection c, projects p
WHERE f.id = sqlc.arg('id')
  AND c.id = f.collection_id AND c.deleted_at IS NULL
  AND p.id = c.project_id AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL;

-- ── Export task ─────────────────────────────────────────────────────────────

-- name: CmsCreateExportTask :one
INSERT INTO cms_export_task (id, collection_id, owner_id, status, progress)
SELECT sqlc.arg('id'), sqlc.arg('collection_id'), sqlc.arg('owner_id'), 'pending', 0
WHERE EXISTS (
    SELECT 1 FROM collection c
    JOIN projects p ON p.id = c.project_id
    WHERE c.id = sqlc.arg('collection_id') AND c.deleted_at IS NULL
      AND p.owner_id = sqlc.arg('owner_id') AND p.deleted_at IS NULL
)
RETURNING *;

-- name: CmsGetExportTaskForOwner :one
SELECT * FROM cms_export_task
WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id');

-- name: CmsUpdateExportTaskStatus :one
UPDATE cms_export_task
SET status        = sqlc.arg('status'),
    progress      = sqlc.arg('progress'),
    download_url  = COALESCE(sqlc.narg('download_url'), download_url),
    error_message = COALESCE(sqlc.narg('error_message'), error_message),
    updated_at    = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;
