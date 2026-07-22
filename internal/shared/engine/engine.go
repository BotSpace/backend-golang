// Package engine abstracts bot deployment orchestration (Docker / Kubernetes)
// and per-project MongoDB provisioning behind interfaces.
//
// KubernetesOrchestrator (kubernetes.go) talks to the botmother namespace
// directly via client-go — the same Deployment/Service/ConfigMap/Secret
// shape the old Django kubernetes_service.py produced. The Stub
// implementation is a safe no-op used when no cluster is reachable (local
// dev) or engine.mode=stub; server.go picks between them. Every method is
// context-aware and returns typed errors so callers degrade gracefully.
package engine

import (
	"context"

	"github.com/google/uuid"
)

// Status mirrors the Django ContainerStatusEnum.
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

// DeploySpec describes a bot deployment request.
type DeploySpec struct {
	ProjectID   uuid.UUID
	ReleaseUUID uuid.UUID
	Version     int
	Debug       bool
	Flow        []byte // constructor graph JSON
	Schemas     []byte // collection -> field -> CMS kind JSON
	Modules     []byte // {"modules": [...]} runtime module routing JSON
	Env         map[string]string

	// Name overrides how KubernetesOrchestrator derives the Deployment/Service
	// name. Empty means "derive from ProjectID/Version/Debug" (bot-engine
	// convention: release-<projectID>-<version> / debug-<projectID>). Shared
	// module deploys (see plugin.moduleDeploySpec) set this explicitly since
	// they have no ProjectID.
	Name string
	// Image overrides the orchestrator's default bot-engine image. Module
	// deploys use their own image (Module.Image); empty keeps the default.
	Image string
	// ContainerPort/ReadinessPath/LivenessPath override the bot-engine
	// defaults (8000, /ready, /health). Zero value keeps the default.
	ContainerPort int
	ReadinessPath string
	LivenessPath  string
	// WebhookUUID, when set, makes KubernetesOrchestrator create/update a
	// "bot-<uuid>" Service pointing at this deployment (release/debug bot
	// engines only — module deploys leave this at uuid.Nil).
	WebhookUUID uuid.UUID
}

// DeployResult is what the orchestrator returns for a deploy.
type DeployResult struct {
	ServiceID string
	Status    Status
}

// ReconcileStatus resolves the display status for a release/debug row that is
// still in a non-terminal DB state by merging in what the orchestrator
// reports right now. This is the "lazy" fix for status never advancing past
// pending/initializing: Django had a k8s-watcher process updating the DB on
// pod events; we don't run one, so callers ask the orchestrator live on each
// read instead. Terminal DB states (running/stopped/failed/stopping/
// suspended) are left untouched — once a release is known to have finished
// one way or another we don't second-guess it here.
func ReconcileStatus(dbStatus Status, live Status) Status {
	switch dbStatus {
	case StatusPending, StatusInitializing:
		switch live {
		case StatusRunning:
			return StatusRunning
		case StatusFailed:
			return StatusFailed
		case StatusStopped:
			// No pod yet backs the deployment. Right after a deploy this is just
			// the scheduling gap (esp. for bigger template flows), NOT a failure
			// — stay initializing and keep polling. A genuinely crash-looping pod
			// reports StatusFailed (handled above); the worker's crash cleanup
			// handles anything that never comes up.
			return StatusInitializing
		default:
			return StatusInitializing
		}
	case StatusFailed:
		// "failed" is NOT terminal: a deploy that recorded a transient failure
		// (slow first start — schemas/flow load — or a best-effort Service
		// error) while the pod actually came up must recover. Re-check live:
		// promote to running only if the pod is genuinely Ready, else stay failed.
		if live == StatusRunning {
			return StatusRunning
		}
		return StatusFailed
	default:
		return dbStatus
	}
}

// MongoCredentials are the per-project MongoDB credentials.
type MongoCredentials struct {
	Username         string
	Password         string
	Database         string
	ConnectionString string
}

// LogSnapshot is a non-streaming tail of a running deployment's pod logs —
// the Go counterpart of Django's deployment_logs_snapshot() dict
// (core/apps/api/services/kubernetes_service.py). PodName is empty when no
// pod backs serviceID (deployment not running / never deployed).
type LogSnapshot struct {
	PodName   string
	Lines     []string
	Truncated bool
}

// Orchestrator deploys/stops bot engine instances. Implemented by a real
// Docker/K8s client (or an HTTP client to engine-golang) in production.
type Orchestrator interface {
	Deploy(ctx context.Context, spec DeploySpec) (DeployResult, error)
	Stop(ctx context.Context, serviceID string) error
	Status(ctx context.Context, serviceID string) (Status, error)
	// Logs returns a snapshot of the last tailLines lines of the deployment's
	// pod logs (clamped 1..2000), optionally restricted to the last
	// sinceSeconds (0/negative means "no restriction"). serviceID == ""
	// (nothing deployed yet) returns a zero-value snapshot, not an error.
	Logs(ctx context.Context, serviceID string, tailLines, sinceSeconds int) (LogSnapshot, error)
}

// MongoProvisioner creates/deletes per-project MongoDB databases + users.
type MongoProvisioner interface {
	Provision(ctx context.Context, projectID uuid.UUID) (MongoCredentials, error)
	Deprovision(ctx context.Context, projectID uuid.UUID) error
}

// Stub is a no-op Orchestrator + MongoProvisioner. It lets the API run and
// serve CRUD without a cluster; deploys report as pending. Replace with a real
// client (engine-golang HTTP) when available.
type Stub struct{}

func NewStub() *Stub { return &Stub{} }

func (Stub) Deploy(_ context.Context, spec DeploySpec) (DeployResult, error) {
	return DeployResult{ServiceID: "", Status: StatusPending}, nil
}
func (Stub) Stop(context.Context, string) error             { return nil }
func (Stub) Status(context.Context, string) (Status, error) { return StatusStopped, nil }
func (Stub) Logs(context.Context, string, int, int) (LogSnapshot, error) {
	return LogSnapshot{}, nil
}
func (Stub) Provision(_ context.Context, projectID uuid.UUID) (MongoCredentials, error) {
	return MongoCredentials{}, nil
}
func (Stub) Deprovision(context.Context, uuid.UUID) error { return nil }
