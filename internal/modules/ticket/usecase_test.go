package ticket

import (
	"context"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required. It
// re-implements just enough of the ownership/status semantics that the real
// SQL enforces, so the usecase tests exercise real behavior.
type fakeRepo struct {
	tickets  map[uuid.UUID]*Ticket
	messages map[uuid.UUID][]TicketMessage
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{tickets: map[uuid.UUID]*Ticket{}, messages: map[uuid.UUID][]TicketMessage{}}
}

func (r *fakeRepo) Create(_ context.Context, in CreateInput) (*Ticket, error) {
	t := &Ticket{ID: uuid.New(), UserID: in.UserID, Subject: in.Subject, Status: StatusOpen}
	msg := TicketMessage{ID: uuid.New(), TicketID: t.ID, Sender: SenderUser, AuthorID: &in.UserID, Message: in.Message}
	r.tickets[t.ID] = t
	r.messages[t.ID] = []TicketMessage{msg}
	return r.snapshot(t.ID), nil
}

func (r *fakeRepo) GetForOwner(_ context.Context, id, ownerID uuid.UUID) (*Ticket, error) {
	t, ok := r.tickets[id]
	if !ok || t.UserID != ownerID {
		return nil, database.ErrNotFound
	}
	return r.snapshot(id), nil
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Ticket, error) {
	if _, ok := r.tickets[id]; !ok {
		return nil, database.ErrNotFound
	}
	return r.snapshot(id), nil
}

func (r *fakeRepo) ListForOwner(_ context.Context, f ListFilter) ([]Ticket, error) {
	var out []Ticket
	for id, t := range r.tickets {
		if t.UserID == f.OwnerID && (f.Status == "" || t.Status == f.Status) {
			out = append(out, *r.snapshot(id))
		}
	}
	return out, nil
}

func (r *fakeRepo) CountForOwner(_ context.Context, ownerID uuid.UUID, status string) (int64, error) {
	items, _ := r.ListForOwner(context.Background(), ListFilter{OwnerID: ownerID, Status: status})
	return int64(len(items)), nil
}

func (r *fakeRepo) ListAll(_ context.Context, f AdminListFilter) ([]Ticket, error) {
	var out []Ticket
	for id, t := range r.tickets {
		if f.Status != "" && t.Status != f.Status {
			continue
		}
		if f.UserID != nil && t.UserID != *f.UserID {
			continue
		}
		out = append(out, *r.snapshot(id))
	}
	return out, nil
}

func (r *fakeRepo) CountAll(ctx context.Context, f AdminListFilter) (int64, error) {
	items, _ := r.ListAll(ctx, f)
	return int64(len(items)), nil
}

func (r *fakeRepo) Reply(_ context.Context, ownerID *uuid.UUID, in ReplyInput) (*Ticket, error) {
	t, ok := r.tickets[in.TicketID]
	if !ok || (ownerID != nil && t.UserID != *ownerID) {
		return nil, database.ErrNotFound
	}
	author := in.AuthorID
	r.messages[t.ID] = append(r.messages[t.ID], TicketMessage{
		ID: uuid.New(), TicketID: t.ID, Sender: in.Sender, AuthorID: &author, Message: in.Message,
	})
	t.Status = in.NewStatus
	return r.snapshot(t.ID), nil
}

func (r *fakeRepo) SetStatus(_ context.Context, id uuid.UUID, ownerID *uuid.UUID, status string) (*Ticket, error) {
	t, ok := r.tickets[id]
	if !ok || (ownerID != nil && t.UserID != *ownerID) {
		return nil, database.ErrNotFound
	}
	t.Status = status
	return r.snapshot(id), nil
}

func (r *fakeRepo) snapshot(id uuid.UUID) *Ticket {
	cp := *r.tickets[id]
	cp.Messages = append([]TicketMessage(nil), r.messages[id]...)
	if len(cp.Messages) > 0 {
		last := cp.Messages[len(cp.Messages)-1]
		cp.LastMessage = &last
	}
	return &cp
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestCreateThenAdminRespondThenUserReplyFlipsStatus(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	ctx := context.Background()
	owner := uuid.New()
	admin := uuid.New()

	tk, err := uc.Create(ctx, CreateInput{UserID: owner, Subject: "Help", Message: "It's broken"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tk.Status != StatusOpen {
		t.Fatalf("expected open status after create, got %s", tk.Status)
	}
	if len(tk.Messages) != 1 {
		t.Fatalf("expected 1 message after create, got %d", len(tk.Messages))
	}

	tk, err = uc.AdminRespond(ctx, tk.ID, admin, "Looking into it", "")
	if err != nil {
		t.Fatalf("admin respond: %v", err)
	}
	if tk.Status != StatusPending {
		t.Fatalf("expected pending status after admin respond, got %s", tk.Status)
	}

	tk, err = uc.Reply(ctx, tk.ID, owner, "Still broken", "")
	if err != nil {
		t.Fatalf("user reply: %v", err)
	}
	if tk.Status != StatusOpen {
		t.Fatalf("expected open status after user reply, got %s", tk.Status)
	}
	if len(tk.Messages) != 3 {
		t.Fatalf("expected 3 messages in thread, got %d", len(tk.Messages))
	}
}

func TestOwnershipIsEnforcedOnGetReplyAndClose(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	ctx := context.Background()
	owner := uuid.New()
	intruder := uuid.New()

	tk, err := uc.Create(ctx, CreateInput{UserID: owner, Subject: "Help", Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := uc.Get(ctx, tk.ID, intruder); status(err) != 404 {
		t.Fatalf("expected 404 for a stranger's Get, got %v", err)
	}
	if _, err := uc.Reply(ctx, tk.ID, intruder, "hijack", ""); status(err) != 404 {
		t.Fatalf("expected 404 for a stranger's Reply, got %v", err)
	}
	if _, err := uc.Close(ctx, tk.ID, intruder); status(err) != 404 {
		t.Fatalf("expected 404 for a stranger's Close, got %v", err)
	}

	if _, err := uc.Close(ctx, tk.ID, owner); err != nil {
		t.Fatalf("owner close: %v", err)
	}
}

func TestCreateRejectsBlankSubjectOrMessage(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	ctx := context.Background()
	owner := uuid.New()

	if _, err := uc.Create(ctx, CreateInput{UserID: owner, Subject: "   ", Message: "hi"}); status(err) != 400 {
		t.Fatalf("expected 400 for blank subject, got %v", err)
	}
	if _, err := uc.Create(ctx, CreateInput{UserID: owner, Subject: "Help", Message: "  "}); status(err) != 400 {
		t.Fatalf("expected 400 for blank message, got %v", err)
	}
}

func TestListRejectsInvalidStatusFilter(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	ctx := context.Background()
	owner := uuid.New()

	if _, _, err := uc.List(ctx, ListFilter{OwnerID: owner, Status: "bogus"}); status(err) != 400 {
		t.Fatalf("expected 400 for invalid status filter, got %v", err)
	}
}
