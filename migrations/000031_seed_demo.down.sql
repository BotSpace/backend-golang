-- Removes only the rows this migration inserted (fixed UUID prefixes), never
-- touching the pre-existing admin account or any other real data.
DELETE FROM audit_log WHERE id::text LIKE '99999999-9999-9999-9999-%';
DELETE FROM ticket_messages WHERE id::text LIKE '67676767-6767-6767-6767-%';
DELETE FROM tickets WHERE id::text LIKE '66666666-6666-6666-6666-%';
DELETE FROM template_withdrawal_request WHERE id::text LIKE '89898989-8989-8989-8989-%';
DELETE FROM payment_withdrawal_request WHERE id::text LIKE '88888888-8888-8888-8888-%';
DELETE FROM template_submission_request WHERE id::text LIKE '7a7a7a7a-7a7a-7a7a-7a7a-%';
DELETE FROM template_order WHERE id::text LIKE '7c7c7c7c-7c7c-7c7c-7c7c-%';
DELETE FROM usertemplate WHERE template_id::text LIKE '79797979-7979-7979-7979-%';
DELETE FROM template_feature_map WHERE template_id::text LIKE '79797979-7979-7979-7979-%';
DELETE FROM template_category_map WHERE template_id::text LIKE '79797979-7979-7979-7979-%';
DELETE FROM template WHERE id::text LIKE '79797979-7979-7979-7979-%';
DELETE FROM template_feature WHERE id::text LIKE '78787878-7878-7878-7878-%';
DELETE FROM template_category WHERE id::text LIKE '77777777-7777-7777-7777-%';
DELETE FROM transaction WHERE id::text LIKE '44444444-4444-4444-4444-%';
DELETE FROM payment_order WHERE id::text LIKE '55555555-5555-5555-5555-%';
DELETE FROM relase WHERE id::text LIKE '33333333-3333-3333-3333-%';
DELETE FROM projects WHERE id::text LIKE '22222222-2222-2222-2222-%';
DELETE FROM user_subscription WHERE user_id::text LIKE '11111111-1111-1111-1111-%';
DELETE FROM users WHERE id::text LIKE '11111111-1111-1111-1111-%';
