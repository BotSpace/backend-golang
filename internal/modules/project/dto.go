package project

import (
	"encoding/json"
	"time"
)

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
}

func toResponse(p *Project) ProjectResponse {
	flow := p.Flow
	if len(flow) == 0 {
		flow = json.RawMessage("[]")
	}
	return ProjectResponse{
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
	}
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
}

type UpdateRequest struct {
	Name        *string         `json:"name" validate:"omitempty,min=1,max=255"`
	Description *string         `json:"description" validate:"omitempty,max=5000"`
	Username    *string         `json:"username" validate:"omitempty,max=255"`
	Flow        json.RawMessage `json:"flow" validate:"omitempty"`
	Token       *string         `json:"token" validate:"omitempty,max=255"`
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
		CreatedAt:     p.CreatedAt,
	}
}

// MonitoringListResponse is a paginated envelope for the admin monitoring
// listing. PrometheusAvailable is always false — this backend has no live
// metrics scraper wired in; the listing is built from the last reported
// resource/snapshot rows instead (see AdminMonitoringProjects in
// queries/projects.sql).
type MonitoringListResponse struct {
	Items               []MonitoringProjectResponse `json:"items"`
	Total               int64                       `json:"total"`
	Limit               int32                       `json:"limit"`
	Offset              int32                       `json:"offset"`
	PrometheusAvailable bool                        `json:"prometheus_available"`
}

func toMonitoringListResponse(items []MonitoringProject, total int64, limit, offset int32) MonitoringListResponse {
	out := make([]MonitoringProjectResponse, len(items))
	for i := range items {
		out[i] = toMonitoringProjectResponse(&items[i])
	}
	return MonitoringListResponse{Items: out, Total: total, Limit: limit, Offset: offset, PrometheusAvailable: false}
}
