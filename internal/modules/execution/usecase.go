package execution

import (
	"context"
	"errors"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

type usecase struct{ repo Repository }

func NewUsecase(repo Repository) Usecase { return &usecase{repo: repo} }

func (u *usecase) Start(ctx context.Context, in StartInput) error {
	if in.ID == uuid.Nil || in.ProjectID == uuid.Nil {
		return httpx.BadRequest("execution.invalid_identity", "execution_id and project_id are required")
	}
	if in.EventTS.IsZero() {
		in.EventTS = time.Now().UTC()
	}
	if in.TriggerType == "" {
		in.TriggerType = "telegram"
	}
	if in.RuntimeMode == "" {
		in.RuntimeMode = "release"
	}
	if !validRuntimeMode(in.RuntimeMode) {
		return httpx.BadRequest("execution.invalid_runtime_mode", "invalid runtime mode")
	}
	_, err := u.repo.Start(ctx, in)
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) Finish(ctx context.Context, in FinishInput) error {
	if !validStatus(in.Status, false) {
		return httpx.BadRequest("execution.invalid_status", "invalid terminal execution status")
	}
	if in.EventTS.IsZero() {
		in.EventTS = time.Now().UTC()
	}
	_, err := u.repo.Finish(ctx, in)
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("execution.not_found", "execution not found")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) RecordNodeRun(ctx context.Context, in NodeRunInput) error {
	if in.ExecutionID == uuid.Nil || in.NodeID == "" {
		return httpx.BadRequest("execution.invalid_node_run", "execution_id and node_id are required")
	}
	if !validStatus(in.Status, true) {
		return httpx.BadRequest("execution.invalid_node_status", "invalid node status")
	}
	if in.Attempt < 1 {
		in.Attempt = 1
	}
	if in.EventTS.IsZero() {
		in.EventTS = time.Now().UTC()
	}
	_, err := u.repo.RecordNodeRun(ctx, in)
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) List(ctx context.Context, in ListFilter) ([]Execution, error) {
	if in.Status != "" && !validStatus(in.Status, true) {
		return nil, httpx.BadRequest("execution.invalid_status", "invalid execution status")
	}
	if in.RuntimeMode != "" && !validRuntimeMode(in.RuntimeMode) {
		return nil, httpx.BadRequest("execution.invalid_runtime_mode", "invalid runtime mode")
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	if in.Offset < 0 {
		in.Offset = 0
	}
	items, err := u.repo.ListForOwner(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return items, nil
}

func validRuntimeMode(mode string) bool {
	return mode == "debug" || mode == "release" || mode == "shared"
}

func (u *usecase) Get(ctx context.Context, id, ownerID uuid.UUID) (*Execution, []NodeRun, error) {
	item, err := u.repo.GetForOwner(ctx, id, ownerID)
	if errors.Is(err, database.ErrNotFound) {
		return nil, nil, httpx.NotFound("execution.not_found", "execution not found")
	}
	if err != nil {
		return nil, nil, httpx.Internal(err)
	}
	nodes, err := u.repo.ListNodeRunsForOwner(ctx, id, ownerID)
	if err != nil {
		return nil, nil, httpx.Internal(err)
	}
	return item, nodes, nil
}

func validStatus(status Status, allowRunning bool) bool {
	switch status {
	case StatusWaiting, StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	case StatusRunning:
		return allowRunning
	default:
		return false
	}
}
