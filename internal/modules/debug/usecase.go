package debug

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// contextEventTTL matches Django's DebugContextEventView cache TTL — a
// "latest snapshot" cache, not a history log.
const contextEventTTL = 10 * time.Minute

// Usecase exposes the write side (called by the redisq consumer) and the
// owner-scoped read side (called by the HTTP handler).
type Usecase interface {
	// RecordError resolves in.ProjectRef and persists the event. Returns
	// httpx.NotFound-shaped error when the ref matches no project — the
	// redisq handler acks on that (a stale/bogus ref will never resolve on
	// retry either) but logs it.
	RecordError(ctx context.Context, in RecordErrorInput) error
	// RecordNodeEvent resolves in.ProjectRef and persists the per-node
	// status event ("debug.node_event"). Same not-found semantics as
	// RecordError.
	RecordNodeEvent(ctx context.Context, in RecordNodeEventInput) error
	// RecordContext resolves in.ProjectRef and caches the latest variable
	// snapshot ("debug.context_event"). No-ops (returns nil) when this
	// usecase was built without a ContextCache (redis unavailable) — same
	// degrade-gracefully-without-Redis pattern used elsewhere in this repo.
	RecordContext(ctx context.Context, in RecordContextEventInput) error
	ListErrors(ctx context.Context, ownerID, projectID uuid.UUID, limit int32) ([]ErrorEvent, error)
	// ListContext is the owner-scoped read side of "debug.context_event" —
	// GET /api/v1/debug/context/?project_id=. Returns an empty (nil) slice,
	// not an error, when this usecase was built without a ContextCache (no
	// Redis wired) — same degrade-gracefully convention as RecordContext.
	ListContext(ctx context.Context, ownerID, projectID uuid.UUID) ([]ContextSnapshot, error)
	RunNode(ctx context.Context, ownerID, projectID uuid.UUID, nodeID string) (*RunNodeResult, error)
	// ResolveProjectRef is exported so other redisq event handlers wired in
	// server.go (bot-user upsert, token usage, …) can resolve the same
	// engine-supplied project reference without duplicating this logic.
	ResolveProjectRef(ctx context.Context, raw string) (uuid.UUID, error)
	// GetProjectOwner exposes Repository.GetProjectOwner so callers outside
	// this module — server.go's redisq consumer, resolving the WS
	// "user_<owner-id>" room to forward node_execution/context/error events
	// to the constructor's ws-hub — can look up a project's owner without
	// reaching into the repository layer directly.
	GetProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error)
}

type usecase struct {
	repo     Repository
	cache    ContextCache // nil-able — see RecordContext
	commands CommandQueue // nil-able — exact-node execution requires Redis
}

func NewUsecase(repo Repository) Usecase { return &usecase{repo: repo} }

// NewUsecaseWithContextCache is NewUsecase plus a ContextCache for
// "debug.context_event" — pass nil cache to keep the no-Redis degrade
// behavior of NewUsecase.
func NewUsecaseWithContextCache(repo Repository, cache ContextCache) Usecase {
	return &usecase{repo: repo, cache: cache}
}

func NewUsecaseWithDebugCommands(repo Repository, cache ContextCache, commands CommandQueue) Usecase {
	return &usecase{repo: repo, cache: cache, commands: commands}
}

func (u *usecase) RecordError(ctx context.Context, in RecordErrorInput) error {
	projectID, err := u.ResolveProjectRef(ctx, in.ProjectRef)
	if err != nil {
		return err
	}
	level := in.Level
	if level == "" {
		level = "error"
	}
	_, err = u.repo.InsertErrorEvent(ctx, ErrorEvent{
		ProjectID: projectID,
		ChatID:    in.ChatID,
		NodeID:    in.NodeID,
		NodeType:  in.NodeType,
		Level:     level,
		Message:   in.Message,
		Detail:    in.Detail,
		EventTS:   in.EventTS,
	})
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) RecordNodeEvent(ctx context.Context, in RecordNodeEventInput) error {
	projectID, err := u.ResolveProjectRef(ctx, in.ProjectRef)
	if err != nil {
		return err
	}
	_, err = u.repo.InsertNodeEvent(ctx, NodeEvent{
		ProjectID: projectID,
		ChatID:    in.ChatID,
		NodeID:    in.NodeID,
		NodeType:  in.NodeType,
		Status:    in.Status,
		Error:     in.Error,
		RunID:     in.RunID,
		Seq:       in.Seq,
		EventTS:   in.EventTS,
	})
	if err != nil {
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) RecordContext(ctx context.Context, in RecordContextEventInput) error {
	if u.cache == nil {
		return nil
	}
	projectID, err := u.ResolveProjectRef(ctx, in.ProjectRef)
	if err != nil {
		return err
	}
	var ts *int64
	if in.EventTS != nil {
		v := in.EventTS.Unix()
		ts = &v
	}
	if err := u.cache.SetContext(ctx, projectID, in.ChatID, in.Variables, ts, contextEventTTL); err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// ListContext owner-scopes the Redis-only context cache: unlike
// debug_error_event (a real Postgres table joinable to projects.owner_id in
// one query, see ListErrorEventsForOwner), the cache has no owner column —
// so ownership is checked explicitly via GetProjectOwner before reading it.
// Mirrors Django's DebugContextListView 403 ("Ruxsat yo'q") for a
// non-owning caller and 404 for an unknown project.
func (u *usecase) ListContext(ctx context.Context, ownerID, projectID uuid.UUID) ([]ContextSnapshot, error) {
	if u.cache == nil {
		return nil, nil
	}
	actualOwner, err := u.repo.GetProjectOwner(ctx, projectID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("project.not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	if actualOwner != ownerID {
		return nil, httpx.Forbidden("project.forbidden", "not the project owner")
	}
	items, err := u.cache.ListContext(ctx, projectID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return items, nil
}

// RunNode queues execution for the most recently active debug chat. The
// engine reads its own full Redis state for that chat; the context cache is
// used only to choose which real user conversation is the latest one.
func (u *usecase) RunNode(ctx context.Context, ownerID, projectID uuid.UUID, nodeID string) (*RunNodeResult, error) {
	if nodeID == "" || len(nodeID) > 255 {
		return nil, httpx.BadRequest("debug.invalid_node_id", "node_id is required")
	}
	if u.cache == nil || u.commands == nil {
		return nil, httpx.Conflict("debug.runtime_unavailable", "debug runtime is unavailable")
	}
	items, err := u.ListContext(ctx, ownerID, projectID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, httpx.Conflict(
			"debug.context_missing",
			"debug botda hali user context yo'q; avval botga xabar yuboring",
		)
	}
	chatID := items[0].ChatID
	if err := u.commands.EnqueueNodeRun(ctx, projectID, chatID, nodeID); err != nil {
		return nil, httpx.Internal(err)
	}
	return &RunNodeResult{NodeID: nodeID, ChatID: chatID, Queued: true}, nil
}

// ResolveProjectRef mirrors Django's cms/views/_common.py resolve_project():
// try the raw value as a Django legacy int pk first (this backend's
// projects.legacy_id), then as a UUID matched against projects.uuid (NOT
// projects.id — see migration 000033/000034 doc comments for why those
// differ). Exported so the redisq wiring in server.go can resolve
// project_id for other future event types too.
func (u *usecase) ResolveProjectRef(ctx context.Context, raw string) (uuid.UUID, error) {
	if n, err := strconv.ParseInt(raw, 10, 32); err == nil {
		id, err := u.repo.ResolveProjectByLegacyID(ctx, int32(n))
		if err != nil {
			return uuid.Nil, mapNotFound(err)
		}
		return id, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httpx.BadRequest("debug.invalid_project_ref", "project_id must be an integer pk or a uuid")
	}
	id, err := u.repo.ResolveProjectByUUID(ctx, parsed)
	if err != nil {
		return uuid.Nil, mapNotFound(err)
	}
	return id, nil
}

func (u *usecase) ListErrors(ctx context.Context, ownerID, projectID uuid.UUID, limit int32) ([]ErrorEvent, error) {
	items, err := u.repo.ListErrorEventsForOwner(ctx, ownerID, projectID, limit)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return items, nil
}

// GetProjectOwner passes straight through to the repository — see the
// Usecase.GetProjectOwner doc comment for why this exists (non-HTTP redisq
// callers want the raw database.ErrNotFound, not an httpx-mapped error, so
// they can decide for themselves whether to skip a best-effort WS publish).
func (u *usecase) GetProjectOwner(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	return u.repo.GetProjectOwner(ctx, projectID)
}

func mapNotFound(err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("project.not_found", "project not found")
	}
	return httpx.Internal(err)
}
