-- Personal access tokens ("bsp_" prefixed, sha256-hashed at rest) — mirrors
-- Django's accounts_user.models.api_token.PersonalAccessToken. Used by MCP
-- clients (Claude Code etc.) to exchange a long-lived PAT for a short-lived
-- JWT access+refresh pair via POST /auth/token/exchange/. NOT populated by
-- the ETL (core/apps/accounts/models/api_token.py rows were never migrated —
-- see go/scripts/etl_django.py) — existing Django-issued "bsp_" tokens do
-- NOT verify here; users must issue new ones via POST /auth/api-tokens/.
CREATE TABLE IF NOT EXISTS personal_access_token (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    token_hash    TEXT NOT NULL UNIQUE,
    prefix        TEXT NOT NULL,
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_personal_access_token_user ON personal_access_token (user_id);
