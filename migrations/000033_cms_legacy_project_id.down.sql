DROP INDEX IF EXISTS uq_projects_legacy_id;
ALTER TABLE projects DROP COLUMN IF EXISTS legacy_id;
