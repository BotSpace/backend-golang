package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/wsevents"
	"github.com/google/uuid"
)

// Usecase exposes owner-scoped release/debug/environment operations. Deploys
// go through engine.Orchestrator — the server wires engine.Stub until a real
// engine-golang client is available (see PORTING_GUIDE.md).
type Usecase interface {
	CreateRelease(ctx context.Context, projectID, ownerID uuid.UUID) (*Release, error)
	ListReleases(ctx context.Context, projectID, ownerID uuid.UUID) ([]Release, error)
	GetRelease(ctx context.Context, id, ownerID uuid.UUID) (*Release, error)
	StopRelease(ctx context.Context, id, ownerID uuid.UUID) (*Release, error)
	// GetReleaseLogs ports ReleaseView.logs (core/apps/api/views/project.py):
	// a non-streaming tail of the release's live pod log. tailLines/
	// sinceSeconds <= 0 fall back to the orchestrator's defaults (200 lines,
	// no time restriction).
	GetReleaseLogs(ctx context.Context, id, ownerID uuid.UUID, tailLines, sinceSeconds int) (engine.LogSnapshot, error)

	StartDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)
	StopDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)
	GetDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)
	// GetDebugLogs ports ProjectView.debug_logs — same snapshot shape as
	// GetReleaseLogs, for the project's single debug container.
	GetDebugLogs(ctx context.Context, projectID, ownerID uuid.UUID, tailLines, sinceSeconds int) (engine.LogSnapshot, error)

	UpsertEnvironment(ctx context.Context, in UpsertEnvironmentInput) (*Environment, error)
	ListEnvironments(ctx context.Context, projectID, ownerID uuid.UUID) ([]Environment, error)
	DeleteEnvironment(ctx context.Context, key string, projectID, ownerID uuid.UUID) error
}

type usecase struct {
	repo   Repository
	orch   engine.Orchestrator
	assets RuntimeAssetLoader
	// pub is nil-able — see NewUsecaseWithEventPublisher's doc comment.
	pub wsevents.Publisher
}

// RuntimeAssetLoader builds the project-specific CMS and module files mounted
// into every engine pod. It lives as a function port so release does not import
// the cms/plugin modules directly.
type RuntimeAssetLoader func(ctx context.Context, projectID, ownerID uuid.UUID) (schemas, modules []byte, err error)

func NewUsecase(repo Repository, orch engine.Orchestrator) Usecase {
	return &usecase{repo: repo, orch: orch}
}

// NewUsecaseWithEventPublisher wires a real-time event publisher (see
// internal/shared/wsevents) that broadcasts release/debug status changes to
// the constructor via the standalone websocket service — mirrors Django's
// watch_k8s_events._send_ws_notification / ReleaseView.perform_create /
// ProjectView._start_debug_container's ws_publish calls (core/domain/ws.py
// EventTypes.RELEASE_STATUS / DEBUG_STATUS). pub may be nil — same
// degrade-gracefully convention as the rest of this backend's optional deps
// (see server.go's cms/chat Mongo wiring): nil just means every publish
// call below no-ops.
func NewUsecaseWithEventPublisher(repo Repository, orch engine.Orchestrator, pub wsevents.Publisher) Usecase {
	return &usecase{repo: repo, orch: orch, pub: pub}
}

func NewUsecaseWithRuntimeAssets(repo Repository, orch engine.Orchestrator, pub wsevents.Publisher, assets RuntimeAssetLoader) Usecase {
	return &usecase{repo: repo, orch: orch, pub: pub, assets: assets}
}

func (u *usecase) runtimeAssets(ctx context.Context, projectID, ownerID uuid.UUID) ([]byte, []byte, error) {
	if u.assets == nil {
		return []byte(`{}`), []byte(`{"modules":[]}`), nil
	}
	schemas, modules, err := u.assets(ctx, projectID, ownerID)
	if err != nil {
		return nil, nil, fmt.Errorf("load engine runtime assets: %w", err)
	}
	return schemas, modules, nil
}

// CreateRelease ports Django's ReleaseView.perform_create + the RelaseSignal
// post_save hook (core/apps/api/signals/project.py): bump the version, copy
// the project's current draft flow, stop every previously RUNNING/INIT
// release for the project, then deploy the new one.
func (u *usecase) CreateRelease(ctx context.Context, projectID, ownerID uuid.UUID) (*Release, error) {
	latest, err := u.repo.LatestReleaseVersion(ctx, projectID, ownerID)
	if err != nil {
		return nil, mapProjectErr(err)
	}

	rel, err := u.repo.CreateRelease(ctx, projectID, ownerID, latest+1)
	if err != nil {
		return nil, mapProjectErr(err)
	}

	// Cleanup rule: stop old RUNNING/INIT releases before deploying the new
	// one — mirrors RelaseSignal's exclude(id=instance.id) loop.
	stopped, err := u.repo.StopActiveReleasesExcept(ctx, projectID, rel.ID)
	if err != nil {
		return nil, httpx.Internal(fmt.Errorf("stop old releases: %w", err))
	}
	for _, old := range stopped {
		if old.ServiceID == "" {
			continue
		}
		// Best-effort like Django's remove_service(): a stop failure on an old
		// container must not block deploying the new release.
		_ = u.orch.Stop(ctx, old.ServiceID)
	}

	env, err := u.envMap(ctx, projectID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	env["IS_RELEASE"] = "true"

	// Best-effort: the project row was just proven to exist/be owned above, so
	// this practically never fails. A failure just means the orchestrator
	// skips creating the "bot-<uuid>" Service (deployment still proceeds).
	releaseWebhookUUID, _, _ := u.repo.GetProjectWebhookUUIDs(ctx, projectID, ownerID)
	schemas, modules, err := u.runtimeAssets(ctx, projectID, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	result, deployErr := u.orch.Deploy(ctx, engine.DeploySpec{
		ProjectID:   projectID,
		ReleaseUUID: rel.ID,
		Version:     int(rel.Version),
		Debug:       false,
		Flow:        rel.Flow,
		Schemas:     schemas,
		Modules:     modules,
		Env:         env,
		WebhookUUID: releaseWebhookUUID,
	})

	status, serviceID := deployOutcome(result, deployErr)
	updated, err := u.repo.UpdateReleaseDeployResult(ctx, rel.ID, serviceID, status)
	if err != nil {
		return nil, httpx.Internal(fmt.Errorf("save release deploy result: %w", err))
	}
	u.publishReleaseStatus(ctx, updated)
	if deployErr != nil {
		return updated, deploymentUnavailable(deployErr)
	}
	return updated, nil
}

func (u *usecase) ListReleases(ctx context.Context, projectID, ownerID uuid.UUID) ([]Release, error) {
	items, err := u.repo.ListReleasesForProject(ctx, projectID, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	for i := range items {
		items[i] = u.reconcileRelease(ctx, items[i])
	}
	return items, nil
}

func (u *usecase) GetRelease(ctx context.Context, id, ownerID uuid.UUID) (*Release, error) {
	rel, err := u.repo.GetReleaseForOwner(ctx, id, ownerID)
	if err != nil {
		return nil, mapReleaseErr(err)
	}
	reconciled := u.reconcileRelease(ctx, *rel)
	return &reconciled, nil
}

// reconcileRelease is the lazy fix for "status never leaves initializing":
// there's no k8s-watcher pushing pod events into the DB (see
// engine.ReconcileStatus doc comment), so every read of a non-terminal
// release asks the orchestrator live and persists the result. Best-effort —
// an orchestrator error just returns the DB row unchanged so a transient k8s
// API hiccup never turns into a request failure.
func (u *usecase) reconcileRelease(ctx context.Context, rel Release) Release {
	if rel.ServiceID == "" || (rel.Status != StatusPending && rel.Status != StatusInitializing && rel.Status != StatusFailed) {
		return rel
	}
	live, err := u.orch.Status(ctx, rel.ServiceID)
	if err != nil {
		return rel
	}
	next := Status(engine.ReconcileStatus(engine.Status(rel.Status), live))
	if next == rel.Status {
		return rel
	}
	if updated, err := u.repo.UpdateReleaseDeployResult(ctx, rel.ID, rel.ServiceID, next); err == nil {
		u.publishReleaseStatus(ctx, updated)
		return *updated
	}
	rel.Status = next
	return rel
}

// StopRelease ports ReleaseView.stop: synchronously stop the deployment and
// flip status to STOPPED (never left dangling in STOPPING — see the comment
// in signals/project.py about the k8s-watcher race).
func (u *usecase) StopRelease(ctx context.Context, id, ownerID uuid.UUID) (*Release, error) {
	rel, err := u.repo.GetReleaseForOwner(ctx, id, ownerID)
	if err != nil {
		return nil, mapReleaseErr(err)
	}
	if rel.ServiceID != "" {
		_ = u.orch.Stop(ctx, rel.ServiceID)
	}
	updated, err := u.repo.UpdateReleaseStatusForOwner(ctx, id, ownerID, StatusStopped)
	if err != nil {
		return nil, mapReleaseErr(err)
	}
	u.publishReleaseStatus(ctx, updated)
	return updated, nil
}

// GetReleaseLogs ports ReleaseView.logs (core/apps/api/views/project.py): a
// release that was never deployed (no service_id) returns a zero-value
// snapshot rather than an error — mirrors Django returning 200 with
// pod_name=null instead of 404/503 in that case.
func (u *usecase) GetReleaseLogs(ctx context.Context, id, ownerID uuid.UUID, tailLines, sinceSeconds int) (engine.LogSnapshot, error) {
	rel, err := u.repo.GetReleaseForOwner(ctx, id, ownerID)
	if err != nil {
		return engine.LogSnapshot{}, mapReleaseErr(err)
	}
	if rel.ServiceID == "" {
		return engine.LogSnapshot{}, nil
	}
	snap, err := u.orch.Logs(ctx, rel.ServiceID, tailLines, sinceSeconds)
	if err != nil {
		return engine.LogSnapshot{}, httpx.Internal(err)
	}
	return snap, nil
}

// StartDebug ports ProjectView._start_debug_container / .debug: get-or-create
// the project's single debug row and (re)deploy it with the current draft
// flow (unlike a release, debug always tracks the live draft, not a frozen
// snapshot).
func (u *usecase) StartDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	dbg, err := u.repo.GetOrCreateDebugForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, mapProjectErr(err)
	}
	flow, err := u.repo.GetProjectFlowForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, mapProjectErr(err)
	}
	env, err := u.envMap(ctx, projectID)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	_, debugWebhookUUID, _ := u.repo.GetProjectWebhookUUIDs(ctx, projectID, ownerID)
	schemas, modules, err := u.runtimeAssets(ctx, projectID, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	result, deployErr := u.orch.Deploy(ctx, engine.DeploySpec{
		ProjectID:   projectID,
		ReleaseUUID: dbg.ID,
		Debug:       true,
		Flow:        flow,
		Schemas:     schemas,
		Modules:     modules,
		Env:         env,
		WebhookUUID: debugWebhookUUID,
	})

	status, serviceID := deployOutcome(result, deployErr)
	updated, err := u.repo.UpdateDebugDeployResult(ctx, dbg.ID, serviceID, status)
	if err != nil {
		return nil, httpx.Internal(fmt.Errorf("save debug deploy result: %w", err))
	}
	u.publishDebugStatus(ctx, updated)
	if deployErr != nil {
		return updated, deploymentUnavailable(deployErr)
	}
	return updated, nil
}

// StopDebug ports ProjectView.stop: synchronously stop the debug deployment.
func (u *usecase) StopDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	dbg, err := u.repo.GetDebugForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, mapDebugErr(err)
	}
	if dbg.ServiceID != "" {
		_ = u.orch.Stop(ctx, dbg.ServiceID)
	}
	updated, err := u.repo.UpdateDebugStatusForOwner(ctx, dbg.ID, ownerID, StatusStopped)
	if err != nil {
		return nil, mapDebugErr(err)
	}
	u.publishDebugStatus(ctx, updated)
	return updated, nil
}

func (u *usecase) GetDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	dbg, err := u.repo.GetDebugForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, mapDebugErr(err)
	}
	reconciled := u.reconcileDebug(ctx, *dbg)
	return &reconciled, nil
}

// GetDebugLogs ports ProjectView.debug_logs: a project with no debug row yet
// (never ran Debug Run) or a debug row with no service_id returns a
// zero-value snapshot rather than a 404 — mirrors Django's soft
// {"pod_name": null, "lines": [], "detail": "Debug is not running"} response.
func (u *usecase) GetDebugLogs(ctx context.Context, projectID, ownerID uuid.UUID, tailLines, sinceSeconds int) (engine.LogSnapshot, error) {
	dbg, err := u.repo.GetDebugForOwner(ctx, projectID, ownerID)
	if errors.Is(err, database.ErrNotFound) {
		return engine.LogSnapshot{}, nil
	}
	if err != nil {
		return engine.LogSnapshot{}, mapDebugErr(err)
	}
	if dbg.ServiceID == "" {
		return engine.LogSnapshot{}, nil
	}
	snap, err := u.orch.Logs(ctx, dbg.ServiceID, tailLines, sinceSeconds)
	if err != nil {
		return engine.LogSnapshot{}, httpx.Internal(err)
	}
	return snap, nil
}

// reconcileDebug is GetDebug's counterpart to reconcileRelease — see its doc
// comment.
func (u *usecase) reconcileDebug(ctx context.Context, dbg Debug) Debug {
	if dbg.ServiceID == "" || (dbg.Status != StatusPending && dbg.Status != StatusInitializing && dbg.Status != StatusFailed) {
		return dbg
	}
	live, err := u.orch.Status(ctx, dbg.ServiceID)
	if err != nil {
		return dbg
	}
	next := Status(engine.ReconcileStatus(engine.Status(dbg.Status), live))
	if next == dbg.Status {
		return dbg
	}
	if updated, err := u.repo.UpdateDebugDeployResult(ctx, dbg.ID, dbg.ServiceID, next); err == nil {
		u.publishDebugStatus(ctx, updated)
		return *updated
	}
	dbg.Status = next
	return dbg
}

func (u *usecase) UpsertEnvironment(ctx context.Context, in UpsertEnvironmentInput) (*Environment, error) {
	env, err := u.repo.UpsertEnvironment(ctx, in)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("project.not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return env, nil
}

func (u *usecase) ListEnvironments(ctx context.Context, projectID, ownerID uuid.UUID) ([]Environment, error) {
	items, err := u.repo.ListEnvironmentsForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return items, nil
}

func (u *usecase) DeleteEnvironment(ctx context.Context, key string, projectID, ownerID uuid.UUID) error {
	if err := u.repo.DeleteEnvironmentForOwner(ctx, key, projectID, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("enviroment.not_found", "environment variable not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

// envMap loads every KEY=VALUE pair for a project into the shape
// engine.DeploySpec expects (Django's project_enviroment() generator).
func (u *usecase) envMap(ctx context.Context, projectID uuid.UUID) (map[string]string, error) {
	envs, err := u.repo.ListEnvironmentsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list environment: %w", err)
	}
	out := make(map[string]string, len(envs)+1)
	for _, e := range envs {
		out[e.Key] = e.Value
	}
	return out, nil
}

// deployOutcome turns an engine.Deploy call into a (status, serviceID) pair,
// treating a transport/orchestrator error as FAILED (Django sets status to
// FAILED and re-raises a 503 when deploy_service raises ApiException).
func deployOutcome(result engine.DeployResult, err error) (Status, string) {
	if err != nil {
		return StatusFailed, ""
	}
	return Status(result.Status), result.ServiceID
}

func deploymentUnavailable(err error) *httpx.AppError {
	return &httpx.AppError{
		Code:    "release.deployment_unavailable",
		Message: "deployment service is temporarily unavailable",
		Status:  503,
		Err:     err,
	}
}

// mapProjectErr is used where ErrNotFound means the project itself is
// missing/not owned by the caller (the SQL join returned zero rows).
func mapProjectErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("project.not_found", "project not found")
	}
	return httpx.Internal(err)
}

func mapReleaseErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("release.not_found", "release not found")
	}
	return httpx.Internal(err)
}

func mapDebugErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("debug.not_found", "debug container not found")
	}
	return httpx.Internal(err)
}
