-- Support tickets (core.apps.tickets). A ticket is a conversation thread
-- between a user and admin support; ticket_messages holds the individual
-- messages (mirrors TicketModel + TicketMessageModel).
CREATE TABLE IF NOT EXISTS tickets (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    subject    TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'pending', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tickets_user ON tickets (user_id);
CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets (status);
CREATE INDEX IF NOT EXISTS idx_tickets_updated_at ON tickets (updated_at DESC);

CREATE TABLE IF NOT EXISTS ticket_messages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id      UUID NOT NULL REFERENCES tickets (id) ON DELETE CASCADE,
    sender         TEXT NOT NULL DEFAULT 'user' CHECK (sender IN ('user', 'admin')),
    author_id      UUID REFERENCES users (id) ON DELETE SET NULL,
    message        TEXT NOT NULL,
    attachment_url TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket ON ticket_messages (ticket_id, created_at);
