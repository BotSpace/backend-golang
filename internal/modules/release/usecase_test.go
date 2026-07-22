package release

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func (r *fakeRepo) ProjectOwnerID(_ context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	owner, ok := r.projectOwner[projectID]
	if !ok {
		return uuid.Nil, database.ErrNotFound
	}
	return owner, nil
}

func (r *fakeRepo) GetProjectWebhookUUIDs(_ context.Context, projectID, ownerID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	if !r.owns(projectID, ownerID) {
		return uuid.Nil, uuid.Nil, database.ErrNotFound
	}
	// Deterministic per-project UUIDs derived from the project id — good
	// enough for tests that only assert a WebhookUUID was passed through.
	return uuid.NewSHA1(projectID, []byte("release")), uuid.NewSHA1(projectID, []byte("debug")), nil
}

func envKey(projectID uuid.UUID, key string) string { return projectID.String() + "|" + key }

// fakeOrchestrator records every Deploy/Stop call so tests can assert on the
// cleanup behavior without a real engine-golang client.
type fakeOrchestrator struct {
	deployed []engine.DeploySpec
	stopped  []string
	status   engine.Status
	// byService overrides status for a specific serviceID — lets a single
	// test give different live services different outcomes (e.g. one
	// running, one vanished/failed) without a second orchestrator instance.
	byService map[string]engine.Status
	logs      engine.LogSnapshot
	logsErr   error
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

func (f *fakeOrchestrator) Status(_ context.Context, serviceID string) (engine.Status, error) {
	if s, ok := f.byService[serviceID]; ok {
		return s, nil
	}
	return f.status, nil
}

func (f *fakeOrchestrator) Logs(_ context.Context, serviceID string, tailLines, sinceSeconds int) (engine.LogSnapshot, error) {
	if f.logsErr != nil {
		return engine.LogSnapshot{}, f.logsErr
	}
	return f.logs, nil
}

// fakePublisher records every Publish call so tests can assert on the room/
// event/payload a release or debug status change broadcasts, without a real
// Redis connection.
type fakePublisher struct {
	calls []publishCall
}

type publishCall struct {
	room  string
	event string
	data  any
}

func (f *fakePublisher) Publish(_ context.Context, room, event string, data any) {
	f.calls = append(f.calls, publishCall{room: room, event: event, data: data})
}

func (f *fakePublisher) last() (publishCall, bool) {
	if len(f.calls) == 0 {
		return publishCall{}, false
	}
	return f.calls[len(f.calls)-1], true
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

func TestCreateReleaseRegistersTelegramWebhook(t *testing.T) {
	repo := newFakeRepo()
	orch := newFakeOrchestrator()
	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))
	_, _ = repo.UpsertEnvironment(context.Background(), UpsertEnvironmentInput{
		ProjectID: projectID, OwnerID: ownerID, UserID: ownerID,
		Key: "RELEASE_BOT_TOKEN", Value: "123:test-token",
	})
	releaseWebhookUUID, _, _ := repo.GetProjectWebhookUUIDs(context.Background(), projectID, ownerID)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:test-token/setWebhook" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		want := "https://hooks.example/api/bot-webhook/release/" + releaseWebhookUUID.String() + "/"
		if got := r.Form.Get("url"); got != want {
			t.Errorf("webhook url = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	oldBase := telegramAPIBaseURL
	telegramAPIBaseURL = server.URL
	t.Cleanup(func() { telegramAPIBaseURL = oldBase })
	t.Setenv("DOMAIN", "hooks.example")

	rel, err := NewUsecase(repo, orch).CreateRelease(context.Background(), projectID, ownerID)
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if rel.Status != StatusRunning {
		t.Fatalf("status = %s, want running", rel.Status)
	}
}

func TestCreateReleaseFailsAndStopsRuntimeWhenWebhookRegistrationFails(t *testing.T) {
	repo := newFakeRepo()
	orch := newFakeOrchestrator()
	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))
	_, _ = repo.UpsertEnvironment(context.Background(), UpsertEnvironmentInput{
		ProjectID: projectID, OwnerID: ownerID, UserID: ownerID,
		Key: "RELEASE_BOT_TOKEN", Value: "bad-token",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"description":"invalid token"}`))
	}))
	defer server.Close()
	oldBase := telegramAPIBaseURL
	telegramAPIBaseURL = server.URL
	t.Cleanup(func() { telegramAPIBaseURL = oldBase })

	rel, err := NewUsecase(repo, orch).CreateRelease(context.Background(), projectID, ownerID)
	if err == nil {
		t.Fatal("expected webhook registration failure")
	}
	if rel == nil || rel.Status != StatusFailed {
		t.Fatalf("release = %#v, want failed", rel)
	}
	if len(orch.stopped) != 1 {
		t.Fatalf("stopped runtimes = %v, want one", orch.stopped)
	}
}

// TestGetReleaseReconcilesLiveStatus ports the "bots hang on launching" fix:
// KubernetesOrchestrator.Deploy always reports StatusInitializing (there's no
// k8s-watcher pushing pod-ready events into the DB), so GetRelease/ListReleases
// must ask the orchestrator live and persist the result instead of returning
// a DB row that's frozen at "initializing" forever.
func TestGetReleaseReconcilesLiveStatus(t *testing.T) {
	repo := newFakeRepo()
	orch := newFakeOrchestrator()
	orch.status = engine.StatusInitializing // Deploy() reports this at create time
	uc := NewUsecase(repo, orch)
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))

	rel, err := uc.CreateRelease(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	if rel.Status != StatusInitializing {
		t.Fatalf("expected initializing right after deploy, got %s", rel.Status)
	}

	// Pod becomes Ready sometime later — GetRelease must pick that up live.
	orch.status = engine.StatusRunning
	got, err := uc.GetRelease(ctx, rel.ID, ownerID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("expected GetRelease to reconcile to running, got %s", got.Status)
	}

	// The reconciled status must be persisted, not just returned once — a
	// second read (e.g. via ListReleases) shouldn't need to ask k8s again to
	// see "running".
	stored, err := repo.GetReleaseForOwner(ctx, rel.ID, ownerID)
	if err != nil {
		t.Fatalf("reload from repo: %v", err)
	}
	if stored.Status != StatusRunning {
		t.Fatalf("expected persisted status running, got %s", stored.Status)
	}

	// No pod yet (scheduling gap right after deploy — common for bigger template
	// flows) must stay initializing and keep polling, NOT false-fail. A crash
	// reports StatusFailed instead; the worker's crash cleanup handles the rest.
	rel2, err := uc.CreateRelease(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("create second release: %v", err)
	}
	repo.releases[rel2.ID].Status = StatusInitializing
	orch.status = engine.StatusStopped
	got2, err := uc.GetRelease(ctx, rel2.ID, ownerID)
	if err != nil {
		t.Fatalf("get second release: %v", err)
	}
	if got2.Status != StatusInitializing {
		t.Fatalf("expected scheduling-gap deployment to stay initializing, got %s", got2.Status)
	}
	// A genuinely crash-looping pod DOES resolve to failed.
	orch.status = engine.StatusFailed
	got3, err := uc.GetRelease(ctx, rel2.ID, ownerID)
	if err != nil {
		t.Fatalf("get release (crash): %v", err)
	}
	if got3.Status != StatusFailed {
		t.Fatalf("expected crash-looping deployment to resolve to failed, got %s", got3.Status)
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

// TestCreateReleasePublishesWSEvent verifies a release deploy broadcasts a
// "release_status" event to the constructor's ws-hub via its
// "user_<owner-id>" WS room — see internal/shared/wsevents.UserRoom and
// ws_events.go's publishReleaseStatus. The ws-hub's plain access token has no
// project_id claim, so the websocket service always assigns it to a user
// room, never a project one; publishing anywhere else means the JSON event
// never reaches it.
func TestCreateReleasePublishesWSEvent(t *testing.T) {
	repo := newFakeRepo()
	orch := newFakeOrchestrator()
	pub := &fakePublisher{}
	uc := NewUsecaseWithEventPublisher(repo, orch, pub)
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))

	rel, err := uc.CreateRelease(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}

	call, ok := pub.last()
	if !ok {
		t.Fatal("expected CreateRelease to publish a WS event")
	}
	if call.room != "user_"+ownerID.String() {
		t.Fatalf("expected room user_%s, got %q", ownerID, call.room)
	}
	if call.event != "release_status" {
		t.Fatalf("expected event release_status, got %q", call.event)
	}
	payload, ok := call.data.(releaseStatusPayload)
	if !ok {
		t.Fatalf("expected releaseStatusPayload, got %T", call.data)
	}
	// Room is keyed by owner, but data.project stays the project uuid — the
	// constructor filters on String(data.project) === project.id (uuid).
	if payload.Project != projectID.String() || payload.Status != string(rel.Status) || payload.Version != rel.Version {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

// TestPublishStatus_OwnerLookupFails_SkipsPublish verifies the best-effort
// contract: if ProjectOwnerID can't resolve the project's owner (e.g. it was
// hard-deleted between the deploy and the publish), the publish is silently
// skipped rather than broadcasting to a bogus/empty room — mirrors Django's
// _send_ws_notification swallowing failures.
func TestPublishStatus_OwnerLookupFails_SkipsPublish(t *testing.T) {
	repo := newFakeRepo()
	pub := &fakePublisher{}
	uc := NewUsecaseWithEventPublisher(repo, newFakeOrchestrator(), pub)
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))
	if _, err := uc.CreateRelease(ctx, projectID, ownerID); err != nil {
		t.Fatalf("create release: %v", err)
	}
	pub.calls = nil // reset — only interested in the next publish attempt

	// Simulate the owner lookup failing (project gone from the owner index).
	delete(repo.projectOwner, projectID)

	uc.(*usecase).publishReleaseStatus(ctx, &Release{ProjectID: projectID, Status: StatusRunning})
	if len(pub.calls) != 0 {
		t.Fatalf("expected no publish when owner lookup fails, got %+v", pub.calls)
	}
}

// TestReconcileReleasePublishesWSEventOnStatusChange verifies a lazily
// reconciled status transition (GetRelease asking the orchestrator live)
// also broadcasts — this is the path that mirrors Django's k8s-watcher-driven
// _send_ws_notification, since this backend has no separate watcher.
func TestReconcileReleasePublishesWSEventOnStatusChange(t *testing.T) {
	repo := newFakeRepo()
	orch := newFakeOrchestrator()
	orch.status = engine.StatusInitializing
	pub := &fakePublisher{}
	uc := NewUsecaseWithEventPublisher(repo, orch, pub)
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))

	rel, err := uc.CreateRelease(ctx, projectID, ownerID)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	pub.calls = nil // reset — only interested in the reconcile-triggered publish

	orch.status = engine.StatusRunning
	got, err := uc.GetRelease(ctx, rel.ID, ownerID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("expected reconciled to running, got %s", got.Status)
	}
	call, ok := pub.last()
	if !ok {
		t.Fatal("expected reconcile to publish a WS event on status change")
	}
	if call.room != "user_"+ownerID.String() || call.event != "release_status" {
		t.Fatalf("unexpected publish call: %+v", call)
	}
}

// TestStartDebugPublishesWSEvent is CreateReleasePublishesWSEvent's Debug
// counterpart, verifying the "debug_status" event and updated_at field.
func TestStartDebugPublishesWSEvent(t *testing.T) {
	repo := newFakeRepo()
	orch := newFakeOrchestrator()
	pub := &fakePublisher{}
	uc := NewUsecaseWithEventPublisher(repo, orch, pub)
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))

	if _, err := uc.StartDebug(ctx, projectID, ownerID); err != nil {
		t.Fatalf("start debug: %v", err)
	}

	call, ok := pub.last()
	if !ok {
		t.Fatal("expected StartDebug to publish a WS event")
	}
	if call.room != "user_"+ownerID.String() || call.event != "debug_status" {
		t.Fatalf("unexpected publish call: %+v", call)
	}
	if _, ok := call.data.(debugStatusPayload); !ok {
		t.Fatalf("expected debugStatusPayload, got %T", call.data)
	}
}

func TestRuntimeReplicasFromProjectEnvironment(t *testing.T) {
	if got := runtimeReplicas(map[string]string{"RUNTIME_REPLICAS": "10"}); got != 10 {
		t.Fatalf("replicas = %d", got)
	}
	if got := runtimeReplicas(map[string]string{"RUNTIME_REPLICAS": "1000"}); got != 100 {
		t.Fatalf("replicas must clamp to 100, got %d", got)
	}
	if got := runtimeReplicas(map[string]string{"RUNTIME_REPLICAS": "bad"}); got != 0 {
		t.Fatalf("invalid value must use default, got %d", got)
	}
}

// TestPublishSkippedWhenNoPublisherWired ensures the degrade-gracefully
// contract: a plain NewUsecase (nil publisher) never touches the publish
// path, matching every other optional-dependency convention in this backend.
func TestPublishSkippedWhenNoPublisherWired(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, newFakeOrchestrator())
	ctx := context.Background()

	ownerID := uuid.New()
	projectID := repo.addProject(ownerID, json.RawMessage(`[]`))

	if _, err := uc.CreateRelease(ctx, projectID, ownerID); err != nil {
		t.Fatalf("create release: %v", err)
	}
	// No assertion beyond "did not panic" — NewUsecase's usecase.pub is nil,
	// so publishReleaseStatus must no-op cleanly.
}
