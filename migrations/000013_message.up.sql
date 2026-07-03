-- Broadcast messages sent to bot users (MessageModel). Project-scoped: a
-- message always belongs to exactly one project, and ownership is enforced
-- transitively through projects.owner_id (see queries/message.sql). buttons
-- is a 2D array of {title, callback_data, link, request_contact,
-- request_location} objects (rows x columns of a Telegram keyboard).
-- total/done/fail are broadcast delivery statistics updated by the sender
-- worker; file_id caches the Telegram file_id after the first send so photo
--/video re-broadcasts don't re-upload the file.
CREATE TABLE IF NOT EXISTS message (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    caption     TEXT NOT NULL DEFAULT '',
    message_type TEXT NOT NULL DEFAULT 'text' CHECK (message_type IN ('text', 'photo', 'video')),
    file_id     TEXT,
    button_type TEXT NOT NULL DEFAULT 'reply' CHECK (button_type IN ('inline', 'reply')),
    buttons     JSONB NOT NULL DEFAULT '[]'::jsonb,
    total       BIGINT NOT NULL DEFAULT 0,
    done        BIGINT NOT NULL DEFAULT 0,
    fail        BIGINT NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'progress' CHECK (status IN ('progress', 'done', 'failed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_message_project ON message (project_id);
