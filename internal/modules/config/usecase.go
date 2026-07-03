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
