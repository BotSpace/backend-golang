package plugin

import (
	"encoding/json"
	"time"
)

// ModuleResponse is the public catalog projection (Django ModuleSerializer).
// docs/nodes/source are derived from ManifestCache so clients never parse it
// themselves.
type ModuleResponse struct {
	ID                string              `json:"id"`
	Slug              string              `json:"slug"`
	Name              string              `json:"name"`
	Description       string              `json:"description,omitempty"`
	Icon              string              `json:"icon,omitempty"`
	GithubURL         string              `json:"github_url,omitempty"`
	Image             string              `json:"image,omitempty"`
	Port              int32               `json:"port"`
	Active            bool                `json:"active"`
	IsDefault         bool                `json:"is_default"`
	ManifestFetchedAt *time.Time          `json:"manifest_fetched_at,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	Docs              string              `json:"docs"`
	Nodes             []ModuleNodeSummary `json:"nodes"`
	Source            moduleSource        `json:"source"`
}

// ModuleNodeSummary is the trimmed-down node listing shown in the catalog
// (full manifest lives behind /projects/{id}/node-manifests).
type ModuleNodeSummary struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Trigger  bool   `json:"trigger"`
}

func toModuleResponse(m *Module) ModuleResponse {
	resp := ModuleResponse{
		ID: m.ID.String(), Slug: m.Slug, Name: m.Name, Description: m.Description,
		Icon: m.Icon, GithubURL: m.GithubURL, Image: m.Image, Port: m.Port,
		Active: m.Active, IsDefault: m.IsDefault, ManifestFetchedAt: m.ManifestFetchedAt,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		Source: parseSource(*m),
		Nodes:  []ModuleNodeSummary{},
	}

	var cache struct {
		Docs string `json:"docs"`
	}
	if len(m.ManifestCache) > 0 {
		_ = json.Unmarshal(m.ManifestCache, &cache)
	}
	resp.Docs = cache.Docs

	for _, raw := range extractNodes(m.ManifestCache) {
		var n struct {
			Type          string `json:"type"`
			TitleFallback string `json:"titleFallback"`
			Title         string `json:"title"`
			Category      string `json:"category"`
			Trigger       bool   `json:"trigger"`
		}
		if json.Unmarshal(raw, &n) != nil {
			continue
		}
		title := n.TitleFallback
		if title == "" {
			title = n.Title
		}
		if title == "" {
			title = n.Type
		}
		resp.Nodes = append(resp.Nodes, ModuleNodeSummary{
			Type: n.Type, Title: title, Category: n.Category,
			Trigger: n.Trigger || n.Category == "triggers",
		})
	}
	return resp
}

// ModuleListResponse is a paginated envelope for the catalog listing.
type ModuleListResponse struct {
	Items  []ModuleResponse `json:"items"`
	Total  int64            `json:"total"`
	Limit  int32            `json:"limit"`
	Offset int32            `json:"offset"`
}

func toModuleListResponse(items []Module, total int64, limit, offset int32) ModuleListResponse {
	out := make([]ModuleResponse, len(items))
	for i := range items {
		out[i] = toModuleResponse(&items[i])
	}
	return ModuleListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// CreateModuleRequest is the admin catalog-create payload.
type CreateModuleRequest struct {
	Slug        string `json:"slug" validate:"required,min=1,max=100"`
	Name        string `json:"name" validate:"required,min=1,max=255"`
	Description string `json:"description" validate:"omitempty,max=5000"`
	Icon        string `json:"icon" validate:"omitempty,max=255"`
	GithubURL   string `json:"github_url" validate:"omitempty,max=500"`
	Image       string `json:"image" validate:"omitempty,max=500"`
	Port        int32  `json:"port" validate:"omitempty,min=1,max=65535"`
	AuthToken   string `json:"auth_token" validate:"omitempty,max=500"`
	Active      bool   `json:"active"`
	IsDefault   bool   `json:"is_default"`
}

// UpdateModuleRequest carries optional fields; nil pointers are left unchanged.
type UpdateModuleRequest struct {
	Name        *string `json:"name" validate:"omitempty,min=1,max=255"`
	Description *string `json:"description" validate:"omitempty,max=5000"`
	Icon        *string `json:"icon" validate:"omitempty,max=255"`
	GithubURL   *string `json:"github_url" validate:"omitempty,max=500"`
	Image       *string `json:"image" validate:"omitempty,max=500"`
	Port        *int32  `json:"port" validate:"omitempty,min=1,max=65535"`
	AuthToken   *string `json:"auth_token" validate:"omitempty,max=500"`
	Active      *bool   `json:"active"`
	IsDefault   *bool   `json:"is_default"`
}

// EnableModuleRequest is the project-scoped "enable a catalog module" payload.
type EnableModuleRequest struct {
	ModuleSlug string `json:"module_slug" validate:"required,min=1,max=100"`
}

// ProjectModuleResponse mirrors Django's ProjectModuleSerializer (nested module).
type ProjectModuleResponse struct {
	ID        string          `json:"id"`
	Module    ModuleResponse  `json:"module"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
	ServiceID string          `json:"service_id,omitempty"`
	Endpoint  string          `json:"endpoint,omitempty"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func toProjectModuleResponse(pm *ProjectModuleWithModule) ProjectModuleResponse {
	cfg := pm.Config
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	return ProjectModuleResponse{
		ID: pm.ID.String(), Module: toModuleResponse(&pm.Module), Enabled: pm.Enabled,
		Config: cfg, ServiceID: pm.ServiceID, Endpoint: pm.Endpoint, Status: pm.Status,
		CreatedAt: pm.CreatedAt, UpdatedAt: pm.UpdatedAt,
	}
}

func toProjectModuleListResponse(items []ProjectModuleWithModule) []ProjectModuleResponse {
	out := make([]ProjectModuleResponse, len(items))
	for i := range items {
		out[i] = toProjectModuleResponse(&items[i])
	}
	return out
}

// ManifestsResponse wraps a flat node-manifest array (Django `{"manifests": [...]}`).
type ManifestsResponse struct {
	Manifests []json.RawMessage `json:"manifests"`
}

// EngineModuleResponse is one entry of GET /projects/:id/modules-for-engine
// (Django get_project_modules_for_engine).
type EngineModuleResponse struct {
	Slug             string              `json:"slug"`
	Endpoint         string              `json:"endpoint"`
	AuthToken        string              `json:"auth_token,omitempty"`
	Nodes            []string            `json:"nodes"`
	Triggers         []EngineTriggerInfo `json:"triggers"`
	CredentialFields map[string][]string `json:"credential_fields"`
}

func toEngineModulesResponse(items []EngineModuleInfo) []EngineModuleResponse {
	out := make([]EngineModuleResponse, len(items))
	for i, it := range items {
		nodes := it.Nodes
		if nodes == nil {
			nodes = []string{}
		}
		triggers := it.Triggers
		if triggers == nil {
			triggers = []EngineTriggerInfo{}
		}
		out[i] = EngineModuleResponse{
			Slug: it.Slug, Endpoint: it.Endpoint, AuthToken: it.AuthToken,
			Nodes: nodes, Triggers: triggers, CredentialFields: it.CredentialFields,
		}
	}
	return out
}

// RedeployResponse is the CI redeploy-hook response (Django ModuleRedeployView).
type RedeployResponse struct {
	OK                bool   `json:"ok"`
	Module            string `json:"module"`
	ManifestRefreshed bool   `json:"manifest_refreshed"`
}

// GithubWebhookResponse is the GitHub push-webhook response.
type GithubWebhookResponse struct {
	OK           bool   `json:"ok"`
	Pong         bool   `json:"pong,omitempty"`
	Module       string `json:"module,omitempty"`
	SHA          string `json:"sha,omitempty"`
	IgnoredEvent string `json:"ignored_event,omitempty"`
	IgnoredRef   string `json:"ignored_ref,omitempty"`
	NoModuleFor  string `json:"no_module_for,omitempty"`
}
