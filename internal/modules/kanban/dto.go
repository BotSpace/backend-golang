package kanban

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type CreateBoardRequest struct {
	Name          string          `json:"name" binding:"required,max=120"`
	Description   string          `json:"description"`
	Icon          string          `json:"icon"`
	Color         string          `json:"color"`
	CardTypeLabel string          `json:"card_type_label"`
	FieldSchema   json.RawMessage `json:"field_schema" swaggertype:"object"`
	ProjectIDs    []uuid.UUID     `json:"project_ids"`
	Columns       []ColumnInput   `json:"columns"`
}
type UpdateBoardRequest struct {
	Name          string          `json:"name" binding:"required,max=120"`
	Description   string          `json:"description"`
	Icon          string          `json:"icon"`
	Color         string          `json:"color"`
	CardTypeLabel string          `json:"card_type_label"`
	FieldSchema   json.RawMessage `json:"field_schema" swaggertype:"object"`
}
type ProjectLinkRequest struct {
	ProjectID uuid.UUID `json:"project_id" binding:"required"`
}
type CreateCardRequest struct {
	ColumnID        uuid.UUID       `json:"column_id" binding:"required"`
	SourceProjectID *uuid.UUID      `json:"source_project_id"`
	Kind            string          `json:"kind"`
	Title           string          `json:"title" binding:"required,max=240"`
	Description     string          `json:"description"`
	Priority        string          `json:"priority"`
	DueAt           *time.Time      `json:"due_at"`
	ExternalKey     *string         `json:"external_key"`
	CustomFields    json.RawMessage `json:"custom_fields" swaggertype:"object"`
}
type UpdateCardRequest struct {
	Kind         string          `json:"kind"`
	Title        string          `json:"title" binding:"required,max=240"`
	Description  string          `json:"description"`
	Priority     string          `json:"priority"`
	DueAt        *time.Time      `json:"due_at"`
	CustomFields json.RawMessage `json:"custom_fields" swaggertype:"object"`
}
type MoveCardRequest struct {
	ColumnID uuid.UUID `json:"column_id" binding:"required"`
	Position int64     `json:"position" binding:"min=0"`
}
type CommentRequest struct {
	Body string `json:"body" binding:"required"`
}
type InternalUpsertCardRequest struct {
	ColumnID     uuid.UUID       `json:"column_id" binding:"required"`
	Title        string          `json:"title" binding:"required,max=240"`
	Description  string          `json:"description"`
	Kind         string          `json:"kind"`
	Priority     string          `json:"priority"`
	DueAt        *time.Time      `json:"due_at"`
	ExternalKey  *string         `json:"external_key"`
	CustomFields json.RawMessage `json:"custom_fields" swaggertype:"object"`
}
