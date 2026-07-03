-- Seed the default pricing plans (idempotent). Mirrors the Django
-- create_pricing_plans management command. A user with no subscription is
-- auto-assigned FREE, so at least this row must exist for the app to work.
INSERT INTO plan (name, price, max_bots, max_tokens, description, is_top, sort_order)
VALUES
    ('free', 0.00,      1, 1000000,  'Bepul tarif — 1 ta bot, oyiga 1M AI token.',        FALSE, 1),
    ('pro',  120000.00, 5, 10000000, 'Pro tarifi — 5 ta bot, oyiga 10M AI token.',        TRUE,  2),
    ('max',  240000.00, 0, 50000000, 'Max tarifi — cheksiz bot, oyiga 50M AI token.',     FALSE, 3)
ON CONFLICT (name) DO NOTHING;
