package debug

import (
	"context"
	"errors"
	"strconv"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Usecase exposes the write side (called by the redisq consumer) and the
// owner-scoped read side (called by the HTTP handler).
type Usecase interface {
	// RecordError resolves in.ProjectRef and persists the event. Returns
	// httpx.NotFound-shaped error when the ref matches no project — the
	// redisq handler acks on that (a stale/bogus ref will never resolve on
	// retry either) but logs it.
	RecordError(ctx context.Context, in RecordErrorInput) error
	ListErrors(ctx context.Context, ownerID, projectID uuid.UUID, limit int32) ([]ErrorEvent, error)
	// ResolveProjectRef is exported so other redisq event handlers wired in
	// server.go (bot-user upsert, token usage, …) can resolve the same
	// engine-supplied project reference without duplicating this logic.
	ResolveProjectRef(ctx context.Context, raw string) (uuid.UUID, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase { return &usecase{repo: repo} }

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
