// Package engine abstracts bot deployment orchestration (Docker / Kubernetes)
// and per-project MongoDB provisioning behind interfaces.
//
// The heavy cluster-side logic lives in the separate engine-golang /
// worker-golang services; this backend only needs to *request* deploys and
// read status. The Stub implementation is a safe no-op used until a real
// orchestrator client is wired in (set via server.go). Every method is
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
func (Stub) Stop(context.Context, string) error                    { return nil }
func (Stub) Status(context.Context, string) (Status, error)        { return StatusStopped, nil }
func (Stub) Provision(_ context.Context, projectID uuid.UUID) (MongoCredentials, error) {
	return MongoCredentials{}, nil
}
func (Stub) Deprovision(context.Context, uuid.UUID) error { return nil }
