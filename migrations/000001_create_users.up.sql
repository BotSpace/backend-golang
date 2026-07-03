CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Users. Phone is the primary login identifier (mirrors the Django
-- USERNAME_FIELD="phone"). A user is "pending" until validated_at is set by
-- OTP confirmation. balance is in tiyin/so'm minor units; template_balance is
-- the marketplace author payout wallet.
CREATE TABLE IF NOT EXISTS users (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone            TEXT UNIQUE,
    first_name       TEXT NOT NULL DEFAULT '',
    last_name        TEXT NOT NULL DEFAULT '',
    username         TEXT,
    email            TEXT,
    password_hash    TEXT NOT NULL DEFAULT '',
    role             TEXT NOT NULL DEFAULT 'user',
    balance          BIGINT NOT NULL DEFAULT 0,
    template_balance NUMERIC(20, 2) NOT NULL DEFAULT 0,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    is_staff         BOOLEAN NOT NULL DEFAULT FALSE,
    is_superuser     BOOLEAN NOT NULL DEFAULT FALSE,
    validated_at     TIMESTAMPTZ,
    kyc_verified_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
