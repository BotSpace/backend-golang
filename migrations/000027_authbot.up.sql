-- Telegram auth-bot domain (Django app: authbot). A Telegram identity shares
-- its phone number with the bot; the bot issues a one-time login code; the
-- frontend exchanges that code for app JWTs via POST /authbot/login.
--
-- authbot_bot_users mirrors authbot.BotUser: a Telegram identity is
-- (telegram_id, phone) — the same Telegram account can, in principle,
-- confirm more than one phone over time, but re-sharing the same phone
-- refreshes the existing row instead of duplicating it.
CREATE TABLE IF NOT EXISTS authbot_bot_users (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    telegram_id  BIGINT NOT NULL,
    phone        TEXT NOT NULL,
    first_name   TEXT,
    last_name    TEXT,
    username     TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (telegram_id, phone)
);

-- authbot_codes mirrors authbot.Code: one active login code per bot user
-- (Django's OneToOneField). Unlike Django, expires_at is enforced (the
-- Django bot message promised "amal qilish muddati: 10 daqiqa" but the
-- model/serializer never actually checked it). message_id records which
-- Telegram message shows the code so the bot can edit/delete it once used.
CREATE TABLE IF NOT EXISTS authbot_codes (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code         TEXT NOT NULL UNIQUE,
    message_id   BIGINT,
    bot_user_id  UUID NOT NULL UNIQUE REFERENCES authbot_bot_users (id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_authbot_codes_bot_user ON authbot_codes (bot_user_id);
