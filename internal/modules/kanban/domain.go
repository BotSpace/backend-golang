package kanban

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	PriorityLow    = "low"
	PriorityNormal = "normal"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"
)

type Board struct {
	ID            uuid.UUID       `json:"id"`
	OwnerID       uuid.UUID       `json:"owner_id,omitempty"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Icon          string          `json:"icon"`
	Color         string          `json:"color"`
	CardTypeLabel string          `json:"card_type_label"`
	FieldSchema   json.RawMessage `json:"field_schema"`
	CardCount     int64           `json:"card_count"`
	ProjectIDs    []uuid.UUID     `json:"project_ids"`
	Projects      []ProjectLink   `json:"projects,omitempty"`
	Columns       []Column        `json:"columns,omitempty"`
	Cards         []Card          `json:"cards,omitempty"`
	ArchivedAt    *time.Time      `json:"archived_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type ProjectLink struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type Column struct {
	ID         uuid.UUID `json:"id"`
	BoardID    uuid.UUID `json:"board_id"`
	Name       string    `json:"name"`
	Color      string    `json:"color"`
	Position   int32     `json:"position"`
	WIPLimit   *int32    `json:"wip_limit"`
	IsTerminal bool      `json:"is_terminal"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Card struct {
	ID              uuid.UUID       `json:"id"`
	BoardID         uuid.UUID       `json:"board_id"`
	ColumnID        uuid.UUID       `json:"column_id"`
	SourceProjectID *uuid.UUID      `json:"source_project_id"`
	Kind            string          `json:"kind"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Priority        string          `json:"priority"`
	Position        int64           `json:"position"`
	DueAt           *time.Time      `json:"due_at"`
	ExternalKey     *string         `json:"external_key"`
	CustomFields    json.RawMessage `json:"custom_fields"`
	ArchivedAt      *time.Time      `json:"archived_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type Activity struct {
	ID        uuid.UUID       `json:"id"`
	CardID    uuid.UUID       `json:"card_id"`
	ActorID   *uuid.UUID      `json:"actor_id"`
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type Comment struct {
	ID        uuid.UUID `json:"id"`
	CardID    uuid.UUID `json:"card_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateBoardInput struct {
	OwnerID       uuid.UUID
	Name          string
	Description   string
	Icon          string
	Color         string
	CardTypeLabel string
	FieldSchema   json.RawMessage
	ProjectIDs    []uuid.UUID
	Columns       []ColumnInput
}

type ColumnInput struct {
	Name       string `json:"name"`
	Color      string `json:"color"`
	WIPLimit   *int32 `json:"wip_limit"`
	IsTerminal bool   `json:"is_terminal"`
}

type CardInput struct {
	OwnerID         uuid.UUID
	BoardID         uuid.UUID
	ColumnID        uuid.UUID
	SourceProjectID *uuid.UUID
	Kind            string
	Title           string
	Description     string
	Priority        string
	DueAt           *time.Time
	ExternalKey     *string
	CustomFields    json.RawMessage
}

type Repository interface {
	ResolveProjectRef(context.Context, string) (uuid.UUID, error)
	CreateBoard(context.Context, CreateBoardInput) (*Board, error)
	ListBoards(context.Context, uuid.UUID, *uuid.UUID) ([]Board, error)
	ListArchivedBoards(context.Context, uuid.UUID, *uuid.UUID) ([]Board, error)
	GetBoard(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID) (*Board, error)
	UpdateBoard(context.Context, Board) (*Board, error)
	ArchiveBoard(context.Context, uuid.UUID, uuid.UUID) error
	RestoreBoard(context.Context, uuid.UUID, uuid.UUID) (*Board, error)
	LinkProject(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	UnlinkProject(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	CreateColumn(context.Context, uuid.UUID, uuid.UUID, ColumnInput) (*Column, error)
	UpdateColumn(context.Context, uuid.UUID, uuid.UUID, ColumnInput) (*Column, error)
	ReorderColumns(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) ([]Column, error)
	DeleteColumn(context.Context, uuid.UUID, uuid.UUID) error
	CreateCard(context.Context, CardInput) (*Card, error)
	GetCard(context.Context, uuid.UUID, uuid.UUID) (*Card, error)
	UpdateCard(context.Context, uuid.UUID, Card) (*Card, error)
	MoveCard(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (*Card, error)
	ArchiveCard(context.Context, uuid.UUID, uuid.UUID) (*Card, error)
	ListArchivedCards(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID) ([]Card, error)
	RestoreCard(context.Context, uuid.UUID, uuid.UUID) (*Card, error)
	UpsertInternalCard(context.Context, uuid.UUID, uuid.UUID, CardInput) (*Board, *Card, bool, error)
	ResolveInternalCard(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, *string) (*Board, *Card, error)
	UpdateInternalCard(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, CardInput) (*Board, *Card, error)
	ArchiveInternalCard(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*Board, *Card, error)
	ListActivities(context.Context, uuid.UUID, uuid.UUID) ([]Activity, error)
	CreateComment(context.Context, uuid.UUID, uuid.UUID, string) (*Comment, error)
	ListComments(context.Context, uuid.UUID, uuid.UUID) ([]Comment, error)
}
