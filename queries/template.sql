-- ===================== categories & features =====================

-- name: TemplateListCategories :many
SELECT * FROM template_category ORDER BY name;

-- name: TemplateCreateCategory :one
INSERT INTO template_category (name) VALUES (sqlc.arg('name')) RETURNING *;

-- name: TemplateListFeatures :many
SELECT * FROM template_feature ORDER BY name;

-- name: TemplateCreateFeature :one
INSERT INTO template_feature (name, value) VALUES (sqlc.arg('name'), sqlc.narg('value')) RETURNING *;

-- ===================== template catalog (public) =====================

-- name: TemplateGetBySlug :one
SELECT * FROM template WHERE slug = sqlc.arg('slug') AND is_verify = TRUE;

-- name: TemplateGetByID :one
SELECT * FROM template WHERE id = sqlc.arg('id');

-- name: TemplateSlugExists :one
SELECT EXISTS(SELECT 1 FROM template WHERE slug = sqlc.arg('slug'));

-- name: TemplateIncrementViews :exec
UPDATE template SET views = views + 1, updated_at = NOW() WHERE id = sqlc.arg('id');

-- name: TemplateListPublic :many
-- AI-friendly smart search: OR across name/description/short_description and, via the
-- EXISTS subqueries, category/feature names (mirrors filters/template.py::filter_search,
-- one keyword is enough to match - not all of them).
SELECT DISTINCT t.* FROM template t
WHERE t.is_verify = TRUE
  AND (sqlc.narg('category_id')::uuid IS NULL
       OR EXISTS (SELECT 1 FROM template_category_map m WHERE m.template_id = t.id AND m.category_id = sqlc.narg('category_id')))
  AND (sqlc.narg('feature_id')::uuid IS NULL
       OR EXISTS (SELECT 1 FROM template_feature_map m WHERE m.template_id = t.id AND m.feature_id = sqlc.narg('feature_id')))
  AND (sqlc.narg('min_price')::numeric IS NULL OR t.original_price >= sqlc.narg('min_price'))
  AND (sqlc.narg('max_price')::numeric IS NULL OR t.original_price <= sqlc.narg('max_price'))
  AND (sqlc.narg('keywords')::text[] IS NULL OR EXISTS (
        SELECT 1 FROM unnest(sqlc.narg('keywords')::text[]) kw
        WHERE t.name ILIKE '%' || kw || '%'
           OR t.description ILIKE '%' || kw || '%'
           OR t.short_description ILIKE '%' || kw || '%'
           OR EXISTS (SELECT 1 FROM template_category_map cm JOIN template_category c ON c.id = cm.category_id
                      WHERE cm.template_id = t.id AND c.name ILIKE '%' || kw || '%')
           OR EXISTS (SELECT 1 FROM template_feature_map fm JOIN template_feature f ON f.id = fm.feature_id
                      WHERE fm.template_id = t.id AND f.name ILIKE '%' || kw || '%')
      ))
ORDER BY t.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: TemplateCountPublic :one
SELECT COUNT(DISTINCT t.id) FROM template t
WHERE t.is_verify = TRUE
  AND (sqlc.narg('category_id')::uuid IS NULL
       OR EXISTS (SELECT 1 FROM template_category_map m WHERE m.template_id = t.id AND m.category_id = sqlc.narg('category_id')))
  AND (sqlc.narg('feature_id')::uuid IS NULL
       OR EXISTS (SELECT 1 FROM template_feature_map m WHERE m.template_id = t.id AND m.feature_id = sqlc.narg('feature_id')))
  AND (sqlc.narg('min_price')::numeric IS NULL OR t.original_price >= sqlc.narg('min_price'))
  AND (sqlc.narg('max_price')::numeric IS NULL OR t.original_price <= sqlc.narg('max_price'))
  AND (sqlc.narg('keywords')::text[] IS NULL OR EXISTS (
        SELECT 1 FROM unnest(sqlc.narg('keywords')::text[]) kw
        WHERE t.name ILIKE '%' || kw || '%'
           OR t.description ILIKE '%' || kw || '%'
           OR t.short_description ILIKE '%' || kw || '%'
           OR EXISTS (SELECT 1 FROM template_category_map cm JOIN template_category c ON c.id = cm.category_id
                      WHERE cm.template_id = t.id AND c.name ILIKE '%' || kw || '%')
           OR EXISTS (SELECT 1 FROM template_feature_map fm JOIN template_feature f ON f.id = fm.feature_id
                      WHERE fm.template_id = t.id AND f.name ILIKE '%' || kw || '%')
      ));

-- name: TemplateCategoriesForTemplates :many
SELECT m.template_id, c.id, c.name FROM template_category_map m
JOIN template_category c ON c.id = m.category_id
WHERE m.template_id = ANY(sqlc.arg('template_ids')::uuid[])
ORDER BY c.name;

-- name: TemplateFeaturesForTemplates :many
SELECT m.template_id, f.id, f.name, f.value FROM template_feature_map m
JOIN template_feature f ON f.id = m.feature_id
WHERE m.template_id = ANY(sqlc.arg('template_ids')::uuid[])
ORDER BY f.name;

-- name: TemplateLikeCounts :many
SELECT template_id, COUNT(*) AS cnt FROM template_like
WHERE template_id = ANY(sqlc.arg('template_ids')::uuid[])
GROUP BY template_id;

-- name: TemplateLikedIDsForUser :many
SELECT template_id FROM template_like
WHERE user_id = sqlc.arg('user_id') AND template_id = ANY(sqlc.arg('template_ids')::uuid[]);

-- name: TemplateOrderedIDsForUser :many
SELECT template_id FROM template_order
WHERE user_id = sqlc.arg('user_id') AND payment_status = TRUE AND status = 'confirm'
  AND template_id = ANY(sqlc.arg('template_ids')::uuid[]);

-- name: TemplateInsertLike :execrows
INSERT INTO template_like (template_id, user_id) VALUES (sqlc.arg('template_id'), sqlc.arg('user_id'))
ON CONFLICT DO NOTHING;

-- name: TemplateDeleteLike :execrows
DELETE FROM template_like WHERE template_id = sqlc.arg('template_id') AND user_id = sqlc.arg('user_id');

-- ===================== template catalog (admin) =====================
-- Unlike the public catalog above, these are not restricted to
-- is_verify = TRUE and join users for the author's display name/phone (the
-- fix for the admin panel's "#undefined" author bug — no admin template
-- endpoint existed at all before this).

-- name: TemplateListAdmin :many
SELECT sqlc.embed(t),
       u.first_name AS author_first_name,
       u.last_name  AS author_last_name,
       u.phone      AS author_phone,
       u.username   AS author_username
FROM template t
LEFT JOIN users u ON u.id = t.author_id
WHERE (sqlc.narg('is_verify')::bool IS NULL OR t.is_verify = sqlc.narg('is_verify'))
  AND (sqlc.narg('search')::text IS NULL
       OR t.name ILIKE '%' || sqlc.narg('search') || '%'
       OR t.slug ILIKE '%' || sqlc.narg('search') || '%'
       OR t.description ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY t.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: TemplateCountAdmin :one
SELECT COUNT(*) FROM template t
WHERE (sqlc.narg('is_verify')::bool IS NULL OR t.is_verify = sqlc.narg('is_verify'))
  AND (sqlc.narg('search')::text IS NULL
       OR t.name ILIKE '%' || sqlc.narg('search') || '%'
       OR t.slug ILIKE '%' || sqlc.narg('search') || '%'
       OR t.description ILIKE '%' || sqlc.narg('search') || '%');

-- name: TemplateGetAdmin :one
SELECT sqlc.embed(t),
       u.first_name AS author_first_name,
       u.last_name  AS author_last_name,
       u.phone      AS author_phone,
       u.username   AS author_username
FROM template t
LEFT JOIN users u ON u.id = t.author_id
WHERE t.id = sqlc.arg('id');

-- name: TemplateAdminCreate :one
INSERT INTO template
    (author_id, project_id, name, slug, image, video, short_description, description,
     original_price, discount, discount_type, demo_url, is_verify, required_envs)
VALUES (sqlc.narg('author_id'), sqlc.narg('project_id'), sqlc.arg('name'), sqlc.arg('slug'), sqlc.arg('image'),
        sqlc.narg('video'), sqlc.arg('short_description'), sqlc.arg('description'), sqlc.arg('original_price'),
        sqlc.arg('discount'), sqlc.arg('discount_type'), sqlc.narg('demo_url'), sqlc.arg('is_verify'),
        sqlc.arg('required_envs'))
RETURNING *;

-- name: TemplateAdminUpdate :one
UPDATE template
SET name              = COALESCE(sqlc.narg('name'), name),
    image             = COALESCE(sqlc.narg('image'), image),
    video             = COALESCE(sqlc.narg('video'), video),
    short_description = COALESCE(sqlc.narg('short_description'), short_description),
    description       = COALESCE(sqlc.narg('description'), description),
    original_price    = COALESCE(sqlc.narg('original_price'), original_price),
    discount          = COALESCE(sqlc.narg('discount'), discount),
    discount_type     = COALESCE(sqlc.narg('discount_type'), discount_type),
    demo_url          = COALESCE(sqlc.narg('demo_url'), demo_url),
    is_verify         = COALESCE(sqlc.narg('is_verify'), is_verify),
    required_envs     = COALESCE(sqlc.narg('required_envs'), required_envs),
    updated_at        = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: TemplateAdminDelete :execrows
DELETE FROM template WHERE id = sqlc.arg('id');

-- name: TemplateSetVerify :one
UPDATE template SET is_verify = sqlc.arg('is_verify'), updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: TemplateClearCategories :exec
DELETE FROM template_category_map WHERE template_id = sqlc.arg('template_id');

-- name: TemplateSetCategories :exec
INSERT INTO template_category_map (template_id, category_id)
SELECT sqlc.arg('template_id'), unnest(sqlc.arg('category_ids')::uuid[])
ON CONFLICT DO NOTHING;

-- name: TemplateClearFeatures :exec
DELETE FROM template_feature_map WHERE template_id = sqlc.arg('template_id');

-- name: TemplateSetFeatures :exec
INSERT INTO template_feature_map (template_id, feature_id)
SELECT sqlc.arg('template_id'), unnest(sqlc.arg('feature_ids')::uuid[])
ON CONFLICT DO NOTHING;

-- ===================== submissions =====================

-- name: TemplateProjectOwnedByUser :one
-- Scopes a submission's source project to its owner without importing the
-- project module (mirrors PORTING_GUIDE.md's cross-module ownership pattern).
SELECT EXISTS(
    SELECT 1 FROM projects
    WHERE id = sqlc.arg('project_id') AND owner_id = sqlc.arg('owner_id') AND deleted_at IS NULL
);

-- name: TemplateCreateSubmission :one
INSERT INTO template_submission_request
    (user_id, project_id, name, short_description, description, image, video, demo_url, original_price, required_envs)
VALUES (sqlc.arg('user_id'), sqlc.arg('project_id'), sqlc.arg('name'), sqlc.arg('short_description'),
        sqlc.arg('description'), sqlc.narg('image'), sqlc.narg('video'), sqlc.narg('demo_url'),
        sqlc.arg('original_price'), sqlc.arg('required_envs'))
RETURNING *;

-- name: TemplateSetSubmissionCategories :exec
INSERT INTO template_submission_category_map (submission_id, category_id)
SELECT sqlc.arg('submission_id'), unnest(sqlc.arg('category_ids')::uuid[])
ON CONFLICT DO NOTHING;

-- name: TemplateSetSubmissionFeatures :exec
INSERT INTO template_submission_feature_map (submission_id, feature_id)
SELECT sqlc.arg('submission_id'), unnest(sqlc.arg('feature_ids')::uuid[])
ON CONFLICT DO NOTHING;

-- name: TemplateClearSubmissionCategories :exec
DELETE FROM template_submission_category_map WHERE submission_id = sqlc.arg('submission_id');

-- name: TemplateClearSubmissionFeatures :exec
DELETE FROM template_submission_feature_map WHERE submission_id = sqlc.arg('submission_id');

-- name: TemplateSubmissionCategoriesFor :many
SELECT m.submission_id, c.id, c.name FROM template_submission_category_map m
JOIN template_category c ON c.id = m.category_id
WHERE m.submission_id = ANY(sqlc.arg('submission_ids')::uuid[])
ORDER BY c.name;

-- name: TemplateSubmissionFeaturesFor :many
SELECT m.submission_id, f.id, f.name, f.value FROM template_submission_feature_map m
JOIN template_feature f ON f.id = m.feature_id
WHERE m.submission_id = ANY(sqlc.arg('submission_ids')::uuid[])
ORDER BY f.name;

-- name: TemplateGetSubmissionForOwner :one
SELECT * FROM template_submission_request WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id');

-- name: TemplateGetSubmission :one
SELECT * FROM template_submission_request WHERE id = sqlc.arg('id');

-- name: TemplateListSubmissionsForOwner :many
SELECT * FROM template_submission_request WHERE user_id = sqlc.arg('user_id')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: TemplateCountSubmissionsForOwner :one
SELECT COUNT(*) FROM template_submission_request WHERE user_id = sqlc.arg('user_id');

-- name: TemplateListSubmissionsAdmin :many
SELECT * FROM template_submission_request
WHERE sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: TemplateCountSubmissionsAdmin :one
SELECT COUNT(*) FROM template_submission_request
WHERE sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status');

-- name: TemplateUpdateSubmission :one
-- Only mutable while status = 'pending' (checked by the caller/usecase too).
UPDATE template_submission_request
SET name              = COALESCE(sqlc.narg('name'), name),
    short_description = COALESCE(sqlc.narg('short_description'), short_description),
    description       = COALESCE(sqlc.narg('description'), description),
    image             = COALESCE(sqlc.narg('image'), image),
    video             = COALESCE(sqlc.narg('video'), video),
    demo_url          = COALESCE(sqlc.narg('demo_url'), demo_url),
    original_price    = COALESCE(sqlc.narg('original_price'), original_price),
    required_envs     = COALESCE(sqlc.narg('required_envs'), required_envs),
    updated_at        = NOW()
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id') AND status = 'pending'
RETURNING *;

-- name: TemplateDeleteSubmission :execrows
DELETE FROM template_submission_request
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id') AND status = 'pending';

-- name: TemplateApproveSubmission :one
UPDATE template_submission_request
SET status = 'approved', template_id = sqlc.arg('template_id'), reviewed_by = sqlc.arg('reviewed_by'), reviewed_at = NOW(),
    updated_at = NOW()
WHERE id = sqlc.arg('id') AND status = 'pending'
RETURNING *;

-- name: TemplateRejectSubmission :one
UPDATE template_submission_request
SET status = 'rejected', rejection_reason = sqlc.arg('rejection_reason'), reviewed_by = sqlc.arg('reviewed_by'),
    reviewed_at = NOW(), updated_at = NOW()
WHERE id = sqlc.arg('id') AND status = 'pending'
RETURNING *;

-- name: TemplateCreateFromSubmission :one
-- Creates the published TemplateModel row when a submission is approved
-- (mirrors admin/submission.py::approve_submissions). Category/feature m2m rows are
-- copied separately with TemplateCopySubmissionCategories/Features.
INSERT INTO template
    (author_id, project_id, name, image, video, short_description, description, original_price,
     discount, discount_type, demo_url, is_verify, required_envs, slug)
VALUES (sqlc.narg('author_id'), sqlc.arg('project_id'), sqlc.arg('name'), sqlc.arg('image'), sqlc.narg('video'),
        sqlc.arg('short_description'), sqlc.arg('description'), sqlc.arg('original_price'),
        sqlc.arg('discount'), sqlc.arg('discount_type'), sqlc.narg('demo_url'), TRUE,
        sqlc.arg('required_envs'), sqlc.arg('slug'))
RETURNING *;

-- name: TemplateCopySubmissionCategories :exec
INSERT INTO template_category_map (template_id, category_id)
SELECT sqlc.arg('template_id'), category_id FROM template_submission_category_map
WHERE submission_id = sqlc.arg('submission_id')
ON CONFLICT DO NOTHING;

-- name: TemplateCopySubmissionFeatures :exec
INSERT INTO template_feature_map (template_id, feature_id)
SELECT sqlc.arg('template_id'), feature_id FROM template_submission_feature_map
WHERE submission_id = sqlc.arg('submission_id')
ON CONFLICT DO NOTHING;

-- ===================== orders / purchase =====================

-- name: TemplateGetOrderForUserAndTemplate :one
SELECT * FROM template_order
WHERE user_id = sqlc.arg('user_id') AND template_id = sqlc.arg('template_id')
ORDER BY created_at DESC LIMIT 1;

-- name: TemplateCreateOrder :one
INSERT INTO template_order (user_id, template_id, amount, status, payment_status, commission_paid)
VALUES (sqlc.arg('user_id'), sqlc.arg('template_id'), sqlc.arg('amount'), sqlc.arg('status'),
        sqlc.arg('payment_status'), sqlc.arg('commission_paid'))
RETURNING *;

-- name: TemplateGetOrderForOwner :one
SELECT * FROM template_order WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id');

-- name: TemplateListOrdersForOwner :many
SELECT * FROM template_order WHERE user_id = sqlc.arg('user_id')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: TemplateCountOrdersForOwner :one
SELECT COUNT(*) FROM template_order WHERE user_id = sqlc.arg('user_id');

-- name: TemplateMarkOrderPaid :one
-- Guarded by commission_paid = FALSE so a retried/double Pay call is a no-op (no rows).
UPDATE template_order
SET status = 'confirm', payment_status = TRUE, commission_paid = TRUE, updated_at = NOW()
WHERE id = sqlc.arg('id') AND commission_paid = FALSE
RETURNING *;

-- name: TemplateDeductBuyerBalance :one
-- balance is an integer (whole so'm) wallet; guarded so it never goes below zero.
UPDATE users
SET balance = balance - sqlc.arg('amount'), updated_at = NOW()
WHERE id = sqlc.arg('user_id') AND balance - sqlc.arg('amount') >= 0
RETURNING id;

-- name: TemplateCreditAuthorCommission :exec
-- Author's share of a sale (see AuthorCommissionPercent in domain.go, mirrors the 80%
-- in signals/order.py::OrderSignal). commission is already the computed share (amount * 0.80).
UPDATE users
SET template_balance = template_balance + sqlc.arg('commission')::numeric, updated_at = NOW()
WHERE id = sqlc.arg('author_id');

-- name: TemplateGrantOwnership :exec
INSERT INTO usertemplate (user_id, template_id) VALUES (sqlc.arg('user_id'), sqlc.arg('template_id'))
ON CONFLICT DO NOTHING;

-- ===================== withdrawals =====================

-- name: TemplateGetAuthorBalance :one
SELECT template_balance FROM users WHERE id = sqlc.arg('id');

-- name: TemplateCreateWithdrawal :one
INSERT INTO template_withdrawal_request (user_id, amount, card_number, card_owner)
VALUES (sqlc.arg('user_id'), sqlc.arg('amount'), sqlc.arg('card_number'), sqlc.arg('card_owner'))
RETURNING *;

-- name: TemplateGetWithdrawalForOwner :one
SELECT * FROM template_withdrawal_request WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id');

-- name: TemplateGetWithdrawal :one
SELECT * FROM template_withdrawal_request WHERE id = sqlc.arg('id');

-- name: TemplateListWithdrawalsForOwner :many
SELECT * FROM template_withdrawal_request WHERE user_id = sqlc.arg('user_id')
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: TemplateCountWithdrawalsForOwner :one
SELECT COUNT(*) FROM template_withdrawal_request WHERE user_id = sqlc.arg('user_id');

-- name: TemplateListWithdrawalsAdmin :many
SELECT sqlc.embed(w), u.phone AS user_phone
FROM template_withdrawal_request w
JOIN users u ON u.id = w.user_id
WHERE sqlc.narg('status')::text IS NULL OR w.status = sqlc.narg('status')
ORDER BY w.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: TemplateGetWithdrawalAdmin :one
SELECT sqlc.embed(w), u.phone AS user_phone
FROM template_withdrawal_request w
JOIN users u ON u.id = w.user_id
WHERE w.id = sqlc.arg('id');

-- name: TemplateCountWithdrawalsAdmin :one
SELECT COUNT(*) FROM template_withdrawal_request
WHERE sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status');

-- name: TemplateDeleteWithdrawal :execrows
DELETE FROM template_withdrawal_request
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id') AND status = 'pending';

-- name: TemplateApproveWithdrawalDeductBalance :one
-- Guarded so approval never drives template_balance negative (mirrors
-- admin/withdrawal.py::approve_withdrawals - falls back to rejection when this returns no row).
UPDATE users
SET template_balance = template_balance - sqlc.arg('amount')::numeric, updated_at = NOW()
WHERE id = sqlc.arg('user_id') AND template_balance >= sqlc.arg('amount')::numeric
RETURNING id;

-- name: TemplateMarkWithdrawalApproved :one
UPDATE template_withdrawal_request
SET status = 'approved', reviewed_by = sqlc.arg('reviewed_by'), reviewed_at = NOW(), updated_at = NOW()
WHERE id = sqlc.arg('id') AND status = 'pending'
RETURNING *;

-- name: TemplateMarkWithdrawalRejected :one
UPDATE template_withdrawal_request
SET status = 'rejected', rejection_reason = sqlc.arg('rejection_reason'), reviewed_by = sqlc.arg('reviewed_by'),
    reviewed_at = NOW(), updated_at = NOW()
WHERE id = sqlc.arg('id') AND status = 'pending'
RETURNING *;
