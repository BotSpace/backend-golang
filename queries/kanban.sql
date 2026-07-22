-- name: CreateKanbanBoard :one
INSERT INTO kanban_boards (id, owner_id, name, description, icon, color, card_type_label, field_schema)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListKanbanBoards :many
SELECT b.*,
       COUNT(DISTINCT c.id)::bigint AS card_count,
       COALESCE(array_agg(DISTINCT bp.project_id) FILTER (WHERE bp.project_id IS NOT NULL), ARRAY[]::uuid[])::uuid[] AS project_ids
FROM kanban_boards b
LEFT JOIN kanban_board_projects bp ON bp.board_id = b.id
LEFT JOIN kanban_cards c ON c.board_id = b.id AND c.archived_at IS NULL
WHERE b.owner_id = sqlc.arg('owner_id')
  AND b.archived_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR EXISTS (
      SELECT 1 FROM kanban_board_projects x WHERE x.board_id = b.id AND x.project_id = sqlc.narg('project_id')
  ))
GROUP BY b.id
ORDER BY b.updated_at DESC, b.id;

-- name: GetKanbanBoardForOwner :one
SELECT * FROM kanban_boards WHERE id = $1 AND owner_id = $2 AND archived_at IS NULL;

-- name: UpdateKanbanBoard :one
UPDATE kanban_boards SET
    name = $3, description = $4, icon = $5, color = $6,
    card_type_label = $7, field_schema = $8, updated_at = NOW()
WHERE id = $1 AND owner_id = $2 AND archived_at IS NULL
RETURNING *;

-- name: ArchiveKanbanBoard :execrows
UPDATE kanban_boards SET archived_at = NOW(), updated_at = NOW()
WHERE id = $1 AND owner_id = $2 AND archived_at IS NULL;

-- name: LinkKanbanBoardProject :execrows
INSERT INTO kanban_board_projects (board_id, project_id)
SELECT sqlc.arg('board_id'), p.id
FROM projects p JOIN kanban_boards b ON b.id = sqlc.arg('board_id')
WHERE p.id = sqlc.arg('project_id') AND p.owner_id = sqlc.arg('owner_id')
  AND b.owner_id = sqlc.arg('owner_id') AND b.archived_at IS NULL
ON CONFLICT (board_id, project_id) DO UPDATE
SET created_at = kanban_board_projects.created_at;

-- name: UnlinkKanbanBoardProject :execrows
DELETE FROM kanban_board_projects bp
USING kanban_boards b
WHERE bp.board_id = b.id AND bp.board_id = sqlc.arg('board_id')
  AND bp.project_id = sqlc.arg('project_id') AND b.owner_id = sqlc.arg('owner_id');

-- name: ListKanbanBoardProjects :many
SELECT p.id, p.name
FROM projects p JOIN kanban_board_projects bp ON bp.project_id = p.id
JOIN kanban_boards b ON b.id = bp.board_id
WHERE bp.board_id = $1 AND b.owner_id = $2
ORDER BY p.name, p.id;

-- name: CreateKanbanColumn :one
INSERT INTO kanban_columns (id, board_id, name, color, position, wip_limit, is_terminal)
SELECT sqlc.arg('id'), b.id, sqlc.arg('name'), sqlc.arg('color'), sqlc.arg('position'), sqlc.narg('wip_limit'), sqlc.arg('is_terminal')
FROM kanban_boards b WHERE b.id = sqlc.arg('board_id') AND b.owner_id = sqlc.arg('owner_id') AND b.archived_at IS NULL
RETURNING *;

-- name: ListKanbanColumns :many
SELECT c.* FROM kanban_columns c JOIN kanban_boards b ON b.id = c.board_id
WHERE c.board_id = $1 AND b.owner_id = $2 ORDER BY c.position, c.id;

-- name: UpdateKanbanColumn :one
UPDATE kanban_columns c SET name = $3, color = $4, wip_limit = sqlc.narg('wip_limit'), is_terminal = $5, updated_at = NOW()
FROM kanban_boards b
WHERE c.id = $1 AND c.board_id = b.id AND b.owner_id = $2 AND b.archived_at IS NULL
RETURNING c.*;

-- name: DeleteKanbanColumn :execrows
DELETE FROM kanban_columns c USING kanban_boards b
WHERE c.id = $1 AND c.board_id = b.id AND b.owner_id = $2
  AND NOT EXISTS (SELECT 1 FROM kanban_cards k WHERE k.column_id = c.id AND k.archived_at IS NULL);

-- name: NextKanbanCardPosition :one
SELECT COALESCE(MAX(c.position), 0)::bigint + 1024
FROM kanban_cards c JOIN kanban_boards b ON b.id = c.board_id
WHERE c.column_id = $1 AND b.owner_id = $2 AND c.archived_at IS NULL;

-- name: CreateKanbanCard :one
INSERT INTO kanban_cards (id, board_id, column_id, source_project_id, kind, title, description, priority, position, due_at, external_key, custom_fields)
SELECT sqlc.arg('id'), b.id, c.id, sqlc.narg('source_project_id'), sqlc.arg('kind'), sqlc.arg('title'), sqlc.arg('description'),
       sqlc.arg('priority'), sqlc.arg('position'), sqlc.narg('due_at'), sqlc.narg('external_key'), sqlc.arg('custom_fields')
FROM kanban_boards b JOIN kanban_columns c ON c.board_id = b.id
WHERE b.id = sqlc.arg('board_id') AND c.id = sqlc.arg('column_id') AND b.owner_id = sqlc.arg('owner_id') AND b.archived_at IS NULL
  AND (c.wip_limit IS NULL OR (SELECT COUNT(*) FROM kanban_cards k WHERE k.column_id = c.id AND k.archived_at IS NULL) < c.wip_limit)
  AND (sqlc.narg('source_project_id')::uuid IS NULL OR EXISTS (
      SELECT 1 FROM kanban_board_projects bp
      JOIN projects p ON p.id = bp.project_id
      WHERE bp.board_id = b.id AND bp.project_id = sqlc.narg('source_project_id') AND p.owner_id = b.owner_id
  ))
RETURNING *;

-- name: GetKanbanCardForOwner :one
SELECT c.* FROM kanban_cards c JOIN kanban_boards b ON b.id = c.board_id
WHERE c.id = $1 AND b.owner_id = $2 AND c.archived_at IS NULL;

-- name: ListKanbanCards :many
SELECT c.* FROM kanban_cards c JOIN kanban_boards b ON b.id = c.board_id
WHERE c.board_id = sqlc.arg('board_id') AND b.owner_id = sqlc.arg('owner_id') AND c.archived_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR c.source_project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('search')::text IS NULL OR c.title ILIKE '%' || sqlc.narg('search') || '%' OR c.description ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY c.column_id, c.position, c.id;

-- name: UpdateKanbanCard :one
UPDATE kanban_cards c SET
    title = sqlc.arg('title'), description = sqlc.arg('description'), kind = sqlc.arg('kind'),
    priority = sqlc.arg('priority'), due_at = sqlc.narg('due_at'), custom_fields = sqlc.arg('custom_fields'), updated_at = NOW()
FROM kanban_boards b
WHERE c.id = sqlc.arg('id') AND c.board_id = b.id AND b.owner_id = sqlc.arg('owner_id') AND c.archived_at IS NULL
RETURNING c.*;

-- name: MoveKanbanCard :one
UPDATE kanban_cards c SET column_id = sqlc.arg('column_id'), position = sqlc.arg('position'), updated_at = NOW()
FROM kanban_boards b, kanban_columns col
WHERE c.id = sqlc.arg('id') AND c.board_id = b.id AND b.owner_id = sqlc.arg('owner_id')
  AND col.id = sqlc.arg('column_id') AND col.board_id = c.board_id AND c.archived_at IS NULL
  AND (c.column_id = col.id OR col.wip_limit IS NULL OR (SELECT COUNT(*) FROM kanban_cards k WHERE k.column_id = col.id AND k.archived_at IS NULL) < col.wip_limit)
RETURNING c.*;

-- name: ArchiveKanbanCard :one
UPDATE kanban_cards c SET archived_at = NOW(), updated_at = NOW()
FROM kanban_boards b
WHERE c.id = $1 AND c.board_id = b.id AND b.owner_id = $2 AND c.archived_at IS NULL
RETURNING c.*;

-- name: GetKanbanCardByExternalKey :one
SELECT c.* FROM kanban_cards c
JOIN kanban_board_projects bp ON bp.board_id = c.board_id AND bp.project_id = sqlc.arg('project_id')
WHERE c.board_id = sqlc.arg('board_id') AND c.source_project_id = sqlc.arg('project_id')
  AND c.external_key = sqlc.arg('external_key') AND c.archived_at IS NULL;

-- name: GetLinkedKanbanBoardForProject :one
SELECT b.* FROM kanban_boards b JOIN kanban_board_projects bp ON bp.board_id = b.id
WHERE b.id = $1 AND bp.project_id = $2 AND b.archived_at IS NULL;

-- name: GetKanbanColumnForBoard :one
SELECT * FROM kanban_columns WHERE id = $1 AND board_id = $2;

-- name: UpdateInternalKanbanCard :one
UPDATE kanban_cards SET title = $4, description = $5, kind = $6, priority = $7,
    due_at = sqlc.narg('due_at'), custom_fields = $8, updated_at = NOW()
WHERE id = $1 AND board_id = $2 AND source_project_id = $3 AND archived_at IS NULL
RETURNING *;

-- name: CreateKanbanActivity :one
INSERT INTO kanban_activities (id, card_id, actor_id, event, data)
VALUES ($1, $2, sqlc.narg('actor_id'), $3, $4) RETURNING *;

-- name: ListKanbanActivities :many
SELECT a.* FROM kanban_activities a JOIN kanban_cards c ON c.id = a.card_id
JOIN kanban_boards b ON b.id = c.board_id
WHERE a.card_id = $1 AND b.owner_id = $2 ORDER BY a.created_at DESC, a.id DESC LIMIT 100;

-- name: CreateKanbanComment :one
INSERT INTO kanban_comments (id, card_id, author_id, body)
SELECT sqlc.arg('id'), c.id, sqlc.arg('author_id'), sqlc.arg('body')
FROM kanban_cards c JOIN kanban_boards b ON b.id = c.board_id
WHERE c.id = sqlc.arg('card_id') AND b.owner_id = sqlc.arg('author_id') AND c.archived_at IS NULL
RETURNING *;

-- name: ListKanbanComments :many
SELECT m.* FROM kanban_comments m JOIN kanban_cards c ON c.id = m.card_id
JOIN kanban_boards b ON b.id = c.board_id
WHERE m.card_id = $1 AND b.owner_id = $2 ORDER BY m.created_at, m.id;
