CREATE TABLE template_flow_revision (
    id UUID PRIMARY KEY,
    template_id UUID NOT NULL REFERENCES template(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    actor_id UUID,
    previous_flow JSONB NOT NULL,
    new_flow JSONB NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX template_flow_revision_template_created_idx
    ON template_flow_revision (template_id, created_at DESC);
