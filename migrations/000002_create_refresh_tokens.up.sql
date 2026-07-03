-- Issued refresh tokens, keyed by their jti claim, so they can be rotated and
-- revoked (stateless JWTs alone cannot be invalidated early).
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         UUID PRIMARY KEY, -- the token's jti claim
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens (user_id);
