package debug

import (
	"context"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository — no Postgres required.
type fakeRepo struct {
	byLegacyID map[int32]uuid.UUID
	byUUID     map[uuid.UUID]uuid.UUID // Django project.uuid -> this backend's internal id
	owners     map[uuid.UUID]uuid.UUID // internal id -> owner
	events     []ErrorEvent
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byLegacyID: map[int32]uuid.UUID{},
		byUUID:     map[uuid.UUID]uuid.UUID{},
		owners:     map[uuid.UUID]uuid.UUID{},
	}
}

func (r *fakeRepo) ResolveProjectByLegacyID(_ context.Context, legacyID int32) (uuid.UUID, error) {
	id, ok := r.byLegacyID[legacyID]
	if !ok {
		return uuid.Nil, database.ErrNotFound
	}
	return id, nil
}

func (r *fakeRepo) ResolveProjectByUUID(_ context.Context, projectUUID uuid.UUID) (uuid.UUID, error) {
	id, ok := r.byUUID[projectUUID]
	if !ok {
		return uuid.Nil, database.ErrNotFound
	}
	return id, nil
}

func (r *fakeRepo) InsertErrorEvent(_ context.Context, e ErrorEvent) (*ErrorEvent, error) {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	r.events = append(r.events, e)
	return &e, nil
}

func (r *fakeRepo) ListErrorEventsForOwner(_ context.Context, ownerID, projectID uuid.UUID, limit int32) ([]ErrorEvent, error) {
	if r.owners[projectID] != ownerID {
		return nil, nil
	}
	var out []ErrorEvent
	for _, e := range r.events {
		if e.ProjectID == projectID {
			out = append(out, e)
		}
	}
	return out, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestResolveProjectRef_LegacyIntPk(t *testing.T) {
	repo := newFakeRepo()
	internal := uuid.New()
	repo.byLegacyID[1408] = internal
	uc := NewUsecase(repo)

	got, err := uc.ResolveProjectRef(context.Background(), "1408")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != internal {
		t.Fatalf("expected %s, got %s", internal, got)
	}
}

func TestResolveProjectRef_DjangoUUID(t *testing.T) {
	repo := newFakeRepo()
	djangoUUID := uuid.New()
	internal := uuid.New()
	repo.byUUID[djangoUUID] = internal
	uc := NewUsecase(repo)

	got, err := uc.ResolveProjectRef(context.Background(), djangoUUID.String())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != internal {
		t.Fatalf("expected %s, got %s", internal, got)
	}
}

func TestResolveProjectRef_UnknownRef(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	if _, err := uc.ResolveProjectRef(context.Background(), "999999"); status(err) != 404 {
		t.Fatalf("expected 404 for unknown legacy id, got %v", err)
	}
	if _, err := uc.ResolveProjectRef(context.Background(), "not-a-ref"); status(err) != 400 {
		t.Fatalf("expected 400 for garbage ref, got %v", err)
	}
}

func TestRecordError_ScopesByResolvedProject(t *testing.T) {
	repo := newFakeRepo()
	owner := uuid.New()
	internal := uuid.New()
	repo.byLegacyID[1408] = internal
	repo.owners[internal] = owner
	uc := NewUsecase(repo)
	ctx := context.Background()

	if err := uc.RecordError(ctx, RecordErrorInput{
		ProjectRef: "1408", ChatID: 42, NodeID: "n1", NodeType: "message",
		Message: "boom",
	}); err != nil {
		t.Fatalf("record error: %v", err)
	}

	items, err := uc.ListErrors(ctx, owner, internal, 10)
	if err != nil {
		t.Fatalf("list errors: %v", err)
	}
	if len(items) != 1 || items[0].Message != "boom" || items[0].ChatID != 42 {
		t.Fatalf("unexpected items: %#v", items)
	}
	if items[0].Level != "error" {
		t.Fatalf("expected default level 'error', got %q", items[0].Level)
	}

	// A different owner must see nothing for the same project.
	otherOwner := uuid.New()
	items, err = uc.ListErrors(ctx, otherOwner, internal, 10)
	if err != nil {
		t.Fatalf("list errors (other owner): %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items for a non-owning caller, got %#v", items)
	}
}
