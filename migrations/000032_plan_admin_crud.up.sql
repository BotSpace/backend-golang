-- Admin plan (tarif) CRUD needs to create plans with arbitrary names, not
-- just the three seeded ones (free/pro/max). The original CHECK constraint
-- mirrored Django's PlanType enum, which this port no longer needs to be
-- source-of-truth for once the admin panel manages plans directly.
ALTER TABLE plan DROP CONSTRAINT IF EXISTS plan_name_check;
