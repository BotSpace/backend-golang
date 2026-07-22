package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNotFound = errors.New("kanban resource not found")

type pgRepo struct {
	pool *pgxpool.Pool
	q    *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{pool: pool, q: sqlcdb.New(pool)}
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
	cols, e := r.q.ListKanbanColumns(ctx, sqlcdb.ListKanbanColumnsParams{BoardID: boardID, OwnerID: ownerID})
	if e != nil {
		return nil, e
	}
	x, e := r.q.CreateKanbanColumn(ctx, sqlcdb.CreateKanbanColumnParams{ID: uuid.New(), BoardID: boardID, OwnerID: ownerID, Name: in.Name, Color: in.Color, Position: int32(len(cols)), WipLimit: in.WIPLimit, IsTerminal: in.IsTerminal})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	c := columnFromRow(x)
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
	pos, e := r.q.NextKanbanCardPosition(ctx, sqlcdb.NextKanbanCardPositionParams{ColumnID: in.ColumnID, OwnerID: in.OwnerID})
	if e != nil {
		return nil, e
	}
	x, e := r.q.CreateKanbanCard(ctx, sqlcdb.CreateKanbanCardParams{ID: uuid.New(), BoardID: in.BoardID, ColumnID: in.ColumnID, OwnerID: in.OwnerID, SourceProjectID: in.SourceProjectID, Kind: in.Kind, Title: in.Title, Description: in.Description, Priority: in.Priority, Position: int64(pos), DueAt: in.DueAt, ExternalKey: in.ExternalKey, CustomFields: in.CustomFields})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	_, _ = r.q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: x.ID, ActorID: &in.OwnerID, Event: "created", Data: json.RawMessage(`{}`)})
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
	x, e := r.q.UpdateKanbanCard(ctx, sqlcdb.UpdateKanbanCardParams{ID: c.ID, OwnerID: ownerID, Title: c.Title, Description: c.Description, Kind: c.Kind, Priority: c.Priority, DueAt: c.DueAt, CustomFields: c.CustomFields})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	_, _ = r.q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: c.ID, ActorID: &ownerID, Event: "updated", Data: json.RawMessage(`{}`)})
	out := cardFromRow(x)
	return &out, nil
}
func (r *pgRepo) MoveCard(ctx context.Context, id, columnID, ownerID uuid.UUID, position int64) (*Card, error) {
	x, e := r.q.MoveKanbanCard(ctx, sqlcdb.MoveKanbanCardParams{ID: id, ColumnID: columnID, OwnerID: ownerID, Position: position})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	data, _ := json.Marshal(map[string]any{"column_id": columnID, "position": position})
	_, _ = r.q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: id, ActorID: &ownerID, Event: "moved", Data: data})
	c := cardFromRow(x)
	return &c, nil
}
func (r *pgRepo) ArchiveCard(ctx context.Context, id, ownerID uuid.UUID) (*Card, error) {
	x, e := r.q.ArchiveKanbanCard(ctx, sqlcdb.ArchiveKanbanCardParams{ID: id, OwnerID: ownerID})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	_, _ = r.q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: id, ActorID: &ownerID, Event: "archived", Data: json.RawMessage(`{}`)})
	c := cardFromRow(x)
	return &c, nil
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
			x, e := r.q.UpdateInternalKanbanCard(ctx, sqlcdb.UpdateInternalKanbanCardParams{ID: old.ID, BoardID: boardID, SourceProjectID: &projectID, Title: in.Title, Description: in.Description, Kind: in.Kind, Priority: in.Priority, DueAt: in.DueAt, CustomFields: in.CustomFields})
			if e != nil {
				return nil, nil, false, e
			}
			c := cardFromRow(x)
			return &b, &c, false, nil
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return nil, nil, false, e
		}
	}
	c, e := r.CreateCard(ctx, in)
	return &b, c, true, e
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
	x, e := r.q.CreateKanbanComment(ctx, sqlcdb.CreateKanbanCommentParams{ID: uuid.New(), CardID: cardID, AuthorID: ownerID, Body: body})
	if e != nil {
		return nil, mapRepoErr(e)
	}
	_, _ = r.q.CreateKanbanActivity(ctx, sqlcdb.CreateKanbanActivityParams{ID: uuid.New(), CardID: cardID, ActorID: &ownerID, Event: "commented", Data: json.RawMessage(`{}`)})
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
	return Board{ID: x.ID, OwnerID: x.OwnerID, Name: x.Name, Description: x.Description, Icon: x.Icon, Color: x.Color, CardTypeLabel: x.CardTypeLabel, FieldSchema: raw(x.FieldSchema, "[]"), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func columnFromRow(x sqlcdb.KanbanColumn) Column {
	return Column{ID: x.ID, BoardID: x.BoardID, Name: x.Name, Color: x.Color, Position: x.Position, WIPLimit: x.WipLimit, IsTerminal: x.IsTerminal, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func cardFromRow(x sqlcdb.KanbanCard) Card {
	return Card{ID: x.ID, BoardID: x.BoardID, ColumnID: x.ColumnID, SourceProjectID: x.SourceProjectID, Kind: x.Kind, Title: x.Title, Description: x.Description, Priority: x.Priority, Position: x.Position, DueAt: x.DueAt, ExternalKey: x.ExternalKey, CustomFields: raw(x.CustomFields, "{}"), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
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
