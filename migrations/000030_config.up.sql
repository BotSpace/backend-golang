-- Admin-editable platform settings: a small key/value JSONB store. No Django
-- equivalent — this unblocks the admin panel's general/AI settings pages,
-- which have no other backing endpoint on this backend (see internal/modules/config).
CREATE TABLE IF NOT EXISTS config (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO config (key, value) VALUES
    ('general', '{
        "site_name": "Botmother",
        "support_email": "support@botmother.uz",
        "support_phone": "+998900000000",
        "maintenance_mode": false,
        "default_language": "uz",
        "commission_percent": 5
    }'::jsonb),
    ('ai', '{
        "provider": "anthropic",
        "model": "claude-sonnet-4-5",
        "api_key": "",
        "max_tokens": 4096,
        "temperature": 0.7,
        "system_prompt_override": ""
    }'::jsonb)
ON CONFLICT (key) DO NOTHING;
