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
	// ResolveProjectRef is exported so other redisq event handlers wired in
	// server.go (bot-user upsert, token usage, …) can resolve the same
	// engine-supplied project reference without duplicating this logic.
	ResolveProjectRef(ctx context.Context, raw string) (uuid.UUID, error)
}

type usecase struct {
	repo  Repository
	cache ContextCache // nil-able — see RecordContext
}

func NewUsecase(repo Repository) Usecase { return &usecase{repo: repo} }

// NewUsecaseWithContextCache is NewUsecase plus a ContextCache for
// "debug.context_event" — pass nil cache to keep the no-Redis degrade
// behavior of NewUsecase.
func NewUsecaseWithContextCache(repo Repository, cache ContextCache) Usecase {
	return &usecase{repo: repo, cache: cache}
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
	if err := u.cache.SetContext(ctx, projectID, in.ChatID, in.Variables, contextEventTTL); err != nil {
		return httpx.Internal(err)
	}
	return nil
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

func mapNotFound(err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("project.not_found", "project not found")
	}
	return httpx.Internal(err)
}
