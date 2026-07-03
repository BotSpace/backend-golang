package message

import (
	"encoding/json"
	"time"
)

// MessageResponse is the public projection of a broadcast message.
type MessageResponse struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	Caption     string          `json:"caption"`
	MessageType string          `json:"message_type"`
	FileID      *string         `json:"file_id,omitempty"`
	ButtonType  string          `json:"button_type"`
	Buttons     json.RawMessage `json:"buttons"`
	Total       int64           `json:"total"`
	Done        int64           `json:"done"`
	Fail        int64           `json:"fail"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func toResponse(m *Message) MessageResponse {
	buttons := m.Buttons
	if len(buttons) == 0 {
		buttons = json.RawMessage("[]")
	}
	return MessageResponse{
		ID:          m.ID.String(),
		ProjectID:   m.ProjectID.String(),
		Caption:     m.Caption,
		MessageType: m.MessageType,
		FileID:      m.FileID,
		ButtonType:  m.ButtonType,
		Buttons:     buttons,
		Total:       m.Total,
		Done:        m.Done,
		Fail:        m.Fail,
		Status:      m.Status,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// CreateRequest is the broadcast creation payload. FileID is only meaningful
// for photo/video messages that already have a cached Telegram file_id;
// uploading raw files is out of scope for this API (handled by the release
// worker via the Telegram Bot API once queued).
type CreateRequest struct {
	ProjectID   string          `json:"project_id" validate:"required,uuid"`
	Caption     string          `json:"caption" validate:"omitempty,max=4096"`
	MessageType string          `json:"message_type" validate:"omitempty,oneof=text photo video"`
	FileID      *string         `json:"file_id" validate:"omitempty,max=255"`
	ButtonType  string          `json:"button_type" validate:"omitempty,oneof=inline reply"`
	Buttons     json.RawMessage `json:"buttons" validate:"omitempty"`
}

// UpdateRequest carries optional fields; omitted fields are left unchanged.
type UpdateRequest struct {
	Caption     *string         `json:"caption" validate:"omitempty,max=4096"`
	MessageType *string         `json:"message_type" validate:"omitempty,oneof=text photo video"`
	FileID      *string         `json:"file_id" validate:"omitempty,max=255"`
	ButtonType  *string         `json:"button_type" validate:"omitempty,oneof=inline reply"`
	Buttons     json.RawMessage `json:"buttons" validate:"omitempty"`
}

// ListResponse is a paginated envelope for a message listing.
type ListResponse struct {
	Items  []MessageResponse `json:"items"`
	Total  int64             `json:"total"`
	Limit  int32             `json:"limit"`
	Offset int32             `json:"offset"`
}

func toListResponse(items []Message, total int64, limit, offset int32) ListResponse {
	out := make([]MessageResponse, len(items))
	for i := range items {
		out[i] = toResponse(&items[i])
	}
	return ListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}
