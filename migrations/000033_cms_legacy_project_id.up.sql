-- CMS Mongo alignment (ponytail — reuse Django's prod data as-is, zero doc
-- migration): real Mongo documents in the shared `botspace_cms` database were
-- tagged with Django's `_project_id`, which is the Django `project` table's
-- integer primary key (e.g. 1408) — NOT this table's `id`/`uuid` columns.
-- The ETL (scripts/etl_django.py) derived `projects.id` as
-- uuid5(NS, "project:<django_pk>"), a one-way hash, so the Django pk cannot
-- be recovered from the row itself. legacy_id stores that recovered pk
-- (backfilled once via scripts/backfill_legacy_project_id, which brute-forces
-- the same uuid5 over a pk range and matches it back to projects.id) so the
-- CMS module can stamp/filter Mongo's `_project_id` with the value Django's
-- existing documents actually carry. NULL means either not-yet-backfilled or
-- a project created after the Go cutover with no Django counterpart — such
-- projects use their own `id` (UUID string) as the Mongo tenant key instead
-- (see internal/modules/cms/usecase.go resolveMongoTenant).
ALTER TABLE projects ADD COLUMN IF NOT EXISTS legacy_id INTEGER;
CREATE UNIQUE INDEX IF NOT EXISTS uq_projects_legacy_id ON projects (legacy_id) WHERE legacy_id IS NOT NULL;
