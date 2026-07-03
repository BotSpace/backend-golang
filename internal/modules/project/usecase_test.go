package project

import (
	"context"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
// Only Create/Update/SetReleaseBotToken are exercised below; the rest of the
// interface is implemented with trivial stubs so fakeRepo satisfies
// Repository.
type fakeRepo struct {
	projects map[uuid.UUID]*Project
	envs     map[string]string // "<projectID>|<key>" -> value
	debugs   map[uuid.UUID]*ProjectDebug
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		projects: map[uuid.UUID]*Project{},
		envs:     map[string]string{},
		debugs:   map[uuid.UUID]*ProjectDebug{},
	}
}

func (r *fakeRepo) Create(_ context.Context, in CreateInput) (*Project, error) {
	p := &Project{
		ID:                 uuid.New(),
		UUID:               uuid.New(),
		Name:               in.Name,
		Description:        in.Description,
		Username:           in.Username,
		OwnerID:            in.OwnerID,
		Flow:               in.Flow,
		IsTemplate:         in.IsTemplate,
		ReleaseWebhookUUID: uuid.New(),
		DebugWebhookUUID:   uuid.New(),
	}
	r.projects[p.ID] = p
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) GetForOwner(_ context.Context, id, ownerID uuid.UUID) (*Project, error) {
	p, ok := r.projects[id]
	if !ok || p.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) Update(_ context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error) {
	p, ok := r.projects[id]
	if !ok || p.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.Username != nil {
		p.Username = *in.Username
	}
	if in.Flow != nil {
		p.Flow = in.Flow
	}
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) SoftDelete(_ context.Context, id, ownerID uuid.UUID) error {
	p, ok := r.projects[id]
	if !ok || p.OwnerID != ownerID {
		return database.ErrNotFound
	}
	delete(r.projects, id)
	return nil
}

func (r *fakeRepo) List(_ context.Context, f ListFilter) ([]Project, error) {
	var out []Project
	for _, p := range r.projects {
		if p.OwnerID == f.OwnerID {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (r *fakeRepo) Count(_ context.Context, ownerID uuid.UUID, _ string) (int64, error) {
	var n int64
	for _, p := range r.projects {
		if p.OwnerID == ownerID {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) ListAdmin(_ context.Context, _ AdminListFilter) ([]AdminProjectSummary, error) {
	return nil, nil
}
func (r *fakeRepo) CountAdmin(_ context.Context, _ string) (int64, error) { return 0, nil }
func (r *fakeRepo) GetAdmin(_ context.Context, _ uuid.UUID) (*AdminProjectDetail, error) {
	return nil, database.ErrNotFound
}
func (r *fakeRepo) ListMonitoring(_ context.Context, _ AdminListFilter) ([]MonitoringProject, error) {
	return nil, nil
}

func (r *fakeRepo) SetReleaseBotToken(_ context.Context, projectID, ownerID uuid.UUID, token string) error {
	p, ok := r.projects[projectID]
	if !ok || p.OwnerID != ownerID {
		return database.ErrNotFound
	}
	r.envs[projectID.String()+"|RELEASE_BOT_TOKEN"] = token
	return nil
}

func (r *fakeRepo) GetDebugForOwner(_ context.Context, projectID, _ uuid.UUID) (*ProjectDebug, error) {
	// Matches pgRepo: "no debug row yet" is nil,nil — never a hard error.
	return r.debugs[projectID], nil
}

func (r *fakeRepo) UpdateDebugStatus(_ context.Context, id uuid.UUID, serviceID, status string) (*ProjectDebug, error) {
	for _, d := range r.debugs {
		if d.ID == id {
			d.ServiceID = serviceID
			d.Status = status
			return d, nil
		}
	}
	return nil, database.ErrNotFound
}

// TestCreate_WithToken_StoresReleaseBotToken ports the fix for the
// BOT_TOKEN-not-found release panic: a token given at create time must land
// in the project's RELEASE_BOT_TOKEN environment variable (Django's
// EnviromentModel key="RELEASE_BOT_TOKEN", bot.py:39) so release.Usecase's
// envMap() — and from there kubernetes.go's BOT_TOKEN mapping — actually sees
// it.
func TestCreate_WithToken_StoresReleaseBotToken(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot", Token: "123:ABC"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]
	if got != "123:ABC" {
		t.Fatalf("RELEASE_BOT_TOKEN = %q, want %q", got, "123:ABC")
	}
}

// TestCreate_WithoutToken_DoesNotForceOne mirrors Django: a tokenless create
// must still succeed (no RELEASE_BOT_TOKEN written) — the dashboard's
// bot_token_required contract only kicks in later, at release-deploy time.
func TestCreate_WithoutToken_DoesNotForceOne(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]; ok {
		t.Fatalf("expected no RELEASE_BOT_TOKEN written for a tokenless create")
	}
}

// TestUpdate_WithToken_StoresReleaseBotToken covers the same wiring on the
// update path (dashboard's updateBotAction posts {..., token} on PATCH).
func TestUpdate_WithToken_StoresReleaseBotToken(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	token := "999:XYZ"
	if _, err := uc.Update(ctx, p.ID, ownerID, UpdateInput{Token: &token}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]
	if got != token {
		t.Fatalf("RELEASE_BOT_TOKEN = %q, want %q", got, token)
	}
}

// TestUpdate_BlankToken_LeavesReleaseBotTokenUntouched: a nil or blank Token
// on update must not clobber an existing RELEASE_BOT_TOKEN (only a non-blank
// value (re)writes it — matches CreateInput's same "store only if given"
// rule).
func TestUpdate_BlankToken_LeavesReleaseBotTokenUntouched(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot", Token: "123:ABC"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newName := "Renamed bot"
	if _, err := uc.Update(ctx, p.ID, ownerID, UpdateInput{Name: &newName}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]
	if got != "123:ABC" {
		t.Fatalf("RELEASE_BOT_TOKEN = %q, want unchanged %q", got, "123:ABC")
	}
}

// fakeOrchestrator is a minimal engine.Orchestrator test double — only
// Status is exercised by the tests below.
type fakeOrchestrator struct{ status engine.Status }

func (f *fakeOrchestrator) Deploy(context.Context, engine.DeploySpec) (engine.DeployResult, error) {
	return engine.DeployResult{}, nil
}
func (f *fakeOrchestrator) Stop(context.Context, string) error { return nil }
func (f *fakeOrchestrator) Status(context.Context, string) (engine.Status, error) {
	return f.status, nil
}

// TestGet_ReconcilesDebugStatus ports the same "hang on launching" fix as
// release.GetRelease/ListReleases, but for the tokenless debug-mode bot
// path: RetrieveProjectSerializer nests the debug row, and the dashboard's
// launch-status poll reads project.debug.status directly (no Release row
// exists for debug-mode bots).
func TestGet_ReconcilesDebugStatus(t *testing.T) {
	repo := newFakeRepo()
	orch := &fakeOrchestrator{status: engine.StatusRunning}
	uc := NewUsecase(repo, orch)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	repo.debugs[p.ID] = &ProjectDebug{ID: uuid.New(), ServiceID: "debug-" + p.ID.String(), Status: "initializing"}

	got, err := uc.Get(ctx, p.ID, ownerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Debug == nil {
		t.Fatal("expected Debug to be populated")
	}
	if got.Debug.Status != "running" {
		t.Fatalf("Debug.Status = %q, want running", got.Debug.Status)
	}

	// Persisted, not just returned once.
	if repo.debugs[p.ID].Status != "running" {
		t.Fatalf("expected reconciled status persisted, got %q", repo.debugs[p.ID].Status)
	}
}

// TestGet_NoDebugRow_LeavesDebugNil: a project that never started a debug
// container (no row yet) must serialize with debug omitted, not a zero-value
// object — matches the dashboard's `project?.debug?.status` optional chain.
func TestGet_NoDebugRow_LeavesDebugNil(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, &fakeOrchestrator{status: engine.StatusRunning})
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := uc.Get(ctx, p.ID, ownerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Debug != nil {
		t.Fatalf("expected nil Debug, got %+v", got.Debug)
	}
}
