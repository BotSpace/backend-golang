// Package webhook ports two related Django domains that both live under
// core.apps.api: webhook-endpoint (a project-scoped URL bound to a flow
// node, resolved by the Go webhook-hook service) and managed-bot (Telegram
// bots created through the manager-bot wizard and handed to an owner).
package webhook

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// WebhookEndpoint is a project-scoped, per-node (optionally per-chat) URL.
// Minted once per (project, node_id, chat_id); a WebhookTrigger node (or a
// module requesting a webhook) reuses the same token forever. chat_id = 0
// means the URL is not bound to a specific chat.
type WebhookEndpoint struct {
	ID        uuid.UUID
	Token     uuid.UUID
	ProjectID uuid.UUID
	NodeID    string
	ChatID    int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MintInput is the owner-scoped get-or-create payload for a webhook endpoint.
type MintInput struct {
	OwnerID   uuid.UUID
	ProjectID uuid.UUID
	NodeID    string
	ChatID    int64
}

// ResolvedHook is the routing info returned to the Go webhook-hook forwarder:
// token -> flow node + the owning project's release/debug uuids (the engine
// instance is addressed by one of those, depending on release vs. debug).
type ResolvedHook struct {
	Token              uuid.UUID
	ProjectID          uuid.UUID
	NodeID             string
	ChatID             int64
	ReleaseWebhookUUID uuid.UUID
	DebugWebhookUUID   uuid.UUID
}

// ManagedBot is a Telegram bot created through the manager-bot wizard and
// held in trust for an owner until a project attaches it as its release bot
// token (see Used). OwnerID is nil until resolved by the claim flow.
type ManagedBot struct {
	ID             uuid.UUID
	OwnerID        *uuid.UUID
	TgBotID        int64
	Username       string
	Name           string
	TokenEncrypted string
	Used           bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UpsertManagedBotInput is the manager-bot-webhook-driven create/update
// payload, keyed by the Telegram bot id. OwnerID is written unconditionally
// (including nil), mirroring the Django update_or_create defaults.
type UpsertManagedBotInput struct {
	OwnerID        *uuid.UUID
	TgBotID        int64
	Username       string
	Name           string
	TokenEncrypted string
}

// Repository is the persistence port. Webhook endpoints and managed bots
// share a migration but are otherwise independent aggregates.
type Repository interface {
	// MintEndpoint is an owner-scoped get-or-create: it returns
	// database.ErrNotFound when the project does not exist, is soft-deleted,
	// or is not owned by ownerID (see queries/webhook.sql).
	MintEndpoint(ctx context.Context, in MintInput) (*WebhookEndpoint, error)
	// ResolveToken looks up routing info by token, unscoped by owner — the
	// caller (the internal handler) gates access via X-Internal-Token.
	ResolveToken(ctx context.Context, token uuid.UUID) (*ResolvedHook, error)

	ListManagedBotsForOwner(ctx context.Context, ownerID uuid.UUID) ([]ManagedBot, error)
	UpsertManagedBot(ctx context.Context, in UpsertManagedBotInput) (*ManagedBot, error)
	// GetManagedBotByIDForOwner returns database.ErrNotFound when the bot
	// does not exist or is not owned by ownerID — the caller (Usecase.
	// ResolveManagedBotToken) cannot and should not distinguish the two.
	GetManagedBotByIDForOwner(ctx context.Context, id, ownerID uuid.UUID) (*ManagedBot, error)
	// MarkManagedBotUsed flips the bot's used flag once its token has been
	// attached to a project's RELEASE_BOT_TOKEN.
	MarkManagedBotUsed(ctx context.Context, id uuid.UUID) error
}
