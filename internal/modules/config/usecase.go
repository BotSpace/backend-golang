package config

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// defaults seeds a sane response the first time a key is read before any
// admin has ever saved it (belt-and-braces on top of the migration seed —
// this module never 404s a well-known key).
var defaults = map[string]json.RawMessage{
	KeyGeneral: json.RawMessage(`{"site_name":"Botmother","support_email":"","support_phone":"","maintenance_mode":false,"default_language":"uz","commission_percent":5}`),
	KeyAI:      json.RawMessage(`{"provider":"anthropic","model":"claude-sonnet-4-5","api_key":"","max_tokens":4096,"temperature":0.7,"system_prompt_override":""}`),
}

// Usecase exposes get/set operations for the well-known config keys.
type Usecase interface {
	Get(ctx context.Context, key string) (*Entry, error)
	Set(ctx context.Context, key string, value json.RawMessage, updatedBy uuid.UUID) (*Entry, error)
	// SharedAIAgentConfig projects the "ai" entry into the flat "ai_agent.*"
	// shape GET /api/v1/shared/config/{public,internal}/ return — see
	// ../agent/src/agent/integrations/backend.py fetch_public_config /
	// fetch_internal_config.
	SharedAIAgentConfig(ctx context.Context, includeSecrets bool) (map[string]any, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

func IsKnownKey(key string) bool {
	switch key {
	case KeyGeneral, KeyAI:
		return true
	default:
		return false
	}
}

func (u *usecase) Get(ctx context.Context, key string) (*Entry, error) {
	if !IsKnownKey(key) {
		return nil, httpx.NotFound("config.unknown_key", "unknown config key")
	}
	e, err := u.repo.Get(ctx, key)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return &Entry{Key: key, Value: defaults[key]}, nil
		}
		return nil, httpx.Internal(err)
	}
	return e, nil
}

// aiAgentKeys are the fields the "ai" config entry may carry, projected into
// the flat "ai_agent.*" shape agentv3 (../agent/src/agent/integrations/backend.py
// fetch_public_config/fetch_internal_config) expects. Public gets only the
// non-secret subset; Internal gets everything (secrets included), gated by
// X-Internal-Token at the handler layer.
var (
	publicAIAgentKeys = []string{"provider", "model", "base_url", "region"}
	secretAIAgentKeys = []string{"api_key", "aws_access_key_id", "aws_secret_access_key"}
)

// SharedAIAgentConfig projects the "ai" config entry into the flat
// "ai_agent.*" map. includeSecrets=false yields the public-safe subset only.
func (u *usecase) SharedAIAgentConfig(ctx context.Context, includeSecrets bool) (map[string]any, error) {
	e, err := u.Get(ctx, KeyAI)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(e.Value, &raw); err != nil {
		return nil, httpx.Internal(err)
	}
	out := map[string]any{}
	keys := publicAIAgentKeys
	if includeSecrets {
		keys = append(append([]string{}, publicAIAgentKeys...), secretAIAgentKeys...)
	}
	for _, k := range keys {
		v, ok := raw[k]
		if !ok {
			v = ""
		}
		out["ai_agent."+k] = v
	}
	return out, nil
}

func (u *usecase) Set(ctx context.Context, key string, value json.RawMessage, updatedBy uuid.UUID) (*Entry, error) {
	if !IsKnownKey(key) {
		return nil, httpx.NotFound("config.unknown_key", "unknown config key")
	}
	if len(value) == 0 || !json.Valid(value) {
		return nil, httpx.BadRequest("config.invalid_value", "value must be a valid JSON object")
	}
	e, err := u.repo.Upsert(ctx, key, value, &updatedBy)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return e, nil
}
