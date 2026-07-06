package collabpersist

import "context"

type Repository interface {
	PersistOp(ctx context.Context, projectRef string, op map[string]any) (projectID string, changed bool, err error)
}
