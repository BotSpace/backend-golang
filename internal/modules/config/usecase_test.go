package config

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/google/uuid"
)

type fakeRepo struct {
	entries map[string]*Entry
}

func newFakeRepo() *fakeRepo { return &fakeRepo{entries: map[string]*Entry{}} }

func (r *fakeRepo) Get(_ context.Context, key string) (*Entry, error) {
	e, ok := r.entries[key]
	if !ok {
		return nil, database.ErrNotFound
	}
	return e, nil
}

func (r *fakeRepo) Upsert(_ context.Context, key string, value json.RawMessage, updatedBy *uuid.UUID) (*Entry, error) {
	e := &Entry{Key: key, Value: value, UpdatedBy: updatedBy}
	r.entries[key] = e
	return e, nil
}

func TestSharedAIAgentConfig_PublicExcludesSecrets(t *testing.T) {
	repo := newFakeRepo()
	repo.entries[KeyAI] = &Entry{Key: KeyAI, Value: json.RawMessage(`{
		"provider": "anthropic", "model": "claude-sonnet-4-5",
		"base_url": "https://api.anthropic.com", "region": "us-east-1",
		"api_key": "sk-secret", "aws_access_key_id": "AKIA...", "aws_secret_access_key": "shh"
	}`)}
	uc := NewUsecase(repo)

	out, err := uc.SharedAIAgentConfig(context.Background(), false)
	if err != nil {
		t.Fatalf("SharedAIAgentConfig: %v", err)
	}
	if out["ai_agent.provider"] != "anthropic" || out["ai_agent.model"] != "claude-sonnet-4-5" {
		t.Fatalf("expected provider/model projected, got %#v", out)
	}
	if out["ai_agent.base_url"] != "https://api.anthropic.com" || out["ai_agent.region"] != "us-east-1" {
		t.Fatalf("expected base_url/region projected, got %#v", out)
	}
	for _, secretKey := range []string{"ai_agent.api_key", "ai_agent.aws_access_key_id", "ai_agent.aws_secret_access_key"} {
		if _, present := out[secretKey]; present {
			t.Fatalf("public projection must not include %q, got %#v", secretKey, out)
		}
	}
}

func TestSharedAIAgentConfig_InternalIncludesSecrets(t *testing.T) {
	repo := newFakeRepo()
	repo.entries[KeyAI] = &Entry{Key: KeyAI, Value: json.RawMessage(`{
		"provider": "anthropic", "api_key": "sk-secret",
		"aws_access_key_id": "AKIA...", "aws_secret_access_key": "shh"
	}`)}
	uc := NewUsecase(repo)

	out, err := uc.SharedAIAgentConfig(context.Background(), true)
	if err != nil {
		t.Fatalf("SharedAIAgentConfig: %v", err)
	}
	if out["ai_agent.api_key"] != "sk-secret" {
		t.Fatalf("expected api_key in internal projection, got %#v", out)
	}
	if out["ai_agent.aws_access_key_id"] != "AKIA..." || out["ai_agent.aws_secret_access_key"] != "shh" {
		t.Fatalf("expected aws creds in internal projection, got %#v", out)
	}
}

func TestSharedAIAgentConfig_FallsBackToDefaultWhenUnset(t *testing.T) {
	// No admin has ever saved the "ai" key — Get() falls back to `defaults`
	// (see usecase.go), so the projection must still succeed with the seed
	// values rather than 404ing agentv3's boot-time config fetch.
	uc := NewUsecase(newFakeRepo())

	out, err := uc.SharedAIAgentConfig(context.Background(), false)
	if err != nil {
		t.Fatalf("SharedAIAgentConfig: %v", err)
	}
	if out["ai_agent.provider"] != "anthropic" {
		t.Fatalf("expected default provider, got %#v", out)
	}
}
