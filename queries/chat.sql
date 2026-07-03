-- Chat CRM read endpoints (GET /api/v1/chat/users/{project_id}/, GET
-- /api/v1/chat/messages/{project_id}/{chat_id}/) port core/apps/api/views/chat.py.
-- Documents themselves live in the shared Mongo botspace_cms database (see
-- internal/modules/chat/mongo.go); these two queries only resolve+verify the
-- owner-scoped project ref (int legacy pk or projects.uuid — mirrors
-- cms/views/_common.py resolve_project()) into the Mongo tenant key,
-- duplicated from internal/modules/cms/repository.go's ProjectMongoTenant
-- per the porting guide's no-cross-module-import rule (scope in SQL, don't
-- import a sibling module).

-- name: ChatResolveProjectByLegacyIDForOwner :one
SELECT id, legacy_id FROM projects
WHERE legacy_id = sqlc.arg('legacy_id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL;

-- name: ChatResolveProjectByUUIDForOwner :one
SELECT id, legacy_id FROM projects
WHERE uuid = sqlc.arg('uuid') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL;
