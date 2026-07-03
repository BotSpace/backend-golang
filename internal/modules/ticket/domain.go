package ticket

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Status values for a support ticket. Mirrors Django TicketStatusEnum.
// open    - waiting for admin to respond (new ticket or user just replied).
// pending - admin responded, waiting for the user.
// closed  - conversation resolved.
const (
	StatusOpen    = "open"
	StatusPending = "pending"
	StatusClosed  = "closed"
)

// Sender values for a ticket message. Mirrors Django TicketSenderEnum.
const (
	SenderUser  = "user"
	SenderAdmin = "admin"
)

// Ticket is a support conversation opened by a user (core.apps.tickets
// TicketModel). Messages carries the full conversation when loaded (by
// Create/Get/Reply/Close); list results populate LastMessage instead, to
// avoid pulling every message for every row in a listing.
type Ticket struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Subject     string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastMessage *TicketMessage
	Messages    []TicketMessage

	// UserPhone is populated only by admin-facing queries that join the
	// users table (ListAll/GetByID); it is empty on owner-scoped paths.
	UserPhone string
}

// TicketMessage is a single message in a ticket's conversation
// (TicketMessageModel). AttachmentURL is empty when no file was attached.
type TicketMessage struct {
	ID            uuid.UUID
	TicketID      uuid.UUID
	Sender        string
	AuthorID      *uuid.UUID
	Message       string
	AttachmentURL string
	CreatedAt     time.Time
}

// CreateInput opens a new ticket with its first message, authored by the
// owner.
type CreateInput struct {
	UserID        uuid.UUID
	Subject       string
	Message       string
	AttachmentURL string
}

// ReplyInput appends a message to an existing ticket and moves it to
// NewStatus in the same operation, so the thread state always reflects who
// is expected to act next.
type ReplyInput struct {
	TicketID      uuid.UUID
	Sender        string // SenderUser or SenderAdmin
	AuthorID      uuid.UUID
	Message       string
	AttachmentURL string
	NewStatus     string
}

// ListFilter narrows and paginates a user's own ticket listing.
type ListFilter struct {
	OwnerID uuid.UUID
	Status  string
	Limit   int32
	Offset  int32
}

// AdminListFilter narrows and paginates the admin-wide ticket listing.
type AdminListFilter struct {
	Status string
	UserID *uuid.UUID
	Search string
	Limit  int32
	Offset int32
}

// Repository is the persistence port. User-scoped methods take ownerID and
// never return or mutate another user's ticket; the "ForAdmin"-style methods
// (nil ownerID passed to Reply/SetStatus, GetByID/ListAll/CountAll) are
// unscoped and only reachable behind an admin-only route.
type Repository interface {
	Create(ctx context.Context, in CreateInput) (*Ticket, error)
	GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Ticket, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Ticket, error)
	ListForOwner(ctx context.Context, f ListFilter) ([]Ticket, error)
	CountForOwner(ctx context.Context, ownerID uuid.UUID, status string) (int64, error)
	ListAll(ctx context.Context, f AdminListFilter) ([]Ticket, error)
	CountAll(ctx context.Context, f AdminListFilter) (int64, error)
	// Reply inserts a message and updates the ticket status atomically.
	// ownerID nil skips the ownership check (admin path).
	Reply(ctx context.Context, ownerID *uuid.UUID, in ReplyInput) (*Ticket, error)
	// SetStatus updates a ticket's status. ownerID nil skips the ownership
	// check (admin path).
	SetStatus(ctx context.Context, id uuid.UUID, ownerID *uuid.UUID, status string) (*Ticket, error)
}
