// Package config ports a small admin-editable platform settings store: a
// key/value JSONB table with two well-known keys today ("general" platform
// settings and "ai" model config). It has no Django equivalent — the old
// backend never exposed a generic settings surface for the new admin panel,
// so this module exists purely to unblock the constructor's settings pages.
package config

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Well-known config keys. The handler only allows GET/PUT on these — an
// arbitrary key is rejected with 400, keeping the surface small and
// intentional rather than a free-form KV store.
const (
	KeyGeneral = "general"
	KeyAI      = "ai"
)

// Entry is one row of the config table.
type Entry struct {
	Key       string
	Value     json.RawMessage
	UpdatedBy *uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Repository is the persistence port.
type Repository interface {
	Get(ctx context.Context, key string) (*Entry, error)
	Upsert(ctx context.Context, key string, value json.RawMessage, updatedBy *uuid.UUID) (*Entry, error)
}
