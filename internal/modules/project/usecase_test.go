package project

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/botspace/backend/internal/modules/cms"
	"github.com/botspace/backend/internal/modules/release"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
// Only Create/Update/SetReleaseBotToken are exercised below; the rest of the
// interface is implemented with trivial stubs so fakeRepo satisfies
// Repository.
type fakeRepo struct {
	projects          map[uuid.UUID]*Project
	envs              map[string]string // "<projectID>|<key>" -> value
	debugs            map[uuid.UUID]*ProjectDebug
	releaseServiceIDs map[uuid.UUID][]string // projectID -> every release's service_id
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		projects:          map[uuid.UUID]*Project{},
		envs:              map[string]string{},
		debugs:            map[uuid.UUID]*ProjectDebug{},
		releaseServiceIDs: map[uuid.UUID][]string{},
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

func (r *fakeRepo) ActiveReleaseServiceIDsForOwner(_ context.Context, projectID, ownerID uuid.UUID) ([]string, error) {
	p, ok := r.projects[projectID]
	if !ok || p.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	return r.releaseServiceIDs[projectID], nil
}

// TestCreate_WithToken_StoresReleaseBotToken ports the fix for the
// BOT_TOKEN-not-found release panic: a token given at create time must land
// in the project's RELEASE_BOT_TOKEN environment variable (Django's
// EnviromentModel key="RELEASE_BOT_TOKEN", bot.py:39) so release.Usecase's
// envMap() — and from there kubernetes.go's BOT_TOKEN mapping — actually sees
// it.
func TestCreate_WithToken_StoresReleaseBotToken(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil, nil, nil, nil)
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
	uc := NewUsecase(repo, nil, nil, nil, nil)
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
	uc := NewUsecase(repo, nil, nil, nil, nil)
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
	uc := NewUsecase(repo, nil, nil, nil, nil)
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

// fakeOrchestrator is a minimal engine.Orchestrator test double. stopped
// records every serviceID passed to Stop (in call order) so Delete's
// container-teardown behavior can be asserted; stopErr, when set, makes Stop
// return an error for every call (still recorded) to verify Delete stays
// best-effort.
type fakeOrchestrator struct {
	status  engine.Status
	stopped []string
	stopErr error
}

func (f *fakeOrchestrator) Deploy(context.Context, engine.DeploySpec) (engine.DeployResult, error) {
	return engine.DeployResult{}, nil
}
func (f *fakeOrchestrator) Stop(_ context.Context, serviceID string) error {
	f.stopped = append(f.stopped, serviceID)
	return f.stopErr
}
func (f *fakeOrchestrator) Status(context.Context, string) (engine.Status, error) {
	return f.status, nil
}
func (f *fakeOrchestrator) Logs(context.Context, string, int, int) (engine.LogSnapshot, error) {
	return engine.LogSnapshot{}, nil
}

// TestGet_ReconcilesDebugStatus ports the same "hang on launching" fix as
// release.GetRelease/ListReleases, but for the tokenless debug-mode bot
// path: RetrieveProjectSerializer nests the debug row, and the dashboard's
// launch-status poll reads project.debug.status directly (no Release row
// exists for debug-mode bots).
func TestGet_ReconcilesDebugStatus(t *testing.T) {
	repo := newFakeRepo()
	orch := &fakeOrchestrator{status: engine.StatusRunning}
	uc := NewUsecase(repo, orch, nil, nil, nil)
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
	uc := NewUsecase(repo, &fakeOrchestrator{status: engine.StatusRunning}, nil, nil, nil)
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

// fakeCollectionProvisioner is a minimal CollectionProvisioner test double —
// records every collection/field it is asked to create so
// TestCreate_ProvisionsDefaultCollections can assert against Django's exact
// SYSTEM_COLLECTIONS spec (core/apps/cms/services/system_collections.py)
// that provisionDefaultCollections ports.
type fakeCollectionProvisioner struct {
	collections map[string]*cms.Collection   // slug -> created collection
	fields      map[string][]cms.FieldCreate // slug -> fields added, in call order
}

func newFakeCollectionProvisioner() *fakeCollectionProvisioner {
	return &fakeCollectionProvisioner{
		collections: map[string]*cms.Collection{},
		fields:      map[string][]cms.FieldCreate{},
	}
}

func (f *fakeCollectionProvisioner) CreateCollection(_ context.Context, _, projectID uuid.UUID, in cms.CreateCollectionInput) (*cms.Collection, error) {
	c := &cms.Collection{
		ID:           uuid.New(),
		ProjectID:    projectID,
		Name:         in.Name,
		Slug:         in.Slug,
		Description:  in.Description,
		PrimaryField: in.PrimaryField,
		System:       in.System,
	}
	f.collections[in.Slug] = c
	return c, nil
}

func (f *fakeCollectionProvisioner) GetCollection(_ context.Context, _, _ uuid.UUID, slug string) (*cms.Collection, error) {
	c, ok := f.collections[slug]
	if !ok {
		return nil, database.ErrNotFound
	}
	return c, nil
}

func (f *fakeCollectionProvisioner) AddField(_ context.Context, _, _ uuid.UUID, collSlug string, in cms.FieldCreate) (*cms.Field, error) {
	if _, ok := f.collections[collSlug]; !ok {
		return nil, database.ErrNotFound
	}
	f.fields[collSlug] = append(f.fields[collSlug], in)
	return &cms.Field{ID: uuid.New(), Name: in.Name}, nil
}

// TestCreate_ProvisionsDefaultCollections ports Django's
// create_default_collections + ensure_system_collections (called from
// ProjectViewSet.perform_create): a freshly created project must get the
// `users`/`chat` system CMS collections with the exact field sets the engine
// writes into Mongo — otherwise the engine's direct Mongo writes are
// invisible in the dashboard/MCP (no matching schema).
func TestCreate_ProvisionsDefaultCollections(t *testing.T) {
	repo := newFakeRepo()
	cmsUC := newFakeCollectionProvisioner()
	uc := NewUsecase(repo, nil, cmsUC, nil, nil)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, slug := range []string{"users", "chat"} {
		coll, ok := cmsUC.collections[slug]
		if !ok {
			t.Fatalf("expected default collection %q to be created", slug)
		}
		if !coll.System {
			t.Fatalf("expected default collection %q to be system=true", slug)
		}
		if coll.ProjectID != p.ID {
			t.Fatalf("collection %q ProjectID = %v, want %v", slug, coll.ProjectID, p.ID)
		}
	}
	if got := cmsUC.collections["users"].PrimaryField; got != "first_name" {
		t.Fatalf("users primary_field = %q, want first_name", got)
	}
	if got := cmsUC.collections["chat"].PrimaryField; got != "chat_id" {
		t.Fatalf("chat primary_field = %q, want chat_id", got)
	}
	// collection.Name is the human-readable label (slug is the Mongo/technical
	// name) — mirrors Django's SYSTEM_COLLECTIONS label field.
	if got := cmsUC.collections["users"].Name; got != "Foydalanuvchilar" {
		t.Fatalf("users label = %q, want Foydalanuvchilar", got)
	}
	if got := cmsUC.collections["chat"].Name; got != "Chat tarixi" {
		t.Fatalf("chat label = %q, want Chat tarixi", got)
	}

	usersFields := cmsUC.fields["users"]
	wantUsers := []struct {
		name     string
		kind     string
		required bool
	}{
		{"chat_id", cms.FieldInt, true},
		{"first_name", cms.FieldText, false},
		{"last_name", cms.FieldText, false},
		{"username", cms.FieldText, false},
		{"last_seen", cms.FieldDateTime, false},
		{"created_at", cms.FieldDateTime, false},
	}
	if len(usersFields) != len(wantUsers) {
		t.Fatalf("users fields = %d, want %d", len(usersFields), len(wantUsers))
	}
	for i, want := range wantUsers {
		got := usersFields[i]
		if got.Name != want.name || got.Type != want.kind || got.Required != want.required {
			t.Fatalf("users field[%d] = %+v, want name=%s type=%s required=%v", i, got, want.name, want.kind, want.required)
		}
	}

	chatFields := cmsUC.fields["chat"]
	wantChat := []struct {
		name     string
		kind     string
		required bool
	}{
		{"chat_id", cms.FieldText, true},
		{"message", cms.FieldText, false},
		{"message_type", cms.FieldText, false},
		{"is_read", cms.FieldBool, false},
		{"from_admin", cms.FieldBool, false},
		{"updated_at", cms.FieldDateTime, false},
		{"created_at", cms.FieldDateTime, false},
	}
	if len(chatFields) != len(wantChat) {
		t.Fatalf("chat fields = %d, want %d", len(chatFields), len(wantChat))
	}
	for i, want := range wantChat {
		got := chatFields[i]
		if got.Name != want.name || got.Type != want.kind || got.Required != want.required {
			t.Fatalf("chat field[%d] = %+v, want name=%s type=%s required=%v", i, got, want.name, want.kind, want.required)
		}
	}
}

// TestCreate_NilCMS_DoesNotFail: a nil CollectionProvisioner (cms module
// unwired, e.g. Mongo unreachable at boot — see server.go) must not break
// project creation itself.
func TestCreate_NilCMS_DoesNotFail(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil, nil, nil, nil)
	ctx := context.Background()
	ownerID := uuid.New()

	if _, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"}); err != nil {
		t.Fatalf("Create with nil cms provisioner: %v", err)
	}
}

// TestDelete_StopsRunningContainers is the regression test for the bug where
// deleting a project left its release/debug k8s containers running forever:
// Delete must call orch.Stop for every release's service_id plus the debug
// row's service_id BEFORE the project disappears behind deleted_at.
func TestDelete_StopsRunningContainers(t *testing.T) {
	repo := newFakeRepo()
	orch := &fakeOrchestrator{}
	uc := NewUsecase(repo, orch, nil, nil, nil)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	releaseV1 := "release-" + p.ID.String() + "-v1"
	releaseV2 := "release-" + p.ID.String() + "-v2"
	debugSvc := "debug-" + p.ID.String()
	repo.releaseServiceIDs[p.ID] = []string{releaseV1, releaseV2}
	repo.debugs[p.ID] = &ProjectDebug{ID: uuid.New(), ServiceID: debugSvc, Status: "running"}

	if err := uc.Delete(ctx, p.ID, ownerID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	want := map[string]bool{releaseV1: true, releaseV2: true, debugSvc: true}
	if len(orch.stopped) != len(want) {
		t.Fatalf("Stop called %d times (%v), want %d", len(orch.stopped), orch.stopped, len(want))
	}
	for _, sid := range orch.stopped {
		if !want[sid] {
			t.Fatalf("unexpected Stop(%q)", sid)
		}
		delete(want, sid)
	}
	if len(want) != 0 {
		t.Fatalf("missing Stop calls for %v", want)
	}

	if _, err := repo.GetForOwner(ctx, p.ID, ownerID); err == nil {
		t.Fatal("expected project to be soft-deleted")
	}
}

// TestDelete_BestEffort_StopErrorDoesNotBlockDelete mirrors
// release.Usecase.StopRelease/StopDebug: an orchestrator error (or an
// already-stopped service) stopping one container must never fail the
// delete request itself.
func TestDelete_BestEffort_StopErrorDoesNotBlockDelete(t *testing.T) {
	repo := newFakeRepo()
	orch := &fakeOrchestrator{stopErr: errors.New("k8s unavailable")}
	uc := NewUsecase(repo, orch, nil, nil, nil)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	repo.releaseServiceIDs[p.ID] = []string{"release-" + p.ID.String()}

	if err := uc.Delete(ctx, p.ID, ownerID); err != nil {
		t.Fatalf("Delete should succeed despite Stop errors: %v", err)
	}
	if len(orch.stopped) != 1 {
		t.Fatalf("expected Stop to still be attempted, got %v", orch.stopped)
	}
}

// TestDelete_NilOrchestrator_StillDeletes: no orchestrator wired (e.g. k8s
// unreachable at boot) must not break the delete itself, just skip stopping
// containers.
func TestDelete_NilOrchestrator_StillDeletes(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil, nil, nil, nil)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := uc.Delete(ctx, p.ID, ownerID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// ===================== CreateFromTemplate =====================

// fakeTemplateResolver is a stub TemplateResolver: it returns a fixed flow
// (or a fixed error, mirroring "template not purchased") regardless of the
// idOrSlug/buyerID it's called with, and records the last call's arguments
// so tests can assert the usecase forwarded them correctly.
type fakeTemplateResolver struct {
	flow         json.RawMessage
	err          error
	lastIDOrSlug string
	lastBuyerID  uuid.UUID
}

func (f *fakeTemplateResolver) ResolveForProjectCreate(_ context.Context, idOrSlug string, buyerID uuid.UUID) (json.RawMessage, error) {
	f.lastIDOrSlug = idOrSlug
	f.lastBuyerID = buyerID
	if f.err != nil {
		return nil, f.err
	}
	return f.flow, nil
}

// fakeReleaseOrchestrator is a stub ReleaseOrchestrator that just records
// what CreateFromTemplate asked it to do.
type fakeReleaseOrchestrator struct {
	envs          map[string]string
	releaseCalled bool
	debugCalled   bool
	deployErr     error
}

func (f *fakeReleaseOrchestrator) UpsertEnvironment(_ context.Context, in release.UpsertEnvironmentInput) (*release.Environment, error) {
	if f.envs == nil {
		f.envs = map[string]string{}
	}
	f.envs[in.Key] = in.Value
	return &release.Environment{ID: uuid.New(), ProjectID: in.ProjectID, Key: in.Key, Value: in.Value}, nil
}

func (f *fakeReleaseOrchestrator) CreateRelease(_ context.Context, projectID, _ uuid.UUID) (*release.Release, error) {
	f.releaseCalled = true
	if f.deployErr != nil {
		return nil, f.deployErr
	}
	return &release.Release{ID: uuid.New(), ProjectID: projectID, Version: 1}, nil
}

func (f *fakeReleaseOrchestrator) StartDebug(_ context.Context, projectID, _ uuid.UUID) (*release.Debug, error) {
	f.debugCalled = true
	if f.deployErr != nil {
		return nil, f.deployErr
	}
	return &release.Debug{ID: uuid.New(), ProjectID: projectID}, nil
}

// TestCreateFromTemplate_CopiesFlowAndProvisionsCollections is the
// deliverable test for the "create a project from a template" feature: it
// must (a) seed the new project with the template's resolved flow — not an
// empty one — and (b) get the exact same default users/chat CMS collections
// TestCreate_ProvisionsDefaultCollections checks for a plain create, since
// CreateFromTemplate is documented to reuse Create's internals rather than
// duplicate them.
func TestCreateFromTemplate_CopiesFlowAndProvisionsCollections(t *testing.T) {
	repo := newFakeRepo()
	cmsUC := newFakeCollectionProvisioner()
	templateFlow := json.RawMessage(`{"nodes":[{"id":"start","type":"message"}],"edges":[]}`)
	tmpl := &fakeTemplateResolver{flow: templateFlow}
	rel := &fakeReleaseOrchestrator{}
	uc := NewUsecase(repo, nil, cmsUC, tmpl, rel)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.CreateFromTemplate(ctx, CreateFromTemplateInput{
		OwnerID:  ownerID,
		Template: "some-template-slug",
		Name:     "My templated bot",
		Token:    "123:abc",
		Envs:     map[string]string{"ADMIN_CHAT_ID": "42"},
	})
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}

	// (a) flow copied from the template, not empty/default.
	if string(p.Flow) != string(templateFlow) {
		t.Fatalf("Flow = %s, want %s", p.Flow, templateFlow)
	}
	if tmpl.lastIDOrSlug != "some-template-slug" || tmpl.lastBuyerID != ownerID {
		t.Fatalf("ResolveForProjectCreate called with (%q, %v), want (%q, %v)",
			tmpl.lastIDOrSlug, tmpl.lastBuyerID, "some-template-slug", ownerID)
	}

	// (b) default system CMS collections provisioned, same as plain Create.
	for _, slug := range []string{"users", "chat"} {
		if _, ok := cmsUC.collections[slug]; !ok {
			t.Fatalf("expected default collection %q to be created", slug)
		}
	}

	// Token stored as RELEASE_BOT_TOKEN (same as a normal create with token).
	if got := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]; got != "123:abc" {
		t.Fatalf("RELEASE_BOT_TOKEN = %q, want 123:abc", got)
	}

	// Requested env applied through the release module.
	if got := rel.envs["ADMIN_CHAT_ID"]; got != "42" {
		t.Fatalf("ADMIN_CHAT_ID env = %q, want 42", got)
	}

	// mode defaults to "release" -> first release deployed, no debug started.
	if !rel.releaseCalled {
		t.Fatal("expected CreateRelease to be called for the default (release) mode")
	}
	if rel.debugCalled {
		t.Fatal("did not expect StartDebug to be called for the default (release) mode")
	}
}

// TestCreateFromTemplate_DebugMode mirrors Django's tokenless debug branch:
// no token is stored, and the debug container is started instead of a
// release being deployed.
func TestCreateFromTemplate_DebugMode(t *testing.T) {
	repo := newFakeRepo()
	cmsUC := newFakeCollectionProvisioner()
	tmpl := &fakeTemplateResolver{flow: json.RawMessage(`{"nodes":[],"edges":[]}`)}
	rel := &fakeReleaseOrchestrator{}
	uc := NewUsecase(repo, nil, cmsUC, tmpl, rel)
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.CreateFromTemplate(ctx, CreateFromTemplateInput{
		OwnerID:  ownerID,
		Template: "free-template",
		Name:     "Debug bot",
		Mode:     "debug",
	})
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if _, ok := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]; ok {
		t.Fatal("did not expect RELEASE_BOT_TOKEN to be set in debug mode")
	}
	if !rel.debugCalled {
		t.Fatal("expected StartDebug to be called for mode=debug")
	}
	if rel.releaseCalled {
		t.Fatal("did not expect CreateRelease to be called for mode=debug")
	}
}

// TestCreateFromTemplate_NotPurchased ports the "template not purchased"
// rejection (ProjectView.create_from_template, project.py:466-468):
// TemplateResolver returning an error must abort before any project row
// (or its collections/deploy) is created.
func TestCreateFromTemplate_NotPurchased(t *testing.T) {
	repo := newFakeRepo()
	cmsUC := newFakeCollectionProvisioner()
	tmpl := &fakeTemplateResolver{err: httpx.BadRequest("template.not_purchased", "template not purchased")}
	rel := &fakeReleaseOrchestrator{}
	uc := NewUsecase(repo, nil, cmsUC, tmpl, rel)
	ctx := context.Background()
	ownerID := uuid.New()

	_, err := uc.CreateFromTemplate(ctx, CreateFromTemplateInput{
		OwnerID: ownerID, Template: "paid-template", Name: "Should not exist",
	})
	if err == nil {
		t.Fatal("expected an error when the template was not purchased")
	}
	if len(repo.projects) != 0 {
		t.Fatalf("expected no project to be created, got %d", len(repo.projects))
	}
	if rel.releaseCalled || rel.debugCalled {
		t.Fatal("did not expect any deploy call when template resolution failed")
	}
}
