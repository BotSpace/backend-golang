package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
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

	StartDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)
	StopDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)
	GetDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)

	UpsertEnvironment(ctx context.Context, in UpsertEnvironmentInput) (*Environment, error)
	ListEnvironments(ctx context.Context, projectID, ownerID uuid.UUID) ([]Environment, error)
	DeleteEnvironment(ctx context.Context, key string, projectID, ownerID uuid.UUID) error
}

type usecase struct {
	repo Repository
	orch engine.Orchestrator
}

func NewUsecase(repo Repository, orch engine.Orchestrator) Usecase {
	return &usecase{repo: repo, orch: orch}
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

	result, deployErr := u.orch.Deploy(ctx, engine.DeploySpec{
		ProjectID:   projectID,
		ReleaseUUID: rel.ID,
		Version:     int(rel.Version),
		Debug:       false,
		Flow:        rel.Flow,
		Env:         env,
		WebhookUUID: releaseWebhookUUID,
	})

	status, serviceID := deployOutcome(result, deployErr)
	updated, err := u.repo.UpdateReleaseDeployResult(ctx, rel.ID, serviceID, status)
	if err != nil {
		return nil, httpx.Internal(fmt.Errorf("save release deploy result: %w", err))
	}
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
	return items, nil
}

func (u *usecase) GetRelease(ctx context.Context, id, ownerID uuid.UUID) (*Release, error) {
	rel, err := u.repo.GetReleaseForOwner(ctx, id, ownerID)
	return rel, mapReleaseErr(err)
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
	return updated, nil
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

	result, deployErr := u.orch.Deploy(ctx, engine.DeploySpec{
		ProjectID:   projectID,
		ReleaseUUID: dbg.ID,
		Debug:       true,
		Flow:        flow,
		Env:         env,
		WebhookUUID: debugWebhookUUID,
	})

	status, serviceID := deployOutcome(result, deployErr)
	updated, err := u.repo.UpdateDebugDeployResult(ctx, dbg.ID, serviceID, status)
	if err != nil {
		return nil, httpx.Internal(fmt.Errorf("save debug deploy result: %w", err))
	}
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
	return updated, nil
}

func (u *usecase) GetDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	dbg, err := u.repo.GetDebugForOwner(ctx, projectID, ownerID)
	return dbg, mapDebugErr(err)
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
