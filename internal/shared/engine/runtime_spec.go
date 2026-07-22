package engine

import (
	"encoding/json"
	"strings"
)

const (
	minRuntimeRoutePort = 10000
	maxRuntimeRoutePort = 59999
)

type storedRuntimeSpec struct {
	WebhookID string            `json:"webhook_id"`
	ProjectID string            `json:"project_id"`
	Name      string            `json:"name"`
	Debug     bool              `json:"debug"`
	RoutePort int               `json:"route_port"`
	Replicas  int               `json:"replicas,omitempty"`
	Flow      json.RawMessage   `json:"flow"`
	Schemas   json.RawMessage   `json:"schemas"`
	Modules   json.RawMessage   `json:"modules"`
	Env       map[string]string `json:"env"`
}

func normalizeJSONObject(raw []byte, fallback string) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '{' {
		return json.RawMessage(fallback)
	}
	return append(json.RawMessage(nil), raw...)
}
