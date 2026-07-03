package cms

import (
	"encoding/json"
	"time"
)

// ── Collection ───────────────────────────────────────────────────────────

type CollectionResponse struct {
	ID            string          `json:"id"`
	ProjectID     string          `json:"project_id"`
	Name          string          `json:"name"`
	Slug          string          `json:"slug"`
	Description   string          `json:"description,omitempty"`
	PrimaryField  string          `json:"primary_field,omitempty"`
	System        bool            `json:"system"`
	SchemaVersion int32           `json:"schema_version"`
	Fields        []FieldResponse `json:"fields,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func toCollectionResponse(c *Collection) CollectionResponse {
	fields := make([]FieldResponse, len(c.Fields))
	for i := range c.Fields {
		fields[i] = toFieldResponse(&c.Fields[i])
	}
	return CollectionResponse{
		ID:            c.ID.String(),
		ProjectID:     c.ProjectID.String(),
		Name:          c.Name,
		Slug:          c.Slug,
		Description:   c.Description,
		PrimaryField:  c.PrimaryField,
		System:        c.System,
		SchemaVersion: c.SchemaVersion,
		Fields:        fields,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}

func toCollectionListResponse(items []Collection) []CollectionResponse {
	out := make([]CollectionResponse, len(items))
	for i := range items {
		out[i] = toCollectionResponse(&items[i])
	}
	return out
}

// CreateCollectionRequest is the "create schema" request body. Slug must be
// lowercase snake_case — it becomes the Mongo collection name and is
// immutable after creation.
type CreateCollectionRequest struct {
	Name         string `json:"name" validate:"required,min=1,max=120"`
	Slug         string `json:"slug" validate:"required,min=1,max=80"`
	Description  string `json:"description" validate:"omitempty,max=2000"`
	PrimaryField string `json:"primary_field" validate:"omitempty,max=64"`
}

type UpdateCollectionRequest struct {
	Name         *string `json:"name" validate:"omitempty,min=1,max=120"`
	Description  *string `json:"description" validate:"omitempty,max=2000"`
	PrimaryField *string `json:"primary_field" validate:"omitempty,max=64"`
}

// ── Field ────────────────────────────────────────────────────────────────

type FieldResponse struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Label                string          `json:"label,omitempty"`
	Type                 string          `json:"type"`
	Required             bool            `json:"required"`
	Default              json.RawMessage `json:"default,omitempty"`
	Validators           json.RawMessage `json:"validators,omitempty"`
	RelationCollectionID string          `json:"relation_collection_id,omitempty"`
	Cascade              string          `json:"cascade"`
	Index                string          `json:"index"`
	Order                int32           `json:"order"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

func toFieldResponse(f *Field) FieldResponse {
	relation := ""
	if f.RelationCollectionID != nil {
		relation = f.RelationCollectionID.String()
	}
	return FieldResponse{
		ID:                   f.ID.String(),
		Name:                 f.Name,
		Label:                f.Label,
		Type:                 f.Type,
		Required:             f.Required,
		Default:              f.Default,
		Validators:           f.Validators,
		RelationCollectionID: relation,
		Cascade:              f.Cascade,
		Index:                f.Index,
		Order:                f.Order,
		CreatedAt:            f.CreatedAt,
		UpdatedAt:            f.UpdatedAt,
	}
}

// FieldCreateRequest is the "add field" request body. RelationCollection
// names the target collection by slug — required when Type is "relation".
type FieldCreateRequest struct {
	Name               string          `json:"name" validate:"required,min=1,max=64"`
	Label              string          `json:"label" validate:"omitempty,max=120"`
	Type               string          `json:"type" validate:"required"`
	Required           bool            `json:"required"`
	Default            json.RawMessage `json:"default" validate:"omitempty"`
	Validators         json.RawMessage `json:"validators" validate:"omitempty"`
	RelationCollection string          `json:"relation_collection" validate:"omitempty,max=80"`
	Cascade            string          `json:"cascade" validate:"omitempty,oneof=cascade restrict set_null"`
	Index              string          `json:"index" validate:"omitempty,oneof=none asc desc text hashed unique ttl"`
	Order              int32           `json:"order"`
}

type FieldUpdateRequest struct {
	Label              *string         `json:"label" validate:"omitempty,max=120"`
	Required           *bool           `json:"required"`
	Default            json.RawMessage `json:"default" validate:"omitempty"`
	Validators         json.RawMessage `json:"validators" validate:"omitempty"`
	RelationCollection *string         `json:"relation_collection" validate:"omitempty,max=80"`
	ClearRelation      bool            `json:"clear_relation"`
	Cascade            *string         `json:"cascade" validate:"omitempty,oneof=cascade restrict set_null"`
	Index              *string         `json:"index" validate:"omitempty,oneof=none asc desc text hashed unique ttl"`
	Order              *int32          `json:"order"`
}

// ── Document ─────────────────────────────────────────────────────────────

// DocumentListRequest is parsed from query params — see handler.go.
type DocumentListRequest struct {
	Filter         map[string]any
	Sort           []DocumentSort
	Limit          int64
	Skip           int64
	IncludeDeleted bool
}

type DocumentListResponse struct {
	Results []map[string]any `json:"results"`
	Total   int64            `json:"total"`
	Limit   int64            `json:"limit"`
	Skip    int64            `json:"skip"`
}

type CountResponse struct {
	Count int64 `json:"count"`
}

// ── Export task ──────────────────────────────────────────────────────────

type ExportTaskResponse struct {
	ID           string    `json:"id"`
	CollectionID string    `json:"collection_id"`
	Status       string    `json:"status"`
	Progress     int32     `json:"progress"`
	DownloadURL  string    `json:"download_url,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toExportTaskResponse(t *ExportTask) ExportTaskResponse {
	url := ""
	if t.DownloadURL != nil {
		url = *t.DownloadURL
	}
	return ExportTaskResponse{
		ID:           t.ID.String(),
		CollectionID: t.CollectionID.String(),
		Status:       t.Status,
		Progress:     t.Progress,
		DownloadURL:  url,
		ErrorMessage: t.ErrorMessage,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
}

// validationDetails renders ValidationErrors into the map[string]any shape
// httpx.ErrorWithDetails expects.
func validationDetails(fields ValidationErrors) map[string]any {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}
