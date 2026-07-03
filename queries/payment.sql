-- ===================== transaction (gateway ledger) =====================

-- name: CreatePaymentTransaction :one
INSERT INTO transaction (id, user_id, amount, idempotency_key)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPaymentTransactionByID :one
SELECT * FROM transaction WHERE id = $1 AND deleted_at IS NULL;

-- name: GetPaymentTransactionForUser :one
SELECT * FROM transaction WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: ListPaymentTransactionsForUser :many
SELECT * FROM transaction WHERE user_id = sqlc.arg('user_id') AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPaymentTransactionsForUser :one
SELECT COUNT(*) FROM transaction WHERE user_id = $1 AND deleted_at IS NULL;

-- name: SetPaymentTransactionStatus :execrows
-- Guarded toggle (mirrors Django TransactionModel.objects.filter(pk=, status=<expected>).update(status=<new>)).
UPDATE transaction SET status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id') AND status = sqlc.arg('expected_status');

-- name: SetPaymentTransactionExternalRef :execrows
-- Repurposes the (otherwise-unused-at-creation) idempotency_key column to
-- remember a gateway's own opaque transaction id (Payme's CreateTransaction
-- params.id), so later Perform/Cancel/Check calls — which only carry that
-- opaque id, not our account/order_id — can resolve back to our row.
UPDATE transaction SET idempotency_key = $2, updated_at = NOW()
WHERE id = $1 AND idempotency_key IS NULL;

-- name: GetPaymentTransactionByExternalRef :one
SELECT * FROM transaction WHERE idempotency_key = $1 AND deleted_at IS NULL;

-- ===================== payment_order =====================

-- name: CreatePaymentOrder :one
INSERT INTO payment_order (
    id, project_id, owner_id, payer_chat_id, payer_first_name, payer_last_name, payer_username,
    amount, commission, net_amount, provider, comment, idempotency_key, transaction_id, is_debug
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetPaymentOrderByID :one
SELECT * FROM payment_order WHERE id = $1 AND deleted_at IS NULL;

-- name: GetPaymentOrderForOwner :one
SELECT * FROM payment_order WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL;

-- name: GetPaymentOrderByIdempotencyKey :one
SELECT * FROM payment_order WHERE idempotency_key = $1 AND deleted_at IS NULL;

-- name: GetPaymentOrderByTransactionID :one
SELECT * FROM payment_order WHERE transaction_id = $1 AND deleted_at IS NULL;

-- name: AttachPaymentTransactionToOrder :one
UPDATE payment_order SET transaction_id = $2, updated_at = NOW() WHERE id = $1 RETURNING *;

-- name: ClearPaymentOrderIdempotencyKey :execrows
-- Frees a finalized (paid/cancelled/refunded) order's key so a new order can reuse it.
UPDATE payment_order SET idempotency_key = NULL, updated_at = NOW() WHERE id = $1;

-- name: SetPaymentOrderPayInstruction :one
-- Paycue path: stores the provider transaction id + rendered instruction text at create time.
UPDATE payment_order SET external_id = $2, pay_instruction = $3, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: MarkPaymentOrderPaid :one
-- Guarded by status='created' so a retried webhook call is a safe no-op (zero rows returned).
UPDATE payment_order
SET status = 'paid', commission = $2, net_amount = $3, external_id = $4, paid_at = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'created'
RETURNING *;

-- name: MarkPaymentOrderCancelled :one
UPDATE payment_order SET status = 'cancelled', updated_at = NOW()
WHERE id = $1 AND status != 'cancelled'
RETURNING *;

-- name: ListPaidPaymentOrdersForOwner :many
SELECT * FROM payment_order
WHERE owner_id = sqlc.arg('owner_id') AND status = 'paid' AND deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
ORDER BY paid_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPaidPaymentOrdersForOwner :one
SELECT COUNT(*) FROM payment_order
WHERE owner_id = sqlc.arg('owner_id') AND status = 'paid' AND deleted_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'));

-- name: SumPaymentEarningsForOwner :one
SELECT
    COALESCE(SUM(net_amount), 0)::numeric AS total_earned,
    COALESCE(SUM(commission), 0)::numeric AS total_commission
FROM payment_order
WHERE owner_id = $1 AND status = 'paid' AND deleted_at IS NULL;

-- name: PaymentEarningsByProject :many
-- Bot-by-bot income breakdown for the earnings dashboard (by_bot payload).
SELECT p.id AS project_id, p.name AS project_name,
       COALESCE(SUM(o.net_amount), 0)::numeric AS total_earned,
       COUNT(o.id) AS order_count
FROM payment_order o
JOIN projects p ON p.id = o.project_id
WHERE o.owner_id = $1 AND o.status = 'paid' AND o.deleted_at IS NULL
GROUP BY p.id, p.name
ORDER BY total_earned DESC;

-- name: SumPaidPaymentOrderNetForOwner :one
-- Total bot-earned net income across all projects (withdrawal eligibility base).
SELECT COALESCE(SUM(net_amount), 0)::numeric AS total FROM payment_order
WHERE owner_id = $1 AND status = 'paid' AND deleted_at IS NULL;

-- ===================== history (balance ledger) =====================

-- name: CreatePaymentHistory :one
-- ON CONFLICT DO NOTHING + :one -> zero rows means the idempotency_key was
-- already applied; the caller treats "no rows" as a successful no-op.
INSERT INTO history (id, user_id, amount, comment, type, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: ListPaymentHistoryForUser :many
SELECT * FROM history WHERE user_id = sqlc.arg('user_id') AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPaymentHistoryForUser :one
SELECT COUNT(*) FROM history WHERE user_id = $1 AND deleted_at IS NULL;

-- name: GetPaymentHistoryForUser :one
SELECT * FROM history WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- ===================== payment_withdrawal_request =====================

-- name: CreatePaymentWithdrawal :one
INSERT INTO payment_withdrawal_request (id, user_id, project_id, amount, card_number, card_owner)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPaymentWithdrawalByID :one
SELECT * FROM payment_withdrawal_request WHERE id = $1;

-- name: GetPaymentWithdrawalForUser :one
SELECT * FROM payment_withdrawal_request WHERE id = $1 AND user_id = $2;

-- name: ListPaymentWithdrawalsForUser :many
SELECT * FROM payment_withdrawal_request WHERE user_id = sqlc.arg('user_id')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPaymentWithdrawalsForUser :one
SELECT COUNT(*) FROM payment_withdrawal_request WHERE user_id = $1;

-- name: DeletePendingPaymentWithdrawal :execrows
DELETE FROM payment_withdrawal_request WHERE id = $1 AND user_id = $2 AND status = 'pending';

-- name: ListPaymentWithdrawalsAdmin :many
SELECT sqlc.embed(w), u.phone AS user_phone, p.name AS project_name
FROM payment_withdrawal_request w
JOIN users u ON u.id = w.user_id
LEFT JOIN projects p ON p.id = w.project_id
WHERE (sqlc.narg('status')::text IS NULL OR w.status = sqlc.narg('status'))
ORDER BY w.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetPaymentWithdrawalByIDAdmin :one
SELECT sqlc.embed(w), u.phone AS user_phone, p.name AS project_name
FROM payment_withdrawal_request w
JOIN users u ON u.id = w.user_id
LEFT JOIN projects p ON p.id = w.project_id
WHERE w.id = sqlc.arg('id');

-- name: CountPaymentWithdrawalsAdmin :one
SELECT COUNT(*) FROM payment_withdrawal_request
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'));

-- name: ApprovePaymentWithdrawal :one
UPDATE payment_withdrawal_request
SET status = 'approved', reviewed_by = $2, reviewed_at = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: RejectPaymentWithdrawal :one
UPDATE payment_withdrawal_request
SET status = 'rejected', rejection_reason = $2, reviewed_by = $3, reviewed_at = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- ===================== admin-wide listings =====================

-- name: ListPaymentTransactionsAdmin :many
SELECT sqlc.embed(t), u.phone AS user_phone
FROM transaction t
JOIN users u ON u.id = t.user_id
WHERE t.deleted_at IS NULL
ORDER BY t.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPaymentTransactionsAdmin :one
SELECT COUNT(*) FROM transaction WHERE deleted_at IS NULL;

-- name: ListPaymentOrdersAdmin :many
SELECT sqlc.embed(o), p.name AS project_name, u.phone AS owner_phone
FROM payment_order o
JOIN projects p ON p.id = o.project_id
JOIN users u ON u.id = o.owner_id
WHERE o.deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR o.status = sqlc.narg('status'))
ORDER BY o.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPaymentOrdersAdmin :one
SELECT COUNT(*) FROM payment_order o
WHERE o.deleted_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR o.status = sqlc.narg('status'));

-- name: SumReservedPaymentWithdrawals :one
-- pending + approved withdrawals reserve bot income until reviewed/paid out.
SELECT COALESCE(SUM(amount), 0)::numeric AS total FROM payment_withdrawal_request
WHERE user_id = $1 AND status IN ('pending', 'approved');
