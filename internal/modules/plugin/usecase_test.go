package plugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
// Ownership is modelled with a tiny projectID->ownerID map, standing in for
// the `projects` table the real queries join against.
type fakeRepo struct {
	modules map[uuid.UUID]*Module
	bySlug  map[string]uuid.UUID
	links   map[[2]uuid.UUID]*ProjectModule // key: [projectID, moduleID]
	owners  map[uuid.UUID]uuid.UUID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		modules: map[uuid.UUID]*Module{},
		bySlug:  map[string]uuid.UUID{},
		links:   map[[2]uuid.UUID]*ProjectModule{},
		owners:  map[uuid.UUID]uuid.UUID{},
	}
}

func (r *fakeRepo) addProject(projectID, ownerID uuid.UUID) { r.owners[projectID] = ownerID }

func (r *fakeRepo) linkKey(p, m uuid.UUID) [2]uuid.UUID { return [2]uuid.UUID{p, m} }

func (r *fakeRepo) checkOwner(projectID, ownerID uuid.UUID) bool {
	o, ok := r.owners[projectID]
	return ok && o == ownerID
}

func (r *fakeRepo) CreateModule(_ context.Context, in CreateModuleInput) (*Module, error) {
	if _, ok := r.bySlug[in.Slug]; ok {
		return nil, ErrSlugTaken
	}
	id := uuid.New()
	port := in.Port
	if port == 0 {
		port = 8100
	}
	m := &Module{
		ID: id, Slug: in.Slug, Name: in.Name, Description: in.Description, Icon: in.Icon,
		GithubURL: in.GithubURL, Image: in.Image, Port: port, AuthToken: in.AuthToken,
		Active: in.Active, IsDefault: in.IsDefault, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.modules[id] = m
	r.bySlug[in.Slug] = id
	cp := *m
	return &cp, nil
}

func (r *fakeRepo) GetModuleBySlug(_ context.Context, slug string) (*Module, error) {
	id, ok := r.bySlug[slug]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *r.modules[id]
	return &cp, nil
}

func (r *fakeRepo) GetModuleByID(_ context.Context, id uuid.UUID) (*Module, error) {
	m, ok := r.modules[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *fakeRepo) UpdateModule(_ context.Context, id uuid.UUID, in UpdateModuleInput) (*Module, error) {
	m, ok := r.modules[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if in.Name != nil {
		m.Name = *in.Name
	}
	if in.Active != nil {
		m.Active = *in.Active
	}
	m.UpdatedAt = time.Now()
	cp := *m
	return &cp, nil
}

func (r *fakeRepo) DeleteModule(_ context.Context, id uuid.UUID) error {
	m, ok := r.modules[id]
	if !ok {
		return database.ErrNotFound
	}
	delete(r.modules, id)
	delete(r.bySlug, m.Slug)
	return nil
}

func (r *fakeRepo) ListModules(_ context.Context, _ ModuleListFilter) ([]Module, error) {
	out := make([]Module, 0, len(r.modules))
	for _, m := range r.modules {
		out = append(out, *m)
	}
	return out, nil
}

func (r *fakeRepo) CountModules(_ context.Context, _ ModuleListFilter) (int64, error) {
	return int64(len(r.modules)), nil
}

func (r *fakeRepo) ListModulesWithGithubURL(_ context.Context) ([]Module, error) {
	out := []Module{}
	for _, m := range r.modules {
		if m.GithubURL != "" {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (r *fakeRepo) UpdateModuleManifest(_ context.Context, id uuid.UUID, manifest json.RawMessage) (*Module, error) {
	m, ok := r.modules[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	m.ManifestCache = manifest
	now := time.Now()
	m.ManifestFetchedAt = &now
	cp := *m
	return &cp, nil
}

func (r *fakeRepo) GetProjectModuleForOwner(_ context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error) {
	if !r.checkOwner(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	pm, ok := r.links[r.linkKey(projectID, moduleID)]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *pm
	return &cp, nil
}

func (r *fakeRepo) EnsureProjectModuleForOwner(_ context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error) {
	if !r.checkOwner(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	key := r.linkKey(projectID, moduleID)
	if pm, ok := r.links[key]; ok {
		pm.UpdatedAt = time.Now()
		cp := *pm
		return &cp, nil
	}
	pm := &ProjectModule{
		ID: uuid.New(), ProjectID: projectID, ModuleID: moduleID, Enabled: true,
		Status: "pending", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.links[key] = pm
	cp := *pm
	return &cp, nil
}

func (r *fakeRepo) UpdateProjectModuleDeployState(_ context.Context, id uuid.UUID, in DeployStateInput) (*ProjectModule, error) {
	for _, pm := range r.links {
		if pm.ID == id {
			pm.Enabled = true
			pm.ServiceID = in.ServiceID
			pm.Endpoint = in.Endpoint
			pm.Status = in.Status
			pm.UpdatedAt = time.Now()
			cp := *pm
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) SetProjectModuleDisabledForOwner(_ context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error) {
	if !r.checkOwner(projectID, ownerID) {
		return nil, database.ErrNotFound
	}
	pm, ok := r.links[r.linkKey(projectID, moduleID)]
	if !ok {
		return nil, database.ErrNotFound
	}
	pm.Enabled = false
	pm.Status = "stopped"
	pm.UpdatedAt = time.Now()
	cp := *pm
	return &cp, nil
}

func (r *fakeRepo) ListProjectModulesForOwner(_ context.Context, projectID, ownerID uuid.UUID) ([]ProjectModule, error) {
	if !r.checkOwner(projectID, ownerID) {
		return nil, nil
	}
	out := []ProjectModule{}
	for _, pm := range r.links {
		if pm.ProjectID == projectID {
			out = append(out, *pm)
		}
	}
	return out, nil
}

func (r *fakeRepo) ListEnabledProjectModulesForOwner(_ context.Context, projectID, ownerID uuid.UUID) ([]ProjectModule, error) {
	if !r.checkOwner(projectID, ownerID) {
		return nil, nil
	}
	out := []ProjectModule{}
	for _, pm := range r.links {
		if pm.ProjectID == projectID && pm.Enabled {
			out = append(out, *pm)
		}
	}
	return out, nil
}

func (r *fakeRepo) CountEnabledProjectModulesExcluding(_ context.Context, moduleID, excludeProjectID uuid.UUID) (int64, error) {
	var n int64
	for _, pm := range r.links {
		if pm.ModuleID == moduleID && pm.Enabled && pm.ProjectID != excludeProjectID {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) BulkMarkProjectModulesRunning(_ context.Context, moduleID uuid.UUID, serviceID, endpoint string) (int64, error) {
	var n int64
	for _, pm := range r.links {
		if pm.ModuleID == moduleID && pm.Enabled {
			pm.ServiceID = serviceID
			pm.Endpoint = endpoint
			pm.Status = "running"
			n++
		}
	}
	return n, nil
}

// fakeFetcher stands in for the module's HTTP describe()/docs() RPC.
type fakeFetcher struct {
	manifest json.RawMessage
	err      error
}

func (f fakeFetcher) Fetch(_ context.Context, _, _, _ string) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.manifest != nil {
		return f.manifest, nil
	}
	return json.RawMessage(`{"nodes":[],"docs":""}`), nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

// selfHostedModule creates a module whose manifest_cache marks it self-hosted
// (mode=endpoint), so EnableModule skips the orchestrator entirely.
func selfHostedModule(t *testing.T, repo *fakeRepo, slug, endpoint string) *Module {
	t.Helper()
	m, err := repo.CreateModule(context.Background(), CreateModuleInput{
		Slug: slug, Name: slug, Active: true,
	})
	if err != nil {
		t.Fatalf("create module: %v", err)
	}
	src := map[string]any{"_source": map[string]string{"mode": "endpoint", "endpoint": endpoint}}
	raw, _ := json.Marshal(src)
	if _, err := repo.UpdateModuleManifest(context.Background(), m.ID, raw); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	return m
}

func TestEnableModule_CreatesThenIdempotent(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	projectID, ownerID := uuid.New(), uuid.New()
	repo.addProject(projectID, ownerID)
	selfHostedModule(t, repo, "echo", "https://dev.example/rpc")

	uc := NewUsecase(repo, engine.NewStub(), fakeFetcher{
		manifest: json.RawMessage(`{"nodes":[{"type":"echo.ping","category":"actions"}],"docs":"hi"}`),
	}, nil)

	pm, created, err := uc.EnableModule(ctx, projectID, ownerID, "echo")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !created {
		t.Fatal("first enable should report created=true")
	}
	if !pm.Enabled || pm.Status != "running" {
		t.Fatalf("expected running+enabled, got status=%s enabled=%v", pm.Status, pm.Enabled)
	}
	if pm.Endpoint != "https://dev.example/rpc" {
		t.Fatalf("self-hosted endpoint not preserved: %q", pm.Endpoint)
	}

	pm2, created2, err := uc.EnableModule(ctx, projectID, ownerID, "echo")
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if created2 {
		t.Fatal("re-enabling an already-enabled module should report created=false")
	}
	if pm2.ID != pm.ID {
		t.Fatal("re-enable should return the same project_module row")
	}
}

func TestEnableModule_WrongOwnerIsNotFound(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	projectID, ownerID := uuid.New(), uuid.New()
	repo.addProject(projectID, ownerID)
	selfHostedModule(t, repo, "echo", "https://dev.example/rpc")

	uc := NewUsecase(repo, engine.NewStub(), fakeFetcher{}, nil)

	_, _, err := uc.EnableModule(ctx, projectID, uuid.New(), "echo")
	if status(err) != 404 {
		t.Fatalf("expected 404 for a project owned by someone else, got %v", err)
	}
}

func TestDisableModule_NotEnabledIsNotFound(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	projectID, ownerID := uuid.New(), uuid.New()
	repo.addProject(projectID, ownerID)
	selfHostedModule(t, repo, "echo", "https://dev.example/rpc")

	uc := NewUsecase(repo, engine.NewStub(), fakeFetcher{}, nil)

	if err := uc.DisableModule(ctx, projectID, ownerID, "echo"); status(err) != 404 {
		t.Fatalf("expected 404 disabling a module that was never enabled, got %v", err)
	}
}

func TestNodeManifests_MergesBuiltinAndModuleNodes(t *testing.T) {
	repo := newFakeRepo()
	ctx := context.Background()
	projectID, ownerID := uuid.New(), uuid.New()
	repo.addProject(projectID, ownerID)
	selfHostedModule(t, repo, "echo", "https://dev.example/rpc")

	uc := NewUsecase(repo, engine.NewStub(), fakeFetcher{
		manifest: json.RawMessage(`{"nodes":[{"type":"echo.ping","category":"actions"}],"docs":"hi"}`),
	}, nil)

	if _, _, err := uc.EnableModule(ctx, projectID, ownerID, "echo"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	moduleOnly, err := uc.ModuleManifests(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("module manifests: %v", err)
	}
	if len(moduleOnly) != 1 {
		t.Fatalf("expected 1 module node, got %d", len(moduleOnly))
	}
	var tagged map[string]any
	if err := json.Unmarshal(moduleOnly[0], &tagged); err != nil {
		t.Fatalf("unmarshal tagged node: %v", err)
	}
	if tagged["moduleSlug"] != "echo" {
		t.Fatalf("node not tagged with moduleSlug: %v", tagged)
	}

	unified, err := uc.NodeManifests(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("node manifests: %v", err)
	}
	if len(unified) != len(builtinNodeManifests)+1 {
		t.Fatalf("expected builtin+1 nodes, got %d (builtin=%d)", len(unified), len(builtinNodeManifests))
	}
}
