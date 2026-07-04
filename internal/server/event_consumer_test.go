package server

import (
	"context"
	"testing"

	debugmod "github.com/botspace/backend/internal/modules/debug"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/google/uuid"
)

// fakeDebugUsecase is a minimal debugmod.Usecase stub — only
// ResolveProjectRef and GetProjectOwner are exercised by
// resolveDebugEventOwner, so every other method just panics if ever called.
type fakeDebugUsecase struct {
	debugmod.Usecase
	resolved   map[string]uuid.UUID
	owners     map[uuid.UUID]uuid.UUID
	resolveErr error
	ownerErr   error
}

func (f *fakeDebugUsecase) ResolveProjectRef(_ context.Context, raw string) (uuid.UUID, error) {
	if f.resolveErr != nil {
		return uuid.Nil, f.resolveErr
	}
	id, ok := f.resolved[raw]
	if !ok {
		return uuid.Nil, database.ErrNotFound
	}
	return id, nil
}

func (f *fakeDebugUsecase) GetProjectOwner(_ context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	if f.ownerErr != nil {
		return uuid.Nil, f.ownerErr
	}
	owner, ok := f.owners[projectID]
	if !ok {
		return uuid.Nil, database.ErrNotFound
	}
	return owner, nil
}

// TestResolveDebugEventOwner_HappyPath verifies the engine's raw project_id
// reference (legacy int pk or Django project.uuid, as sent over
// "backend:events") resolves all the way down to the project's owner_id, so
// startEventConsumer's node_execution/context/error forwarding can publish to
// wsevents.UserRoom(owner) — the room the constructor's ws-hub actually
// joins (see internal/shared/wsevents doc comment).
func TestResolveDebugEventOwner_HappyPath(t *testing.T) {
	projectID := uuid.New()
	ownerID := uuid.New()
	uc := &fakeDebugUsecase{
		resolved: map[string]uuid.UUID{"1408": projectID},
		owners:   map[uuid.UUID]uuid.UUID{projectID: ownerID},
	}

	got, err := resolveDebugEventOwner(context.Background(), uc, "1408")
	if err != nil {
		t.Fatalf("resolveDebugEventOwner: %v", err)
	}
	if got != ownerID {
		t.Fatalf("expected owner %s, got %s", ownerID, got)
	}
}

// TestResolveDebugEventOwner_UnknownRef ensures an unresolvable project_id
// (stale/bogus ref from the engine) surfaces an error instead of a zero-value
// uuid, so the caller skips the WS publish rather than broadcasting to
// "user_00000000-0000-0000-0000-000000000000".
func TestResolveDebugEventOwner_UnknownRef(t *testing.T) {
	uc := &fakeDebugUsecase{resolved: map[string]uuid.UUID{}, owners: map[uuid.UUID]uuid.UUID{}}

	if _, err := resolveDebugEventOwner(context.Background(), uc, "does-not-exist"); err == nil {
		t.Fatal("expected an error for an unresolvable project ref")
	}
}

// TestResolveDebugEventOwner_OwnerLookupFails covers the second failure mode:
// the project ref resolves fine, but the owner lookup itself fails (e.g. the
// project row vanished between resolution and the owner query).
func TestResolveDebugEventOwner_OwnerLookupFails(t *testing.T) {
	projectID := uuid.New()
	uc := &fakeDebugUsecase{
		resolved: map[string]uuid.UUID{"1408": projectID},
		owners:   map[uuid.UUID]uuid.UUID{},
	}

	if _, err := resolveDebugEventOwner(context.Background(), uc, "1408"); err == nil {
		t.Fatal("expected an error when the owner lookup fails")
	}
}
