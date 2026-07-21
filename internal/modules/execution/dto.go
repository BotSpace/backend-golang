package execution

import (
	"time"

	"github.com/google/uuid"
)

type Response struct {
	ID               uuid.UUID  `json:"id"`
	ProjectID        uuid.UUID  `json:"project_id"`
	ReleaseID        *uuid.UUID `json:"release_id,omitempty"`
	ChatID           int64      `json:"chat_id"`
	TelegramUpdateID *int64     `json:"telegram_update_id,omitempty"`
	RunID            int64      `json:"run_id"`
	TriggerType      string     `json:"trigger_type"`
	RuntimeMode      string     `json:"runtime_mode"`
	FlowHash         string     `json:"flow_hash,omitempty"`
	Status           Status     `json:"status"`
	CurrentNodeID    string     `json:"current_node_id,omitempty"`
	Error            string     `json:"error,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
}

type NodeRunResponse struct {
	ID         uuid.UUID  `json:"id"`
	NodeID     string     `json:"node_id"`
	NodeType   string     `json:"node_type"`
	Attempt    int32      `json:"attempt"`
	Status     Status     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Sequence   int64      `json:"sequence"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type DetailResponse struct {
	Execution Response          `json:"execution"`
	NodeRuns  []NodeRunResponse `json:"node_runs"`
}

func toResponse(e Execution) Response {
	return Response{
		ID: e.ID, ProjectID: e.ProjectID, ReleaseID: e.ReleaseID, ChatID: e.ChatID,
		TelegramUpdateID: e.TelegramUpdateID, RunID: e.RunID, TriggerType: e.TriggerType,
		RuntimeMode: e.RuntimeMode,
		FlowHash:    e.FlowHash, Status: e.Status, CurrentNodeID: e.CurrentNodeID,
		Error: e.Error, StartedAt: e.StartedAt, FinishedAt: e.FinishedAt,
	}
}

func toListResponse(items []Execution) []Response {
	out := make([]Response, len(items))
	for i := range items {
		out[i] = toResponse(items[i])
	}
	return out
}

func toNodeRunResponse(items []NodeRun) []NodeRunResponse {
	out := make([]NodeRunResponse, len(items))
	for i, item := range items {
		out[i] = NodeRunResponse{
			ID: item.ID, NodeID: item.NodeID, NodeType: item.NodeType,
			Attempt: item.Attempt, Status: item.Status, Error: item.Error,
			Sequence: item.Sequence, StartedAt: item.StartedAt, FinishedAt: item.FinishedAt,
		}
	}
	return out
}
