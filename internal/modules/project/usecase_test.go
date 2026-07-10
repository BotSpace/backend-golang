package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	monitoring        []MonitoringProject
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
	return r.monitoring, nil
}

func (r *fakeRepo) DiskHistory(_ context.Context, _ uuid.UUID, _ time.Time) ([]MetricPoint, error) {
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

func (r *fakeRepo) GetReleaseBotToken(_ context.Context, projectID uuid.UUID) (string, error) {
	return r.envs[projectID.String()+"|RELEASE_BOT_TOKEN"], nil
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

// ===================== bot_link (debug vs release) =====================

// fakeBotUsernameResolver is a scripted BotUsernameResolver test double: it
// maps a token to a canned username (or error), and records every token it
// was asked to resolve so tests can assert which token (RELEASE_BOT_TOKEN vs
// the platform TEST_BOT_TOKEN) populateBotLink actually used.
type fakeBotUsernameResolver struct {
	usernames map[string]string
	err       error
	calls     []string
}

func (f *fakeBotUsernameResolver) ResolveUsername(_ context.Context, token string) (string, error) {
	f.calls = append(f.calls, token)
	if f.err != nil {
		return "", f.err
	}
	return f.usernames[token], nil
}

// TestGet_DebugMode_BotLinkHasStartParam is the deliverable regression test
// for gap 1: a tokenless (no RELEASE_BOT_TOKEN) project must get bot_mode
// "debug" and a bot_link built from the platform TEST_BOT_TOKEN's username
// PLUS the required ?start=<debug_webhook_uuid> deep-link — a bare t.me link
// to the shared test bot can't route to any one specific project.
func TestGet_DebugMode_BotLinkHasStartParam(t *testing.T) {
	repo := newFakeRepo()
	resolver := &fakeBotUsernameResolver{usernames: map[string]string{"test-bot-token": "botmother_test_robot"}}
	uc := NewUsecaseWithBotLinks(repo, nil, nil, nil, nil, resolver, "test-bot-token")
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "Debug bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := uc.Get(ctx, p.ID, ownerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BotMode != "debug" {
		t.Fatalf("BotMode = %q, want debug", got.BotMode)
	}
	if got.BotUsername != "botmother_test_robot" {
		t.Fatalf("BotUsername = %q, want botmother_test_robot", got.BotUsername)
	}
	want := fmt.Sprintf("https://t.me/botmother_test_robot?start=%s", got.DebugWebhookUUID.String())
	if got.BotLink != want {
		t.Fatalf("BotLink = %q, want %q", got.BotLink, want)
	}
	if len(resolver.calls) != 1 || resolver.calls[0] != "test-bot-token" {
		t.Fatalf("expected resolver called with the platform test bot token, got %v", resolver.calls)
	}
}

// TestGet_ReleaseMode_BotLinkHasNoStartParam mirrors Django's release branch:
// a project with its own RELEASE_BOT_TOKEN gets bot_mode "release" and a
// plain https://t.me/<username> link (no ?start=, since a release bot is
// dedicated to this one project already).
func TestGet_ReleaseMode_BotLinkHasNoStartParam(t *testing.T) {
	repo := newFakeRepo()
	resolver := &fakeBotUsernameResolver{usernames: map[string]string{
		"123:ABC":        "my_own_bot",
		"test-bot-token": "botmother_test_robot",
	}}
	uc := NewUsecaseWithBotLinks(repo, nil, nil, nil, nil, resolver, "test-bot-token")
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "Release bot", Token: "123:ABC"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := uc.Get(ctx, p.ID, ownerID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BotMode != "release" {
		t.Fatalf("BotMode = %q, want release", got.BotMode)
	}
	if got.BotLink != "https://t.me/my_own_bot" {
		t.Fatalf("BotLink = %q, want https://t.me/my_own_bot (no ?start=)", got.BotLink)
	}
	if len(resolver.calls) != 1 || resolver.calls[0] != "123:ABC" {
		t.Fatalf("expected resolver called with the project's own token, got %v", resolver.calls)
	}
}

// TestGet_NoResolver_LeavesBotLinkEmpty: a usecase built via plain NewUsecase
// (no bot resolver wired) must leave bot_mode/bot_username/bot_link empty —
// same optional-dependency convention as a nil orch/cms/tmpl/rel.
func TestGet_NoResolver_LeavesBotLinkEmpty(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil, nil, nil, nil)
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
	if got.BotLink != "" || got.BotMode != "" || got.BotUsername != "" {
		t.Fatalf("expected empty bot fields with no resolver wired, got %+v", got)
	}
}

// TestGet_ResolverError_LeavesBotLinkEmpty: a getMe failure (bad/rotated
// token, Telegram unreachable) must never fail the whole Get — best-effort,
// same convention as reconcileDebug swallowing an orchestrator error.
func TestGet_ResolverError_LeavesBotLinkEmpty(t *testing.T) {
	repo := newFakeRepo()
	resolver := &fakeBotUsernameResolver{err: errors.New("telegram unreachable")}
	uc := NewUsecaseWithBotLinks(repo, nil, nil, nil, nil, resolver, "test-bot-token")
	ctx := context.Background()
	ownerID := uuid.New()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "My bot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := uc.Get(ctx, p.ID, ownerID)
	if err != nil {
		t.Fatalf("Get should still succeed despite a resolver error: %v", err)
	}
	if got.BotLink != "" {
		t.Fatalf("expected empty BotLink on resolver error, got %q", got.BotLink)
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

// ===================== managed_bot_id =====================

// fakeManagedBotResolver is a stub ManagedBotTokenResolver: it resolves a
// single fixed (managedBotID, ownerID) pair to a fixed token, and 404s for
// anything else — mirrors webhook.Usecase.ResolveManagedBotToken's own
// not-found-or-not-owned behaviour.
type fakeManagedBotResolver struct {
	managedBotID uuid.UUID
	ownerID      uuid.UUID
	token        string

	calls int
}

func (f *fakeManagedBotResolver) ResolveManagedBotToken(_ context.Context, managedBotID, ownerID uuid.UUID) (string, error) {
	f.calls++
	if managedBotID != f.managedBotID || ownerID != f.ownerID {
		return "", httpx.NotFound("managed_bot.not_found", "managed bot topilmadi")
	}
	return f.token, nil
}

// TestCreate_WithManagedBotID_StoresDecryptedTokenAsReleaseBotToken ports
// Django's ProjectView._attach_managed_bot: a managed_bot_id given at create
// time (with no explicit token) must resolve through the injected
// ManagedBotTokenResolver and land in RELEASE_BOT_TOKEN exactly like a
// manually-entered token would.
func TestCreate_WithManagedBotID_StoresDecryptedTokenAsReleaseBotToken(t *testing.T) {
	repo := newFakeRepo()
	ownerID := uuid.New()
	managedBotID := uuid.New()
	resolver := &fakeManagedBotResolver{managedBotID: managedBotID, ownerID: ownerID, token: "999:decrypted-managed-token"}
	uc := NewUsecaseWithManagedBotResolver(repo, nil, nil, nil, nil, nil, "", resolver)
	ctx := context.Background()

	p, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "Managed bot project", ManagedBotID: managedBotID.String()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("expected the resolver to be called exactly once, got %d", resolver.calls)
	}
	if got := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]; got != "999:decrypted-managed-token" {
		t.Fatalf("RELEASE_BOT_TOKEN = %q, want the resolved managed-bot token", got)
	}
}

// TestCreate_WithManagedBotID_NotOwned_ReturnsError ensures a managed_bot_id
// that doesn't exist (or isn't owned by the caller) fails the create with a
// clear error — not a silently tokenless project.
func TestCreate_WithManagedBotID_NotOwned_ReturnsError(t *testing.T) {
	repo := newFakeRepo()
	ownerID := uuid.New()
	strangerID := uuid.New()
	managedBotID := uuid.New()
	resolver := &fakeManagedBotResolver{managedBotID: managedBotID, ownerID: ownerID, token: "999:decrypted-managed-token"}
	uc := NewUsecaseWithManagedBotResolver(repo, nil, nil, nil, nil, nil, "", resolver)
	ctx := context.Background()

	_, err := uc.Create(ctx, CreateInput{OwnerID: strangerID, Name: "Should fail", ManagedBotID: managedBotID.String()})
	if err == nil {
		t.Fatal("expected an error for a managed_bot_id not owned by the caller")
	}
	ae, ok := httpx.AsAppError(err)
	if !ok || (ae.Status != 404 && ae.Status != 400) {
		t.Fatalf("expected a 400/404 app error, got %v", err)
	}
}

// TestCreate_WithManagedBotID_NoResolverConfigured_Returns500 documents the
// nil-resolver degrade path (same optional-dependency convention as
// orch/cms/tmpl/rel): a managed_bot_id given without a resolver wired must
// fail loudly instead of silently deploying a tokenless bot.
func TestCreate_WithManagedBotID_NoResolverConfigured_Returns500(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, nil, nil, nil, nil)
	ctx := context.Background()
	ownerID := uuid.New()

	_, err := uc.Create(ctx, CreateInput{OwnerID: ownerID, Name: "No resolver", ManagedBotID: uuid.New().String()})
	if err == nil {
		t.Fatal("expected an error when no ManagedBotTokenResolver is configured")
	}
}

// TestCreateFromTemplate_WithManagedBotID mirrors Django's
// create_from_template managed_bot_id branch: mode=release with a
// managed_bot_id (no token) resolves the same way as Create, stores
// RELEASE_BOT_TOKEN, and still deploys the first release like a
// manually-entered token would.
func TestCreateFromTemplate_WithManagedBotID(t *testing.T) {
	repo := newFakeRepo()
	cmsUC := newFakeCollectionProvisioner()
	tmpl := &fakeTemplateResolver{flow: json.RawMessage(`{"nodes":[],"edges":[]}`)}
	rel := &fakeReleaseOrchestrator{}
	ownerID := uuid.New()
	managedBotID := uuid.New()
	resolver := &fakeManagedBotResolver{managedBotID: managedBotID, ownerID: ownerID, token: "777:managed-template-token"}
	uc := NewUsecaseWithManagedBotResolver(repo, nil, cmsUC, tmpl, rel, nil, "", resolver)
	ctx := context.Background()

	p, err := uc.CreateFromTemplate(ctx, CreateFromTemplateInput{
		OwnerID:      ownerID,
		Template:     "some-template",
		Name:         "Managed template bot",
		ManagedBotID: managedBotID.String(),
	})
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if got := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]; got != "777:managed-template-token" {
		t.Fatalf("RELEASE_BOT_TOKEN = %q, want the resolved managed-bot token", got)
	}
	if !rel.releaseCalled {
		t.Fatal("expected CreateRelease to be called (release mode with a resolved managed bot token)")
	}
}

// TestCreateFromTemplate_DebugMode_IgnoresManagedBotID mirrors Django's
// tokenless debug branch ignoring managed_bot_id exactly like it ignores
// token: mode=debug must never touch the resolver at all.
func TestCreateFromTemplate_DebugMode_IgnoresManagedBotID(t *testing.T) {
	repo := newFakeRepo()
	cmsUC := newFakeCollectionProvisioner()
	tmpl := &fakeTemplateResolver{flow: json.RawMessage(`{"nodes":[],"edges":[]}`)}
	rel := &fakeReleaseOrchestrator{}
	ownerID := uuid.New()
	managedBotID := uuid.New()
	resolver := &fakeManagedBotResolver{managedBotID: managedBotID, ownerID: ownerID, token: "should-not-be-used"}
	uc := NewUsecaseWithManagedBotResolver(repo, nil, cmsUC, tmpl, rel, nil, "", resolver)
	ctx := context.Background()

	p, err := uc.CreateFromTemplate(ctx, CreateFromTemplateInput{
		OwnerID:      ownerID,
		Template:     "free-template",
		Name:         "Debug bot",
		Mode:         "debug",
		ManagedBotID: managedBotID.String(),
	})
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("expected the resolver never to be called in debug mode, got %d calls", resolver.calls)
	}
	if _, ok := repo.envs[p.ID.String()+"|RELEASE_BOT_TOKEN"]; ok {
		t.Fatal("did not expect RELEASE_BOT_TOKEN to be set in debug mode")
	}
	if !rel.debugCalled {
		t.Fatal("expected StartDebug to be called for mode=debug")
	}
}

// ---------------------------------------------------------------------
// IssueCollabToken
// ---------------------------------------------------------------------

func appErrStatus(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestIssueCollabToken_NotConfigured_FailsLoudly(t *testing.T) {
	repo := newFakeRepo()
	ownerID := uuid.New()
	p, _ := repo.Create(context.Background(), CreateInput{OwnerID: ownerID, Name: "Bot"})
	uc := NewUsecase(repo, nil, nil, nil, nil) // no collab signer wired

	if _, _, err := uc.IssueCollabToken(context.Background(), p.ID, ownerID); appErrStatus(err) != 500 {
		t.Fatalf("expected a 500 when no signer is configured, got %v", err)
	}
}

func TestIssueCollabToken_NotOwner_404s(t *testing.T) {
	repo := newFakeRepo()
	ownerID := uuid.New()
	p, _ := repo.Create(context.Background(), CreateInput{OwnerID: ownerID, Name: "Bot"})
	signer, err := NewCollabTokenSignerFromEnv(0)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	_ = signer // nil (PRIVATE_KEY unset in this test) — irrelevant, ownership is checked first
	uc := NewUsecaseWithCollabSigner(NewUsecase(repo, nil, nil, nil, nil), signer)

	if _, _, err := uc.IssueCollabToken(context.Background(), p.ID, uuid.New()); appErrStatus(err) != 404 {
		t.Fatalf("expected 404 for a non-owning caller, got %v", err)
	}
}

func TestIssueCollabToken_UsesLegacyIDWhenPresent(t *testing.T) {
	privPEM, pub := generateTestRSAKeyPEM(t)
	t.Setenv("PRIVATE_KEY", string(privPEM))

	repo := newFakeRepo()
	ownerID := uuid.New()
	p, _ := repo.Create(context.Background(), CreateInput{OwnerID: ownerID, Name: "Bot"})
	legacyID := int32(1408)
	repo.projects[p.ID].LegacyID = &legacyID

	signer, err := NewCollabTokenSignerFromEnv(0)
	if err != nil || signer == nil {
		t.Fatalf("build signer: %v", err)
	}
	uc := NewUsecaseWithCollabSigner(NewUsecase(repo, nil, nil, nil, nil), signer)

	token, room, err := uc.IssueCollabToken(context.Background(), p.ID, ownerID)
	if err != nil {
		t.Fatalf("IssueCollabToken: %v", err)
	}
	if room != "project_1408" {
		t.Fatalf("expected room project_1408, got %q", room)
	}
	claims := verifyLikeWebsocketService(t, token, pub)
	if pid, ok := claims["project_id"].(float64); !ok || pid != 1408 {
		t.Fatalf("expected project_id 1408 (float64), got %#v", claims["project_id"])
	}
}

func TestIssueCollabToken_FallsBackToUUIDWithoutLegacyID(t *testing.T) {
	privPEM, pub := generateTestRSAKeyPEM(t)
	t.Setenv("PRIVATE_KEY", string(privPEM))

	repo := newFakeRepo()
	ownerID := uuid.New()
	p, _ := repo.Create(context.Background(), CreateInput{OwnerID: ownerID, Name: "Bot"})
	// no LegacyID set — a project created after the Go cutover.

	signer, err := NewCollabTokenSignerFromEnv(0)
	if err != nil || signer == nil {
		t.Fatalf("build signer: %v", err)
	}
	uc := NewUsecaseWithCollabSigner(NewUsecase(repo, nil, nil, nil, nil), signer)

	token, room, err := uc.IssueCollabToken(context.Background(), p.ID, ownerID)
	if err != nil {
		t.Fatalf("IssueCollabToken: %v", err)
	}
	if room != "project_"+p.ID.String() {
		t.Fatalf("expected room project_%s, got %q", p.ID, room)
	}
	claims := verifyLikeWebsocketService(t, token, pub)
	if pid, ok := claims["project_id"].(string); !ok || pid != p.ID.String() {
		t.Fatalf("expected project_id %s (string), got %#v", p.ID, claims["project_id"])
	}
}

type fakeMetricsProvider struct {
	values map[uuid.UUID]LiveMetrics
	ok     bool
	err    error
}

func (p fakeMetricsProvider) ProjectMetrics(context.Context) (map[uuid.UUID]LiveMetrics, bool, error) {
	return p.values, p.ok, p.err
}

func TestAdminMonitoring_MergesPrometheusMetrics(t *testing.T) {
	repo := newFakeRepo()
	projectID := uuid.New()
	repo.monitoring = []MonitoringProject{{
		Project:     Project{ID: projectID, OwnerID: uuid.New(), Name: "bot"},
		CPUCores:    1,
		MemoryBytes: 2,
		DiskBytes:   3,
	}}
	uc := NewUsecase(repo, nil, nil, nil, nil)
	uc = NewUsecaseWithMetrics(uc, fakeMetricsProvider{
		ok: true,
		values: map[uuid.UUID]LiveMetrics{
			projectID: {CPUMilli: 42, MemoryBytes: 123456},
		},
	})

	page, err := uc.AdminMonitoring(context.Background(), AdminListFilter{})
	if err != nil {
		t.Fatalf("AdminMonitoring: %v", err)
	}
	if !page.PrometheusAvailable {
		t.Fatal("PrometheusAvailable = false, want true")
	}
	if got := page.Items[0].CPUCores; got != 42 {
		t.Fatalf("CPUCores = %d, want 42", got)
	}
	if got := page.Items[0].MemoryBytes; got != 123456 {
		t.Fatalf("MemoryBytes = %d, want 123456", got)
	}
	if got := page.Items[0].DiskBytes; got != 3 {
		t.Fatalf("DiskBytes = %d, want DB value 3", got)
	}
}

func TestAdminMonitoring_PrometheusErrorFallsBackToSnapshots(t *testing.T) {
	repo := newFakeRepo()
	projectID := uuid.New()
	repo.monitoring = []MonitoringProject{{
		Project:     Project{ID: projectID, OwnerID: uuid.New(), Name: "bot"},
		CPUCores:    7,
		MemoryBytes: 8,
	}}
	uc := NewUsecaseWithMetrics(NewUsecase(repo, nil, nil, nil, nil), fakeMetricsProvider{err: errors.New("prom down")})

	page, err := uc.AdminMonitoring(context.Background(), AdminListFilter{})
	if err != nil {
		t.Fatalf("AdminMonitoring: %v", err)
	}
	if page.PrometheusAvailable {
		t.Fatal("PrometheusAvailable = true, want false")
	}
	if page.Items[0].CPUCores != 7 || page.Items[0].MemoryBytes != 8 {
		t.Fatalf("snapshot values not preserved: %#v", page.Items[0])
	}
}

func TestMonitoring_MergesPrometheusMetricsForOwner(t *testing.T) {
	repo := newFakeRepo()
	ownerID := uuid.New()
	projectID := uuid.New()
	repo.projects[projectID] = &Project{ID: projectID, OwnerID: ownerID, Name: "bot"}
	uc := NewUsecaseWithMetrics(NewUsecase(repo, nil, nil, nil, nil), fakeMetricsProvider{
		ok: true,
		values: map[uuid.UUID]LiveMetrics{
			projectID: {
				CPUMilli:     125,
				MemoryBytes:  64 * 1024 * 1024,
				NetworkRXBps: 1024,
				NetworkTXBps: 2048,
			},
		},
	})

	metrics, err := uc.Monitoring(context.Background(), projectID, ownerID)
	if err != nil {
		t.Fatalf("Monitoring: %v", err)
	}
	if !metrics.PrometheusAvailable {
		t.Fatal("PrometheusAvailable = false, want true")
	}
	if metrics.CPUCores != 0.125 {
		t.Fatalf("CPUCores = %v, want 0.125", metrics.CPUCores)
	}
	if metrics.MemoryBytes != 64*1024*1024 {
		t.Fatalf("MemoryBytes = %d, want 67108864", metrics.MemoryBytes)
	}
	if metrics.NetworkRXBps != 1024 || metrics.NetworkTXBps != 2048 {
		t.Fatalf("network = %v/%v, want 1024/2048", metrics.NetworkRXBps, metrics.NetworkTXBps)
	}
}

func TestPrometheusMetricsProvider_ProjectMetrics(t *testing.T) {
	projectID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		switch {
		case strings.Contains(query, "container_cpu_usage_seconds_total"):
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":[{"metric":{"project_id":"%s"},"value":[1,"0.025"]}]}}`, projectID)
		case strings.Contains(query, "container_memory_working_set_bytes"):
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":[{"metric":{"project_id":"%s"},"value":[1,"10485760"]}]}}`, projectID)
		case strings.Contains(query, "container_network_receive_bytes_total"):
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":[{"metric":{"project_id":"%s"},"value":[1,"128"]}]}}`, projectID)
		case strings.Contains(query, "container_network_transmit_bytes_total"):
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":[{"metric":{"project_id":"%s"},"value":[1,"256"]}]}}`, projectID)
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer srv.Close()

	provider := NewPrometheusMetricsProvider(srv.URL)
	metrics, ok, err := provider.ProjectMetrics(context.Background())
	if err != nil {
		t.Fatalf("ProjectMetrics: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	got := metrics[projectID]
	if got.CPUMilli != 25 {
		t.Fatalf("CPUMilli = %d, want 25", got.CPUMilli)
	}
	if got.MemoryBytes != 10485760 {
		t.Fatalf("MemoryBytes = %d, want 10485760", got.MemoryBytes)
	}
	if got.NetworkRXBps != 1024 || got.NetworkTXBps != 2048 {
		t.Fatalf("network = %v/%v, want 1024/2048", got.NetworkRXBps, got.NetworkTXBps)
	}
}
