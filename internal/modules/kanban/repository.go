package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errNotFound           = errors.New("kanban resource not found")
	errWIPLimit           = errors.New("kanban column WIP limit reached")
	errInvalidColumnOrder = errors.New("kanban invalid column order")
	errRestoreConflict    = errors.New("kanban restore external key conflict")
)

type pgRepo struct {
	pool *pgxpool.Pool
	q    *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{pool: pool, q: sqlcdb.New(pool)}
}

func (r *pgRepo) ResolveProjectRef(ctx context.Context, rawRef string) (uuid.UUID, error) {
	if legacy, err := strconv.ParseInt(rawRef, 10, 32); err == nil {
		value := int32(legacy)
		id, queryErr := r.q.ResolveKanbanProjectByLegacyRef(ctx, &value)
		if queryErr != nil {
			return uuid.Nil, mapRepoErr(queryErr)
		}
		return id, nil
	}
	parsed, err := uuid.Parse(rawRef)
	if err != nil {
		return uuid.Nil, errNotFound
	}
	id, err := r.q.ResolveKanbanProjectByUUIDRef(ctx, parsed)
	if err != nil {
		return uuid.Nil, mapRepoErr(err)
	}
	return id, nil
}

func (r *pgRepo) CreateBoard(ctx context.Context, in CreateBoardInput) (*Board, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin board create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	row, err := q.CreateKanbanBoard(ctx, sqlcdb.CreateKanbanBoardParams{
		ID: uuid.New(), OwnerID: in.OwnerID, Name: in.Name, Description: in.Description,
		Icon: in.Icon, Color: in.Color, CardTypeLabel: in.CardTypeLabel, FieldSchema: in.FieldSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("create board: %w", err)
	}
	for i, col := range in.Columns {
		if _, err = q.CreateKanbanColumn(ctx, sqlcdb.CreateKanbanColumnParams{
			ID: uuid.New(), BoardID: row.ID, OwnerID: in.OwnerID, Name: col.Name, Color: col.Color,
			Position: int32(i), WipLimit: col.WIPLimit, IsTerminal: col.IsTerminal,
		}); err != nil {
			return nil, fmt.Errorf("create board column: %w", err)
		}
	}
	for _, projectID := range in.ProjectIDs {
		if _, err = q.LinkKanbanBoardProject(ctx, sqlcdb.LinkKanbanBoardProjectParams{BoardID: row.ID, ProjectID: projectID, OwnerID: in.OwnerID}); err != nil {
			return nil, fmt.Errorf("link project: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit board create: %w", err)
	}
	return r.GetBoard(ctx, row.ID, in.OwnerID, nil)
}

func (r *pgRepo) ListBoards(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) ([]Board, error) {
	rows, err := r.q.ListKanbanBoards(ctx, sqlcdb.ListKanbanBoardsParams{OwnerID: ownerID, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	out := make([]Board, len(rows))
	for i, x := range rows {
		out[i] = Board{ID: x.ID, OwnerID: x.OwnerID, Name: x.Name, Description: x.Description, Icon: x.Icon, Color: x.Color, CardTypeLabel: x.CardTypeLabel, FieldSchema: raw(x.FieldSchema, "[]"), CardCount: x.CardCount, ProjectIDs: x.ProjectIds, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
	}
	return out, nil
}

func (r *pgRepo) ListArchivedBoards(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) ([]Board, error) {
	rows, err := r.q.ListArchivedKanbanBoards(ctx, sqlcdb.ListArchivedKanbanBoardsParams{OwnerID: ownerID, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	out := make([]Board, len(rows))
	for i, x := range rows {
		out[i] = Board{ID: x.ID, OwnerID: x.OwnerID, Name: x.Name, Description: x.Description, Icon: x.Icon, Color: x.Color, CardTypeLabel: x.CardTypeLabel, FieldSchema: raw(x.FieldSchema, "[]"), CardCount: x.CardCount, ProjectIDs: x.ProjectIds, ArchivedAt: x.ArchivedAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
	}
	return out, nil
}

func (r *pgRepo) GetBoard(ctx context.Context, id, ownerID uuid.UUID, projectID *uuid.UUID) (*Board, error) {
	x, err := r.q.GetKanbanBoardForOwner(ctx, sqlcdb.GetKanbanBoardForOwnerParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	cols, err := r.q.ListKanbanColumns(ctx, sqlcdb.ListKanbanColumnsParams{BoardID: id, OwnerID: ownerID})
	if err != nil {
		return nil, err
	}
	cards, err := r.q.ListKanbanCards(ctx, sqlcdb.ListKanbanCardsParams{BoardID: id, OwnerID: ownerID, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	projects, err := r.q.ListKanbanBoardProjects(ctx, sqlcdb.ListKanbanBoardProjectsParams{BoardID: id, OwnerID: ownerID})
	if err != nil {
		return nil, err
	}
	b := boardFromRow(x)
	for _, c := range cols {
		b.Columns = append(b.Columns, columnFromRow(c))
	}
	for _, c := range cards {
		b.Cards = append(b.Cards, cardFromRow(c))
	}
	for _, p := range projects {
		b.Projects = append(b.Projects, ProjectLink{ID: p.ID, Name: p.Name})
		b.ProjectIDs = append(b.ProjectIDs, p.ID)
	}
	b.CardCount = int64(len(b.Cards))
	return &b, nil
}

func (r *pgRepo) UpdateBoard(ctx context.Context, b Board) (*Board, error) {
	x, err := r.q.UpdateKanbanBoard(ctx, sqlcdb.UpdateKanbanBoardParams{ID: b.ID, OwnerID: b.OwnerID, Name: b.Name, Description: b.Description, Icon: b.Icon, Color: b.Color, CardTypeLabel: b.CardTypeLabel, FieldSchema: b.FieldSchema})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	out := boardFromRow(x)
	return &out, nil
}
func (r *pgRepo) ArchiveBoard(ctx context.Context, id, ownerID uuid.UUID) error {
	n, e := r.q.ArchiveKanbanBoard(ctx, sqlcdb.ArchiveKanbanBoardParams{ID: id, OwnerID: ownerID})
	if e != nil {
		return e
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}
func (r *pgRepo) RestoreBoard(ctx context.Context, id, ownerID uuid.UUID) (*Board, error) {
	x, e := r.q.RestoreKanbanBoard(ctx, sqlcdb.RestoreKanbanBoardParams{ID: id, OwnerID: ownerID})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	b := boardFromRow(x)
	return &b, nil
}
func (r *pgRepo) LinkProject(ctx context.Context, boardID, projectID, ownerID uuid.UUID) error {
	n, err := r.q.LinkKanbanBoardProject(ctx, sqlcdb.LinkKanbanBoardProjectParams{BoardID: boardID, ProjectID: projectID, OwnerID: ownerID})
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}
func (r *pgRepo) UnlinkProject(ctx context.Context, boardID, projectID, ownerID uuid.UUID) error {
	n, e := r.q.UnlinkKanbanBoardProject(ctx, sqlcdb.UnlinkKanbanBoardProjectParams{BoardID: boardID, ProjectID: projectID, OwnerID: ownerID})
	if e != nil {
		return e
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}

func (r *pgRepo) CreateColumn(ctx context.Context, boardID, ownerID uuid.UUID, in ColumnInput) (*Column, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	if _, e = q.LockKanbanBoardForColumnCreate(ctx, sqlcdb.LockKanbanBoardForColumnCreateParams{ID: boardID, OwnerID: ownerID}); e != nil {
		return nil, mapRepoErr(e)
	}
	cols, e := q.ListKanbanColumns(ctx, sqlcdb.ListKanbanColumnsParams{BoardID: boardID, OwnerID: ownerID})
	if e != nil {
		return nil, e
	}
	txRow, e := q.CreateKanbanColumn(ctx, sqlcdb.CreateKanbanColumnParams{ID: uuid.New(), BoardID: boardID, OwnerID: ownerID, Name: in.Name, Color: in.Color, Position: int32(len(cols)), WipLimit: in.WIPLimit, IsTerminal: in.IsTerminal})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	c := columnFromRow(txRow)
	return &c, nil
}
func (r *pgRepo) UpdateColumn(ctx context.Context, id, ownerID uuid.UUID, in ColumnInput) (*Column, error) {
	x, e := r.q.UpdateKanbanColumn(ctx, sqlcdb.UpdateKanbanColumnParams{ID: id, OwnerID: ownerID, Name: in.Name, Color: in.Color, WipLimit: in.WIPLimit, IsTerminal: in.IsTerminal})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	c := columnFromRow(x)
	return &c, nil
}
func (r *pgRepo) ReorderColumns(ctx context.Context, boardID, ownerID uuid.UUID, ids []uuid.UUID) ([]Column, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	count, err := q.CountKanbanColumnsForOwner(ctx, sqlcdb.CountKanbanColumnsForOwnerParams{BoardID: boardID, OwnerID: ownerID})
	if err != nil {
		return nil, err
	}
	if int(count) != len(ids) {
		return nil, errInvalidColumnOrder
	}
	// The schema enforces UNIQUE(board_id, position). Move every column into a
	// collision-free negative range before assigning the requested dense order.
	for position, id := range ids {
		affected, execErr := q.SetKanbanColumnPosition(ctx, sqlcdb.SetKanbanColumnPositionParams{Position: int32(-position - 1), ID: id, BoardID: boardID, OwnerID: ownerID})
		if execErr != nil {
			return nil, execErr
		}
		if affected != 1 {
			return nil, errInvalidColumnOrder
		}
	}
	for position, id := range ids {
		affected, execErr := q.SetKanbanColumnPosition(ctx, sqlcdb.SetKanbanColumnPositionParams{Position: int32(position), ID: id, BoardID: boardID, OwnerID: ownerID})
		if execErr != nil {
			return nil, execErr
		}
		if affected != 1 {
			return nil, errInvalidColumnOrder
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	rows, err := r.q.ListKanbanColumns(ctx, sqlcdb.ListKanbanColumnsParams{BoardID: boardID, OwnerID: ownerID})
	if err != nil {
		return nil, err
	}
	out := make([]Column, len(rows))
	for i, row := range rows {
		out[i] = columnFromRow(row)
	}
	return out, nil
}
func (r *pgRepo) DeleteColumn(ctx context.Context, id, ownerID uuid.UUID) error {
	n, e := r.q.DeleteKanbanColumn(ctx, sqlcdb.DeleteKanbanColumnParams{ID: id, OwnerID: ownerID})
	if e != nil {
		return e
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}

func (r *pgRepo) CreateCard(ctx context.Context, in CardInput) (*Card, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	limit, e := q.LockKanbanColumnForCreate(ctx, sqlcdb.LockKanbanColumnForCreateParams{ColumnID: in.ColumnID, BoardID: in.BoardID, OwnerID: in.OwnerID})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	if limit != nil {
		count, countErr := q.CountActiveKanbanCardsInColumn(ctx, in.ColumnID)
		if countErr != nil {
			return nil, countErr
		}
		if count >= *limit {
			return nil, errWIPLimit
		}
	}
	pos, e := q.NextKanbanCardPosition(ctx, sqlcdb.NextKanbanCardPositionParams{ColumnID: in.ColumnID, OwnerID: in.OwnerID})
	if e != nil {
		return nil, e
	}
	x, e := q.CreateKanbanCard(ctx, sqlcdb.CreateKanbanCardParams{ID: uuid.New(), BoardID: in.BoardID, ColumnID: in.ColumnID, OwnerID: in.OwnerID, SourceProjectID: in.SourceProjectID, Kind: in.Kind, Title: in.Title, Description: in.Description, Priority: in.Priority, Position: int64(pos), DueAt: in.DueAt, ExternalKey: in.ExternalKey, CustomFields: in.CustomFields})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	if _, e = q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: x.ID, ActorID: &in.OwnerID, Event: "created", Data: json.RawMessage(`{}`)}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	c := cardFromRow(x)
	return &c, nil
}
func (r *pgRepo) GetCard(ctx context.Context, id, ownerID uuid.UUID) (*Card, error) {
	x, e := r.q.GetKanbanCardForOwner(ctx, sqlcdb.GetKanbanCardForOwnerParams{ID: id, OwnerID: ownerID})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	c := cardFromRow(x)
	return &c, nil
}
func (r *pgRepo) UpdateCard(ctx context.Context, ownerID uuid.UUID, c Card) (*Card, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	x, e := q.UpdateKanbanCard(ctx, sqlcdb.UpdateKanbanCardParams{ID: c.ID, OwnerID: ownerID, Title: c.Title, Description: c.Description, Kind: c.Kind, Priority: c.Priority, DueAt: c.DueAt, CustomFields: c.CustomFields})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	if _, e = q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: c.ID, ActorID: &ownerID, Event: "updated", Data: json.RawMessage(`{}`)}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	out := cardFromRow(x)
	return &out, nil
}
func (r *pgRepo) MoveCard(ctx context.Context, id, columnID, ownerID uuid.UUID, position int64) (*Card, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	locked, e := q.LockKanbanCardForMove(ctx, sqlcdb.LockKanbanCardForMoveParams{ID: id, OwnerID: ownerID})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	boardID, sourceColumn := locked.BoardID, locked.ColumnID
	limit, e := q.LockKanbanDestinationColumn(ctx, sqlcdb.LockKanbanDestinationColumnParams{ID: columnID, BoardID: boardID})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	if sourceColumn != columnID && limit != nil {
		count, countErr := q.CountActiveKanbanCardsInColumn(ctx, columnID)
		if countErr != nil {
			return nil, countErr
		}
		if count >= *limit {
			return nil, errWIPLimit
		}
	}
	loadIDs := func(column uuid.UUID) ([]uuid.UUID, error) {
		return q.LockOrderedKanbanCardIDs(ctx, sqlcdb.LockOrderedKanbanCardIDsParams{ColumnID: column, ExcludeID: id})
	}
	destination, e := loadIDs(columnID)
	if e != nil {
		return nil, e
	}
	idx := int(position)
	if idx < 0 {
		idx = 0
	}
	if idx > len(destination) {
		idx = len(destination)
	}
	destination = append(destination, uuid.Nil)
	copy(destination[idx+1:], destination[idx:])
	destination[idx] = id
	for i, cardID := range destination {
		if _, e = q.SetKanbanCardPosition(ctx, sqlcdb.SetKanbanCardPositionParams{ColumnID: columnID, Position: int64((i + 1) * 1024), ID: cardID}); e != nil {
			return nil, e
		}
	}
	if sourceColumn != columnID {
		source, loadErr := loadIDs(sourceColumn)
		if loadErr != nil {
			return nil, loadErr
		}
		for i, cardID := range source {
			if _, e = q.SetKanbanCardPosition(ctx, sqlcdb.SetKanbanCardPositionParams{ColumnID: sourceColumn, Position: int64((i + 1) * 1024), ID: cardID}); e != nil {
				return nil, e
			}
		}
	}
	data, _ := json.Marshal(map[string]any{"column_id": columnID, "position": idx})
	if _, e = q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: id, ActorID: &ownerID, Event: "moved", Data: data}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return r.GetCard(ctx, id, ownerID)
}
func (r *pgRepo) ArchiveCard(ctx context.Context, id, ownerID uuid.UUID) (*Card, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	x, e := q.ArchiveKanbanCard(ctx, sqlcdb.ArchiveKanbanCardParams{ID: id, OwnerID: ownerID})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	if _, e = q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: id, ActorID: &ownerID, Event: "archived", Data: json.RawMessage(`{}`)}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	c := cardFromRow(x)
	return &c, nil
}

func (r *pgRepo) ListArchivedCards(ctx context.Context, boardID, ownerID uuid.UUID, projectID *uuid.UUID) ([]Card, error) {
	rows, err := r.q.ListArchivedKanbanCards(ctx, sqlcdb.ListArchivedKanbanCardsParams{BoardID: boardID, OwnerID: ownerID, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	out := make([]Card, len(rows))
	for i, row := range rows {
		out[i] = cardFromRow(row)
	}
	return out, nil
}

func (r *pgRepo) RestoreCard(ctx context.Context, id, ownerID uuid.UUID) (*Card, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	archived, err := q.LockArchivedKanbanCardForRestore(ctx, sqlcdb.LockArchivedKanbanCardForRestoreParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	limit, err := q.LockKanbanDestinationColumn(ctx, sqlcdb.LockKanbanDestinationColumnParams{ID: archived.ColumnID, BoardID: archived.BoardID})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if limit != nil {
		count, countErr := q.CountActiveKanbanCardsInColumn(ctx, archived.ColumnID)
		if countErr != nil {
			return nil, countErr
		}
		if count >= *limit {
			return nil, errWIPLimit
		}
	}
	if archived.ExternalKey != nil {
		exists, existsErr := q.HasActiveKanbanCardExternalKey(ctx, sqlcdb.HasActiveKanbanCardExternalKeyParams{BoardID: archived.BoardID, SourceProjectID: archived.SourceProjectID, ExternalKey: archived.ExternalKey})
		if existsErr != nil {
			return nil, existsErr
		}
		if exists {
			return nil, errRestoreConflict
		}
	}
	position, err := q.NextKanbanCardPosition(ctx, sqlcdb.NextKanbanCardPositionParams{ColumnID: archived.ColumnID, OwnerID: ownerID})
	if err != nil {
		return nil, err
	}
	x, err := q.RestoreKanbanCard(ctx, sqlcdb.RestoreKanbanCardParams{ID: id, Position: int64(position)})
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if _, err = q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: id, ActorID: &ownerID, Event: "restored", Data: json.RawMessage(`{}`)}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	card := cardFromRow(x)
	return &card, nil
}

func (r *pgRepo) UpsertInternalCard(ctx context.Context, projectID, boardID uuid.UUID, in CardInput) (*Board, *Card, bool, error) {
	bx, e := r.q.GetLinkedKanbanBoardForProject(ctx, sqlcdb.GetLinkedKanbanBoardForProjectParams{ID: boardID, ProjectID: projectID})
	if e != nil {
		return nil, nil, false, mapRepoErr(e)
	}
	b := boardFromRow(bx)
	in.OwnerID = b.OwnerID
	in.BoardID = boardID
	in.SourceProjectID = &projectID
	if in.ExternalKey != nil {
		if old, e := r.q.GetKanbanCardByExternalKey(ctx, sqlcdb.GetKanbanCardByExternalKeyParams{BoardID: boardID, ProjectID: projectID, ExternalKey: in.ExternalKey}); e == nil {
			x, e := r.UpdateCard(ctx, b.OwnerID, Card{ID: old.ID, Title: in.Title, Description: in.Description, Kind: in.Kind, Priority: in.Priority, DueAt: in.DueAt, CustomFields: in.CustomFields})
			if e != nil {
				return nil, nil, false, e
			}
			return &b, x, false, nil
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return nil, nil, false, e
		}
	}
	c, e := r.CreateCard(ctx, in)
	return &b, c, true, e
}

func (r *pgRepo) ResolveInternalCard(ctx context.Context, projectID, boardID uuid.UUID, cardID *uuid.UUID, externalKey *string) (*Board, *Card, error) {
	bx, err := r.q.GetLinkedKanbanBoardForProject(ctx, sqlcdb.GetLinkedKanbanBoardForProjectParams{ID: boardID, ProjectID: projectID})
	if err != nil {
		return nil, nil, mapRepoErr(err)
	}
	board := boardFromRow(bx)
	var row sqlcdb.KanbanCard
	if cardID != nil {
		row, err = r.q.GetInternalKanbanCardByID(ctx, sqlcdb.GetInternalKanbanCardByIDParams{ProjectID: projectID, ID: *cardID, BoardID: boardID})
	} else if externalKey != nil {
		row, err = r.q.GetKanbanCardByExternalKey(ctx, sqlcdb.GetKanbanCardByExternalKeyParams{BoardID: boardID, ProjectID: projectID, ExternalKey: externalKey})
	} else {
		return nil, nil, errNotFound
	}
	if err != nil {
		return nil, nil, mapRepoErr(err)
	}
	card := cardFromRow(row)
	return &board, &card, nil
}

func (r *pgRepo) UpdateInternalCard(ctx context.Context, projectID, boardID, cardID uuid.UUID, in CardInput) (*Board, *Card, error) {
	board, current, err := r.ResolveInternalCard(ctx, projectID, boardID, &cardID, nil)
	if err != nil {
		return nil, nil, err
	}
	updated, err := r.UpdateCard(ctx, board.OwnerID, Card{ID: current.ID, Title: in.Title, Description: in.Description, Kind: in.Kind, Priority: in.Priority, DueAt: in.DueAt, CustomFields: in.CustomFields})
	if err != nil {
		return nil, nil, mapRepoErr(err)
	}
	card := *updated
	if in.ColumnID != uuid.Nil && in.ColumnID != card.ColumnID {
		cardPtr, moveErr := r.MoveCard(ctx, card.ID, in.ColumnID, board.OwnerID, 0)
		if moveErr != nil {
			return nil, nil, moveErr
		}
		card = *cardPtr
	}
	return board, &card, nil
}

func (r *pgRepo) ArchiveInternalCard(ctx context.Context, projectID, boardID, cardID uuid.UUID) (*Board, *Card, error) {
	board, card, err := r.ResolveInternalCard(ctx, projectID, boardID, &cardID, nil)
	if err != nil {
		return nil, nil, err
	}
	archived, err := r.ArchiveCard(ctx, card.ID, board.OwnerID)
	if err != nil {
		return nil, nil, err
	}
	return board, archived, nil
}
func (r *pgRepo) ListActivities(ctx context.Context, cardID, ownerID uuid.UUID) ([]Activity, error) {
	xs, e := r.q.ListKanbanActivities(ctx, sqlcdb.ListKanbanActivitiesParams{CardID: cardID, OwnerID: ownerID})
	if e != nil {
		return nil, e
	}
	out := make([]Activity, len(xs))
	for i, x := range xs {
		out[i] = Activity{ID: x.ID, CardID: x.CardID, ActorID: x.ActorID, Event: x.Event, Data: raw(x.Data, "{}"), CreatedAt: x.CreatedAt}
	}
	return out, nil
}
func (r *pgRepo) CreateComment(ctx context.Context, cardID, ownerID uuid.UUID, body string) (*Comment, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := r.q.WithTx(tx)
	x, e := q.CreateKanbanComment(ctx, sqlcdb.CreateKanbanCommentParams{ID: uuid.New(), CardID: cardID, AuthorID: ownerID, Body: body})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	if _, e = q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: cardID, ActorID: &ownerID, Event: "commented", Data: json.RawMessage(`{}`)}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	c := commentFromRow(x)
	return &c, nil
}
func (r *pgRepo) ListComments(ctx context.Context, cardID, ownerID uuid.UUID) ([]Comment, error) {
	xs, e := r.q.ListKanbanComments(ctx, sqlcdb.ListKanbanCommentsParams{CardID: cardID, OwnerID: ownerID})
	if e != nil {
		return nil, e
	}
	out := make([]Comment, len(xs))
	for i, x := range xs {
		out[i] = commentFromRow(x)
	}
	return out, nil
}

func boardFromRow(x sqlcdb.KanbanBoard) Board {
	return Board{ID: x.ID, OwnerID: x.OwnerID, Name: x.Name, Description: x.Description, Icon: x.Icon, Color: x.Color, CardTypeLabel: x.CardTypeLabel, FieldSchema: raw(x.FieldSchema, "[]"), ArchivedAt: x.ArchivedAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func columnFromRow(x sqlcdb.KanbanColumn) Column {
	return Column{ID: x.ID, BoardID: x.BoardID, Name: x.Name, Color: x.Color, Position: x.Position, WIPLimit: x.WipLimit, IsTerminal: x.IsTerminal, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func cardFromRow(x sqlcdb.KanbanCard) Card {
	return Card{ID: x.ID, BoardID: x.BoardID, ColumnID: x.ColumnID, SourceProjectID: x.SourceProjectID, Kind: x.Kind, Title: x.Title, Description: x.Description, Priority: x.Priority, Position: x.Position, DueAt: x.DueAt, ExternalKey: x.ExternalKey, CustomFields: raw(x.CustomFields, "{}"), ArchivedAt: x.ArchivedAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func commentFromRow(x sqlcdb.KanbanComment) Comment {
	return Comment{ID: x.ID, CardID: x.CardID, AuthorID: x.AuthorID, Body: x.Body, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func raw(v []byte, fallback string) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage(fallback)
	}
	return json.RawMessage(v)
}
func mapRepoErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	return err
}
