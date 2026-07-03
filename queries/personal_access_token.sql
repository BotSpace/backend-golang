-- name: CreatePersonalAccessToken :one
INSERT INTO personal_access_token (id, user_id, name, token_hash, prefix)
VALUES (sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('name'), sqlc.arg('token_hash'), sqlc.arg('prefix'))
RETURNING *;

-- name: ListPersonalAccessTokensForUser :many
SELECT * FROM personal_access_token WHERE user_id = sqlc.arg('user_id') ORDER BY created_at DESC;

-- name: GetPersonalAccessTokenByHash :one
SELECT * FROM personal_access_token WHERE token_hash = sqlc.arg('token_hash');

-- name: TouchPersonalAccessToken :exec
UPDATE personal_access_token SET last_used_at = NOW() WHERE id = sqlc.arg('id');

-- name: DeletePersonalAccessTokenForUser :execrows
DELETE FROM personal_access_token WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id');

-- name: CountPersonalAccessTokensForUser :one
SELECT COUNT(*) FROM personal_access_token WHERE user_id = sqlc.arg('user_id');
