package collabpersist

import (
	"context"
	"encoding/json"
)

type Repository interface {
	PersistOp(ctx context.Context, projectRef string, op map[string]any) (projectID string, changed bool, err error)
	RuntimeSnapshot(ctx context.Context, projectID string) (debugWebhookID string, flow json.RawMessage, err error)
}

type RuntimeReloader interface {
	ReloadProject(ctx context.Context, projectID string) error
}
