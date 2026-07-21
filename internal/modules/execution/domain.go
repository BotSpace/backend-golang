// Package execution stores the durable lifecycle of one workflow evaluation.
// It deliberately separates short-lived execution state from the bot's
// long-lived conversation context kept by engine-golang in Redis.
package execution

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Execution struct {
	ID               uuid.UUID
	ProjectID        uuid.UUID
	ReleaseID        *uuid.UUID
	ChatID           int64
	TelegramUpdateID *int64
	RunID            int64
	TriggerType      string
	RuntimeMode      string
	FlowHash         string
	Status           Status
	CurrentNodeID    string
	Error            string
	StartedAt        time.Time
	FinishedAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type NodeRun struct {
	ID          uuid.UUID
	ExecutionID uuid.UUID
	NodeID      string
	NodeType    string
	Attempt     int32
	Status      Status
	Error       string
	Sequence    int64
	StartedAt   time.Time
	FinishedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type StartInput struct {
	ID               uuid.UUID
	ProjectID        uuid.UUID
	ChatID           int64
	TelegramUpdateID *int64
	RunID            int64
	TriggerType      string
	RuntimeMode      string
	FlowHash         string
	EventTS          time.Time
}

type FinishInput struct {
	ID      uuid.UUID
	Status  Status
	Error   string
	EventTS time.Time
}

type NodeRunInput struct {
	ExecutionID uuid.UUID
	NodeID      string
	NodeType    string
	Attempt     int32
	Status      Status
	Error       string
	Sequence    int64
	EventTS     time.Time
}

type ListFilter struct {
	ProjectID   uuid.UUID
	OwnerID     uuid.UUID
	Status      Status
	RuntimeMode string
	Limit       int32
	Offset      int32
}

type Repository interface {
	Start(context.Context, StartInput) (*Execution, error)
	Finish(context.Context, FinishInput) (*Execution, error)
	RecordNodeRun(context.Context, NodeRunInput) (*NodeRun, error)
	ListForOwner(context.Context, ListFilter) ([]Execution, error)
	GetForOwner(context.Context, uuid.UUID, uuid.UUID) (*Execution, error)
	ListNodeRunsForOwner(context.Context, uuid.UUID, uuid.UUID) ([]NodeRun, error)
}

type Usecase interface {
	Start(context.Context, StartInput) error
	Finish(context.Context, FinishInput) error
	RecordNodeRun(context.Context, NodeRunInput) error
	List(context.Context, ListFilter) ([]Execution, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (*Execution, []NodeRun, error)
}
