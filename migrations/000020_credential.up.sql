-- Owner-scoped encrypted credentials (API keys, bot tokens, OAuth tokens...).
-- Usable across all of an owner's projects — no project_id here on purpose.
-- type_key is validated against the code-defined registry (internal/modules/credential/registry.go),
-- not a DB table. data_encrypted holds AES-256-GCM(base64(nonce||ciphertext||tag)) of a JSON field map.
-- deleted_at implements soft delete (Django SoftDeleteModel).
CREATE TABLE IF NOT EXISTS credential (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type_key       TEXT NOT NULL,
    name           TEXT NOT NULL,
    data_encrypted TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at     TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_credential_owner_name
    ON credential (owner_id, name) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_credential_owner ON credential (owner_id) WHERE deleted_at IS NULL;
