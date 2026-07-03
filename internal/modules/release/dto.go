package release

import (
	"encoding/json"
	"time"

	"github.com/botspace/backend/internal/shared/engine"
)

// LogSnapshotResponse is the wire shape for GET /relase/{id}/logs and
// GET /projects/{id}/debug/logs — matches Django's deployment_logs_snapshot()
// dict exactly (pod_name/lines/truncated) so agentv3's backend.py
// get_release_logs/get_debug_logs (both call _unwrap_item, which accepts a
// flat object with no envelope) parse it unchanged.
type LogSnapshotResponse struct {
	PodName   *string  `json:"pod_name"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

func toLogSnapshotResponse(s engine.LogSnapshot) LogSnapshotResponse {
	lines := s.Lines
	if lines == nil {
		lines = []string{}
	}
	var podName *string
	if s.PodName != "" {
		podName = &s.PodName
	}
	return LogSnapshotResponse{PodName: podName, Lines: lines, Truncated: s.Truncated}
}

// maskedValue mirrors Django's ListEnviromentSerializer.MASKED_VALUE — secret
// environment values are never sent back to the client as plaintext.
const maskedValue = "••••"

// ---- Release --------------------------------------------------------------

type ReleaseResponse struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Version   int32           `json:"version"`
	Status    string          `json:"status"`
	ServiceID string          `json:"service_id,omitempty"`
	Flow      json.RawMessage `json:"flow,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func toReleaseResponse(r *Release) ReleaseResponse {
	return ReleaseResponse{
		ID:        r.ID.String(),
		ProjectID: r.ProjectID.String(),
		Version:   r.Version,
		Status:    string(r.Status),
		ServiceID: r.ServiceID,
		Flow:      r.Flow,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func toReleaseListResponse(items []Release) []ReleaseResponse {
	out := make([]ReleaseResponse, len(items))
	for i := range items {
		out[i] = toReleaseResponse(&items[i])
	}
	return out
}

// CreateReleaseRequest carries the target project — mirrors Django's
// CreateRelaseSerializer (the legacy plan_id/months fields are dropped:
// project plans are no longer used, see relase.py's validate()).
type CreateReleaseRequest struct {
	ProjectID string `json:"project_id" validate:"required,uuid"`
}

// ---- Debug ------------------------------------------------------------------

type DebugResponse struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	FlowHash  string    `json:"flow_hash,omitempty"`
	ServiceID string    `json:"service_id,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toDebugResponse(d *Debug) DebugResponse {
	return DebugResponse{
		ID:        d.ID.String(),
		ProjectID: d.ProjectID.String(),
		FlowHash:  d.FlowHash,
		ServiceID: d.ServiceID,
		Status:    string(d.Status),
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

// ---- Environment ------------------------------------------------------------

type EnvironmentResponse struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Secret    bool      `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// toEnvironmentResponse masks the value when Secret is set — matches
// ListEnviromentSerializer.get_value (Django never returns a secret's
// plaintext on read, only on the write that set it).
func toEnvironmentResponse(e *Environment) EnvironmentResponse {
	value := e.Value
	if e.Secret {
		value = maskedValue
	}
	return EnvironmentResponse{
		ID:        e.ID.String(),
		Key:       e.Key,
		Value:     value,
		Secret:    e.Secret,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

func toEnvironmentListResponse(items []Environment) []EnvironmentResponse {
	out := make([]EnvironmentResponse, len(items))
	for i := range items {
		out[i] = toEnvironmentResponse(&items[i])
	}
	return out
}

// UpsertEnvironmentRequest ports CreateEnviromentSerializer — a second POST
// with the same key+project overwrites the value (perform_create's
// update_or_create), it is never rejected as a duplicate.
type UpsertEnvironmentRequest struct {
	ProjectID string `json:"project_id" validate:"required,uuid"`
	Key       string `json:"key" validate:"required,min=1,max=255"`
	Value     string `json:"value" validate:"required"`
	Secret    bool   `json:"secret"`
}
