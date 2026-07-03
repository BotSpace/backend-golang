-- Payment domain: gateway transaction ledger, bot payment orders, bot income
-- withdrawal requests, and the balance ledger (history). Mirrors Django
-- core.apps.payment (TransactionModel, PaymentOrderModel,
-- WithdrawalRequestModel, HistoryModel).

-- Gateway transaction ledger row. Click/Payme identify the payer's item via
-- this row's id (their "merchant_trans_id" / "account.order_id"). A
-- transaction may be linked 1:1 to a payment_order (bot payment flow) or
-- stand alone (legacy direct balance top-up flow).
CREATE TABLE IF NOT EXISTS transaction (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    amount           NUMERIC(20, 2) NOT NULL,
    status           BOOLEAN NOT NULL DEFAULT FALSE,
    idempotency_key  TEXT UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_transaction_user ON transaction (user_id) WHERE deleted_at IS NULL;

-- Bot payment order. Flow: engine -> payment/order/create -> Click/Payme
-- pay_url -> bot user pays -> provider webhook -> owner net credit +
-- Botspace commission -> engine notified.
CREATE TABLE IF NOT EXISTS payment_order (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    owner_id          UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    payer_chat_id     BIGINT NOT NULL,
    payer_first_name  TEXT,
    payer_last_name   TEXT,
    payer_username    TEXT,
    amount            NUMERIC(20, 2) NOT NULL,
    commission        NUMERIC(20, 2) NOT NULL DEFAULT 0,
    net_amount        NUMERIC(20, 2) NOT NULL DEFAULT 0,
    provider          TEXT NOT NULL DEFAULT 'click',
    status            TEXT NOT NULL DEFAULT 'created',
    external_id       TEXT,
    comment           TEXT,
    idempotency_key   TEXT UNIQUE,
    transaction_id    UUID REFERENCES transaction (id) ON DELETE SET NULL,
    paid_at           TIMESTAMPTZ,
    is_debug          BOOLEAN NOT NULL DEFAULT FALSE,
    pay_instruction   TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_payment_order_owner ON payment_order (owner_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_payment_order_project ON payment_order (project_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_payment_order_status ON payment_order (status);
CREATE INDEX IF NOT EXISTS idx_payment_order_transaction ON payment_order (transaction_id);

-- Bot income withdrawal request. amount is deducted from the shared
-- users.balance only when an admin approves; requesting a withdrawal
-- requires the user to already be KYC-verified (users.kyc_verified_at).
CREATE TABLE IF NOT EXISTS payment_withdrawal_request (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id        UUID REFERENCES projects (id) ON DELETE SET NULL,
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

CREATE INDEX IF NOT EXISTS idx_payment_withdrawal_user ON payment_withdrawal_request (user_id);
CREATE INDEX IF NOT EXISTS idx_payment_withdrawal_status ON payment_withdrawal_request (status);

-- Balance ledger: every users.balance mutation from this domain is recorded
-- here first. The unique idempotency_key makes a retried webhook/admin call
-- a safe no-op (INSERT ... ON CONFLICT DO NOTHING).
CREATE TABLE IF NOT EXISTS history (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID REFERENCES users (id) ON DELETE SET NULL,
    amount           NUMERIC(30, 2) NOT NULL,
    comment          TEXT,
    type             TEXT NOT NULL DEFAULT 'income',
    idempotency_key  TEXT UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_history_user ON history (user_id) WHERE deleted_at IS NULL;
