package project

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Project is a bot project owned by a user. Flow is the constructor graph,
// carried as raw JSON so this layer never needs to understand its shape.
type Project struct {
	ID                 uuid.UUID
	UUID               uuid.UUID
	Name               string
	Description        string
	Username           string
	OwnerID            uuid.UUID
	Flow               json.RawMessage
	IsTemplate         bool
	ReleaseWebhookUUID uuid.UUID
	DebugWebhookUUID   uuid.UUID
	CreatedAt          time.Time
	UpdatedAt          time.Time

	// LegacyID is the Django project table's integer primary key, nullable
	// (NULL means a project created after the Go cutover with no Django
	// counterpart — see migration 000033's doc comment). Used by
	// IssueCollabToken to pick the "project_<id>" collab room the same way
	// the engine/collab_persist_worker's Django int pk always has —
	// int(payload["project_id"]) in core/apps/api/management/commands/
	// collab_persist_worker.py hard-requires an integer, so a project
	// without a legacy_id falls back to its own uuid string (live relay
	// still works; only the Postgres flow-persistence side then no-ops).
	LegacyID *int32

	// Debug is only populated by Get (single retrieve) — mirrors Django's
	// RetrieveProjectSerializer, which nests RetrieveDebugSerializer, vs
	// List/Create/Update which don't. The dashboard's launch-status poll
	// reads project.debug.status directly (use-launch-polling.ts /
	// actions.ts getBotLaunchStatusAction), since tokenless debug-mode bots
	// never create a Release row — nil here means "no debug container ever
	// started for this project".
	Debug *ProjectDebug

	// BotMode/BotUsername/BotLink are only populated by Get (and therefore
	// CreateFromTemplate, which re-fetches via Get) — same "single-fetch
	// only" convention as Debug above. Mirrors Django's create/
	// create_from_template bot_link addition (core/apps/api/views/
	// project.py:210-216, 571-575), extended here to the plain GET so the
	// dashboard's launch wizard can read project.bot_link after a
	// StartDebug/CreateRelease call without re-parsing the create response.
	// Empty when no resolver is wired (see usecase.populateBotLink) or when
	// the project has neither a RELEASE_BOT_TOKEN nor a configured test bot.
	BotMode     string // "release" or "debug"
	BotUsername string
	BotLink     string
}

// ProjectDebug is the minimal debug-container projection embedded in
// RetrieveProjectSerializer-equivalent responses.
type ProjectDebug struct {
	ID        uuid.UUID
	ServiceID string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateInput is the owner-scoped create payload. Token is optional — mirrors
// Django's CreateProjectSerializer "token" field (project.py:202): when
// non-empty it is stored as the project's RELEASE_BOT_TOKEN environment
// variable (see bot.py:39, kubernetes_service.py:680-685) instead of being
// forced/required, so a tokenless create still succeeds (dashboard's
// bot_token_required contract only kicks in later, at release-deploy time).
type CreateInput struct {
	OwnerID     uuid.UUID
	Name        string
	Description string
	Username    string
	Flow        json.RawMessage
	IsTemplate  bool
	Token       string
	// ManagedBotID is the id (UUID string) of a manager-bot-wizard-created
	// bot to attach instead of a manually-entered Token — mirrors Django's
	// CreateProjectSerializer.managed_bot_id (ProjectView._attach_managed_bot).
	// Only consulted when Token is blank; resolved via the injected
	// ManagedBotTokenResolver (see usecase.resolveManagedBotToken).
	ManagedBotID string
}

// UpdateInput carries optional fields; nil pointers are left unchanged. A
// non-nil, non-empty Token (re)writes RELEASE_BOT_TOKEN, same as CreateInput.
type UpdateInput struct {
	Name        *string
	Description *string
	Username    *string
	Flow        json.RawMessage
	Token       *string
}

// ListFilter narrows and paginates an owner's project listing.
type ListFilter struct {
	OwnerID uuid.UUID
	Search  string
	Limit   int32
	Offset  int32
}

// AdminListFilter narrows and paginates the admin-wide (all-owners) project
// listing.
type AdminListFilter struct {
	Search string
	Limit  int32
	Offset int32
}

// AdminProjectSummary is a project row enriched with owner phone and release
// info for the admin-wide project listing (unlike Project, it is never
// owner-scoped).
type AdminProjectSummary struct {
	Project
	OwnerPhone    string
	ReleasesCount int64
	ReleaseStatus string
}

// AdminRelease is the compact release projection shown on the admin project
// detail page.
type AdminRelease struct {
	ID        uuid.UUID
	Version   int32
	Status    string
	ServiceID string
	CreatedAt time.Time
}

// AdminProjectDetail is the admin-wide project detail: the project row plus
// owner contact info and its full release history.
type AdminProjectDetail struct {
	Project
	OwnerPhone string
	OwnerEmail string
	Releases   []AdminRelease
}

// MonitoringProject is one row of the admin monitoring page: a project plus
// its latest reported resource usage. CPUCores/MemoryBytes are 0 when the
// bot has never reported (no resource row yet).
type MonitoringProject struct {
	Project
	OwnerPhone    string
	ReleaseStatus string
	CPUCores      int32
	MemoryBytes   int32
	DiskBytes     int64
}

type LiveMetrics struct {
	CPUMilli     int32
	MemoryBytes  int32
	NetworkRXBps float64
	NetworkTXBps float64
}

type ProjectMonitoring struct {
	CPUCores            float64
	MemoryBytes         int64
	NetworkRXBps        float64
	NetworkTXBps        float64
	DiskBytes           int64
	DiskFilesBytes      int64
	DiskCMSBytes        int64
	PrometheusAvailable bool
}

type MonitoringPage struct {
	Items               []MonitoringProject
	Total               int64
	PrometheusAvailable bool
}

type MetricPoint struct {
	Timestamp int64
	Value     float64
}

type MonitoringHistory struct {
	CPU                 []MetricPoint
	Memory              []MetricPoint
	NetworkRX           []MetricPoint
	NetworkTX           []MetricPoint
	Disk                []MetricPoint
	PrometheusAvailable bool
}

// Repository is the persistence port; owner-scoped methods never return or
// mutate another user's projects. The Admin*/Monitoring methods are
// intentionally unscoped and only reachable behind an admin-only route.
type Repository interface {
	Create(ctx context.Context, in CreateInput) (*Project, error)
	GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Project, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error)
	SoftDelete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Project, error)
	Count(ctx context.Context, ownerID uuid.UUID, search string) (int64, error)

	ListAdmin(ctx context.Context, f AdminListFilter) ([]AdminProjectSummary, error)
	CountAdmin(ctx context.Context, search string) (int64, error)
	GetAdmin(ctx context.Context, id uuid.UUID) (*AdminProjectDetail, error)
	ListMonitoring(ctx context.Context, f AdminListFilter) ([]MonitoringProject, error)
	// DiskHistory returns disk-usage snapshots (disksnap worker's hourly rows)
	// since the given time, ascending — the source for the monitoring Disk chart
	// since Prometheus has no filesystem metric for the engine pods.
	DiskHistory(ctx context.Context, projectID uuid.UUID, since time.Time) ([]MetricPoint, error)

	// SetReleaseBotToken upserts (key=RELEASE_BOT_TOKEN, project_id) in the
	// shared enviroment table, owner-scoped. This is a SQL-only cross-module
	// write against the release module's table (no Go import of the release
	// package), the same convention internal/modules/administration and
	// internal/modules/user already use for cross-module reads. It reuses the
	// generated sqlcdb.Queries.UpsertEnviromentForOwner query that the
	// release module's Repository also calls.
	SetReleaseBotToken(ctx context.Context, projectID, ownerID uuid.UUID, token string) error

	// GetDebugForOwner and UpdateDebugStatus are the same cross-module
	// SQL-only convention: they reuse the release module's "debug" table via
	// the shared generated sqlcdb.Queries, with no Go import of the release
	// package. Returns (nil, nil) — not an error — when the project has no
	// debug row yet (never started).
	GetDebugForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (*ProjectDebug, error)
	UpdateDebugStatus(ctx context.Context, id uuid.UUID, serviceID, status string) (*ProjectDebug, error)

	// ActiveReleaseServiceIDsForOwner returns the service_id of every release
	// row that still has one, owner-scoped — same cross-module SQL-only
	// convention as GetDebugForOwner. Used by Delete to stop each release's
	// k8s deployment before the project's rows disappear behind deleted_at.
	ActiveReleaseServiceIDsForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]string, error)

	// GetReleaseBotToken returns the project's RELEASE_BOT_TOKEN environment
	// value (unscoped by owner — same convention as ListEnviromentsByProject
	// in the release module's envMap), or "" with a nil error when no such
	// env var is set yet. Used by populateBotLink to tell a "release" bot
	// (has its own token) from a tokenless "debug" one.
	GetReleaseBotToken(ctx context.Context, projectID uuid.UUID) (string, error)
}
