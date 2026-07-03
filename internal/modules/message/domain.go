package message

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Message types recognised for a broadcast (mirrors MessageTypeEnum).
const (
	TypeText  = "text"
	TypePhoto = "photo"
	TypeVideo = "video"
)

// Keyboard types for the attached buttons (mirrors ButtonTypeEnum).
const (
	ButtonInline = "inline"
	ButtonReply  = "reply"
)

// Delivery statuses for a broadcast (mirrors MessageStatusEnum).
const (
	StatusProgress = "progress"
	StatusDone     = "done"
	StatusFailed   = "failed"
)

// Message is a broadcast sent to a project's bot users. It always belongs to
// exactly one project; ownership is enforced transitively through
// projects.owner_id (there is no owner_id column on this table).
type Message struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	Caption     string
	MessageType string
	FileID      *string
	ButtonType  string
	Buttons     json.RawMessage
	Total       int64
	Done        int64
	Fail        int64
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateInput is the owner-scoped create payload. OwnerID is used purely to
// verify ProjectID belongs to the caller; it is not persisted on the row.
type CreateInput struct {
	OwnerID     uuid.UUID
	ProjectID   uuid.UUID
	Caption     string
	MessageType string
	FileID      *string
	ButtonType  string
	Buttons     json.RawMessage
}

// UpdateInput carries optional fields; nil pointers are left unchanged.
type UpdateInput struct {
	Caption     *string
	MessageType *string
	FileID      *string
	ButtonType  *string
	Buttons     json.RawMessage
}

// ListFilter narrows and paginates an owner's message listing. ProjectID is
// optional: nil lists messages across every project the owner has.
type ListFilter struct {
	OwnerID   uuid.UUID
	ProjectID *uuid.UUID
	Limit     int32
	Offset    int32
}

// Repository is the persistence port. Every operation is scoped to the
// caller's projects so a user can never touch another user's messages.
type Repository interface {
	Create(ctx context.Context, in CreateInput) (*Message, error)
	GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Message, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Message, error)
	Delete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Message, error)
	Count(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error)
}
