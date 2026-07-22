CREATE TABLE kanban_boards (
    id              UUID PRIMARY KEY,
    owner_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            VARCHAR(120) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    icon            VARCHAR(40) NOT NULL DEFAULT 'layout-kanban',
    color           VARCHAR(20) NOT NULL DEFAULT '#6366f1',
    card_type_label VARCHAR(80) NOT NULL DEFAULT 'Karta',
    field_schema    JSONB NOT NULL DEFAULT '[]'::jsonb,
    archived_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX kanban_boards_owner_idx
    ON kanban_boards(owner_id, updated_at DESC)
    WHERE archived_at IS NULL;

CREATE TABLE kanban_board_projects (
    board_id   UUID NOT NULL REFERENCES kanban_boards(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (board_id, project_id)
);

CREATE INDEX kanban_board_projects_project_idx ON kanban_board_projects(project_id);

CREATE TABLE kanban_columns (
    id          UUID PRIMARY KEY,
    board_id    UUID NOT NULL REFERENCES kanban_boards(id) ON DELETE CASCADE,
    name        VARCHAR(100) NOT NULL,
    color       VARCHAR(20) NOT NULL DEFAULT '#64748b',
    position    INTEGER NOT NULL,
    wip_limit   INTEGER CHECK (wip_limit IS NULL OR wip_limit > 0),
    is_terminal BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (board_id, position)
);

CREATE TABLE kanban_cards (
    id                UUID PRIMARY KEY,
    board_id          UUID NOT NULL REFERENCES kanban_boards(id) ON DELETE CASCADE,
    column_id         UUID NOT NULL REFERENCES kanban_columns(id) ON DELETE CASCADE,
    source_project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    kind              VARCHAR(40) NOT NULL DEFAULT 'item',
    title             VARCHAR(240) NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    priority          VARCHAR(16) NOT NULL DEFAULT 'normal'
                      CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    position          BIGINT NOT NULL,
    due_at            TIMESTAMPTZ,
    external_key      VARCHAR(240),
    custom_fields     JSONB NOT NULL DEFAULT '{}'::jsonb,
    archived_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX kanban_cards_column_idx
    ON kanban_cards(column_id, position)
    WHERE archived_at IS NULL;
CREATE INDEX kanban_cards_project_idx
    ON kanban_cards(source_project_id, updated_at DESC)
    WHERE archived_at IS NULL;
CREATE INDEX kanban_cards_due_idx
    ON kanban_cards(due_at)
    WHERE due_at IS NOT NULL AND archived_at IS NULL;
CREATE UNIQUE INDEX kanban_cards_external_key_uq
    ON kanban_cards(board_id, source_project_id, external_key)
    WHERE external_key IS NOT NULL AND archived_at IS NULL;

CREATE TABLE kanban_activities (
    id         UUID PRIMARY KEY,
    card_id    UUID NOT NULL REFERENCES kanban_cards(id) ON DELETE CASCADE,
    actor_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    event      VARCHAR(60) NOT NULL,
    data       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX kanban_activities_card_idx ON kanban_activities(card_id, created_at DESC);

CREATE TABLE kanban_comments (
    id         UUID PRIMARY KEY,
    card_id    UUID NOT NULL REFERENCES kanban_cards(id) ON DELETE CASCADE,
    author_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX kanban_comments_card_idx ON kanban_comments(card_id, created_at);
