package release

import (
	"context"
	"time"

	"github.com/botspace/backend/internal/shared/wsevents"
)

// releaseStatusPayload mirrors Django's ReleaseContainerStatus pydantic model
// (core/domain/ws.py) EXACTLY — field names/shape must match, since the
// unchanged websocket service and constructor already know this schema.
type releaseStatusPayload struct {
	Project   int32   `json:"project"`
	Status    string  `json:"status"`
	Version   int32   `json:"version"`
	ServiceID *string `json:"service_id"`
}

// debugStatusPayload mirrors Django's DebugContainerStatus pydantic model
// (core/domain/ws.py) EXACTLY.
type debugStatusPayload struct {
	Project   int32   `json:"project"`
	Status    string  `json:"status"`
	ServiceID *string `json:"service_id"`
	UpdatedAt *string `json:"updated_at"`
}

// publishReleaseStatus best-effort broadcasts a release status change to the
// constructor's "project_<legacy-id>" WS room. Silently skipped when no
// publisher is wired, the project has no legacy id yet, or the legacy-id
// lookup fails — mirrors Django's _send_ws_notification wrapping the whole
// call in a bare try/except.
func (u *usecase) publishReleaseStatus(ctx context.Context, rel *Release) {
	if u.pub == nil || rel == nil {
		return
	}
	legacyID, err := u.repo.ProjectLegacyID(ctx, rel.ProjectID)
	if err != nil || legacyID == nil {
		return
	}
	u.pub.Publish(ctx, wsevents.ProjectRoom(*legacyID), wsevents.EventReleaseStatus, releaseStatusPayload{
		Project:   *legacyID,
		Status:    string(rel.Status),
		Version:   rel.Version,
		ServiceID: optionalString(rel.ServiceID),
	})
}

// publishDebugStatus is publishReleaseStatus's Debug counterpart.
func (u *usecase) publishDebugStatus(ctx context.Context, dbg *Debug) {
	if u.pub == nil || dbg == nil {
		return
	}
	legacyID, err := u.repo.ProjectLegacyID(ctx, dbg.ProjectID)
	if err != nil || legacyID == nil {
		return
	}
	var updatedAt *string
	if !dbg.UpdatedAt.IsZero() {
		s := dbg.UpdatedAt.UTC().Format(time.RFC3339)
		updatedAt = &s
	}
	u.pub.Publish(ctx, wsevents.ProjectRoom(*legacyID), wsevents.EventDebugStatus, debugStatusPayload{
		Project:   *legacyID,
		Status:    string(dbg.Status),
		ServiceID: optionalString(dbg.ServiceID),
		UpdatedAt: updatedAt,
	})
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
