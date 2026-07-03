-- webhook_endpoint: a unique, project-scoped webhook URL bound to a single
-- flow node (optionally to one chat). Minted once per (project, node_id,
-- chat_id) by the owner; the Go webhook-hook service resolves token -> the
-- project's release/debug uuid + node_id and forwards the call to the
-- running engine. chat_id = 0 means "no chat binding".
CREATE TABLE IF NOT EXISTS webhook_endpoint (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token      UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    node_id    TEXT NOT NULL,
    chat_id    BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, node_id, chat_id)
);

CREATE INDEX IF NOT EXISTS idx_webhook_endpoint_project ON webhook_endpoint (project_id);

-- managed_bot: a Telegram bot created through the manager-bot wizard and held
-- in trust for an owner until a project attaches it as its release bot token
-- (see used). owner_id is nullable: the wizard's webhook may arrive before
-- ownership is resolved (see the claim flow in the webhook module).
CREATE TABLE IF NOT EXISTS managed_bot (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        UUID REFERENCES users (id) ON DELETE CASCADE,
    tg_bot_id       BIGINT NOT NULL UNIQUE,
    username        TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL DEFAULT '',
    token_encrypted TEXT NOT NULL,
    used            BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_managed_bot_owner ON managed_bot (owner_id);
