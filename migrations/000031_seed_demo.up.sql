-- Demo/representative data for the admin panel. Idempotent (fixed UUIDs +
-- ON CONFLICT DO NOTHING everywhere) so re-running it is always a safe
-- no-op; the existing admin account (+998900000001) is left untouched.
-- All timestamps are backdated relative to NOW() so the dashboard's 7d/30d
-- KPIs and growth charts have something to show.

-- Bootstrap admin account (id 524b9b0f-34ee-4eca-a2d4-52d4cace5f6c, phone
-- +998900000001) referenced as actor_id/author_id by ticket_messages and
-- audit_log below. On a fresh/empty database no such user exists yet, so
-- create it here; ON CONFLICT (phone) DO NOTHING keeps this a no-op on any
-- database where the admin account already exists (whatever its id).
INSERT INTO users (id, phone, first_name, last_name, username, email, password_hash, role, is_active, is_staff, is_superuser, validated_at, created_at, updated_at) VALUES
    ('524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', '+998900000001', 'Admin', 'Botmother', 'admin', 'admin@botspace.uz', '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'superuser', TRUE, TRUE, TRUE, NOW() - INTERVAL '30 days', NOW() - INTERVAL '30 days', NOW() - INTERVAL '30 days')
ON CONFLICT (phone) DO NOTHING;

-- ===================== users (15) =====================
-- password_hash is bcrypt("Demo12345!") — usable for local testing, not a
-- security concern since this is seed/demo data.
INSERT INTO users (id, phone, first_name, last_name, username, email, password_hash, role, balance, template_balance, is_active, validated_at, kyc_verified_at, created_at, updated_at) VALUES
    ('11111111-1111-1111-1111-000000000001', '+998901112201', 'Aziz',    'Karimov',     'aziz_karimov',    'aziz.karimov@example.uz',    '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  1250000, 0,      TRUE, NOW() - INTERVAL '29 days', NOW() - INTERVAL '20 days', NOW() - INTERVAL '29 days', NOW() - INTERVAL '29 days'),
    ('11111111-1111-1111-1111-000000000002', '+998901112202', 'Malika',  'Yusupova',    'malika_yusupova', 'malika.yusupova@example.uz', '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'admin', 0,       0,      TRUE, NOW() - INTERVAL '28 days', NULL,                        NOW() - INTERVAL '28 days', NOW() - INTERVAL '28 days'),
    ('11111111-1111-1111-1111-000000000003', '+998901112203', 'Sardor',  'Toshkentov',  'sardor_t',        'sardor.t@example.uz',        '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  340000,  0,      TRUE, NOW() - INTERVAL '27 days', NOW() - INTERVAL '15 days', NOW() - INTERVAL '27 days', NOW() - INTERVAL '27 days'),
    ('11111111-1111-1111-1111-000000000004', '+998901112204', 'Dilnoza', 'Rashidova',   'dilnoza_r',       'dilnoza.r@example.uz',       '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  85000,   120000, TRUE, NOW() - INTERVAL '25 days', NOW() - INTERVAL '10 days', NOW() - INTERVAL '25 days', NOW() - INTERVAL '25 days'),
    ('11111111-1111-1111-1111-000000000005', '+998901112205', 'Bekzod',  'Yuldashev',   'bekzod_y',        'bekzod.y@example.uz',        '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  0,       0,      TRUE, NOW() - INTERVAL '24 days', NULL,                        NOW() - INTERVAL '24 days', NOW() - INTERVAL '24 days'),
    ('11111111-1111-1111-1111-000000000006', '+998901112206', 'Nodira',  'Abdullayeva', 'nodira_a',        'nodira.a@example.uz',        '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  2100000, 340000, TRUE, NOW() - INTERVAL '22 days', NOW() - INTERVAL '18 days', NOW() - INTERVAL '22 days', NOW() - INTERVAL '22 days'),
    ('11111111-1111-1111-1111-000000000007', '+998901112207', 'Jasur',   'Mirzayev',    'jasur_m',         'jasur.m@example.uz',         '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  15000,   0,      TRUE, NOW() - INTERVAL '20 days', NULL,                        NOW() - INTERVAL '20 days', NOW() - INTERVAL '20 days'),
    ('11111111-1111-1111-1111-000000000008', '+998901112208', 'Gulnora', 'Xakimova',    'gulnora_x',       'gulnora.x@example.uz',       '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'admin', 0,       0,      TRUE, NOW() - INTERVAL '18 days', NULL,                        NOW() - INTERVAL '18 days', NOW() - INTERVAL '18 days'),
    ('11111111-1111-1111-1111-000000000009', '+998901112209', 'Otabek',  'Sodiqov',     'otabek_s',        'otabek.s@example.uz',        '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  560000,  0,      TRUE, NOW() - INTERVAL '15 days', NOW() - INTERVAL '5 days',  NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days'),
    ('11111111-1111-1111-1111-000000000010', '+998901112210', 'Feruza',  'Nazarova',    'feruza_n',        'feruza.n@example.uz',        '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  0,       75000,  TRUE, NOW() - INTERVAL '13 days', NULL,                        NOW() - INTERVAL '13 days', NOW() - INTERVAL '13 days'),
    ('11111111-1111-1111-1111-000000000011', '+998901112211', 'Sherzod', 'Qodirov',     'sherzod_q',       'sherzod.q@example.uz',       '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  430000,  0,      TRUE, NOW() - INTERVAL '10 days', NOW() - INTERVAL '3 days',  NOW() - INTERVAL '10 days', NOW() - INTERVAL '10 days'),
    ('11111111-1111-1111-1111-000000000012', '+998901112212', 'Zilola',  'Ergasheva',   'zilola_e',        'zilola.e@example.uz',        '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  0,       0,      TRUE, NOW() - INTERVAL '8 days',  NULL,                        NOW() - INTERVAL '8 days',  NOW() - INTERVAL '8 days'),
    ('11111111-1111-1111-1111-000000000013', '+998901112213', 'Ravshan', 'Tursunov',    'ravshan_t',       'ravshan.t@example.uz',       '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  95000,   0,      TRUE, NOW() - INTERVAL '6 days',  NULL,                        NOW() - INTERVAL '6 days',  NOW() - INTERVAL '6 days'),
    ('11111111-1111-1111-1111-000000000014', '+998901112214', 'Madina',  'Yoqubova',    'madina_y',        'madina.y@example.uz',        '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  0,       0,      TRUE, NOW() - INTERVAL '3 days',  NULL,                        NOW() - INTERVAL '3 days',  NOW() - INTERVAL '3 days'),
    ('11111111-1111-1111-1111-000000000015', '+998901112215', 'Ulug''bek','Xolmatov',   'ulugbek_x',       'ulugbek.x@example.uz',       '$2a$10$opw8FG5jiJQW5NmPDQaSG.GXzjfp7PNAU8LlurrgPNP2LlR7CF9km', 'user',  20000,   0,      TRUE, NOW() - INTERVAL '1 day',   NULL,                        NOW() - INTERVAL '1 day',   NOW() - INTERVAL '1 day')
ON CONFLICT (phone) DO NOTHING;

-- Subscriptions: free plan for most, pro/max for a few power users.
INSERT INTO user_subscription (user_id, plan_id, status, started_at, bots_count, tokens_used_input, tokens_used_output)
SELECT u.id, p.id, 'active', u.created_at, 1, 12000, 4300
FROM users u, plan p
WHERE u.id = '11111111-1111-1111-1111-000000000001' AND p.name = 'free'
ON CONFLICT (user_id) DO NOTHING;
INSERT INTO user_subscription (user_id, plan_id, status, started_at, bots_count, tokens_used_input, tokens_used_output)
SELECT u.id, p.id, 'active', u.created_at, 2, 340000, 98000
FROM users u, plan p
WHERE u.id = '11111111-1111-1111-1111-000000000004' AND p.name = 'pro'
ON CONFLICT (user_id) DO NOTHING;
INSERT INTO user_subscription (user_id, plan_id, status, started_at, bots_count, tokens_used_input, tokens_used_output)
SELECT u.id, p.id, 'active', u.created_at, 3, 890000, 210000
FROM users u, plan p
WHERE u.id = '11111111-1111-1111-1111-000000000006' AND p.name = 'max'
ON CONFLICT (user_id) DO NOTHING;
INSERT INTO user_subscription (user_id, plan_id, status, started_at, bots_count, tokens_used_input, tokens_used_output)
SELECT u.id, p.id, 'active', u.created_at, 1, 5000, 1200
FROM users u, plan p
WHERE u.id = '11111111-1111-1111-1111-000000000009' AND p.name = 'free'
ON CONFLICT (user_id) DO NOTHING;
INSERT INTO user_subscription (user_id, plan_id, status, started_at, bots_count, tokens_used_input, tokens_used_output)
SELECT u.id, p.id, 'active', u.created_at, 2, 156000, 42000
FROM users u, plan p
WHERE u.id = '11111111-1111-1111-1111-000000000011' AND p.name = 'pro'
ON CONFLICT (user_id) DO NOTHING;

-- ===================== projects (10) =====================
INSERT INTO projects (id, name, description, username, owner_id, flow, is_template, created_at, updated_at) VALUES
    ('22222222-2222-2222-2222-000000000001', 'Do''kon boti',        'Onlayn do''kon uchun buyurtma qabul qiluvchi bot', 'dokon_bot',      '11111111-1111-1111-1111-000000000001', '[]'::jsonb, FALSE, NOW() - INTERVAL '26 days', NOW() - INTERVAL '2 days'),
    ('22222222-2222-2222-2222-000000000002', 'Yetkazib berish',     'Kuryer va yetkazib berish nazorati boti',           'delivery_bot',   '11111111-1111-1111-1111-000000000003', '[]'::jsonb, FALSE, NOW() - INTERVAL '24 days', NOW() - INTERVAL '1 day'),
    ('22222222-2222-2222-2222-000000000003', 'CRM integratsiya',    'Mijozlar bilan ishlash uchun CRM bot',              'crm_bot',        '11111111-1111-1111-1111-000000000004', '[]'::jsonb, FALSE, NOW() - INTERVAL '21 days', NOW() - INTERVAL '3 days'),
    ('22222222-2222-2222-2222-000000000004', 'Ta''lim markazi',     'Kurslarga yozilish va jadval boti',                 'edu_bot',        '11111111-1111-1111-1111-000000000006', '[]'::jsonb, FALSE, NOW() - INTERVAL '19 days', NOW() - INTERVAL '5 days'),
    ('22222222-2222-2222-2222-000000000005', 'Restoran buyurtma',   'Restoran uchun onlayn buyurtma boti',               'restoran_bot',   '11111111-1111-1111-1111-000000000007', '[]'::jsonb, FALSE, NOW() - INTERVAL '17 days', NOW() - INTERVAL '4 days'),
    ('22222222-2222-2222-2222-000000000006', 'Salon navbat',        'Go''zallik saloni uchun navbat olish boti',         'salon_bot',      '11111111-1111-1111-1111-000000000009', '[]'::jsonb, FALSE, NOW() - INTERVAL '14 days', NOW() - INTERVAL '2 days'),
    ('22222222-2222-2222-2222-000000000007', 'Fitness klub',        'Fitness klub a''zolik va bron qilish boti',         'fitness_bot',    '11111111-1111-1111-1111-000000000011', '[]'::jsonb, FALSE, NOW() - INTERVAL '11 days', NOW() - INTERVAL '1 day'),
    ('22222222-2222-2222-2222-000000000008', 'Onlayn kurslar',      'Video darslar va test boti',                        'courses_bot',    '11111111-1111-1111-1111-000000000004', '[]'::jsonb, FALSE, NOW() - INTERVAL '9 days',  NOW() - INTERVAL '1 day'),
    ('22222222-2222-2222-2222-000000000009', 'Ijtimoiy tarmoq',     'Ijtimoiy tarmoq uchun avtoposting boti',            'social_bot',     '11111111-1111-1111-1111-000000000013', '[]'::jsonb, FALSE, NOW() - INTERVAL '6 days',  NOW() - INTERVAL '1 day'),
    ('22222222-2222-2222-2222-000000000010', 'Kassa boti',          'Kichik biznes uchun kassa va hisobot boti',         'kassa_bot',      '11111111-1111-1111-1111-000000000015', '[]'::jsonb, FALSE, NOW() - INTERVAL '2 days',  NOW())
ON CONFLICT (id) DO NOTHING;

-- ===================== releases (5) =====================
INSERT INTO relase (id, project_id, version, flow, status, created_at, updated_at) VALUES
    ('33333333-3333-3333-3333-000000000001', '22222222-2222-2222-2222-000000000001', 1, '[]'::jsonb, 'running', NOW() - INTERVAL '20 days', NOW() - INTERVAL '20 days'),
    ('33333333-3333-3333-3333-000000000002', '22222222-2222-2222-2222-000000000002', 1, '[]'::jsonb, 'running', NOW() - INTERVAL '18 days', NOW() - INTERVAL '18 days'),
    ('33333333-3333-3333-3333-000000000003', '22222222-2222-2222-2222-000000000004', 1, '[]'::jsonb, 'stopped', NOW() - INTERVAL '15 days', NOW() - INTERVAL '10 days'),
    ('33333333-3333-3333-3333-000000000004', '22222222-2222-2222-2222-000000000006', 1, '[]'::jsonb, 'running', NOW() - INTERVAL '10 days', NOW() - INTERVAL '10 days'),
    ('33333333-3333-3333-3333-000000000005', '22222222-2222-2222-2222-000000000007', 1, '[]'::jsonb, 'failed',  NOW() - INTERVAL '8 days',  NOW() - INTERVAL '7 days')
ON CONFLICT (id) DO NOTHING;

-- ===================== bot payment orders (12) =====================
INSERT INTO payment_order (id, project_id, owner_id, payer_chat_id, payer_first_name, payer_username, amount, commission, net_amount, provider, status, comment, paid_at, is_debug, created_at, updated_at) VALUES
    ('55555555-5555-5555-5555-000000000001', '22222222-2222-2222-2222-000000000001', '11111111-1111-1111-1111-000000000001', 100001, 'Alisher',  'alisher01', 150000, 7500, 142500, 'click', 'paid',      'Buyurtma #1042', NOW() - INTERVAL '25 days', FALSE, NOW() - INTERVAL '25 days', NOW() - INTERVAL '25 days'),
    ('55555555-5555-5555-5555-000000000002', '22222222-2222-2222-2222-000000000001', '11111111-1111-1111-1111-000000000001', 100002, 'Kamola',   'kamola02',  85000,  4250, 80750,  'payme', 'paid',      'Buyurtma #1043', NOW() - INTERVAL '22 days', FALSE, NOW() - INTERVAL '22 days', NOW() - INTERVAL '22 days'),
    ('55555555-5555-5555-5555-000000000003', '22222222-2222-2222-2222-000000000002', '11111111-1111-1111-1111-000000000003', 100003, 'Ilhom',    'ilhom03',   230000, 11500,218500, 'click', 'paid',      'Yetkazib berish', NOW() - INTERVAL '20 days', FALSE, NOW() - INTERVAL '20 days', NOW() - INTERVAL '20 days'),
    ('55555555-5555-5555-5555-000000000004', '22222222-2222-2222-2222-000000000002', '11111111-1111-1111-1111-000000000003', 100004, 'Zebo',     'zebo04',    60000,  3000, 57000,  'click', 'cancelled', 'Bekor qilindi',   NULL,                        FALSE, NOW() - INTERVAL '18 days', NOW() - INTERVAL '18 days'),
    ('55555555-5555-5555-5555-000000000005', '22222222-2222-2222-2222-000000000004', '11111111-1111-1111-1111-000000000006', 100005, 'Dostonbek','doston05',  450000, 22500,427500, 'payme', 'paid',      'Kurs to''lovi',   NOW() - INTERVAL '16 days', FALSE, NOW() - INTERVAL '16 days', NOW() - INTERVAL '16 days'),
    ('55555555-5555-5555-5555-000000000006', '22222222-2222-2222-2222-000000000005', '11111111-1111-1111-1111-000000000007', 100006, 'Ozoda',    'ozoda06',   95000,  4750, 90250,  'click', 'paid',      'Stol band qilish', NOW() - INTERVAL '14 days', FALSE, NOW() - INTERVAL '14 days', NOW() - INTERVAL '14 days'),
    ('55555555-5555-5555-5555-000000000007', '22222222-2222-2222-2222-000000000006', '11111111-1111-1111-1111-000000000009', 100007, 'Shaxzod',  'shaxzod07', 120000, 6000, 114000, 'click', 'paid',      'Navbat #22',      NOW() - INTERVAL '12 days', FALSE, NOW() - INTERVAL '12 days', NOW() - INTERVAL '12 days'),
    ('55555555-5555-5555-5555-000000000008', '22222222-2222-2222-2222-000000000007', '11111111-1111-1111-1111-000000000011', 100008, 'Rustam',   'rustam08',  300000, 15000,285000, 'payme', 'paid',      'Oylik a''zolik',  NOW() - INTERVAL '9 days',  FALSE, NOW() - INTERVAL '9 days',  NOW() - INTERVAL '9 days'),
    ('55555555-5555-5555-5555-000000000009', '22222222-2222-2222-2222-000000000008', '11111111-1111-1111-1111-000000000004', 100009, 'Lobar',    'lobar09',   175000, 8750, 166250, 'click', 'paid',      'Video kurs',      NOW() - INTERVAL '7 days',  FALSE, NOW() - INTERVAL '7 days',  NOW() - INTERVAL '7 days'),
    ('55555555-5555-5555-5555-000000000010', '22222222-2222-2222-2222-000000000009', '11111111-1111-1111-1111-000000000013', 100010, 'Aziza',    'aziza10',   50000,  2500, 47500,  'click', 'created',   'Kutilmoqda',      NULL,                        FALSE, NOW() - INTERVAL '4 days',  NOW() - INTERVAL '4 days'),
    ('55555555-5555-5555-5555-000000000011', '22222222-2222-2222-2222-000000000010', '11111111-1111-1111-1111-000000000015', 100011, 'Jahongir', 'jahongir11',65000,  3250, 61750,  'click', 'paid',      'Kassa hisoboti',  NOW() - INTERVAL '1 day',   FALSE, NOW() - INTERVAL '1 day',   NOW() - INTERVAL '1 day'),
    ('55555555-5555-5555-5555-000000000012', '22222222-2222-2222-2222-000000000001', '11111111-1111-1111-1111-000000000001', 100012, 'Sitora',   'sitora12',  110000, 5500, 104500, 'payme', 'refunded',  'Qaytarildi',      NOW() - INTERVAL '10 days', FALSE, NOW() - INTERVAL '10 days', NOW() - INTERVAL '9 days')
ON CONFLICT (id) DO NOTHING;

-- ===================== standalone gateway transactions (8, legacy top-up style) =====================
INSERT INTO transaction (id, user_id, amount, status, created_at, updated_at) VALUES
    ('44444444-4444-4444-4444-000000000001', '11111111-1111-1111-1111-000000000001', 200000, TRUE,  NOW() - INTERVAL '23 days', NOW() - INTERVAL '23 days'),
    ('44444444-4444-4444-4444-000000000002', '11111111-1111-1111-1111-000000000003', 100000, TRUE,  NOW() - INTERVAL '19 days', NOW() - INTERVAL '19 days'),
    ('44444444-4444-4444-4444-000000000003', '11111111-1111-1111-1111-000000000004', 500000, TRUE,  NOW() - INTERVAL '17 days', NOW() - INTERVAL '17 days'),
    ('44444444-4444-4444-4444-000000000004', '11111111-1111-1111-1111-000000000006', 750000, TRUE,  NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days'),
    ('44444444-4444-4444-4444-000000000005', '11111111-1111-1111-1111-000000000007', 30000,  FALSE, NOW() - INTERVAL '12 days', NOW() - INTERVAL '12 days'),
    ('44444444-4444-4444-4444-000000000006', '11111111-1111-1111-1111-000000000009', 220000, TRUE,  NOW() - INTERVAL '8 days',  NOW() - INTERVAL '8 days'),
    ('44444444-4444-4444-4444-000000000007', '11111111-1111-1111-1111-000000000011', 180000, TRUE,  NOW() - INTERVAL '5 days',  NOW() - INTERVAL '5 days'),
    ('44444444-4444-4444-4444-000000000008', '11111111-1111-1111-1111-000000000015', 20000,  FALSE, NOW() - INTERVAL '1 day',   NOW() - INTERVAL '1 day')
ON CONFLICT (id) DO NOTHING;

-- ===================== template categories & features =====================
INSERT INTO template_category (id, name) VALUES
    ('77777777-7777-7777-7777-000000000001', 'Savdo'),
    ('77777777-7777-7777-7777-000000000002', 'Ta''lim'),
    ('77777777-7777-7777-7777-000000000003', 'Xizmat ko''rsatish'),
    ('77777777-7777-7777-7777-000000000004', 'Marketing')
ON CONFLICT (id) DO NOTHING;

INSERT INTO template_feature (id, name, value) VALUES
    ('78787878-7878-7878-7878-000000000001', 'To''lov integratsiyasi', 'click,payme'),
    ('78787878-7878-7878-7878-000000000002', 'AI yordamchi', 'gpt'),
    ('78787878-7878-7878-7878-000000000003', 'Statistika paneli', NULL)
ON CONFLICT (id) DO NOTHING;

-- Dedicated source projects for the published templates (is_template=true).
INSERT INTO projects (id, name, description, username, owner_id, flow, is_template, created_at, updated_at) VALUES
    ('22222222-2222-2222-2222-000000000101', 'Shablon: Do''kon',    'Savdo shabloni manba loyihasi',  'tpl_dokon',    '11111111-1111-1111-1111-000000000001', '[]'::jsonb, TRUE, NOW() - INTERVAL '20 days', NOW() - INTERVAL '20 days'),
    ('22222222-2222-2222-2222-000000000102', 'Shablon: Ta''lim',    'Ta''lim shabloni manba loyihasi', 'tpl_talim',    '11111111-1111-1111-1111-000000000006', '[]'::jsonb, TRUE, NOW() - INTERVAL '18 days', NOW() - INTERVAL '18 days'),
    ('22222222-2222-2222-2222-000000000103', 'Shablon: Restoran',   'Xizmat shabloni manba loyihasi',  'tpl_restoran', '11111111-1111-1111-1111-000000000007', '[]'::jsonb, TRUE, NOW() - INTERVAL '15 days', NOW() - INTERVAL '15 days'),
    ('22222222-2222-2222-2222-000000000104', 'Shablon: Marketing',  'Marketing shabloni manba loyihasi','tpl_marketing','11111111-1111-1111-1111-000000000011', '[]'::jsonb, TRUE, NOW() - INTERVAL '12 days', NOW() - INTERVAL '12 days'),
    ('22222222-2222-2222-2222-000000000105', 'Shablon: Fitness',    'Xizmat shabloni manba loyihasi',  'tpl_fitness',  '11111111-1111-1111-1111-000000000009', '[]'::jsonb, TRUE, NOW() - INTERVAL '9 days',  NOW() - INTERVAL '9 days')
ON CONFLICT (id) DO NOTHING;

-- ===================== templates (5) =====================
INSERT INTO template (id, author_id, project_id, name, slug, image, short_description, description, original_price, discount, discount_type, views, is_verify, demo_url, created_at, updated_at) VALUES
    ('79797979-7979-7979-7979-000000000001', '11111111-1111-1111-1111-000000000001', '22222222-2222-2222-2222-000000000101', 'Do''kon boti shabloni',   'dokon-bot-shabloni',   '', 'Onlayn do''kon uchun tayyor bot', 'To''liq funksional savdo boti shabloni: katalog, savat, to''lov.', 250000, 10, 'percent', 342, TRUE,  'https://demo.botmother.uz/dokon',   NOW() - INTERVAL '19 days', NOW() - INTERVAL '5 days'),
    ('79797979-7979-7979-7979-000000000002', '11111111-1111-1111-1111-000000000006', '22222222-2222-2222-2222-000000000102', 'Ta''lim markazi shabloni','talim-markazi-shabloni','', 'O''quv markazlari uchun bot',      'Kursga yozilish, jadval va eslatmalar bilan to''liq shablon.',      180000, 0,  'percent', 210, TRUE,  'https://demo.botmother.uz/talim',   NOW() - INTERVAL '17 days', NOW() - INTERVAL '4 days'),
    ('79797979-7979-7979-7979-000000000003', '11111111-1111-1111-1111-000000000007', '22222222-2222-2222-2222-000000000103', 'Restoran buyurtma shabloni','restoran-buyurtma-shabloni','', 'Restoran va kafelar uchun bot', 'Menyu, buyurtma va yetkazib berish nazorati.',                     220000, 15, 'percent', 156, TRUE,  'https://demo.botmother.uz/restoran',NOW() - INTERVAL '14 days', NOW() - INTERVAL '3 days'),
    ('79797979-7979-7979-7979-000000000004', '11111111-1111-1111-1111-000000000011','22222222-2222-2222-2222-000000000104', 'Marketing avtomatlashtirish','marketing-avtomatlashtirish','', 'Ommaviy xabar va aksiyalar boti', 'Segmentlash va avtomatik xabarnoma yuborish moduli bilan.',        300000, 0,  'percent', 88,  FALSE, '',                                   NOW() - INTERVAL '11 days', NOW() - INTERVAL '2 days'),
    ('79797979-7979-7979-7979-000000000005', '11111111-1111-1111-1111-000000000009','22222222-2222-2222-2222-000000000105', 'Fitness klub shabloni',   'fitness-klub-shabloni','', 'Fitness klublar uchun bron tizimi', 'A''zolik, bron va to''lov kuzatuvi bilan tayyor shablon.',         190000, 5,  'percent', 64,  TRUE,  'https://demo.botmother.uz/fitness', NOW() - INTERVAL '8 days',  NOW() - INTERVAL '1 day')
ON CONFLICT (id) DO NOTHING;

INSERT INTO template_category_map (template_id, category_id) VALUES
    ('79797979-7979-7979-7979-000000000001', '77777777-7777-7777-7777-000000000001'),
    ('79797979-7979-7979-7979-000000000002', '77777777-7777-7777-7777-000000000002'),
    ('79797979-7979-7979-7979-000000000003', '77777777-7777-7777-7777-000000000003'),
    ('79797979-7979-7979-7979-000000000004', '77777777-7777-7777-7777-000000000004'),
    ('79797979-7979-7979-7979-000000000005', '77777777-7777-7777-7777-000000000003')
ON CONFLICT DO NOTHING;

INSERT INTO template_feature_map (template_id, feature_id) VALUES
    ('79797979-7979-7979-7979-000000000001', '78787878-7878-7878-7878-000000000001'),
    ('79797979-7979-7979-7979-000000000002', '78787878-7878-7878-7878-000000000003'),
    ('79797979-7979-7979-7979-000000000003', '78787878-7878-7878-7878-000000000001'),
    ('79797979-7979-7979-7979-000000000005', '78787878-7878-7878-7878-000000000002')
ON CONFLICT DO NOTHING;

-- A couple of purchases, to give the admin user-detail page something to show.
INSERT INTO usertemplate (user_id, template_id, created_at, updated_at) VALUES
    ('11111111-1111-1111-1111-000000000003', '79797979-7979-7979-7979-000000000001', NOW() - INTERVAL '10 days', NOW() - INTERVAL '10 days'),
    ('11111111-1111-1111-1111-000000000009', '79797979-7979-7979-7979-000000000002', NOW() - INTERVAL '6 days',  NOW() - INTERVAL '6 days')
ON CONFLICT DO NOTHING;

INSERT INTO template_order (id, user_id, template_id, amount, status, payment_status, commission_paid, created_at, updated_at) VALUES
    ('7c7c7c7c-7c7c-7c7c-7c7c-000000000001', '11111111-1111-1111-1111-000000000003', '79797979-7979-7979-7979-000000000001', 225000, 'confirm', TRUE, TRUE, NOW() - INTERVAL '10 days', NOW() - INTERVAL '10 days'),
    ('7c7c7c7c-7c7c-7c7c-7c7c-000000000002', '11111111-1111-1111-1111-000000000009', '79797979-7979-7979-7979-000000000002', 180000, 'confirm', TRUE, TRUE, NOW() - INTERVAL '6 days',  NOW() - INTERVAL '6 days')
ON CONFLICT (id) DO NOTHING;

-- ===================== template submissions (review queue, 3) =====================
INSERT INTO template_submission_request (id, user_id, project_id, name, short_description, description, original_price, status, rejection_reason, created_at, updated_at) VALUES
    ('7a7a7a7a-7a7a-7a7a-7a7a-000000000001', '11111111-1111-1111-1111-000000000013', '22222222-2222-2222-2222-000000000009', 'Ijtimoiy tarmoq avtoposting shabloni', 'Avtomatik post joylash boti', 'Ijtimoiy tarmoqlarga rejalashtirilgan post joylash imkoniyati.', 150000, 'pending',  NULL, NOW() - INTERVAL '3 days', NOW() - INTERVAL '3 days'),
    ('7a7a7a7a-7a7a-7a7a-7a7a-000000000002', '11111111-1111-1111-1111-000000000015', '22222222-2222-2222-2222-000000000010', 'Kassa va hisobot shabloni',            'Kichik biznes kassa boti',   'Kunlik/oylik hisobotlar va xarajatlar nazorati.',                 130000, 'approved', NULL, NOW() - INTERVAL '7 days', NOW() - INTERVAL '5 days'),
    ('7a7a7a7a-7a7a-7a7a-7a7a-000000000003', '11111111-1111-1111-1111-000000000004', '22222222-2222-2222-2222-000000000003', 'CRM mijozlar shabloni',                'CRM integratsiyasi boti',    'Mijozlar bazasi va follow-up eslatmalari.',                       210000, 'rejected', 'Tavsif yetarli emas, qayta yuboring.', NOW() - INTERVAL '9 days', NOW() - INTERVAL '8 days')
ON CONFLICT (id) DO NOTHING;

-- ===================== withdrawal requests (4) =====================
-- Bot income withdrawals (payment_withdrawal_request).
INSERT INTO payment_withdrawal_request (id, user_id, project_id, amount, card_number, card_owner, status, created_at, updated_at) VALUES
    ('88888888-8888-8888-8888-000000000001', '11111111-1111-1111-1111-000000000001', '22222222-2222-2222-2222-000000000001', 100000, '8600123456781234', 'AZIZ KARIMOV',   'pending',  NOW() - INTERVAL '2 days', NOW() - INTERVAL '2 days'),
    ('88888888-8888-8888-8888-000000000002', '11111111-1111-1111-1111-000000000009', '22222222-2222-2222-2222-000000000006', 80000,  '9860123456785678', 'OTABEK SODIQOV', 'approved', NOW() - INTERVAL '6 days', NOW() - INTERVAL '5 days')
ON CONFLICT (id) DO NOTHING;

-- Template-author earnings withdrawals (template_withdrawal_request).
INSERT INTO template_withdrawal_request (id, user_id, amount, card_number, card_owner, status, created_at, updated_at) VALUES
    ('89898989-8989-8989-8989-000000000001', '11111111-1111-1111-1111-000000000006', 90000, '8600987612345678', 'NODIRA ABDULLAYEVA', 'pending',  NOW() - INTERVAL '4 days',  NOW() - INTERVAL '4 days'),
    ('89898989-8989-8989-8989-000000000002', '11111111-1111-1111-1111-000000000007', 12000, '9860987612348765', 'JASUR MIRZAYEV',     'rejected', NOW() - INTERVAL '11 days', NOW() - INTERVAL '10 days')
ON CONFLICT (id) DO NOTHING;

UPDATE template_withdrawal_request SET rejection_reason = 'Yetarli mablag'' mavjud emas' WHERE id = '89898989-8989-8989-8989-000000000002' AND rejection_reason IS NULL;

-- ===================== support tickets (8) =====================
INSERT INTO tickets (id, user_id, subject, status, created_at, updated_at) VALUES
    ('66666666-6666-6666-6666-000000000001', '11111111-1111-1111-1111-000000000001', 'Botim ishlamayapti',                 'open',    NOW() - INTERVAL '2 days',  NOW() - INTERVAL '2 days'),
    ('66666666-6666-6666-6666-000000000002', '11111111-1111-1111-1111-000000000003', 'To''lov o''tmadi',                    'pending', NOW() - INTERVAL '5 days',  NOW() - INTERVAL '4 days'),
    ('66666666-6666-6666-6666-000000000003', '11111111-1111-1111-1111-000000000004', 'Shablon sotib olishda muammo',       'closed',  NOW() - INTERVAL '12 days', NOW() - INTERVAL '11 days'),
    ('66666666-6666-6666-6666-000000000004', '11111111-1111-1111-1111-000000000006', 'Balans yechishda savol',             'closed',  NOW() - INTERVAL '9 days',  NOW() - INTERVAL '8 days'),
    ('66666666-6666-6666-6666-000000000005', '11111111-1111-1111-1111-000000000007', 'KYC tasdiqlanmayapti',               'open',    NOW() - INTERVAL '1 day',   NOW() - INTERVAL '1 day'),
    ('66666666-6666-6666-6666-000000000006', '11111111-1111-1111-1111-000000000009', 'Bot deploy bo''lmayapti',             'pending', NOW() - INTERVAL '3 days',  NOW() - INTERVAL '2 days'),
    ('66666666-6666-6666-6666-000000000007', '11111111-1111-1111-1111-000000000011','Tarif rejasini o''zgartirish',        'closed',  NOW() - INTERVAL '15 days', NOW() - INTERVAL '14 days'),
    ('66666666-6666-6666-6666-000000000008', '11111111-1111-1111-1111-000000000013','Webhook xatoligi',                    'open',    NOW() - INTERVAL '6 hours', NOW() - INTERVAL '6 hours')
ON CONFLICT (id) DO NOTHING;

INSERT INTO ticket_messages (id, ticket_id, sender, author_id, message, created_at) VALUES
    ('67676767-6767-6767-6767-000000000001', '66666666-6666-6666-6666-000000000001', 'user',  '11111111-1111-1111-1111-000000000001', 'Assalomu alaykum, botim to''xtab qoldi, yordam bering.', NOW() - INTERVAL '2 days'),
    ('67676767-6767-6767-6767-000000000002', '66666666-6666-6666-6666-000000000002', 'user',  '11111111-1111-1111-1111-000000000003', 'Click orqali to''lov qilib bo''lmayapti.',               NOW() - INTERVAL '5 days'),
    ('67676767-6767-6767-6767-000000000003', '66666666-6666-6666-6666-000000000002', 'admin', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'Tekshirib ko''rmoqdamiz, tez orada javob beramiz.',      NOW() - INTERVAL '4 days'),
    ('67676767-6767-6767-6767-000000000004', '66666666-6666-6666-6666-000000000003', 'user',  '11111111-1111-1111-1111-000000000004', 'Shablon sotib olganimda xatolik chiqdi.',                NOW() - INTERVAL '12 days'),
    ('67676767-6767-6767-6767-000000000005', '66666666-6666-6666-6666-000000000003', 'admin', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'Muammo hal qilindi, balansingizga qaytarildi.',          NOW() - INTERVAL '11 days'),
    ('67676767-6767-6767-6767-000000000006', '66666666-6666-6666-6666-000000000008', 'user',  '11111111-1111-1111-1111-000000000013','Webhook 500 xato qaytaryapti.',                           NOW() - INTERVAL '6 hours')
ON CONFLICT (id) DO NOTHING;

-- ===================== audit log (8) =====================
INSERT INTO audit_log (id, actor_id, action, target, metadata, created_at) VALUES
    ('99999999-9999-9999-9999-000000000001', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'user.role_change',       'user:11111111-1111-1111-1111-000000000002', '{"role":"admin"}'::jsonb,      NOW() - INTERVAL '20 days'),
    ('99999999-9999-9999-9999-000000000002', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'user.role_change',       'user:11111111-1111-1111-1111-000000000008', '{"role":"admin"}'::jsonb,      NOW() - INTERVAL '18 days'),
    ('99999999-9999-9999-9999-000000000003', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'template_submission.approve', 'submission:7a7a7a7a-7a7a-7a7a-7a7a-000000000002', '{}'::jsonb,       NOW() - INTERVAL '5 days'),
    ('99999999-9999-9999-9999-000000000004', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'template_submission.reject',  'submission:7a7a7a7a-7a7a-7a7a-7a7a-000000000003', '{"reason":"Tavsif yetarli emas"}'::jsonb, NOW() - INTERVAL '8 days'),
    ('99999999-9999-9999-9999-000000000005', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'withdrawal.approve',     'withdrawal:88888888-8888-8888-8888-000000000002', '{"amount":"80000"}'::jsonb,   NOW() - INTERVAL '5 days'),
    ('99999999-9999-9999-9999-000000000006', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'withdrawal.reject',      'withdrawal:89898989-8989-8989-8989-000000000002', '{"reason":"insufficient_balance"}'::jsonb, NOW() - INTERVAL '10 days'),
    ('99999999-9999-9999-9999-000000000007', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'ticket.close',           'ticket:66666666-6666-6666-6666-000000000003',     '{}'::jsonb,                   NOW() - INTERVAL '11 days'),
    ('99999999-9999-9999-9999-000000000008', '524b9b0f-34ee-4eca-a2d4-52d4cace5f6c', 'ticket.close',           'ticket:66666666-6666-6666-6666-000000000007',     '{}'::jsonb,                   NOW() - INTERVAL '14 days')
ON CONFLICT (id) DO NOTHING;
