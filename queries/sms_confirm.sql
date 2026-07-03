-- name: GetSmsConfirm :one
SELECT * FROM sms_confirm WHERE phone = $1;

-- name: UpsertSmsConfirm :one
-- Set/refresh the code for a phone. Resets try_count and bumps resend_count.
INSERT INTO sms_confirm (phone, code, try_count, resend_count, expired_time, resend_unlock_time)
VALUES ($1, $2, 0, 1, sqlc.arg('expired_time'), sqlc.arg('resend_unlock_time'))
ON CONFLICT (phone) DO UPDATE
SET code               = EXCLUDED.code,
    try_count          = 0,
    resend_count       = sms_confirm.resend_count + 1,
    expired_time       = EXCLUDED.expired_time,
    resend_unlock_time = EXCLUDED.resend_unlock_time,
    updated_at         = NOW()
RETURNING *;

-- name: IncrSmsConfirmTry :one
UPDATE sms_confirm
SET try_count = try_count + 1, unlock_time = sqlc.narg('unlock_time'), updated_at = NOW()
WHERE phone = sqlc.arg('phone')
RETURNING *;

-- name: DeleteSmsConfirm :exec
DELETE FROM sms_confirm WHERE phone = $1;
