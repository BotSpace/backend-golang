// Package release ports the Django project-runtime domain (RelaseModel,
// DebugModel, EnviromentModel — core/apps/api/models/project.py) to Go.
//
// A Release is one deployed version of a project's flow; a Debug is the
// single live debug container per project (one-to-one); an Environment is a
// KEY=VALUE pair injected into a project's deploy. Deploy/stop is delegated
// to engine.Orchestrator (see usecase.go) — this package never talks to
// Kubernetes/Docker directly.
package release

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Status mirrors engine.Status (Django ContainerStatusEnum). Kept as an
// independent string type so this domain layer does not import the engine
// package — the usecase converts at the boundary (identical underlying type).
type Status string

const (
	StatusPending      Status = "pending"
	StatusInitializing Status = "initializing"
	StatusRunning      Status = "running"
	StatusStopped      Status = "stopped"
	StatusStopping     Status = "stopping"
	StatusFailed       Status = "failed"
	StatusSuspended    Status = "suspended"
)

// Release is one deployed version of a project's flow.
type Release struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Version   int32
	Flow      json.RawMessage
	ServiceID string
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Debug is the single live debug container for a project.
type Debug struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	FlowHash  string
	ServiceID string
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Environment is one KEY=VALUE pair injected into a project's deploy.
type Environment struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	UserID    uuid.UUID
	Key       string
	Value     string
	Secret    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertEnvironmentInput is the owner-scoped create-or-update payload for an
// environment variable (unique on key+project — a second write to the same
// key overwrites the value, matching Django's update_or_create).
type UpsertEnvironmentInput struct {
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	UserID    uuid.UUID
	Key       string
	Value     string
	Secret    bool
}

// Repository is the persistence port. Every method is project-owner-scoped
// (enforced via a join to projects in queries/release.sql) except the
// ListEnvironmentsByProject/GetProjectFlowForOwner internal reads used while
// building a deploy — callers must have already established ownership.
type Repository interface {
	// Release
	CreateRelease(ctx context.Context, projectID, ownerID uuid.UUID, version int32) (*Release, error)
	GetReleaseForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Release, error)
	ListReleasesForProject(ctx context.Context, projectID, ownerID uuid.UUID) ([]Release, error)
	LatestReleaseVersion(ctx context.Context, projectID, ownerID uuid.UUID) (int32, error)
	UpdateReleaseDeployResult(ctx context.Context, id uuid.UUID, serviceID string, status Status) (*Release, error)
	UpdateReleaseStatusForOwner(ctx context.Context, id, ownerID uuid.UUID, status Status) (*Release, error)
	// StopActiveReleasesExcept flips every RUNNING/INIT release of the project
	// (excluding excludeID) to STOPPED and returns the rows that were changed,
	// so the caller can tell the engine to stop their containers too.
	StopActiveReleasesExcept(ctx context.Context, projectID, excludeID uuid.UUID) ([]Release, error)

	// Debug
	GetDebugForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)
	GetOrCreateDebugForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error)
	UpdateDebugDeployResult(ctx context.Context, id uuid.UUID, serviceID string, status Status) (*Debug, error)
	UpdateDebugStatusForOwner(ctx context.Context, id, ownerID uuid.UUID, status Status) (*Debug, error)

	// Environment
	UpsertEnvironment(ctx context.Context, in UpsertEnvironmentInput) (*Environment, error)
	ListEnvironmentsForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]Environment, error)
	GetEnvironmentForOwner(ctx context.Context, key string, projectID, ownerID uuid.UUID) (*Environment, error)
	DeleteEnvironmentForOwner(ctx context.Context, key string, projectID, ownerID uuid.UUID) error

	// Internal reads used while assembling an engine.DeploySpec.
	ListEnvironmentsByProject(ctx context.Context, projectID uuid.UUID) ([]Environment, error)
	GetProjectFlowForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (json.RawMessage, error)
	// GetProjectWebhookUUIDs returns the project's stable release/debug webhook
	// UUIDs, used to name the orchestrator's "bot-<uuid>" Service so Telegram
	// webhook routing survives redeploys.
	GetProjectWebhookUUIDs(ctx context.Context, projectID, ownerID uuid.UUID) (releaseUUID, debugUUID uuid.UUID, err error)

	// ListNonTerminalReleases/ListNonTerminalDebugs are unscoped
	// (system-wide) reads for the scheduler's reconcile tick — see
	// Usecase.ReconcileAll, the janitor porting Django's
	// cleanup_failed_releases beat task.
	ListNonTerminalReleases(ctx context.Context) ([]Release, error)
	ListNonTerminalDebugs(ctx context.Context) ([]Debug, error)
}
