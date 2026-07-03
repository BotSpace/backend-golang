-- Pricing plans + per-user subscriptions (Django: core.apps.api.models.plan).
-- A plan is no longer attached to a project — it is attached to a USER via
-- user_subscription, and it enforces two resource limits: max bots the user
-- may create (max_bots, 0 = unlimited) and the monthly AI token budget
-- (max_tokens). max_output_tokens/max_input_tokens are legacy columns kept
-- only for historical/migration parity (Django marks them deprecated).
CREATE TABLE IF NOT EXISTS plan (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT NOT NULL UNIQUE CHECK (name IN ('free', 'pro', 'max')),
    price             NUMERIC(20, 2) NOT NULL DEFAULT 0,
    description       TEXT,
    is_top            BOOLEAN NOT NULL DEFAULT FALSE,
    is_active         BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    max_bots          INTEGER NOT NULL DEFAULT 1,
    max_tokens        BIGINT NOT NULL DEFAULT 1000000,
    max_output_tokens BIGINT NOT NULL DEFAULT 200000,
    max_input_tokens  BIGINT NOT NULL DEFAULT 800000,
    cached_token_rate NUMERIC(4, 2) NOT NULL DEFAULT 0.10,
    features          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_plan_active_order ON plan (is_active, sort_order);

-- One subscription per user (Django OneToOneField). plan is PROTECT in
-- Django (can't delete a plan that still has subscribers) -> ON DELETE
-- RESTRICT here.
CREATE TABLE IF NOT EXISTS user_subscription (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    plan_id             UUID NOT NULL REFERENCES plan (id) ON DELETE RESTRICT,
    status              TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended', 'grace_period', 'cancelled')),
    started_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ,
    tokens_used_input   BIGINT NOT NULL DEFAULT 0,
    tokens_used_output  BIGINT NOT NULL DEFAULT 0,
    token_reset_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    bots_count          INTEGER NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_subscription_plan ON user_subscription (plan_id);
