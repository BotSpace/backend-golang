package project

import (
	"encoding/json"
	"time"
)

// CollabTokenResponse mirrors Django's collab_token action response exactly
// ({"token": "...", "room": "project_<id>"}) — see collab_token.go.
type CollabTokenResponse struct {
	Token string `json:"token"`
	Room  string `json:"room"`
}

// ProjectResponse is the public projection of a project.
type ProjectResponse struct {
	ID                 string          `json:"id"`
	UUID               string          `json:"uuid"`
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	Username           string          `json:"username,omitempty"`
	OwnerID            string          `json:"owner_id"`
	Flow               json.RawMessage `json:"flow"`
	IsTemplate         bool            `json:"is_template"`
	ReleaseWebhookUUID string          `json:"release_webhook_uuid"`
	DebugWebhookUUID   string          `json:"debug_webhook_uuid"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	// Debug is only set on the single-project fetch (Usecase.Get) — mirrors
	// Django's RetrieveProjectSerializer nesting RetrieveDebugSerializer. The
	// dashboard's launch-status poll reads project.debug.status directly.
	Debug *DebugResponse `json:"debug,omitempty"`
	// BotMode/BotUsername/BotLink are only set on the single-project fetch
	// (Usecase.Get, and therefore CreateFromTemplate's response, which
	// re-fetches via Get) — see Project.BotMode's doc comment. BotLink is
	// what the dashboard's launch wizard shows as "Botga kirish": for a
	// debug (tokenless) project it already carries the required
	// ?start=<debug_webhook_uuid> deep-link, since the shared test bot can't
	// route a bare t.me link to one specific project.
	BotMode     string `json:"bot_mode,omitempty"`
	BotUsername string `json:"bot_username,omitempty"`
	BotLink     string `json:"bot_link,omitempty"`
}

// DebugResponse is the debug-container projection nested in ProjectResponse.
type DebugResponse struct {
	ID        string    `json:"id"`
	ServiceID string    `json:"service_id,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toResponse(p *Project) ProjectResponse {
	flow := p.Flow
	if len(flow) == 0 {
		flow = json.RawMessage("[]")
	}
	resp := ProjectResponse{
		ID:                 p.ID.String(),
		UUID:               p.UUID.String(),
		Name:               p.Name,
		Description:        p.Description,
		Username:           p.Username,
		OwnerID:            p.OwnerID.String(),
		Flow:               flow,
		IsTemplate:         p.IsTemplate,
		ReleaseWebhookUUID: p.ReleaseWebhookUUID.String(),
		DebugWebhookUUID:   p.DebugWebhookUUID.String(),
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
		BotMode:            p.BotMode,
		BotUsername:        p.BotUsername,
		BotLink:            p.BotLink,
	}
	if p.Debug != nil {
		resp.Debug = &DebugResponse{
			ID:        p.Debug.ID.String(),
			ServiceID: p.Debug.ServiceID,
			Status:    p.Debug.Status,
			CreatedAt: p.Debug.CreatedAt,
			UpdatedAt: p.Debug.UpdatedAt,
		}
	}
	return resp
}

type CreateRequest struct {
	Name        string          `json:"name" validate:"required,min=1,max=255"`
	Description string          `json:"description" validate:"omitempty,max=5000"`
	Username    string          `json:"username" validate:"omitempty,max=255"`
	Flow        json.RawMessage `json:"flow" validate:"omitempty"`
	IsTemplate  bool            `json:"is_template"`
	// Token is the Telegram bot token (optional — mirrors Django's
	// CreateProjectSerializer.token). Blank is fine: the dashboard falls back
	// to its bot_token_required flow on the first /relase call.
	Token string `json:"token" validate:"omitempty,max=255"`
	// ManagedBotID is an alternative to Token: a bot created through the
	// manager-bot wizard (mirrors Django's CreateProjectSerializer.
	// managed_bot_id). Ignored when Token is also given.
	ManagedBotID string `json:"managed_bot_id" validate:"omitempty"`
}

// CreateFromTemplateRequest is the body for
// POST /projects/create-from-template — mirrors Django's
// CreateProjectFromTemplate serializer (core/apps/api/serializers/project/
// project.py) and the dashboard's ProjectsAPI.createFromTemplate payload
// shape (services.ts). ManagedBotID is an alternative to Token (mode
// "release" requires one of the two — see Handler.CreateFromTemplate).
// Credentials is accepted (so a client sending the full dashboard body
// doesn't get a 400 for unknown fields) but not wired yet — see
// project.Usecase.CreateFromTemplate's doc comment for why.
type CreateFromTemplateRequest struct {
	// Template is the template's id (UUID) or slug.
	Template     string            `json:"template" validate:"required"`
	Name         string            `json:"name" validate:"required,min=1,max=255"`
	Token        string            `json:"token" validate:"omitempty,max=255"`
	ManagedBotID string            `json:"managed_bot_id" validate:"omitempty"`
	Mode         string            `json:"mode" validate:"omitempty,oneof=release debug"`
	Envs         map[string]string `json:"envs"`
	Credentials  map[string]any    `json:"credentials"`
}

type UpdateRequest struct {
	Name         *string         `json:"name" validate:"omitempty,min=1,max=255"`
	Description  *string         `json:"description" validate:"omitempty,max=5000"`
	Username     *string         `json:"username" validate:"omitempty,max=255"`
	Flow         json.RawMessage `json:"flow" validate:"omitempty"`
	Token        *string         `json:"token" validate:"omitempty,max=255"`
	ManagedBotID *string         `json:"managed_bot_id" validate:"omitempty"`
}

// ListResponse is a paginated envelope for a project listing.
type ListResponse struct {
	Items  []ProjectResponse `json:"items"`
	Total  int64             `json:"total"`
	Limit  int32             `json:"limit"`
	Offset int32             `json:"offset"`
}

func toListResponse(items []Project, total int64, limit, offset int32) ListResponse {
	out := make([]ProjectResponse, len(items))
	for i := range items {
		out[i] = toResponse(&items[i])
	}
	return ListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// ===================== admin-wide (cross-owner) =====================

// AdminProjectResponse is the admin-wide project projection: the same shape
// as ProjectResponse plus owner contact info and release status, but never
// exposes the (potentially large) flow JSON — the admin listing/detail pages
// only need metadata.
type AdminProjectResponse struct {
	ID                 string    `json:"id"`
	UUID               string    `json:"uuid"`
	Name               string    `json:"name"`
	Description        string    `json:"description,omitempty"`
	Username           string    `json:"username,omitempty"`
	OwnerID            string    `json:"owner_id"`
	OwnerPhone         string    `json:"owner_phone,omitempty"`
	IsTemplate         bool      `json:"is_template"`
	ReleaseWebhookUUID string    `json:"release_webhook_uuid"`
	DebugWebhookUUID   string    `json:"debug_webhook_uuid"`
	ReleaseStatus      *string   `json:"release_status"`
	HasRelease         bool      `json:"has_release"`
	ReleasesCount      int64     `json:"releases_count"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func toAdminProjectResponse(p *AdminProjectSummary) AdminProjectResponse {
	var releaseStatus *string
	if p.ReleaseStatus != "" {
		releaseStatus = &p.ReleaseStatus
	}
	return AdminProjectResponse{
		ID:                 p.ID.String(),
		UUID:               p.UUID.String(),
		Name:               p.Name,
		Description:        p.Description,
		Username:           p.Username,
		OwnerID:            p.OwnerID.String(),
		OwnerPhone:         p.OwnerPhone,
		IsTemplate:         p.IsTemplate,
		ReleaseWebhookUUID: p.ReleaseWebhookUUID.String(),
		DebugWebhookUUID:   p.DebugWebhookUUID.String(),
		ReleaseStatus:      releaseStatus,
		HasRelease:         p.ReleasesCount > 0,
		ReleasesCount:      p.ReleasesCount,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

// AdminProjectListResponse is a paginated envelope for the admin-wide
// project listing.
type AdminProjectListResponse struct {
	Items  []AdminProjectResponse `json:"items"`
	Total  int64                  `json:"total"`
	Limit  int32                  `json:"limit"`
	Offset int32                  `json:"offset"`
}

func toAdminProjectListResponse(items []AdminProjectSummary, total int64, limit, offset int32) AdminProjectListResponse {
	out := make([]AdminProjectResponse, len(items))
	for i := range items {
		out[i] = toAdminProjectResponse(&items[i])
	}
	return AdminProjectListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// AdminProjectOwnerResponse is the compact owner projection embedded in the
// admin project detail response.
type AdminProjectOwnerResponse struct {
	ID    string `json:"id"`
	Phone string `json:"phone"`
	Email string `json:"email,omitempty"`
}

// AdminProjectReleaseResponse is the compact release projection shown on the
// admin project detail page.
type AdminProjectReleaseResponse struct {
	ID        string    `json:"id"`
	Version   int32     `json:"version"`
	Status    string    `json:"status"`
	ServiceID string    `json:"service_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AdminProjectDetailResponse is the admin-wide project detail projection.
type AdminProjectDetailResponse struct {
	ID                 string                        `json:"id"`
	UUID               string                        `json:"uuid"`
	Name               string                        `json:"name"`
	Description        string                        `json:"description,omitempty"`
	Username           string                        `json:"username,omitempty"`
	OwnerID            string                        `json:"owner_id"`
	Owner              AdminProjectOwnerResponse     `json:"owner"`
	IsTemplate         bool                          `json:"is_template"`
	ReleaseWebhookUUID string                        `json:"release_webhook_uuid"`
	DebugWebhookUUID   string                        `json:"debug_webhook_uuid"`
	Releases           []AdminProjectReleaseResponse `json:"releases"`
	ReleasesCount      int64                         `json:"releases_count"`
	CreatedAt          time.Time                     `json:"created_at"`
	UpdatedAt          time.Time                     `json:"updated_at"`
}

func toAdminProjectDetailResponse(p *AdminProjectDetail) AdminProjectDetailResponse {
	releases := make([]AdminProjectReleaseResponse, len(p.Releases))
	for i, r := range p.Releases {
		releases[i] = AdminProjectReleaseResponse{
			ID: r.ID.String(), Version: r.Version, Status: r.Status,
			ServiceID: r.ServiceID, CreatedAt: r.CreatedAt,
		}
	}
	return AdminProjectDetailResponse{
		ID:                 p.ID.String(),
		UUID:               p.UUID.String(),
		Name:               p.Name,
		Description:        p.Description,
		Username:           p.Username,
		OwnerID:            p.OwnerID.String(),
		Owner:              AdminProjectOwnerResponse{ID: p.OwnerID.String(), Phone: p.OwnerPhone, Email: p.OwnerEmail},
		IsTemplate:         p.IsTemplate,
		ReleaseWebhookUUID: p.ReleaseWebhookUUID.String(),
		DebugWebhookUUID:   p.DebugWebhookUUID.String(),
		Releases:           releases,
		ReleasesCount:      int64(len(releases)),
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

// ===================== monitoring =====================

type ProjectMonitoringResponse struct {
	CPUCores            float64 `json:"cpu_cores"`
	MemoryBytes         int64   `json:"memory_bytes"`
	NetRXBps            float64 `json:"net_rx_bps"`
	NetTXBps            float64 `json:"net_tx_bps"`
	DiskBytes           int64   `json:"disk_bytes"`
	DiskFilesBytes      int64   `json:"disk_files_bytes"`
	DiskCMSBytes        int64   `json:"disk_cms_bytes"`
	PrometheusAvailable bool    `json:"prometheus_available"`
}

func toProjectMonitoringResponse(m *ProjectMonitoring) ProjectMonitoringResponse {
	if m == nil {
		return ProjectMonitoringResponse{}
	}
	return ProjectMonitoringResponse{
		CPUCores:            m.CPUCores,
		MemoryBytes:         m.MemoryBytes,
		NetRXBps:            m.NetworkRXBps,
		NetTXBps:            m.NetworkTXBps,
		DiskBytes:           m.DiskBytes,
		DiskFilesBytes:      m.DiskFilesBytes,
		DiskCMSBytes:        m.DiskCMSBytes,
		PrometheusAvailable: m.PrometheusAvailable,
	}
}

// MonitoringProjectResponse is one row of the admin monitoring page.
type MonitoringProjectResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Username      string    `json:"username,omitempty"`
	OwnerID       string    `json:"owner_id"`
	OwnerPhone    string    `json:"owner_phone,omitempty"`
	ReleaseStatus *string   `json:"release_status"`
	CPUCores      int32     `json:"cpu_cores"`
	MemoryBytes   int32     `json:"memory_bytes"`
	DiskBytes     int64     `json:"disk_bytes"`
	NetBytes      int64     `json:"net_bytes"`
	IsTemplate    bool      `json:"is_template"`
	CreatedAt     time.Time `json:"created_at"`
}

func toMonitoringProjectResponse(p *MonitoringProject) MonitoringProjectResponse {
	var releaseStatus *string
	if p.ReleaseStatus != "" {
		releaseStatus = &p.ReleaseStatus
	}
	return MonitoringProjectResponse{
		ID:            p.ID.String(),
		Name:          p.Name,
		Username:      p.Username,
		OwnerID:       p.OwnerID.String(),
		OwnerPhone:    p.OwnerPhone,
		ReleaseStatus: releaseStatus,
		CPUCores:      p.CPUCores,
		MemoryBytes:   p.MemoryBytes,
		DiskBytes:     p.DiskBytes,
		NetBytes:      p.NetBytes,
		IsTemplate:    p.IsTemplate,
		CreatedAt:     p.CreatedAt,
	}
}

// MonitoringListResponse is a paginated envelope for the admin monitoring
// listing. CPU/RAM are filled from Prometheus when it is reachable, otherwise
// from the latest resource snapshot rows (see AdminMonitoringProjects in
// queries/projects.sql).
type MonitoringListResponse struct {
	Items               []MonitoringProjectResponse `json:"items"`
	Total               int64                       `json:"total"`
	Limit               int32                       `json:"limit"`
	Offset              int32                       `json:"offset"`
	PrometheusAvailable bool                        `json:"prometheus_available"`
}

type MonitoringSeriesResponse struct {
	CPUCores    [][2]float64 `json:"cpu_cores"`
	MemoryBytes [][2]float64 `json:"memory_bytes"`
	NetRXBps    [][2]float64 `json:"net_rx_bps"`
	NetTXBps    [][2]float64 `json:"net_tx_bps"`
	DiskBytes   [][2]float64 `json:"disk_bytes"`
}

type MonitoringHistoryResponse struct {
	Series              MonitoringSeriesResponse `json:"series"`
	PrometheusAvailable bool                     `json:"prometheus_available"`
}

func toMonitoringListResponse(page *MonitoringPage, limit, offset int32) MonitoringListResponse {
	items := page.Items
	out := make([]MonitoringProjectResponse, len(items))
	for i := range items {
		out[i] = toMonitoringProjectResponse(&items[i])
	}
	return MonitoringListResponse{
		Items: out, Total: page.Total, Limit: limit, Offset: offset,
		PrometheusAvailable: page.PrometheusAvailable,
	}
}

func toMonitoringHistoryResponse(history *MonitoringHistory) MonitoringHistoryResponse {
	return MonitoringHistoryResponse{
		Series: MonitoringSeriesResponse{
			CPUCores:    toSeries(history.CPU),
			MemoryBytes: toSeries(history.Memory),
			NetRXBps:    toSeries(history.NetworkRX),
			NetTXBps:    toSeries(history.NetworkTX),
			DiskBytes:   toSeries(history.Disk),
		},
		PrometheusAvailable: history.PrometheusAvailable,
	}
}

func toSeries(points []MetricPoint) [][2]float64 {
	out := make([][2]float64, len(points))
	for i, p := range points {
		out[i] = [2]float64{float64(p.Timestamp), p.Value}
	}
	return out
}
