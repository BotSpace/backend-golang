package config

import (
	"encoding/json"
	"time"
)

// ConfigResponse is the public projection of a config entry.
type ConfigResponse struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
}

func toResponse(e *Entry) ConfigResponse {
	var updatedAt *time.Time
	if !e.UpdatedAt.IsZero() {
		updatedAt = &e.UpdatedAt
	}
	return ConfigResponse{Key: e.Key, Value: e.Value, UpdatedAt: updatedAt}
}

// UpdateConfigRequest is the PUT body: an arbitrary JSON object stored as-is.
type UpdateConfigRequest = json.RawMessage
