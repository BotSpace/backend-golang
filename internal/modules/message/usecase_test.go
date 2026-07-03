package message

import (
	"context"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
// projects maps a project id to its owner, mirroring the SQL ownership check
// performed transitively through projects.owner_id.
type fakeRepo struct {
	projects map[uuid.UUID]uuid.UUID
	byID     map[uuid.UUID]*Message
}

func newFakeRepo(projects map[uuid.UUID]uuid.UUID) *fakeRepo {
	return &fakeRepo{projects: projects, byID: map[uuid.UUID]*Message{}}
}

func (r *fakeRepo) Create(_ context.Context, in CreateInput) (*Message, error) {
	if owner, ok := r.projects[in.ProjectID]; !ok || owner != in.OwnerID {
		return nil, database.ErrNotFound
	}
	m := &Message{
		ID:          uuid.New(),
		ProjectID:   in.ProjectID,
		Caption:     in.Caption,
		MessageType: in.MessageType,
		FileID:      in.FileID,
		ButtonType:  in.ButtonType,
		Buttons:     in.Buttons,
	}
	r.byID[m.ID] = m
	cp := *m
	return &cp, nil
}

func (r *fakeRepo) GetForOwner(_ context.Context, id, ownerID uuid.UUID) (*Message, error) {
	m, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if r.projects[m.ProjectID] != ownerID {
		return nil, database.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *fakeRepo) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Message, error) {
	m, err := r.GetForOwner(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}
	if in.Caption != nil {
		m.Caption = *in.Caption
	}
	if in.MessageType != nil {
		m.MessageType = *in.MessageType
	}
	if in.FileID != nil {
		m.FileID = in.FileID
	}
	if in.ButtonType != nil {
		m.ButtonType = *in.ButtonType
	}
	if in.Buttons != nil {
		m.Buttons = in.Buttons
	}
	r.byID[id] = m
	cp := *m
	return &cp, nil
}

func (r *fakeRepo) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	if _, err := r.GetForOwner(ctx, id, ownerID); err != nil {
		return err
	}
	delete(r.byID, id)
	return nil
}

func (r *fakeRepo) List(_ context.Context, f ListFilter) ([]Message, error) {
	var out []Message
	for _, m := range r.byID {
		if r.projects[m.ProjectID] != f.OwnerID {
			continue
		}
		if f.ProjectID != nil && m.ProjectID != *f.ProjectID {
			continue
		}
		out = append(out, *m)
	}
	return out, nil
}

func (r *fakeRepo) Count(_ context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error) {
	var n int64
	for _, m := range r.byID {
		if r.projects[m.ProjectID] != ownerID {
			continue
		}
		if projectID != nil && m.ProjectID != *projectID {
			continue
		}
		n++
	}
	return n, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestCreateAppliesDefaultsAndRoundtrips(t *testing.T) {
	ownerID := uuid.New()
	projectID := uuid.New()
	repo := newFakeRepo(map[uuid.UUID]uuid.UUID{projectID: ownerID})
	uc := NewUsecase(repo)
	ctx := context.Background()

	m, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, ProjectID: projectID, Caption: "hello"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.MessageType != TypeText {
		t.Fatalf("expected default message_type %q, got %q", TypeText, m.MessageType)
	}
	if m.ButtonType != ButtonReply {
		t.Fatalf("expected default button_type %q, got %q", ButtonReply, m.ButtonType)
	}

	got, err := uc.Get(ctx, m.ID, ownerID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Caption != "hello" {
		t.Fatalf("expected caption %q, got %q", "hello", got.Caption)
	}
}

func TestCreateForForeignProjectIsNotFound(t *testing.T) {
	ownerID := uuid.New()
	otherOwner := uuid.New()
	projectID := uuid.New()
	repo := newFakeRepo(map[uuid.UUID]uuid.UUID{projectID: otherOwner})
	uc := NewUsecase(repo)
	ctx := context.Background()

	if _, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, ProjectID: projectID}); status(err) != 404 {
		t.Fatalf("expected 404 for foreign project, got %v", err)
	}
}

func TestGetUnknownMessageIsNotFound(t *testing.T) {
	uc := NewUsecase(newFakeRepo(nil))
	if _, err := uc.Get(context.Background(), uuid.New(), uuid.New()); status(err) != 404 {
		t.Fatalf("expected 404 for unknown message, got %v", err)
	}
}
