-- OTP confirmation codes, one row per phone (upserted on each send). Mirrors
-- the Django SmsConfirm model: try_count guards brute force, resend_count +
-- resend_unlock_time throttle re-sends, expired_time bounds validity.
CREATE TABLE IF NOT EXISTS sms_confirm (
    phone              TEXT PRIMARY KEY,
    code               TEXT NOT NULL,
    try_count          INT NOT NULL DEFAULT 0,
    resend_count       INT NOT NULL DEFAULT 0,
    expired_time       TIMESTAMPTZ NOT NULL,
    resend_unlock_time TIMESTAMPTZ,
    unlock_time        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
