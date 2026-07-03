package ticket

import (
	"context"
	"errors"
	"strings"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Usecase exposes ticket operations: owner-scoped for a regular user, and
// unscoped ("Admin*") for support staff (gated by RequireRole at the route).
type Usecase interface {
	Create(ctx context.Context, in CreateInput) (*Ticket, error)
	Get(ctx context.Context, id, ownerID uuid.UUID) (*Ticket, error)
	List(ctx context.Context, f ListFilter) ([]Ticket, int64, error)
	Reply(ctx context.Context, id, ownerID uuid.UUID, message, attachmentURL string) (*Ticket, error)
	Close(ctx context.Context, id, ownerID uuid.UUID) (*Ticket, error)

	AdminGet(ctx context.Context, id uuid.UUID) (*Ticket, error)
	AdminList(ctx context.Context, f AdminListFilter) ([]Ticket, int64, error)
	AdminRespond(ctx context.Context, id, adminID uuid.UUID, message, attachmentURL string) (*Ticket, error)
	AdminClose(ctx context.Context, id uuid.UUID) (*Ticket, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

func (u *usecase) Create(ctx context.Context, in CreateInput) (*Ticket, error) {
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, httpx.BadRequest("ticket.empty_subject", "subject must not be empty")
	}
	in.Message = strings.TrimSpace(in.Message)
	if in.Message == "" {
		return nil, httpx.BadRequest("ticket.empty_message", "message must not be empty")
	}
	t, err := u.repo.Create(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return t, nil
}

func (u *usecase) Get(ctx context.Context, id, ownerID uuid.UUID) (*Ticket, error) {
	t, err := u.repo.GetForOwner(ctx, id, ownerID)
	return mapErrTicket(t, err)
}

func (u *usecase) List(ctx context.Context, f ListFilter) ([]Ticket, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	if f.Status != "" && !isValidStatus(f.Status) {
		return nil, 0, httpx.BadRequest("ticket.invalid_status", "invalid status filter")
	}
	items, err := u.repo.ListForOwner(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountForOwner(ctx, f.OwnerID, f.Status)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) Reply(ctx context.Context, id, ownerID uuid.UUID, message, attachmentURL string) (*Ticket, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, httpx.BadRequest("ticket.empty_message", "message must not be empty")
	}
	// A user reply always signals the ticket needs admin attention again.
	t, err := u.repo.Reply(ctx, &ownerID, ReplyInput{
		TicketID:      id,
		Sender:        SenderUser,
		AuthorID:      ownerID,
		Message:       message,
		AttachmentURL: attachmentURL,
		NewStatus:     StatusOpen,
	})
	return mapErrTicket(t, err)
}

func (u *usecase) Close(ctx context.Context, id, ownerID uuid.UUID) (*Ticket, error) {
	t, err := u.repo.SetStatus(ctx, id, &ownerID, StatusClosed)
	return mapErrTicket(t, err)
}

func (u *usecase) AdminGet(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	t, err := u.repo.GetByID(ctx, id)
	return mapErrTicket(t, err)
}

func (u *usecase) AdminList(ctx context.Context, f AdminListFilter) ([]Ticket, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	if f.Status != "" && !isValidStatus(f.Status) {
		return nil, 0, httpx.BadRequest("ticket.invalid_status", "invalid status filter")
	}
	items, err := u.repo.ListAll(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountAll(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) AdminRespond(ctx context.Context, id, adminID uuid.UUID, message, attachmentURL string) (*Ticket, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, httpx.BadRequest("ticket.empty_message", "message must not be empty")
	}
	// An admin response hands the ticket back to the user; pending means
	// "waiting on the user" until they reply again (which reopens it).
	t, err := u.repo.Reply(ctx, nil, ReplyInput{
		TicketID:      id,
		Sender:        SenderAdmin,
		AuthorID:      adminID,
		Message:       message,
		AttachmentURL: attachmentURL,
		NewStatus:     StatusPending,
	})
	return mapErrTicket(t, err)
}

func (u *usecase) AdminClose(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	t, err := u.repo.SetStatus(ctx, id, nil, StatusClosed)
	return mapErrTicket(t, err)
}

func isValidStatus(s string) bool {
	switch s {
	case StatusOpen, StatusPending, StatusClosed:
		return true
	default:
		return false
	}
}

// mapErrTicket translates a not-found into a 404 and any other error into a 500.
func mapErrTicket(t *Ticket, err error) (*Ticket, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("ticket.not_found", "ticket not found")
		}
		return nil, httpx.Internal(err)
	}
	return t, nil
}
