UPDATE projects
SET flow = '{"nodes":[],"edges":[]}'::jsonb
WHERE flow = '[]'::jsonb;

UPDATE relase
SET flow = '{"nodes":[],"edges":[]}'::jsonb
WHERE flow = '[]'::jsonb;

ALTER TABLE projects
    ALTER COLUMN flow SET DEFAULT '{"nodes":[],"edges":[]}'::jsonb,
    ADD CONSTRAINT projects_flow_object CHECK (jsonb_typeof(flow) = 'object');

ALTER TABLE relase
    ALTER COLUMN flow SET DEFAULT '{"nodes":[],"edges":[]}'::jsonb,
    ADD CONSTRAINT relase_flow_object CHECK (jsonb_typeof(flow) = 'object');
