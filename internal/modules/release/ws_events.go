package release

import (
	"context"
	"time"

	"github.com/botspace/backend/internal/shared/wsevents"
	"github.com/google/uuid"
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

// projectRoomRef returns the WS room key for a project, matching
// IssueCollabToken EXACTLY (legacy int if the project has one, else its uuid) —
// the constructor joins the room named by its collab-token's project_id claim,
// so the publish room MUST derive it the same way or the event never arrives.
func projectRoomRef(legacyID *int32, id uuid.UUID) any {
	if legacyID != nil {
		return *legacyID
	}
	return id.String()
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
	legacyID, _ := u.repo.ProjectLegacyID(ctx, rel.ProjectID)
	u.pub.Publish(ctx, wsevents.ProjectRoom(projectRoomRef(legacyID, rel.ProjectID)), wsevents.EventReleaseStatus, releaseStatusPayload{
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
	legacyID, _ := u.repo.ProjectLegacyID(ctx, dbg.ProjectID)
	var updatedAt *string
	if !dbg.UpdatedAt.IsZero() {
		s := dbg.UpdatedAt.UTC().Format(time.RFC3339)
		updatedAt = &s
	}
	u.pub.Publish(ctx, wsevents.ProjectRoom(projectRoomRef(legacyID, dbg.ProjectID)), wsevents.EventDebugStatus, debugStatusPayload{
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
