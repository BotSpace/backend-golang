-- Admin audit log — append-only record of destructive admin actions.
-- Intentionally denormalised (no FK to the affected object, just a free-form
-- "target" string) so deleting the target later doesn't drop its history.
-- actor_id is nullable + ON DELETE SET NULL so deleting the admin user does
-- not remove the trail of what they did.
CREATE TABLE IF NOT EXISTS audit_log (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id   UUID REFERENCES users (id) ON DELETE SET NULL,
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    metadata   JSONB,
    ip         TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_actor ON audit_log (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log (action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log (created_at DESC);
