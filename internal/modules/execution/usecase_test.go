package execution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRepository struct {
	started  []StartInput
	finished []FinishInput
	nodes    []NodeRunInput
	items    []Execution
	nodeRuns []NodeRun
}

func (r *fakeRepository) Start(_ context.Context, in StartInput) (*Execution, error) {
	r.started = append(r.started, in)
	return &Execution{ID: in.ID, ProjectID: in.ProjectID, Status: StatusRunning}, nil
}
func (r *fakeRepository) Finish(_ context.Context, in FinishInput) (*Execution, error) {
	r.finished = append(r.finished, in)
	return &Execution{ID: in.ID, Status: in.Status}, nil
}
func (r *fakeRepository) RecordNodeRun(_ context.Context, in NodeRunInput) (*NodeRun, error) {
	r.nodes = append(r.nodes, in)
	return &NodeRun{ExecutionID: in.ExecutionID, NodeID: in.NodeID, Attempt: in.Attempt}, nil
}
func (r *fakeRepository) ListForOwner(_ context.Context, _ ListFilter) ([]Execution, error) {
	return r.items, nil
}
func (r *fakeRepository) GetForOwner(_ context.Context, id, _ uuid.UUID) (*Execution, error) {
	return &Execution{ID: id}, nil
}
func (r *fakeRepository) ListNodeRunsForOwner(_ context.Context, _, _ uuid.UUID) ([]NodeRun, error) {
	return r.nodeRuns, nil
}

func TestLifecycleDefaultsAndNodeAttempt(t *testing.T) {
	repo := &fakeRepository{}
	uc := NewUsecase(repo)
	executionID, projectID := uuid.New(), uuid.New()

	if err := uc.Start(context.Background(), StartInput{ID: executionID, ProjectID: projectID}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(repo.started) != 1 || repo.started[0].TriggerType != "telegram" || repo.started[0].EventTS.IsZero() {
		t.Fatalf("start defaults not applied: %#v", repo.started)
	}
	if err := uc.RecordNodeRun(context.Background(), NodeRunInput{
		ExecutionID: executionID, NodeID: "send", Status: StatusSucceeded,
	}); err != nil {
		t.Fatalf("record node: %v", err)
	}
	if len(repo.nodes) != 1 || repo.nodes[0].Attempt != 1 || repo.nodes[0].EventTS.IsZero() {
		t.Fatalf("node defaults not applied: %#v", repo.nodes)
	}
}

func TestRejectsInvalidLifecycleStatus(t *testing.T) {
	uc := NewUsecase(&fakeRepository{})
	err := uc.Finish(context.Background(), FinishInput{
		ID: uuid.New(), Status: Status("unknown"), EventTS: time.Now(),
	})
	if err == nil {
		t.Fatal("expected invalid status error")
	}
}
