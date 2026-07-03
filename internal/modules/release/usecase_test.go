package release

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
// It only enforces the one invariant the tests below care about: every
// method is scoped to (projectID, ownerID), mirroring the SQL joins in
// queries/release.sql.
type fakeRepo struct {
	projectOwner map[uuid.UUID]uuid.UUID
	projectFlow  map[uuid.UUID]json.RawMessage
	releases     map[uuid.UUID]*Release
	debugs       map[uuid.UUID]*Debug // keyed by projectID (one-to-one)
	envs         map[string]*Environment
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		projectOwner: map[uuid.UUID]uuid.UUID{},
		projectFlow:  map[uuid.UUID]json.RawMessage{},
		releases:     map[uuid.UUID]*Release{},
		debugs:       map[uuid.UUID]*Debug{},
		envs:         map[string]*Environment{},
	}
}

func (r *fakeRepo) addProject(ownerID uuid.UUID, flow json.RawMessage) uuid.UUID {
	id := uuid.New()
	r.projectOwner[id] = ownerID
	r.projectFlow[id] = flow
	return id
}

func (r *fakeRepo) owns(projectID, ownerID uuid.UUID) bool {
	owner, ok := r.projectOwner[projectID]
	return ok && owner == ownerID
}

func (r *fakeRepo) CreateRelease(_ context.Context, projectID, ownerID uuid.UUID, version int32) (*Release, error) {
	if !r.owns(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	rel := &Release{
		ID:        uuid.New(),
		ProjectID: projectID,
		Version:   version,
		Flow:      r.projectFlow[projectID],
		Status:    StatusPending,
	}
	r.releases[rel.ID] = rel
	cp := *rel
	return &cp, nil
}

func (r *fakeRepo) GetReleaseForOwner(_ context.Context, id, ownerID uuid.UUID) (*Release, error) {
	rel, ok := r.releases[id]
	if !ok || !r.owns(rel.ProjectID, ownerID) {
		return nil, database.ErrNotFound
	}
	cp := *rel
	return &cp, nil
}

func (r *fakeRepo) ListReleasesForProject(_ context.Context, projectID, ownerID uuid.UUID) ([]Release, error) {
	if !r.owns(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	var out []Release
	for _, rel := range r.releases {
		if rel.ProjectID == projectID {
			out = append(out, *rel)
		}
	}
	return out, nil
}

func (r *fakeRepo) LatestReleaseVersion(_ context.Context, projectID, ownerID uuid.UUID) (int32, error) {
	if !r.owns(projectID, ownerID) {
		return 0, database.ErrNotFound
	}
	var max int32
	for _, rel := range r.releases {
		if rel.ProjectID == projectID && rel.Version > max {
			max = rel.Version
		}
	}
	return max, nil
}

func (r *fakeRepo) UpdateReleaseDeployResult(_ context.Context, id uuid.UUID, serviceID string, status Status) (*Release, error) {
	rel, ok := r.releases[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	rel.ServiceID = serviceID
	rel.Status = status
	cp := *rel
	return &cp, nil
}

func (r *fakeRepo) UpdateReleaseStatusForOwner(_ context.Context, id, ownerID uuid.UUID, status Status) (*Release, error) {
	rel, ok := r.releases[id]
	if !ok || !r.owns(rel.ProjectID, ownerID) {
		return nil, database.ErrNotFound
	}
	rel.Status = status
	cp := *rel
	return &cp, nil
}

func (r *fakeRepo) StopActiveReleasesExcept(_ context.Context, projectID, excludeID uuid.UUID) ([]Release, error) {
	var stopped []Release
	for _, rel := range r.releases {
		if rel.ProjectID != projectID || rel.ID == excludeID {
			continue
		}
		if rel.Status == StatusRunning || rel.Status == StatusInitializing {
			rel.Status = StatusStopped
			stopped = append(stopped, *rel)
		}
	}
	return stopped, nil
}

func (r *fakeRepo) GetDebugForOwner(_ context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	dbg, ok := r.debugs[projectID]
	if !ok || !r.owns(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	cp := *dbg
	return &cp, nil
}

func (r *fakeRepo) GetOrCreateDebugForOwner(_ context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	if !r.owns(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	dbg, ok := r.debugs[projectID]
	if !ok {
		dbg = &Debug{ID: uuid.New(), ProjectID: projectID, Status: StatusPending}
		r.debugs[projectID] = dbg
	}
	cp := *dbg
	return &cp, nil
}

func (r *fakeRepo) UpdateDebugDeployResult(_ context.Context, id uuid.UUID, serviceID string, status Status) (*Debug, error) {
	for _, dbg := range r.debugs {
		if dbg.ID == id {
			dbg.ServiceID = serviceID
			dbg.Status = status
			cp := *dbg
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) UpdateDebugStatusForOwner(_ context.Context, id, ownerID uuid.UUID, status Status) (*Debug, error) {
	for _, dbg := range r.debugs {
		if dbg.ID == id && r.owns(dbg.ProjectID, ownerID) {
			dbg.Status = status
			cp := *dbg
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) UpsertEnvironment(_ context.Context, in UpsertEnvironmentInput) (*Environment, error) {
	if !r.owns(in.ProjectID, in.OwnerID) {
		return nil, database.ErrNotFound
	}
	k := envKey(in.ProjectID, in.Key)
	env, ok := r.envs[k]
	if !ok {
		env = &Environment{ID: uuid.New(), ProjectID: in.ProjectID, Key: in.Key}
		r.envs[k] = env
	}
	env.UserID = in.UserID
	env.Value = in.Value
	env.Secret = in.Secret
	cp := *env
	return &cp, nil
}

func (r *fakeRepo) ListEnvironmentsForOwner(_ context.Context, projectID, ownerID uuid.UUID) ([]Environment, error) {
	if !r.owns(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	return r.ListEnvironmentsByProject(context.Background(), projectID)
}

func (r *fakeRepo) GetEnvironmentForOwner(_ context.Context, key string, projectID, ownerID uuid.UUID) (*Environment, error) {
	if !r.owns(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	env, ok := r.envs[envKey(projectID, key)]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *env
	return &cp, nil
}

func (r *fakeRepo) DeleteEnvironmentForOwner(_ context.Context, key string, projectID, ownerID uuid.UUID) error {
	if !r.owns(projectID, ownerID) {
		return database.ErrNotFound
	}
	k := envKey(projectID, key)
	if _, ok := r.envs[k]; !ok {
		return database.ErrNotFound
	}
	delete(r.envs, k)
	return nil
}

func (r *fakeRepo) ListEnvironmentsByProject(_ context.Context, projectID uuid.UUID) ([]Environment, error) {
	var out []Environment
	for _, env := range r.envs {
		if env.ProjectID == projectID {
			out = append(out, *env)
		}
	}
	return out, nil
}

func (r *fakeRepo) GetProjectFlowForOwner(_ context.Context, projectID, ownerID uuid.UUID) (json.RawMessage, error) {
	if !r.owns(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	return r.projectFlow[projectID], nil
}

func envKey(projectID uuid.UUID, key string) string { return projectID.String() + "|" + key }

// fakeOrchestrator records every Deploy/Stop call so tests can assert on the
// cleanup behavior without a real engine-golang client.
type fakeOrchestrator struct {
	deployed []engine.DeploySpec
	stopped  []string
	status   engine.Status
}

func newFakeOrchestrator() *fakeOrchestrator {
	return &fakeOrchestrator{status: engine.StatusRunning}
}

func (f *fakeOrchestrator) Deploy(_ context.Context, spec engine.DeploySpec) (engine.DeployResult, error) {
	f.deployed = append(f.deployed, spec)
	return engine.DeployResult{ServiceID: "svc-" + spec.ReleaseUUID.String(), Status: f.status}, nil
}

func (f *fakeOrchestrator) Stop(_ context.Context, serviceID string) error {
	f.stopped = append(f.stopped, serviceID)
	return nil
}

func (f *fakeOrchestrator) Status(context.Context, string) (engine.Status, error) {
	return f.status, nil
}

// TestCreateReleaseStopsPriorActiveReleases ports Django's RelaseSignal
// cleanup rule (core/apps/api/signals/project.py, tests/test_release_cleanup.py):
// creating a new release must stop every RUNNING/INIT release the project had
// before, and call the orchestrator to stop their live containers too.
func TestCreateReleaseStopsPriorActiveReleases(t *testing.T) {
	repo := newFakeRepo()
	orch := newFakeOrchestrator()
	uc := NewUsecase(repo, orch)
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))

	first, err := uc.CreateRelease(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("create first release: %v", err)
	}
	if first.Status != StatusRunning {
		t.Fatalf("expected first release running, got %s", first.Status)
	}

	second, err := uc.CreateRelease(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("create second release: %v", err)
	}
	if second.Version != first.Version+1 {
		t.Fatalf("expected version to bump, got %d after %d", second.Version, first.Version)
	}

	reloadedFirst, err := uc.GetRelease(ctx, first.ID, ownerID)
	if err != nil {
		t.Fatalf("get first release: %v", err)
	}
	if reloadedFirst.Status != StatusStopped {
		t.Fatalf("expected prior release to be stopped, got %s", reloadedFirst.Status)
	}

	if len(orch.stopped) != 1 || orch.stopped[0] != first.ServiceID {
		t.Fatalf("expected orchestrator.Stop called for prior release's service, got %v", orch.stopped)
	}
	if len(orch.deployed) != 2 {
		t.Fatalf("expected 2 deploy calls, got %d", len(orch.deployed))
	}
}

// TestUpsertEnvironmentIsIdempotentByKey ports the CreateEnviromentSerializer
// upsert semantics: re-posting the same key+project overwrites the value
// instead of erroring, and a project owned by someone else is rejected.
func TestUpsertEnvironmentIsIdempotentByKey(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, newFakeOrchestrator())
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))

	in := UpsertEnvironmentInput{ProjectID: projectID, OwnerID: ownerID, UserID: ownerID, Key: "TOKEN", Value: "v1"}
	if _, err := uc.UpsertEnvironment(ctx, in); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	in.Value = "v2"
	env, err := uc.UpsertEnvironment(ctx, in)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if env.Value != "v2" {
		t.Fatalf("expected overwritten value v2, got %q", env.Value)
	}

	items, err := uc.ListEnvironments(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("list environments: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one row after upsert, got %d", len(items))
	}

	// A stranger owner must not be able to write into this project's env.
	stranger := uuid.New()
	if _, err := uc.UpsertEnvironment(ctx, UpsertEnvironmentInput{
		ProjectID: projectID, OwnerID: stranger, UserID: stranger, Key: "X", Value: "y",
	}); err == nil {
		t.Fatal("expected error when a non-owner writes an environment variable")
	}
}
