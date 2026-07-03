// Package plugin ports Django's "module" app (core/apps/api/models/module.py) —
// a catalog of CI-built or self-hosted flow-engine plugins that projects can
// enable. Named "plugin" (not "module") to avoid clashing with Go's own
// module vocabulary.
package plugin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Module is a catalog entry (Django ModuleModel). A module is either
// platform-hosted (github_url + image -> the engine builds/runs a shared pod)
// or self-hosted (the developer runs their own RPC endpoint; the source mode
// lives inside ManifestCache["_source"], parsed by IsSelfHosted).
type Module struct {
	ID                uuid.UUID
	Slug              string
	Name              string
	Description       string
	Icon              string
	GithubURL         string
	Image             string
	Port              int32
	AuthToken         string
	ManifestCache     json.RawMessage
	ManifestFetchedAt *time.Time
	Active            bool
	IsDefault         bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ProjectModule links a project to an enabled Module (Django ProjectModuleModel).
type ProjectModule struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	ModuleID  uuid.UUID
	Enabled   bool
	Config    json.RawMessage
	ServiceID string
	Endpoint  string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProjectModuleWithModule pairs a link row with its parent Module, mirroring
// the nested `module` object Django's ProjectModuleSerializer emits.
type ProjectModuleWithModule struct {
	ProjectModule
	Module Module
}

// CreateModuleInput is the admin catalog-create payload.
type CreateModuleInput struct {
	Slug        string
	Name        string
	Description string
	Icon        string
	GithubURL   string
	Image       string
	Port        int32
	AuthToken   string
	Active      bool
	IsDefault   bool
}

// UpdateModuleInput carries optional fields; nil pointers are left unchanged.
type UpdateModuleInput struct {
	Name        *string
	Description *string
	Icon        *string
	GithubURL   *string
	Image       *string
	Port        *int32
	AuthToken   *string
	Active      *bool
	IsDefault   *bool
}

// ModuleListFilter narrows and paginates the catalog listing.
type ModuleListFilter struct {
	Search string
	Active *bool
	Limit  int32
	Offset int32
}

// Repository is the persistence port. Project-module operations are scoped to
// the caller's own projects entirely in SQL (joined against `projects`) so
// this package never imports the project module (porting guide rule #4).
type Repository interface {
	// Catalog
	CreateModule(ctx context.Context, in CreateModuleInput) (*Module, error)
	GetModuleBySlug(ctx context.Context, slug string) (*Module, error)
	GetModuleByID(ctx context.Context, id uuid.UUID) (*Module, error)
	UpdateModule(ctx context.Context, id uuid.UUID, in UpdateModuleInput) (*Module, error)
	DeleteModule(ctx context.Context, id uuid.UUID) error
	ListModules(ctx context.Context, f ModuleListFilter) ([]Module, error)
	CountModules(ctx context.Context, f ModuleListFilter) (int64, error)
	ListModulesWithGithubURL(ctx context.Context) ([]Module, error)
	UpdateModuleManifest(ctx context.Context, id uuid.UUID, manifest json.RawMessage) (*Module, error)

	// Project links
	GetProjectModuleForOwner(ctx context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error)
	EnsureProjectModuleForOwner(ctx context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error)
	UpdateProjectModuleDeployState(ctx context.Context, id uuid.UUID, in DeployStateInput) (*ProjectModule, error)
	SetProjectModuleDisabledForOwner(ctx context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error)
	ListProjectModulesForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]ProjectModule, error)
	ListEnabledProjectModulesForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]ProjectModule, error)
	CountEnabledProjectModulesExcluding(ctx context.Context, moduleID, excludeProjectID uuid.UUID) (int64, error)
	BulkMarkProjectModulesRunning(ctx context.Context, moduleID uuid.UUID, serviceID, endpoint string) (int64, error)
}

// DeployStateInput is written back onto a project_module row once the
// orchestrator (or a self-hosted endpoint check) confirms the module is up.
type DeployStateInput struct {
	ServiceID string
	Endpoint  string
	Status    string
}

// ManifestFetcher performs a module's `describe`/`docs` JSON-RPC calls over
// HTTP and returns the merged, validated manifest ready to cache. The default
// implementation (manifest.go) talks to the real endpoint; tests inject a fake.
type ManifestFetcher interface {
	Fetch(ctx context.Context, slug, endpoint, authToken string) (json.RawMessage, error)
}

// ReleaseRelay forwards a module-triggered event payload to a project's
// currently running engine release (Django service_send_webhook(release.service_id, ...)).
// The release/webhook domain has not been ported yet, so this is a port:
// server.go injects the real implementation once it exists; until then a nil
// value degrades module-trigger relaying to a clear 500 instead of a panic.
type ReleaseRelay interface {
	SendToProjectRelease(ctx context.Context, projectUUID uuid.UUID, payload []byte) error
}
