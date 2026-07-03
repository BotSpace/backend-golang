-- KYC verification requests (Django UserVerificationModel / accounts app).
-- A user submits passport + selfie + proof-of-residence; an admin approves or
-- rejects. Approval also sets users.kyc_verified_at (see queries/kyc.sql),
-- which unlocks withdrawals elsewhere in the system.
-- File columns store the relative path under APP_MEDIA_DIR (mirrors Django's
-- FileField.name, which is a MEDIA_ROOT-relative path).
CREATE TABLE IF NOT EXISTS user_verification (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    full_name            TEXT NOT NULL,
    passport_image       TEXT NOT NULL,
    selfie_image         TEXT NOT NULL,
    residence_reference  TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'approved', 'rejected')),
    rejection_reason     TEXT,
    reviewed_by          UUID REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_verification_user ON user_verification (user_id);
CREATE INDEX IF NOT EXISTS idx_user_verification_status ON user_verification (status);
