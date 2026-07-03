-- name: CreateTicket :one
INSERT INTO tickets (id, user_id, subject)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateTicketMessage :one
INSERT INTO ticket_messages (id, ticket_id, sender, author_id, message, attachment_url)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetTicketForOwner :one
SELECT * FROM tickets WHERE id = $1 AND user_id = $2;

-- name: GetTicketByID :one
SELECT sqlc.embed(t), u.phone AS user_phone
FROM tickets t
JOIN users u ON u.id = t.user_id
WHERE t.id = $1;

-- name: ListTicketMessages :many
SELECT * FROM ticket_messages WHERE ticket_id = $1 ORDER BY created_at, id;

-- name: ListTicketsForOwner :many
-- Joins each ticket to its most recent message so the listing can show a
-- preview without loading the full conversation (mirrors Django's
-- last_message serializer field).
SELECT sqlc.embed(t),
       COALESCE(lm.sender, '')  AS last_message_sender,
       COALESCE(lm.message, '') AS last_message_text
FROM tickets t
LEFT JOIN LATERAL (
    SELECT sender, message
    FROM ticket_messages m
    WHERE m.ticket_id = t.id
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT 1
) lm ON TRUE
WHERE t.user_id = sqlc.arg('owner_id')
  AND (sqlc.narg('status')::text IS NULL OR t.status = sqlc.narg('status'))
ORDER BY t.updated_at DESC, t.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountTicketsForOwner :one
SELECT COUNT(*) FROM tickets
WHERE user_id = sqlc.arg('owner_id')
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'));

-- name: ListAllTickets :many
-- Admin-wide listing, optionally filtered by status/user/subject search.
SELECT sqlc.embed(t),
       u.phone    AS user_phone,
       COALESCE(lm.sender, '')  AS last_message_sender,
       COALESCE(lm.message, '') AS last_message_text
FROM tickets t
JOIN users u ON u.id = t.user_id
LEFT JOIN LATERAL (
    SELECT sender, message
    FROM ticket_messages m
    WHERE m.ticket_id = t.id
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT 1
) lm ON TRUE
WHERE (sqlc.narg('status')::text IS NULL OR t.status = sqlc.narg('status'))
  AND (sqlc.narg('user_id')::uuid IS NULL OR t.user_id = sqlc.narg('user_id'))
  AND (sqlc.narg('search')::text IS NULL OR t.subject ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY t.updated_at DESC, t.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAllTickets :one
SELECT COUNT(*) FROM tickets t
WHERE (sqlc.narg('status')::text IS NULL OR t.status = sqlc.narg('status'))
  AND (sqlc.narg('user_id')::uuid IS NULL OR t.user_id = sqlc.narg('user_id'))
  AND (sqlc.narg('search')::text IS NULL OR t.subject ILIKE '%' || sqlc.narg('search') || '%');

-- name: UpdateTicketStatus :one
-- ownerID nil (via sqlc.narg('owner_id')) bypasses the ownership check for
-- the admin path; a user-scoped call always passes its own id.
UPDATE tickets
SET status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND (sqlc.narg('owner_id')::uuid IS NULL OR user_id = sqlc.narg('owner_id'))
RETURNING *;
