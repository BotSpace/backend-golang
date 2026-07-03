-- name: CreateVerification :one
INSERT INTO user_verification (user_id, full_name, passport_image, selfie_image, residence_reference)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetLatestVerificationForUser :one
SELECT * FROM user_verification WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: HasPendingVerificationForUser :one
SELECT EXISTS (
    SELECT 1 FROM user_verification WHERE user_id = $1 AND status = 'pending'
);

-- name: IsUserKYCVerified :one
SELECT (kyc_verified_at IS NOT NULL)::bool AS verified FROM users WHERE id = $1;

-- name: GetVerificationWithReviewer :one
SELECT
    v.*,
    u.phone AS user_phone,
    r.phone AS reviewed_by_phone
FROM user_verification v
JOIN users u ON u.id = v.user_id
LEFT JOIN users r ON r.id = v.reviewed_by
WHERE v.id = sqlc.arg('id');

-- name: ListVerificationsForAdmin :many
SELECT
    v.*,
    u.phone AS user_phone,
    r.phone AS reviewed_by_phone
FROM user_verification v
JOIN users u ON u.id = v.user_id
LEFT JOIN users r ON r.id = v.reviewed_by
WHERE sqlc.narg('status')::text IS NULL OR v.status = sqlc.narg('status')
ORDER BY v.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountVerificationsForAdmin :one
SELECT COUNT(*) FROM user_verification v
WHERE sqlc.narg('status')::text IS NULL OR v.status = sqlc.narg('status');

-- name: ApproveVerification :one
UPDATE user_verification
SET status      = 'approved',
    reviewed_by = sqlc.arg('reviewed_by'),
    reviewed_at = NOW(),
    updated_at  = NOW()
WHERE id = sqlc.arg('id') AND status = 'pending'
RETURNING *;

-- name: RejectVerification :one
UPDATE user_verification
SET status            = 'rejected',
    rejection_reason  = sqlc.arg('rejection_reason'),
    reviewed_by       = sqlc.arg('reviewed_by'),
    reviewed_at       = NOW(),
    updated_at        = NOW()
WHERE id = sqlc.arg('id') AND status = 'pending'
RETURNING *;

-- name: SetUserKYCVerifiedAt :exec
UPDATE users SET kyc_verified_at = NOW(), updated_at = NOW() WHERE id = sqlc.arg('id');
