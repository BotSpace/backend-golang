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

// MongoCredentials are the per-project MongoDB credentials.
type MongoCredentials struct {
	Username         string
	Password         string
	Database         string
	ConnectionString string
}

// Orchestrator deploys/stops bot engine instances. Implemented by a real
// Docker/K8s client (or an HTTP client to engine-golang) in production.
type Orchestrator interface {
	Deploy(ctx context.Context, spec DeploySpec) (DeployResult, error)
	Stop(ctx context.Context, serviceID string) error
	Status(ctx context.Context, serviceID string) (Status, error)
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
func (Stub) Provision(_ context.Context, projectID uuid.UUID) (MongoCredentials, error) {
	return MongoCredentials{}, nil
}
func (Stub) Deprovision(context.Context, uuid.UUID) error { return nil }
