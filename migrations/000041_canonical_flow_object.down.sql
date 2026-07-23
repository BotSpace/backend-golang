ALTER TABLE projects
    DROP CONSTRAINT IF EXISTS projects_flow_object,
    ALTER COLUMN flow SET DEFAULT '[]'::jsonb;

ALTER TABLE relase
    DROP CONSTRAINT IF EXISTS relase_flow_object,
    ALTER COLUMN flow SET DEFAULT '[]'::jsonb;
