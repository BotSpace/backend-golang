package collabpersist

import (
	"context"
	"encoding/json"
)

type Repository interface {
	PersistOp(ctx context.Context, projectRef string, op map[string]any) (projectID string, changed bool, err error)
	RuntimeSnapshot(ctx context.Context, projectID string) (debugWebhookID string, flow json.RawMessage, err error)
}

// RuntimeIdentityRepository resolves persisted tenants using live SQL project
// records and verifies the selected debug/release webhook belongs to that row.
type RuntimeIdentityRepository interface {
	ResolveRuntimeProject(ctx context.Context, projectRef, webhookID string, debug bool) (string, error)
}

type RuntimeReconciler interface {
	ReconcileRuntimes(ctx context.Context) error
}

type RuntimeReloader interface {
	ReloadProject(ctx context.Context, projectID string) error
}
