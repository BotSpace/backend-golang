package ticket

import (
	"context"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. Create and Reply need multi-statement
// atomicity (ticket + message together), so the pool is kept alongside the
// queries to open ad-hoc transactions.
type pgRepo struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{pool: pool, queries: sqlcdb.New(pool)}
}

func (r *pgRepo) Create(ctx context.Context, in CreateInput) (*Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create ticket: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := r.queries.WithTx(tx)
	row, err := q.CreateTicket(ctx, sqlcdb.CreateTicketParams{
		ID:      uuid.New(),
		UserID:  in.UserID,
		Subject: in.Subject,
	})
	if err != nil {
		return nil, fmt.Errorf("create ticket: %w", err)
	}

	authorID := in.UserID
	msgRow, err := q.CreateTicketMessage(ctx, sqlcdb.CreateTicketMessageParams{
		ID:            uuid.New(),
		TicketID:      row.ID,
		Sender:        SenderUser,
		AuthorID:      &authorID,
		Message:       in.Message,
		AttachmentUrl: strPtr(in.AttachmentURL),
	})
	if err != nil {
		return nil, fmt.Errorf("create ticket message: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create ticket: %w", err)
	}

	t := fromSQLC(row)
	msg := messageFromSQLC(msgRow)
	t.Messages = []TicketMessage{msg}
	t.LastMessage = &msg
	return &t, nil
}

func (r *pgRepo) GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Ticket, error) {
	row, err := r.queries.GetTicketForOwner(ctx, sqlcdb.GetTicketForOwnerParams{ID: id, UserID: ownerID})
	if err != nil {
		return nil, mapErr(err)
	}
	return r.withMessages(ctx, fromSQLC(row))
}

func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	row, err := r.queries.GetTicketByID(ctx, id)
	if err != nil {
		return nil, mapErr(err)
	}
	t := fromSQLC(row.Ticket)
	t.UserPhone = deref(row.UserPhone)
	return r.withMessages(ctx, t)
}

func (r *pgRepo) ListForOwner(ctx context.Context, f ListFilter) ([]Ticket, error) {
	rows, err := r.queries.ListTicketsForOwner(ctx, sqlcdb.ListTicketsForOwnerParams{
		OwnerID: f.OwnerID,
		Status:  strPtr(f.Status),
		Limit:   f.Limit,
		Offset:  f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	out := make([]Ticket, len(rows))
	for i, row := range rows {
		out[i] = ticketWithLastMessage(row.Ticket, row.LastMessageSender, row.LastMessageText)
	}
	return out, nil
}

func (r *pgRepo) CountForOwner(ctx context.Context, ownerID uuid.UUID, status string) (int64, error) {
	n, err := r.queries.CountTicketsForOwner(ctx, sqlcdb.CountTicketsForOwnerParams{
		OwnerID: ownerID,
		Status:  strPtr(status),
	})
	if err != nil {
		return 0, fmt.Errorf("count tickets: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListAll(ctx context.Context, f AdminListFilter) ([]Ticket, error) {
	rows, err := r.queries.ListAllTickets(ctx, sqlcdb.ListAllTicketsParams{
		Status: strPtr(f.Status),
		UserID: f.UserID,
		Search: strPtr(f.Search),
		Limit:  f.Limit,
		Offset: f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list all tickets: %w", err)
	}
	out := make([]Ticket, len(rows))
	for i, row := range rows {
		t := ticketWithLastMessage(row.Ticket, row.LastMessageSender, row.LastMessageText)
		t.UserPhone = deref(row.UserPhone)
		out[i] = t
	}
	return out, nil
}

func (r *pgRepo) CountAll(ctx context.Context, f AdminListFilter) (int64, error) {
	n, err := r.queries.CountAllTickets(ctx, sqlcdb.CountAllTicketsParams{
		Status: strPtr(f.Status),
		UserID: f.UserID,
		Search: strPtr(f.Search),
	})
	if err != nil {
		return 0, fmt.Errorf("count all tickets: %w", err)
	}
	return n, nil
}

func (r *pgRepo) Reply(ctx context.Context, ownerID *uuid.UUID, in ReplyInput) (*Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reply: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := r.queries.WithTx(tx)

	// Confirm the ticket exists (and, for a user, that they own it) before
	// writing the message, so a mismatched owner can never leave a message
	// on someone else's ticket.
	var ticketID uuid.UUID
	if ownerID != nil {
		row, err := q.GetTicketForOwner(ctx, sqlcdb.GetTicketForOwnerParams{ID: in.TicketID, UserID: *ownerID})
		if err != nil {
			return nil, mapErr(err)
		}
		ticketID = row.ID
	} else {
		row, err := q.GetTicketByID(ctx, in.TicketID)
		if err != nil {
			return nil, mapErr(err)
		}
		ticketID = row.Ticket.ID
	}

	authorID := in.AuthorID
	if _, err := q.CreateTicketMessage(ctx, sqlcdb.CreateTicketMessageParams{
		ID:            uuid.New(),
		TicketID:      ticketID,
		Sender:        in.Sender,
		AuthorID:      &authorID,
		Message:       in.Message,
		AttachmentUrl: strPtr(in.AttachmentURL),
	}); err != nil {
		return nil, fmt.Errorf("create reply message: %w", err)
	}

	updated, err := q.UpdateTicketStatus(ctx, sqlcdb.UpdateTicketStatusParams{
		ID:      ticketID,
		Status:  in.NewStatus,
		OwnerID: ownerID,
	})
	if err != nil {
		return nil, mapErr(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reply: %w", err)
	}
	return r.withMessages(ctx, fromSQLC(updated))
}

func (r *pgRepo) SetStatus(ctx context.Context, id uuid.UUID, ownerID *uuid.UUID, status string) (*Ticket, error) {
	row, err := r.queries.UpdateTicketStatus(ctx, sqlcdb.UpdateTicketStatusParams{
		ID:      id,
		Status:  status,
		OwnerID: ownerID,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return r.withMessages(ctx, fromSQLC(row))
}

// withMessages loads the full conversation for a ticket row.
func (r *pgRepo) withMessages(ctx context.Context, t Ticket) (*Ticket, error) {
	rows, err := r.queries.ListTicketMessages(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("list ticket messages: %w", err)
	}
	t.Messages = make([]TicketMessage, len(rows))
	for i, row := range rows {
		t.Messages[i] = messageFromSQLC(row)
	}
	if len(t.Messages) > 0 {
		last := t.Messages[len(t.Messages)-1]
		t.LastMessage = &last
	}
	return &t, nil
}

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return database.ErrNotFound
	}
	return fmt.Errorf("query ticket: %w", err)
}

func fromSQLC(r sqlcdb.Ticket) Ticket {
	return Ticket{
		ID:        r.ID,
		UserID:    r.UserID,
		Subject:   r.Subject,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// ticketWithLastMessage builds a list-row Ticket from the joined last-message
// columns emitted by ListTicketsForOwner/ListAllTickets. An empty sender
// means the LEFT JOIN LATERAL found no message for the ticket.
func ticketWithLastMessage(r sqlcdb.Ticket, lastSender, lastMessage string) Ticket {
	t := fromSQLC(r)
	if lastSender != "" {
		t.LastMessage = &TicketMessage{Sender: lastSender, Message: lastMessage}
	}
	return t
}

func messageFromSQLC(r sqlcdb.TicketMessage) TicketMessage {
	return TicketMessage{
		ID:            r.ID,
		TicketID:      r.TicketID,
		Sender:        r.Sender,
		AuthorID:      r.AuthorID,
		Message:       r.Message,
		AttachmentURL: deref(r.AttachmentUrl),
		CreatedAt:     r.CreatedAt,
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
