package release

import (
	"context"
	"time"

	"github.com/botspace/backend/internal/shared/wsevents"
)

// releaseStatusPayload mirrors Django's ReleaseContainerStatus. `project` is
// the project's UUID string (NOT the legacy int): the constructor filters
// events with `String(data.project) === String(project.id)` and its
// `project.id` from the Go API is the uuid, so a legacy int here would make
// every event get silently dropped client-side.
type releaseStatusPayload struct {
	Project   string  `json:"project"`
	Status    string  `json:"status"`
	Version   int32   `json:"version"`
	ServiceID *string `json:"service_id"`
}

// debugStatusPayload mirrors Django's DebugContainerStatus (project = uuid, see
// releaseStatusPayload).
type debugStatusPayload struct {
	Project   string  `json:"project"`
	Status    string  `json:"status"`
	ServiceID *string `json:"service_id"`
	UpdatedAt *string `json:"updated_at"`
}

// publishReleaseStatus best-effort broadcasts a release status change to the
// constructor's ws-hub via its "user_<owner-id>" WS room (see
// wsevents.UserRoom — the ws-hub's plain access token has no project_id
// claim, so it always sits in a user room, never a project one). Silently
// skipped when no publisher is wired or the owner lookup fails — mirrors
// Django's _send_ws_notification wrapping the whole call in a bare
// try/except.
func (u *usecase) publishReleaseStatus(ctx context.Context, rel *Release) {
	if u.pub == nil || rel == nil {
		return
	}
	ownerID, err := u.repo.ProjectOwnerID(ctx, rel.ProjectID)
	if err != nil {
		return
	}
	u.pub.Publish(ctx, wsevents.UserRoom(ownerID), wsevents.EventReleaseStatus, releaseStatusPayload{
		Project:   rel.ProjectID.String(),
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
	ownerID, err := u.repo.ProjectOwnerID(ctx, dbg.ProjectID)
	if err != nil {
		return
	}
	var updatedAt *string
	if !dbg.UpdatedAt.IsZero() {
		s := dbg.UpdatedAt.UTC().Format(time.RFC3339)
		updatedAt = &s
	}
	u.pub.Publish(ctx, wsevents.UserRoom(ownerID), wsevents.EventDebugStatus, debugStatusPayload{
		Project:   dbg.ProjectID.String(),
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
