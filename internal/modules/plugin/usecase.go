package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// ErrSlugTaken is returned by the repository when a module slug already
// exists (unique constraint on module.slug).
var ErrSlugTaken = errors.New("module slug already exists")

// manifestTTL bounds how long a cached manifest is trusted before a fresh
// describe() call is made (Django MANIFEST_TTL_SECONDS).
const manifestTTL = 120 * time.Second

// GithubPushResult reports what the GitHub webhook usecase did with a push
// event, mirroring the JSON shape GitHubModuleWebhookView returns.
type GithubPushResult struct {
	OK          bool
	Module      string
	SHA         string
	IgnoredRef  string
	NoModuleFor string
}

// EngineTriggerInfo is one module-declared trigger node (Django's per-module
// `triggers` list fed to the engine).
type EngineTriggerInfo struct {
	Type string `json:"type"`
	Mode string `json:"mode"`
}

// EngineModuleInfo is what the engine needs to route a project's enabled
// modules: endpoint, node types, trigger nodes, and per-node credential field
// keys (Django get_project_modules_for_engine).
type EngineModuleInfo struct {
	Slug             string              `json:"slug"`
	Endpoint         string              `json:"endpoint"`
	AuthToken        string              `json:"auth_token,omitempty"`
	Nodes            []string            `json:"nodes"`
	Triggers         []EngineTriggerInfo `json:"triggers"`
	CredentialFields map[string][]string `json:"credential_fields"`
}

// Usecase exposes the module catalog + per-project module management.
type Usecase interface {
	// Catalog (admin-curated, browsable by any authenticated user).
	ListModules(ctx context.Context, f ModuleListFilter) ([]Module, int64, error)
	GetModule(ctx context.Context, slug string) (*Module, error)
	CreateModule(ctx context.Context, in CreateModuleInput) (*Module, error)
	UpdateModule(ctx context.Context, slug string, in UpdateModuleInput) (*Module, error)
	DeleteModule(ctx context.Context, slug string) error
	RefreshManifest(ctx context.Context, slug string) (*Module, error)
	// Redeploy recreates the shared pod (CI push / manual admin trigger) and
	// best-effort refreshes the manifest. manifestRefreshed reports whether
	// the refresh succeeded (the pod may still be restarting).
	Redeploy(ctx context.Context, slug string) (module *Module, manifestRefreshed bool, err error)
	HandleGithubPush(ctx context.Context, htmlURL, cloneURL, fullName, ref, defaultBranch, afterSHA string) (GithubPushResult, error)

	// Project scope (owner-checked in SQL).
	ListProjectModules(ctx context.Context, projectID, ownerID uuid.UUID) ([]ProjectModuleWithModule, error)
	// EnableModule returns created=false when the module was already enabled
	// (Django's "already enabled" 200 vs a fresh 201).
	EnableModule(ctx context.Context, projectID, ownerID uuid.UUID, slug string) (pm *ProjectModuleWithModule, created bool, err error)
	DisableModule(ctx context.Context, projectID, ownerID uuid.UUID, slug string) error
	ModuleManifests(ctx context.Context, projectID, ownerID uuid.UUID) ([]json.RawMessage, error)
	NodeManifests(ctx context.Context, projectID, ownerID uuid.UUID) ([]json.RawMessage, error)
	ModulesForEngine(ctx context.Context, projectID, ownerID uuid.UUID) ([]EngineModuleInfo, error)

	// RelayTrigger forwards a module-pushed event to the project's running
	// engine release.
	RelayTrigger(ctx context.Context, projectUUID uuid.UUID, payload []byte) error
}

type usecase struct {
	repo    Repository
	orch    engine.Orchestrator
	fetcher ManifestFetcher
	relay   ReleaseRelay
}

// NewUsecase wires the module usecase. orch deploys/stops shared module pods;
// fetcher performs the describe()/docs() RPC calls (nil -> NewHTTPManifestFetcher);
// relay forwards module-trigger events to a project's release and may be nil
// until the release/webhook module is ported (RelayTrigger then fails clearly
// instead of panicking).
func NewUsecase(repo Repository, orch engine.Orchestrator, fetcher ManifestFetcher, relay ReleaseRelay) Usecase {
	if fetcher == nil {
		fetcher = NewHTTPManifestFetcher()
	}
	return &usecase{repo: repo, orch: orch, fetcher: fetcher, relay: relay}
}

// ---- Catalog ----------------------------------------------------------

func (u *usecase) ListModules(ctx context.Context, f ModuleListFilter) ([]Module, int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.ListModules(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountModules(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) GetModule(ctx context.Context, slug string) (*Module, error) {
	return mapModuleErr(u.repo.GetModuleBySlug(ctx, slug))
}

func (u *usecase) CreateModule(ctx context.Context, in CreateModuleInput) (*Module, error) {
	if in.Slug == "" || in.Name == "" {
		return nil, httpx.BadRequest("module.invalid", "slug and name are required")
	}
	m, err := u.repo.CreateModule(ctx, in)
	if err != nil {
		if errors.Is(err, ErrSlugTaken) {
			return nil, httpx.Conflict("module.slug_taken", "module slug already exists")
		}
		return nil, httpx.Internal(err)
	}
	return m, nil
}

func (u *usecase) UpdateModule(ctx context.Context, slug string, in UpdateModuleInput) (*Module, error) {
	m, err := u.repo.GetModuleBySlug(ctx, slug)
	if err != nil {
		return mapModuleErr(nil, err)
	}
	return mapModuleErr(u.repo.UpdateModule(ctx, m.ID, in))
}

func (u *usecase) DeleteModule(ctx context.Context, slug string) error {
	m, err := u.repo.GetModuleBySlug(ctx, slug)
	if err != nil {
		return moduleNotFoundOr500(err)
	}
	if !isSelfHosted(*m) {
		// Best-effort teardown: a cluster error must not block the catalog
		// delete (mirrors Django undeploy_module_completely, which logs and
		// continues on k8s failure).
		_ = u.orch.Stop(ctx, deploymentName(m.Slug))
	}
	if err := u.repo.DeleteModule(ctx, m.ID); err != nil {
		return moduleNotFoundOr500(err)
	}
	return nil
}

func (u *usecase) RefreshManifest(ctx context.Context, slug string) (*Module, error) {
	m, err := u.repo.GetModuleBySlug(ctx, slug)
	if err != nil {
		return mapModuleErr(nil, err)
	}
	return u.refreshManifest(ctx, m)
}

// refreshManifest performs the describe()/docs() calls and persists the
// result, preserving the module.yaml `_source` block across the refresh.
func (u *usecase) refreshManifest(ctx context.Context, m *Module) (*Module, error) {
	endpoint := sharedEndpoint(*m)
	manifest, err := u.fetcher.Fetch(ctx, m.Slug, endpoint, m.AuthToken)
	if err != nil {
		return nil, httpx.BadRequest("module.manifest_fetch_failed", err.Error())
	}
	manifest = preserveSource(*m, manifest)
	updated, err := u.repo.UpdateModuleManifest(ctx, m.ID, manifest)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return updated, nil
}

func (u *usecase) Redeploy(ctx context.Context, slug string) (*Module, bool, error) {
	m, err := u.repo.GetModuleBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, false, httpx.NotFound("module.not_found", "module not found")
		}
		return nil, false, httpx.Internal(err)
	}

	if !isSelfHosted(*m) {
		result, err := u.orch.Deploy(ctx, moduleDeploySpec(*m))
		if err != nil {
			return nil, false, httpx.Internal(fmt.Errorf("redeploy module %s: %w", slug, err))
		}
		serviceID := result.ServiceID
		if serviceID == "" {
			serviceID = deploymentName(m.Slug)
		}
		if _, err := u.repo.BulkMarkProjectModulesRunning(ctx, m.ID, serviceID, sharedEndpoint(*m)); err != nil {
			return nil, false, httpx.Internal(err)
		}
	}

	// Manifest refresh is best-effort: Recreate means the pod may still be
	// restarting right now, so a failure here does not fail the redeploy.
	refreshed, err := u.refreshManifest(ctx, m)
	if err != nil {
		return m, false, nil
	}
	return refreshed, true, nil
}

func (u *usecase) HandleGithubPush(ctx context.Context, htmlURL, cloneURL, fullName, ref, defaultBranch, afterSHA string) (GithubPushResult, error) {
	wantRef := "refs/heads/" + defaultBranch
	if ref != wantRef {
		return GithubPushResult{OK: true, IgnoredRef: ref}, nil
	}

	modules, err := u.repo.ListModulesWithGithubURL(ctx)
	if err != nil {
		return GithubPushResult{}, httpx.Internal(err)
	}
	m := matchModuleByRepo(modules, htmlURL, cloneURL, fullName)
	if m == nil {
		return GithubPushResult{OK: true, NoModuleFor: fullName}, nil
	}

	if _, _, err := u.Redeploy(ctx, m.Slug); err != nil {
		return GithubPushResult{}, err
	}
	sha := afterSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return GithubPushResult{OK: true, Module: m.Slug, SHA: sha}, nil
}

// ---- Project scope ------------------------------------------------------

func (u *usecase) ListProjectModules(ctx context.Context, projectID, ownerID uuid.UUID) ([]ProjectModuleWithModule, error) {
	links, err := u.repo.ListProjectModulesForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return u.attachModules(ctx, links)
}

func (u *usecase) EnableModule(ctx context.Context, projectID, ownerID uuid.UUID, slug string) (*ProjectModuleWithModule, bool, error) {
	m, err := u.repo.GetModuleBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, false, httpx.NotFound("module.not_found", "module not found")
		}
		return nil, false, httpx.Internal(err)
	}
	if !m.Active {
		return nil, false, httpx.BadRequest("module.inactive", "module is not active")
	}

	existing, getErr := u.repo.GetProjectModuleForOwner(ctx, projectID, m.ID, ownerID)
	if getErr != nil && !errors.Is(getErr, database.ErrNotFound) {
		return nil, false, httpx.Internal(getErr)
	}
	if getErr == nil && existing.Enabled {
		return &ProjectModuleWithModule{ProjectModule: *existing, Module: *m}, false, nil
	}
	isNew := errors.Is(getErr, database.ErrNotFound)

	pm, err := u.repo.EnsureProjectModuleForOwner(ctx, projectID, m.ID, ownerID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, false, httpx.NotFound("project.not_found", "project not found")
		}
		return nil, false, httpx.Internal(err)
	}

	var serviceID, endpoint, status string
	if isSelfHosted(*m) {
		// Developer hosts the module on their own server — no pod to deploy.
		endpoint = sharedEndpoint(*m)
		status = "running"
	} else {
		result, derr := u.orch.Deploy(ctx, moduleDeploySpec(*m))
		if derr != nil {
			_, _ = u.repo.UpdateProjectModuleDeployState(ctx, pm.ID, DeployStateInput{Status: "failed"})
			return nil, false, httpx.Internal(fmt.Errorf("deploy module %s: %w", slug, derr))
		}
		serviceID = result.ServiceID
		if serviceID == "" {
			serviceID = deploymentName(m.Slug)
		}
		endpoint = sharedEndpoint(*m)
		status = mapOrchStatus(result.Status)
	}

	updated, err := u.repo.UpdateProjectModuleDeployState(ctx, pm.ID, DeployStateInput{
		ServiceID: serviceID, Endpoint: endpoint, Status: status,
	})
	if err != nil {
		return nil, false, httpx.Internal(err)
	}

	// Best-effort manifest warm-up so the constructor sees nodes immediately.
	if refreshed, rerr := u.refreshManifest(ctx, m); rerr == nil {
		m = refreshed
	}

	return &ProjectModuleWithModule{ProjectModule: *updated, Module: *m}, isNew, nil
}

func (u *usecase) DisableModule(ctx context.Context, projectID, ownerID uuid.UUID, slug string) error {
	m, err := u.repo.GetModuleBySlug(ctx, slug)
	if err != nil {
		return moduleNotFoundOr500(err)
	}
	if _, err := u.repo.SetProjectModuleDisabledForOwner(ctx, projectID, m.ID, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("project_module.not_found", "module is not enabled for this project")
		}
		return httpx.Internal(err)
	}
	if !isSelfHosted(*m) {
		// Shared pod: only the last project using it tears it down.
		stillUsed, cerr := u.repo.CountEnabledProjectModulesExcluding(ctx, m.ID, projectID)
		if cerr == nil && stillUsed == 0 {
			_ = u.orch.Stop(ctx, deploymentName(m.Slug)) // best-effort
		}
	}
	return nil
}

func (u *usecase) ModuleManifests(ctx context.Context, projectID, ownerID uuid.UUID) ([]json.RawMessage, error) {
	links, err := u.repo.ListEnabledProjectModulesForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	withModules, err := u.attachModules(ctx, links)
	if err != nil {
		return nil, err
	}
	return u.flattenModuleNodes(ctx, withModules), nil
}

func (u *usecase) NodeManifests(ctx context.Context, projectID, ownerID uuid.UUID) ([]json.RawMessage, error) {
	moduleNodes, err := u.ModuleManifests(ctx, projectID, ownerID)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(builtinNodeManifests))
	out := make([]json.RawMessage, 0, len(builtinNodeManifests)+len(moduleNodes))
	for _, n := range builtinNodeManifests {
		if t, _ := n["type"].(string); t != "" {
			seen[t] = true
		}
		raw, err := json.Marshal(n)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	// Built-in nodes always win a type collision (Django get_unified_node_manifests).
	for _, raw := range moduleNodes {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		if t, _ := obj["type"].(string); t != "" && seen[t] {
			continue
		}
		out = append(out, raw)
	}
	return out, nil
}

func (u *usecase) ModulesForEngine(ctx context.Context, projectID, ownerID uuid.UUID) ([]EngineModuleInfo, error) {
	links, err := u.repo.ListEnabledProjectModulesForOwner(ctx, projectID, ownerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	withModules, err := u.attachModules(ctx, links)
	if err != nil {
		return nil, err
	}

	out := make([]EngineModuleInfo, 0, len(withModules))
	for i := range withModules {
		pm := withModules[i].ProjectModule
		m := withModules[i].Module
		nodesRaw := extractNodes(m.ManifestCache)
		if len(nodesRaw) == 0 {
			// Self-heal: enabled before the pod finished starting -> the cache
			// has no nodes yet. Refetch once so a (re)deploy ships current nodes.
			if refreshed, rerr := u.refreshManifest(ctx, &m); rerr == nil {
				m = *refreshed
				nodesRaw = extractNodes(m.ManifestCache)
			}
		}

		info := EngineModuleInfo{
			Slug: m.Slug, Endpoint: pm.Endpoint, AuthToken: m.AuthToken,
			CredentialFields: map[string][]string{},
		}
		for _, raw := range nodesRaw {
			var node struct {
				Type        string `json:"type"`
				Trigger     bool   `json:"trigger"`
				Category    string `json:"category"`
				TriggerMode string `json:"triggerMode"`
				Content     []struct {
					Type string `json:"type"`
					Key  string `json:"key"`
				} `json:"content"`
			}
			if json.Unmarshal(raw, &node) != nil || node.Type == "" {
				continue
			}
			info.Nodes = append(info.Nodes, node.Type)
			if node.Trigger || node.Category == "triggers" {
				mode := node.TriggerMode
				if mode == "" {
					mode = "event-match"
				}
				info.Triggers = append(info.Triggers, EngineTriggerInfo{Type: node.Type, Mode: mode})
			}
			var keys []string
			for _, f := range node.Content {
				if f.Type == "credential" && f.Key != "" {
					keys = append(keys, f.Key)
				}
			}
			if len(keys) > 0 {
				info.CredentialFields[node.Type] = keys
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (u *usecase) RelayTrigger(ctx context.Context, projectUUID uuid.UUID, payload []byte) error {
	if u.relay == nil {
		return httpx.Internal(errors.New("module trigger relay is not configured"))
	}
	if err := u.relay.SendToProjectRelease(ctx, projectUUID, payload); err != nil {
		return httpx.Internal(fmt.Errorf("relay module trigger: %w", err))
	}
	return nil
}

// ---- helpers --------------------------------------------------------

// attachModules joins project_module rows with their parent Module, skipping
// any row whose module was concurrently deleted (FK cascade would normally
// prevent this, but a plain read races harmlessly with a delete).
func (u *usecase) attachModules(ctx context.Context, links []ProjectModule) ([]ProjectModuleWithModule, error) {
	out := make([]ProjectModuleWithModule, 0, len(links))
	cache := make(map[uuid.UUID]Module, len(links))
	for _, l := range links {
		m, ok := cache[l.ModuleID]
		if !ok {
			mp, err := u.repo.GetModuleByID(ctx, l.ModuleID)
			if err != nil {
				if errors.Is(err, database.ErrNotFound) {
					continue
				}
				return nil, httpx.Internal(err)
			}
			m = *mp
			cache[l.ModuleID] = m
		}
		out = append(out, ProjectModuleWithModule{ProjectModule: l, Module: m})
	}
	return out, nil
}

// flattenModuleNodes tags each enabled module's cached nodes with
// moduleSlug/moduleName (Django get_project_module_manifests), refreshing a
// stale (TTL-expired) cache best-effort first.
func (u *usecase) flattenModuleNodes(ctx context.Context, links []ProjectModuleWithModule) []json.RawMessage {
	out := []json.RawMessage{}
	for i := range links {
		m := links[i].Module
		if m.ManifestFetchedAt == nil || time.Since(*m.ManifestFetchedAt) > manifestTTL {
			if refreshed, err := u.refreshManifest(ctx, &m); err == nil {
				m = *refreshed
			}
		}
		for _, n := range extractNodes(m.ManifestCache) {
			out = append(out, tagNode(n, m.Slug, m.Name))
		}
	}
	return out
}

func extractNodes(manifest json.RawMessage) []json.RawMessage {
	if len(manifest) == 0 {
		return nil
	}
	var wrapper struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(manifest, &wrapper); err != nil {
		return nil
	}
	return wrapper.Nodes
}

func tagNode(node json.RawMessage, slug, name string) json.RawMessage {
	var obj map[string]any
	if json.Unmarshal(node, &obj) != nil {
		return node
	}
	obj["moduleSlug"] = slug
	obj["moduleName"] = name
	out, err := json.Marshal(obj)
	if err != nil {
		return node
	}
	return out
}

// moduleSource is the module.yaml `source` block cached at manifest_cache["_source"].
type moduleSource struct {
	Mode     string `json:"mode"`
	Endpoint string `json:"endpoint"`
	Github   string `json:"github"`
}

type cachedManifest struct {
	Source *moduleSource `json:"_source"`
}

func parseSource(m Module) moduleSource {
	if len(m.ManifestCache) > 0 {
		var c cachedManifest
		if json.Unmarshal(m.ManifestCache, &c) == nil && c.Source != nil && c.Source.Mode != "" {
			return *c.Source
		}
	}
	mode := "endpoint"
	if m.Image != "" {
		mode = "github"
	}
	return moduleSource{Mode: mode, Github: m.GithubURL}
}

// preserveSource re-attaches the module's `_source` config to a freshly
// fetched manifest, which describe() knows nothing about.
func preserveSource(old Module, newManifest json.RawMessage) json.RawMessage {
	src := parseSource(old)
	var obj map[string]any
	if json.Unmarshal(newManifest, &obj) != nil {
		return newManifest
	}
	obj["_source"] = src
	out, err := json.Marshal(obj)
	if err != nil {
		return newManifest
	}
	return out
}

func isSelfHosted(m Module) bool { return parseSource(m).Mode == "endpoint" }

// deploymentName is the deterministic shared-pod name for a module slug
// (Django module_service._deployment_name).
func deploymentName(slug string) string {
	return "module-" + strings.ToLower(strings.ReplaceAll(slug, "_", "-"))
}

// sharedEndpoint is the RPC endpoint every project sharing this module calls:
// the developer's own server for self-hosted modules, or the deterministic
// in-cluster service address for platform-hosted ones.
func sharedEndpoint(m Module) string {
	src := parseSource(m)
	if src.Mode == "endpoint" && src.Endpoint != "" {
		return src.Endpoint
	}
	return fmt.Sprintf("http://%s:%d/rpc", deploymentName(m.Slug), m.Port)
}

func moduleDeploySpec(m Module) engine.DeploySpec {
	env := map[string]string{"MODULE_SLUG": m.Slug, "PORT": fmt.Sprintf("%d", m.Port)}
	if m.AuthToken != "" {
		env["MODULE_AUTH_TOKEN"] = m.AuthToken
	}
	// Modules are shared pods, not project releases: there is no ProjectID to
	// give the generic Orchestrator contract, so ReleaseUUID carries the
	// module's own id and Env carries what the pod needs to boot. Name pins
	// the k8s object name to deploymentName(slug) (matches sharedEndpoint's
	// DNS name + the Stop(deploymentName(...)) calls elsewhere in this file).
	// Image is the module's own image (not the bot-engine image); modules only
	// expose /health (no separate /ready), unlike bot engines.
	return engine.DeploySpec{
		ReleaseUUID:   m.ID,
		Env:           env,
		Name:          deploymentName(m.Slug),
		Image:         m.Image,
		ContainerPort: int(m.Port),
		ReadinessPath: "/health",
		LivenessPath:  "/health",
	}
}

func mapOrchStatus(s engine.Status) string {
	switch s {
	case engine.StatusRunning:
		return "running"
	case engine.StatusFailed:
		return "failed"
	case engine.StatusStopped, engine.StatusStopping, engine.StatusSuspended:
		return "stopped"
	default:
		return "pending"
	}
}

func normalizeRepoURL(s string) string {
	s = strings.ToLower(strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(s), "/"), ".git"))
	return s
}

func matchModuleByRepo(modules []Module, htmlURL, cloneURL, fullName string) *Module {
	candidates := map[string]bool{}
	if htmlURL != "" {
		candidates[normalizeRepoURL(htmlURL)] = true
	}
	if cloneURL != "" {
		candidates[normalizeRepoURL(cloneURL)] = true
	}
	fn := strings.ToLower(fullName)
	for i := range modules {
		gu := normalizeRepoURL(modules[i].GithubURL)
		if gu == "" {
			continue
		}
		if candidates[gu] || (fn != "" && strings.HasSuffix(gu, fn)) {
			return &modules[i]
		}
	}
	return nil
}

func mapModuleErr(m *Module, err error) (*Module, error) {
	if err != nil {
		return nil, moduleNotFoundOr500(err)
	}
	return m, nil
}

func moduleNotFoundOr500(err error) error {
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound("module.not_found", "module not found")
	}
	return httpx.Internal(err)
}
