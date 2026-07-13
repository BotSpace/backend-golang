-- MCP OAuth authorization server (migrated from the legacy Django `web`
-- service, whose SimpleJWT auth 500'd on the Go-issued UUID user_id). Public
-- clients only (PKCE, token_endpoint_auth_method=none) — no client secret.

CREATE TABLE IF NOT EXISTS oauth_client (
    id                         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id                  text NOT NULL UNIQUE,
    client_name                text NOT NULL DEFAULT '',
    redirect_uris              text[] NOT NULL DEFAULT '{}',
    grant_types                text[] NOT NULL DEFAULT '{}',
    response_types             text[] NOT NULL DEFAULT '{}',
    token_endpoint_auth_method text NOT NULL DEFAULT 'none',
    created_at                 timestamptz NOT NULL DEFAULT now()
);

-- Short-lived, single-use authorization codes bound to the approving user and
-- the PKCE challenge. Deleted on redemption; the expiry index backs cleanup.
CREATE TABLE IF NOT EXISTS oauth_authorization_code (
    code                  text PRIMARY KEY,
    client_id             text NOT NULL,
    user_id               uuid NOT NULL,
    redirect_uri          text NOT NULL,
    code_challenge        text NOT NULL DEFAULT '',
    code_challenge_method text NOT NULL DEFAULT 'S256',
    scope                 text NOT NULL DEFAULT '',
    resource              text NOT NULL DEFAULT '',
    expires_at            timestamptz NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_oauth_code_expires_at ON oauth_authorization_code (expires_at);
