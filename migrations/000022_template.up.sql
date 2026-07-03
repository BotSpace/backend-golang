-- Template marketplace (Django core.apps.template). A verified TemplateModel is
-- published to a public catalog; an author submits a project as a template via
-- TemplateSubmissionRequestModel (admin approves -> TemplateModel is created);
-- a buyer creates a TemplateOrderModel and pays for it (commission split: 80%
-- to the author's template_balance, 20% platform commission - mirrors
-- core/apps/template/signals/order.py::OrderSignal); TemplateWithdrawalRequestModel
-- lets an author cash out their template_balance wallet (users.template_balance,
-- see migrations/000001_create_users.up.sql).

CREATE TABLE IF NOT EXISTS template_category (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS template_feature (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    value      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- template.project references api.ProjectModel: legacy templates carry their flow on
-- the linked project; templates created from an approved submission also point here.
-- template.spec (jsonb) is the newer self-contained {"flow": ..., "envs": ...} shape
-- (see services/template_spec.py); required_envs is the legacy/back-compat mirror.
CREATE TABLE IF NOT EXISTS template (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id          UUID REFERENCES users (id) ON DELETE SET NULL,
    project_id         UUID REFERENCES projects (id) ON DELETE SET NULL,
    name               TEXT NOT NULL,
    slug               TEXT NOT NULL UNIQUE,
    image              TEXT NOT NULL DEFAULT '',
    video              TEXT,
    spec               JSONB NOT NULL DEFAULT '{}'::jsonb,
    short_description  TEXT NOT NULL DEFAULT '',
    description        TEXT NOT NULL DEFAULT '',
    original_price     NUMERIC(20, 2) NOT NULL DEFAULT 0,
    discount           NUMERIC(20, 2) NOT NULL DEFAULT 0,
    discount_type      TEXT NOT NULL DEFAULT 'percent',
    views              BIGINT NOT NULL DEFAULT 0,
    version            INTEGER NOT NULL DEFAULT 1,
    is_verify          BOOLEAN NOT NULL DEFAULT FALSE,
    demo_url           TEXT,
    required_envs      JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_template_author ON template (author_id);
CREATE INDEX IF NOT EXISTS idx_template_verified ON template (is_verify) WHERE is_verify = TRUE;

CREATE TABLE IF NOT EXISTS template_category_map (
    template_id UUID NOT NULL REFERENCES template (id) ON DELETE CASCADE,
    category_id UUID NOT NULL REFERENCES template_category (id) ON DELETE CASCADE,
    PRIMARY KEY (template_id, category_id)
);

CREATE TABLE IF NOT EXISTS template_feature_map (
    template_id UUID NOT NULL REFERENCES template (id) ON DELETE CASCADE,
    feature_id  UUID NOT NULL REFERENCES template_feature (id) ON DELETE CASCADE,
    PRIMARY KEY (template_id, feature_id)
);

CREATE TABLE IF NOT EXISTS template_like (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES template (id) ON DELETE CASCADE,
    user_id     UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_template_like_user ON template_like (template_id, user_id) WHERE user_id IS NOT NULL;

-- Purchased-template ownership (Django UserTemplateModel, db_table "usertemplate").
-- Written when a TemplateOrderModel is paid (see TemplatePay usecase).
CREATE TABLE IF NOT EXISTS usertemplate (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    template_id UUID NOT NULL REFERENCES template (id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_usertemplate_user_template ON usertemplate (user_id, template_id);

-- status: pending|confirm|cancel (TemplateOrderStatusEnum). payment_status flips to
-- true once paid (free templates auto-confirm at creation); commission_paid guards the
-- author payout from double-processing (mirrors signals/order.py).
CREATE TABLE IF NOT EXISTS template_order (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID REFERENCES users (id) ON DELETE SET NULL,
    template_id      UUID REFERENCES template (id) ON DELETE SET NULL,
    amount           NUMERIC(20, 2) NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'pending',
    payment_status   BOOLEAN NOT NULL DEFAULT FALSE,
    commission_paid  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_template_order_user ON template_order (user_id);
CREATE INDEX IF NOT EXISTS idx_template_order_template ON template_order (template_id);

-- status: pending|approved|rejected (TemplateSubmissionStatusEnum). template_id is set
-- once an admin approves the request and a TemplateModel row is created from it.
CREATE TABLE IF NOT EXISTS template_submission_request (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id         UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    short_description  TEXT NOT NULL DEFAULT '',
    description        TEXT NOT NULL DEFAULT '',
    image              TEXT,
    video              TEXT,
    demo_url           TEXT,
    original_price     NUMERIC(20, 2) NOT NULL DEFAULT 0,
    status             TEXT NOT NULL DEFAULT 'pending',
    rejection_reason   TEXT,
    template_id        UUID REFERENCES template (id) ON DELETE SET NULL,
    reviewed_by        UUID REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at        TIMESTAMPTZ,
    required_envs      JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_template_submission_user ON template_submission_request (user_id);
CREATE INDEX IF NOT EXISTS idx_template_submission_status ON template_submission_request (status);

CREATE TABLE IF NOT EXISTS template_submission_category_map (
    submission_id UUID NOT NULL REFERENCES template_submission_request (id) ON DELETE CASCADE,
    category_id   UUID NOT NULL REFERENCES template_category (id) ON DELETE CASCADE,
    PRIMARY KEY (submission_id, category_id)
);

CREATE TABLE IF NOT EXISTS template_submission_feature_map (
    submission_id UUID NOT NULL REFERENCES template_submission_request (id) ON DELETE CASCADE,
    feature_id    UUID NOT NULL REFERENCES template_feature (id) ON DELETE CASCADE,
    PRIMARY KEY (submission_id, feature_id)
);

-- Append-only spec snapshot history (Django TemplateVersionModel). Not yet exposed via
-- HTTP in this port; the table exists so the constructor's future rollback/history UI
-- has schema parity with Django.
CREATE TABLE IF NOT EXISTS template_version (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES template (id) ON DELETE CASCADE,
    version    INTEGER NOT NULL,
    spec       JSONB NOT NULL DEFAULT '{}'::jsonb,
    note       TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_template_version ON template_version (template_id, version);

-- status: pending|approved|rejected (WithdrawalStatusEnum). Approval deducts
-- users.template_balance (mirrors admin/withdrawal.py::approve_withdrawals).
CREATE TABLE IF NOT EXISTS template_withdrawal_request (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    amount            NUMERIC(20, 2) NOT NULL,
    card_number       TEXT NOT NULL,
    card_owner        TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT,
    reviewed_by       UUID REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_template_withdrawal_user ON template_withdrawal_request (user_id);
CREATE INDEX IF NOT EXISTS idx_template_withdrawal_status ON template_withdrawal_request (status);
