package debug

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	nodeEvents []NodeEvent
}

// fakeCache is an in-memory ContextCache — no Redis required.
type fakeCache struct {
	sets map[string]json.RawMessage
}

func newFakeCache() *fakeCache { return &fakeCache{sets: map[string]json.RawMessage{}} }

func (c *fakeCache) SetContext(_ context.Context, projectID uuid.UUID, chatID int64, variables json.RawMessage, _ time.Duration) error {
	c.sets[contextCacheKey(projectID, chatID)] = variables
	return nil
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

func (r *fakeRepo) InsertNodeEvent(_ context.Context, e NodeEvent) (*NodeEvent, error) {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	r.nodeEvents = append(r.nodeEvents, e)
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

func TestRecordNodeEvent_ScopesByResolvedProject(t *testing.T) {
	repo := newFakeRepo()
	internal := uuid.New()
	repo.byLegacyID[1408] = internal
	uc := NewUsecase(repo)

	if err := uc.RecordNodeEvent(context.Background(), RecordNodeEventInput{
		ProjectRef: "1408", ChatID: 42, NodeID: "n1", NodeType: "message",
		Status: "done", RunID: 7, Seq: 3,
	}); err != nil {
		t.Fatalf("record node event: %v", err)
	}

	if len(repo.nodeEvents) != 1 {
		t.Fatalf("expected 1 node event, got %d", len(repo.nodeEvents))
	}
	got := repo.nodeEvents[0]
	if got.ProjectID != internal || got.ChatID != 42 || got.Status != "done" || got.RunID != 7 || got.Seq != 3 {
		t.Fatalf("unexpected node event: %#v", got)
	}
}

func TestRecordNodeEvent_UnknownProjectRef(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	if err := uc.RecordNodeEvent(context.Background(), RecordNodeEventInput{ProjectRef: "999999"}); status(err) != 404 {
		t.Fatalf("expected 404 for unknown legacy id, got %v", err)
	}
}

func TestRecordContext_CachesLatestSnapshot(t *testing.T) {
	repo := newFakeRepo()
	internal := uuid.New()
	repo.byLegacyID[1408] = internal
	cache := newFakeCache()
	uc := NewUsecaseWithContextCache(repo, cache)

	vars := json.RawMessage(`{"foo":"bar"}`)
	if err := uc.RecordContext(context.Background(), RecordContextEventInput{
		ProjectRef: "1408", ChatID: 42, Variables: vars,
	}); err != nil {
		t.Fatalf("record context: %v", err)
	}

	got, ok := cache.sets[contextCacheKey(internal, 42)]
	if !ok {
		t.Fatalf("expected a cached snapshot for project %s chat 42", internal)
	}
	if string(got) != string(vars) {
		t.Fatalf("unexpected cached variables: %s", got)
	}
}

func TestRecordContext_NoopsWithoutCache(t *testing.T) {
	repo := newFakeRepo()
	repo.byLegacyID[1408] = uuid.New()
	uc := NewUsecase(repo) // no ContextCache wired

	if err := uc.RecordContext(context.Background(), RecordContextEventInput{
		ProjectRef: "1408", ChatID: 42, Variables: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("expected no-op (nil error) without a cache, got %v", err)
	}
}
