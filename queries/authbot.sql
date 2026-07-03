-- name: UpsertAuthbotBotUser :one
-- Registers/refreshes the Telegram identity that shared its phone number
-- (Django: BotUser.objects.get_or_create(user_id=..., phone=..., defaults=...)).
-- Positional args (not sqlc.arg) so nullable name columns come back as *string,
-- matching the nullable authbot_bot_users schema (guide rule 3).
INSERT INTO authbot_bot_users (telegram_id, phone, first_name, last_name, username)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (telegram_id, phone) DO UPDATE
SET first_name = EXCLUDED.first_name,
    last_name  = EXCLUDED.last_name,
    username   = EXCLUDED.username,
    updated_at = NOW()
RETURNING *;

-- name: UpsertAuthbotCode :one
-- Mirrors CodeManager.get_or_create_code: one code per bot user. If the
-- existing code already expired it is replaced (new code/expiry, message_id
-- cleared so the caller re-sends); otherwise the still-valid row is returned
-- untouched. The caller retries with a freshly generated code on a unique
-- violation of the `code` column (cross-bot-user collision, same as Django's
-- @retry(stop_after_attempt(10))).
INSERT INTO authbot_codes (bot_user_id, code, expires_at)
VALUES (sqlc.arg('bot_user_id'), sqlc.arg('code'), sqlc.arg('expires_at'))
ON CONFLICT (bot_user_id) DO UPDATE
SET code       = CASE WHEN authbot_codes.expires_at < NOW() THEN EXCLUDED.code ELSE authbot_codes.code END,
    expires_at = CASE WHEN authbot_codes.expires_at < NOW() THEN EXCLUDED.expires_at ELSE authbot_codes.expires_at END,
    message_id = CASE WHEN authbot_codes.expires_at < NOW() THEN NULL ELSE authbot_codes.message_id END,
    updated_at = NOW()
RETURNING *;

-- name: SetAuthbotCodeMessageID :execrows
-- Records which Telegram message displays the code (Django: code.message_id),
-- so the bot can later edit/delete it.
UPDATE authbot_codes SET message_id = sqlc.arg('message_id'), updated_at = NOW() WHERE id = sqlc.arg('id');

-- name: GetAuthbotCodeWithBotUser :one
-- Resolves a submitted code together with its BotUser in one round-trip
-- (Django: LoginSerializer.validate_code + code.user).
SELECT
    c.id         AS code_id,
    c.code       AS code_value,
    c.message_id AS code_message_id,
    c.expires_at AS code_expires_at,
    c.created_at AS code_created_at,
    c.updated_at AS code_updated_at,
    b.id         AS bot_user_id,
    b.telegram_id,
    b.phone,
    b.first_name,
    b.last_name,
    b.username,
    b.created_at AS bot_user_created_at,
    b.updated_at AS bot_user_updated_at
FROM authbot_codes c
JOIN authbot_bot_users b ON b.id = c.bot_user_id
WHERE c.code = sqlc.arg('code');

-- name: DeleteAuthbotCode :execrows
-- Consumes a code so it cannot be replayed (Django: code.delete() after login).
DELETE FROM authbot_codes WHERE id = sqlc.arg('id');

-- name: DeleteExpiredAuthbotCodes :execrows
-- Best-effort janitor for codes nobody ever redeemed; safe to run periodically.
DELETE FROM authbot_codes WHERE expires_at < NOW();
