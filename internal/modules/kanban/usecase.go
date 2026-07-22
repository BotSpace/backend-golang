package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/wsevents"
	"github.com/google/uuid"
)

type Usecase interface {
	CreateBoard(context.Context, CreateBoardInput) (*Board, error)
	ResolveProjectRef(context.Context, string) (uuid.UUID, error)
	ListBoards(context.Context, uuid.UUID, *uuid.UUID) ([]Board, error)
	GetBoard(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID) (*Board, error)
	UpdateBoard(context.Context, uuid.UUID, uuid.UUID, UpdateBoardRequest) (*Board, error)
	ArchiveBoard(context.Context, uuid.UUID, uuid.UUID) error
	LinkProject(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	UnlinkProject(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	CreateColumn(context.Context, uuid.UUID, uuid.UUID, ColumnInput) (*Column, error)
	UpdateColumn(context.Context, uuid.UUID, uuid.UUID, ColumnInput) (*Column, error)
	ReorderColumns(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) ([]Column, error)
	DeleteColumn(context.Context, uuid.UUID, uuid.UUID) error
	CreateCard(context.Context, CardInput) (*Card, error)
	GetCard(context.Context, uuid.UUID, uuid.UUID) (*Card, error)
	UpdateCard(context.Context, uuid.UUID, uuid.UUID, UpdateCardRequest) (*Card, error)
	MoveCard(context.Context, uuid.UUID, uuid.UUID, MoveCardRequest) (*Card, error)
	ArchiveCard(context.Context, uuid.UUID, uuid.UUID) (*Card, error)
	UpsertInternalCard(context.Context, uuid.UUID, uuid.UUID, InternalUpsertCardRequest) (*Card, bool, error)
	MutateInternalCard(context.Context, uuid.UUID, uuid.UUID, InternalUpsertCardRequest) (*Card, bool, error)
	ListActivities(context.Context, uuid.UUID, uuid.UUID) ([]Activity, error)
	CreateComment(context.Context, uuid.UUID, uuid.UUID, string) (*Comment, error)
	ListComments(context.Context, uuid.UUID, uuid.UUID) ([]Comment, error)
}

type usecase struct {
	repo Repository
	pub  wsevents.Publisher
}

func NewUsecase(repo Repository, pub wsevents.Publisher) Usecase {
	return &usecase{repo: repo, pub: pub}
}

func (u *usecase) ResolveProjectRef(ctx context.Context, raw string) (uuid.UUID, error) {
	id, err := u.repo.ResolveProjectRef(ctx, strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, mapOnlyErr(err)
	}
	return id, nil
}

func (u *usecase) CreateBoard(ctx context.Context, in CreateBoardInput) (*Board, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, httpx.BadRequest("kanban.name_required", "board name is required")
	}
	if len(in.Name) > 120 {
		return nil, httpx.BadRequest("kanban.name_too_long", "board name is too long")
	}
	if in.Icon == "" {
		in.Icon = "layout-kanban"
	}
	if in.Color == "" {
		in.Color = "#6366f1"
	}
	if in.CardTypeLabel == "" {
		in.CardTypeLabel = "Karta"
	}
	if !json.Valid(in.FieldSchema) {
		return nil, httpx.BadRequest("kanban.invalid_field_schema", "field_schema must be valid JSON")
	}
	if len(in.Columns) == 0 {
		in.Columns = defaultColumns()
	}
	for i := range in.Columns {
		if err := validateColumn(&in.Columns[i]); err != nil {
			return nil, err
		}
	}
	b, e := u.repo.CreateBoard(ctx, in)
	if e != nil {
		return nil, httpx.Internal(e)
	}
	u.event(ctx, b.OwnerID, "kanban_board_created", b)
	return b, nil
}
func (u *usecase) ListBoards(ctx context.Context, owner uuid.UUID, project *uuid.UUID) ([]Board, error) {
	x, e := u.repo.ListBoards(ctx, owner, project)
	if e != nil {
		return nil, httpx.Internal(e)
	}
	return x, nil
}
func (u *usecase) GetBoard(ctx context.Context, id, owner uuid.UUID, project *uuid.UUID) (*Board, error) {
	b, e := u.repo.GetBoard(ctx, id, owner, project)
	return mapErr(b, e)
}
func (u *usecase) UpdateBoard(ctx context.Context, id, owner uuid.UUID, r UpdateBoardRequest) (*Board, error) {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return nil, httpx.BadRequest("kanban.name_required", "board name is required")
	}
	if !json.Valid(r.FieldSchema) {
		return nil, httpx.BadRequest("kanban.invalid_field_schema", "field_schema must be valid JSON")
	}
	b, e := u.repo.UpdateBoard(ctx, Board{ID: id, OwnerID: owner, Name: r.Name, Description: r.Description, Icon: r.Icon, Color: r.Color, CardTypeLabel: r.CardTypeLabel, FieldSchema: r.FieldSchema})
	if e != nil {
		return mapErr(b, e)
	}
	u.event(ctx, owner, "kanban_board_updated", b)
	return b, nil
}
func (u *usecase) ArchiveBoard(ctx context.Context, id, owner uuid.UUID) error {
	e := u.repo.ArchiveBoard(ctx, id, owner)
	if e != nil {
		return mapOnlyErr(e)
	}
	u.event(ctx, owner, "kanban_board_archived", map[string]any{"id": id})
	return nil
}
func (u *usecase) LinkProject(ctx context.Context, b, p, o uuid.UUID) error {
	if e := u.repo.LinkProject(ctx, b, p, o); e != nil {
		return httpx.Internal(e)
	}
	u.event(ctx, o, "kanban_board_linked", map[string]any{"board_id": b, "project_id": p})
	return nil
}
func (u *usecase) UnlinkProject(ctx context.Context, b, p, o uuid.UUID) error {
	if e := u.repo.UnlinkProject(ctx, b, p, o); e != nil {
		return mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_board_unlinked", map[string]any{"board_id": b, "project_id": p})
	return nil
}
func (u *usecase) CreateColumn(ctx context.Context, b, o uuid.UUID, in ColumnInput) (*Column, error) {
	if e := validateColumn(&in); e != nil {
		return nil, e
	}
	c, e := u.repo.CreateColumn(ctx, b, o, in)
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_column_created", c)
	return c, nil
}
func (u *usecase) UpdateColumn(ctx context.Context, id, o uuid.UUID, in ColumnInput) (*Column, error) {
	if e := validateColumn(&in); e != nil {
		return nil, e
	}
	c, e := u.repo.UpdateColumn(ctx, id, o, in)
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_column_updated", c)
	return c, nil
}
func (u *usecase) ReorderColumns(ctx context.Context, boardID, ownerID uuid.UUID, ids []uuid.UUID) ([]Column, error) {
	if len(ids) == 0 {
		return nil, httpx.BadRequest("kanban.columns_required", "column_ids is required")
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, httpx.BadRequest("kanban.invalid_id", "column id is invalid")
		}
		if _, exists := seen[id]; exists {
			return nil, httpx.BadRequest("kanban.duplicate_column", "column_ids contains duplicates")
		}
		seen[id] = struct{}{}
	}
	cols, err := u.repo.ReorderColumns(ctx, boardID, ownerID, ids)
	if err != nil {
		return nil, mapOnlyErr(err)
	}
	u.event(ctx, ownerID, "kanban_columns_reordered", map[string]any{"board_id": boardID, "column_ids": ids})
	return cols, nil
}
func (u *usecase) DeleteColumn(ctx context.Context, id, o uuid.UUID) error {
	if e := u.repo.DeleteColumn(ctx, id, o); e != nil {
		return mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_column_deleted", map[string]any{"id": id})
	return nil
}
func (u *usecase) CreateCard(ctx context.Context, in CardInput) (*Card, error) {
	if e := validateCard(&in); e != nil {
		return nil, e
	}
	c, e := u.repo.CreateCard(ctx, in)
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	u.event(ctx, in.OwnerID, "kanban_card_created", c)
	return c, nil
}
func (u *usecase) GetCard(ctx context.Context, id, o uuid.UUID) (*Card, error) {
	c, e := u.repo.GetCard(ctx, id, o)
	return mapErr(c, e)
}
func (u *usecase) UpdateCard(ctx context.Context, id, o uuid.UUID, r UpdateCardRequest) (*Card, error) {
	in := CardInput{Title: r.Title, Description: r.Description, Kind: r.Kind, Priority: r.Priority, DueAt: r.DueAt, CustomFields: r.CustomFields}
	if e := validateCard(&in); e != nil {
		return nil, e
	}
	c, e := u.repo.UpdateCard(ctx, o, Card{ID: id, Title: in.Title, Description: in.Description, Kind: in.Kind, Priority: in.Priority, DueAt: in.DueAt, CustomFields: in.CustomFields})
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_card_updated", c)
	return c, nil
}
func (u *usecase) MoveCard(ctx context.Context, id, o uuid.UUID, r MoveCardRequest) (*Card, error) {
	if r.Position < 0 {
		return nil, httpx.BadRequest("kanban.invalid_position", "position must not be negative")
	}
	c, e := u.repo.MoveCard(ctx, id, r.ColumnID, o, r.Position)
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_card_moved", c)
	return c, nil
}
func (u *usecase) ArchiveCard(ctx context.Context, id, o uuid.UUID) (*Card, error) {
	c, e := u.repo.ArchiveCard(ctx, id, o)
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_card_archived", c)
	return c, nil
}
func (u *usecase) UpsertInternalCard(ctx context.Context, p, b uuid.UUID, r InternalUpsertCardRequest) (*Card, bool, error) {
	r.Operation = "upsert"
	return u.MutateInternalCard(ctx, p, b, r)
}

func (u *usecase) MutateInternalCard(ctx context.Context, p, b uuid.UUID, r InternalUpsertCardRequest) (*Card, bool, error) {
	op := strings.ToLower(strings.TrimSpace(r.Operation))
	if op == "" {
		op = "upsert"
	}
	in := CardInput{BoardID: b, ColumnID: r.ColumnID, SourceProjectID: &p, Title: r.Title, Description: r.Description, Kind: r.Kind, Priority: r.Priority, DueAt: r.DueAt, ExternalKey: r.ExternalKey, CustomFields: r.CustomFields}
	if op == "move" || op == "archive" {
		if r.CardID == nil && (r.ExternalKey == nil || strings.TrimSpace(*r.ExternalKey) == "") {
			return nil, false, httpx.BadRequest("kanban.card_reference_required", "card_id or external_key is required")
		}
		board, current, e := u.repo.ResolveInternalCard(ctx, p, b, r.CardID, r.ExternalKey)
		if e != nil {
			return nil, false, mapOnlyErr(e)
		}
		if op == "archive" {
			board, current, e = u.repo.ArchiveInternalCard(ctx, p, b, current.ID)
			if e != nil {
				return nil, false, mapOnlyErr(e)
			}
			u.event(ctx, board.OwnerID, "kanban_card_archived", current)
			return current, false, nil
		}
		if r.ColumnID == uuid.Nil {
			return nil, false, httpx.BadRequest("kanban.column_required", "column_id is required")
		}
		current, e = u.repo.MoveCard(ctx, current.ID, r.ColumnID, board.OwnerID, 0)
		if e != nil {
			return nil, false, mapOnlyErr(e)
		}
		u.event(ctx, board.OwnerID, "kanban_card_moved", current)
		return current, false, nil
	}
	if e := validateCard(&in); e != nil {
		return nil, false, e
	}
	var board *Board
	var c *Card
	var created bool
	var e error
	switch op {
	case "upsert", "create":
		board, c, created, e = u.repo.UpsertInternalCard(ctx, p, b, in)
	case "update":
		if r.CardID == nil && (r.ExternalKey == nil || strings.TrimSpace(*r.ExternalKey) == "") {
			return nil, false, httpx.BadRequest("kanban.card_reference_required", "card_id or external_key is required")
		}
		_, current, resolveErr := u.repo.ResolveInternalCard(ctx, p, b, r.CardID, r.ExternalKey)
		if resolveErr != nil {
			return nil, false, mapOnlyErr(resolveErr)
		}
		board, c, e = u.repo.UpdateInternalCard(ctx, p, b, current.ID, in)
	default:
		return nil, false, httpx.BadRequest("kanban.invalid_operation", "operation must be upsert, create, update, move, or archive")
	}
	if e != nil {
		return nil, false, mapOnlyErr(e)
	}
	event := "kanban_card_updated"
	if created {
		event = "kanban_card_created"
	}
	u.event(ctx, board.OwnerID, event, c)
	return c, created, nil
}
func (u *usecase) ListActivities(ctx context.Context, c, o uuid.UUID) ([]Activity, error) {
	x, e := u.repo.ListActivities(ctx, c, o)
	if e != nil {
		return nil, httpx.Internal(e)
	}
	return x, nil
}
func (u *usecase) CreateComment(ctx context.Context, c, o uuid.UUID, body string) (*Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, httpx.BadRequest("kanban.comment_required", "comment body is required")
	}
	x, e := u.repo.CreateComment(ctx, c, o, body)
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	u.event(ctx, o, "kanban_comment_created", x)
	return x, nil
}
func (u *usecase) ListComments(ctx context.Context, c, o uuid.UUID) ([]Comment, error) {
	x, e := u.repo.ListComments(ctx, c, o)
	if e != nil {
		return nil, httpx.Internal(e)
	}
	return x, nil
}
func (u *usecase) event(ctx context.Context, owner uuid.UUID, event string, data any) {
	if u.pub != nil {
		u.pub.Publish(ctx, wsevents.UserRoom(owner), event, data)
	}
}

func validateColumn(c *ColumnInput) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return httpx.BadRequest("kanban.column_name_required", "column name is required")
	}
	if c.Color == "" {
		c.Color = "#64748b"
	}
	return nil
}
func validateCard(c *CardInput) error {
	c.Title = strings.TrimSpace(c.Title)
	if c.Title == "" {
		return httpx.BadRequest("kanban.card_title_required", "card title is required")
	}
	if c.Kind == "" {
		c.Kind = "item"
	}
	if c.Priority == "" {
		c.Priority = PriorityNormal
	}
	switch c.Priority {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent:
	default:
		return httpx.BadRequest("kanban.invalid_priority", "invalid priority")
	}
	if len(c.CustomFields) == 0 {
		c.CustomFields = json.RawMessage(`{}`)
	}
	if !json.Valid(c.CustomFields) {
		return httpx.BadRequest("kanban.invalid_custom_fields", "custom_fields must be valid JSON")
	}
	return nil
}
func defaultColumns() []ColumnInput {
	return []ColumnInput{{Name: "Yangi", Color: "#64748b"}, {Name: "Jarayonda", Color: "#3b82f6"}, {Name: "Bajarildi", Color: "#22c55e", IsTerminal: true}}
}
func mapOnlyErr(e error) error {
	if errors.Is(e, errWIPLimit) {
		return httpx.Conflict("kanban.wip_limit_reached", "column WIP limit has been reached")
	}
	if errors.Is(e, errInvalidColumnOrder) {
		return httpx.Conflict("kanban.invalid_column_order", "column order must contain every board column exactly once")
	}
	if errors.Is(e, errNotFound) {
		return httpx.NotFound("kanban.not_found", "kanban resource not found")
	}
	return httpx.Internal(e)
}
func mapErr[T any](v *T, e error) (*T, error) {
	if e != nil {
		return nil, mapOnlyErr(e)
	}
	return v, nil
}

var _ = fmt.Sprintf
