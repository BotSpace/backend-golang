package engine

import (
	"encoding/json"
	"fmt"
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

func canonicalJSONObject(raw []byte, empty string, field string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return json.RawMessage(empty), nil
	}
	if !json.Valid(raw) || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s must be a JSON object", field)
	}
	return append(json.RawMessage(nil), raw...), nil
}
