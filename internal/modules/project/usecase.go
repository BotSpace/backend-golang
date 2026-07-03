package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/botspace/backend/internal/modules/cms"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Usecase exposes owner-scoped project operations, plus admin-wide
// (unscoped) listing/detail/monitoring reachable only behind an admin route.
type Usecase interface {
	Create(ctx context.Context, in CreateInput) (*Project, error)
	Get(ctx context.Context, id, ownerID uuid.UUID) (*Project, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error)
	Delete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Project, int64, error)

	AdminList(ctx context.Context, f AdminListFilter) ([]AdminProjectSummary, int64, error)
	AdminGet(ctx context.Context, id uuid.UUID) (*AdminProjectDetail, error)
	AdminMonitoring(ctx context.Context, f AdminListFilter) ([]MonitoringProject, int64, error)
}

// CollectionProvisioner is the minimal cms.Usecase surface this module needs
// to provision a new project's default system CMS collections (see
// provisionDefaultCollections). Defined narrowly here — instead of taking
// cms.Usecase's full document/export-task surface directly — purely so unit
// tests can fake just these three methods; cms.Usecase satisfies this
// interface structurally, no adapter type required.
type CollectionProvisioner interface {
	CreateCollection(ctx context.Context, ownerID, projectID uuid.UUID, in cms.CreateCollectionInput) (*cms.Collection, error)
	GetCollection(ctx context.Context, ownerID, projectID uuid.UUID, slug string) (*cms.Collection, error)
	AddField(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, in cms.FieldCreate) (*cms.Field, error)
}

type usecase struct {
	repo Repository
	// orch is nil-able: an "" orchestrator (e.g. engine.NewStub()) or nil
	// simply means Get returns whatever status is in the debug row, no live
	// reconciliation, and Delete skips stopping any running containers. Kept
	// optional so existing callers/tests that only care about project CRUD
	// don't need to wire k8s.
	orch engine.Orchestrator
	// cms is nil-able: nil simply means Create skips provisioning the
	// project's default system CMS collections (see
	// provisionDefaultCollections) — kept optional for the same reason as
	// orch above.
	cms CollectionProvisioner
}

// NewUsecase wires the project usecase. orch may be nil — Get then skips
// live debug-status reconciliation and Delete skips stopping containers.
// cmsUC may be nil (including a nil cms.Usecase, which satisfies
// CollectionProvisioner too) — Create then skips provisioning default
// collections.
func NewUsecase(repo Repository, orch engine.Orchestrator, cmsUC CollectionProvisioner) Usecase {
	return &usecase{repo: repo, orch: orch, cms: cmsUC}
}

func (u *usecase) Create(ctx context.Context, in CreateInput) (*Project, error) {
	p, err := u.repo.Create(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if err := u.applyToken(ctx, p, p.OwnerID, in.Token); err != nil {
		return nil, err
	}
	u.provisionDefaultCollections(ctx, p)
	return p, nil
}

// defaultCollectionField is one field of a defaultCollection spec.
type defaultCollectionField struct {
	name, label, kind string
	required          bool
}

// defaultCollectionSpec mirrors one entry of Django's SYSTEM_COLLECTIONS
// (core/apps/cms/services/system_collections.py) — the schema the engine's
// direct Mongo writes need to be visible in the dashboard/MCP.
type defaultCollectionSpec struct {
	name, label, description, primaryField string
	fields                                 []defaultCollectionField
}

// defaultCollections is the exact set Django provisions on every project
// create — see system_collections.py SYSTEM_COLLECTIONS (labels/descriptions/
// field kinds copied verbatim, including the "first_name"/"chat_id" primary
// fields).
var defaultCollections = []defaultCollectionSpec{
	{
		name:         "users",
		label:        "Foydalanuvchilar",
		description:  "Bot foydalanuvchilari (engine avtomatik yozadi).",
		primaryField: "first_name",
		fields: []defaultCollectionField{
			{name: "chat_id", label: "Chat ID", kind: cms.FieldInt, required: true},
			{name: "first_name", label: "Ism", kind: cms.FieldText},
			{name: "last_name", label: "Familiya", kind: cms.FieldText},
			{name: "username", label: "Username", kind: cms.FieldText},
			{name: "last_seen", label: "Oxirgi faollik", kind: cms.FieldDateTime},
			{name: "created_at", label: "Yaratilgan vaqt", kind: cms.FieldDateTime},
		},
	},
	{
		name:         "chat",
		label:        "Chat tarixi",
		description:  "Bot chat xabarlari tarixi.",
		primaryField: "chat_id",
		fields: []defaultCollectionField{
			{name: "chat_id", label: "Chat ID", kind: cms.FieldText, required: true},
			{name: "message", label: "Xabar", kind: cms.FieldText},
			{name: "message_type", label: "Xabar turi", kind: cms.FieldText},
			{name: "is_read", label: "O'qilgan", kind: cms.FieldBool},
			{name: "from_admin", label: "Admindan", kind: cms.FieldBool},
			{name: "updated_at", label: "Yangilangan vaqt", kind: cms.FieldDateTime},
			{name: "created_at", label: "Yaratilgan vaqt", kind: cms.FieldDateTime},
		},
	},
}

// provisionDefaultCollections ports Django's create_default_collections +
// ensure_system_collections, both called synchronously from
// ProjectViewSet.perform_create (core/apps/api/views/project.py): every new
// project gets the `users`/`chat` system CMS collections up front, since the
// engine writes bot users/chat history straight into Mongo and those
// documents are only visible in the dashboard/MCP once a matching schema
// exists.
//
// Get-or-create per collection (skip if the slug already exists) mirrors
// Django's get_or_create idempotency; best-effort like every other
// non-essential side effect on this path (applyToken/setReleaseWebhook
// above) — a CMS hiccup must never fail project creation.
func (u *usecase) provisionDefaultCollections(ctx context.Context, p *Project) {
	if u.cms == nil {
		return
	}
	for _, def := range defaultCollections {
		if _, err := u.cms.GetCollection(ctx, p.OwnerID, p.ID, def.name); err == nil {
			continue // already provisioned
		}
		// collection.name is the human-readable label shown in the UI
		// (migrations/000023_cms.up.sql); slug is the immutable Mongo
		// collection name the engine actually writes to. Django's
		// SYSTEM_COLLECTIONS keeps these as two separate fields
		// (label/name) — same split here, just different Go field names.
		if _, err := u.cms.CreateCollection(ctx, p.OwnerID, p.ID, cms.CreateCollectionInput{
			Name:         def.label,
			Slug:         def.name,
			Description:  def.description,
			PrimaryField: def.primaryField,
			System:       true,
		}); err != nil {
			log.Printf("project: create default collection %q for project %s failed: %v", def.name, p.ID, err)
			continue
		}
		for i, f := range def.fields {
			if _, err := u.cms.AddField(ctx, p.OwnerID, p.ID, def.name, cms.FieldCreate{
				Name:     f.name,
				Label:    f.label,
				Type:     f.kind,
				Required: f.required,
				Order:    int32(i),
			}); err != nil {
				log.Printf("project: add field %q to default collection %q for project %s failed: %v", f.name, def.name, p.ID, err)
			}
		}
	}
}

// Get ports RetrieveProjectSerializer, which nests RetrieveDebugSerializer
// (core/apps/api/serializers/project/project.py) — the single-project fetch
// embeds the project's debug-container status. See reconcileDebug for why
// that status is asked from the orchestrator live rather than trusted as-is.
func (u *usecase) Get(ctx context.Context, id, ownerID uuid.UUID) (*Project, error) {
	p, err := u.repo.GetForOwner(ctx, id, ownerID)
	if p, err = mapErr(p, err); err != nil {
		return nil, err
	}
	dbg, err := u.repo.GetDebugForOwner(ctx, id, ownerID)
	if err != nil || dbg == nil {
		return p, nil
	}
	p.Debug = u.reconcileDebug(ctx, dbg)
	return p, nil
}

// reconcileDebug is the project-module twin of release.reconcileRelease /
// release.reconcileDebug — same lazy live-status fix, duplicated instead of
// imported because this module deliberately never imports the release
// package (see Repository's GetDebugForOwner doc comment).
func (u *usecase) reconcileDebug(ctx context.Context, dbg *ProjectDebug) *ProjectDebug {
	if u.orch == nil || dbg.ServiceID == "" {
		return dbg
	}
	dbStatus := engine.Status(dbg.Status)
	if dbStatus != engine.StatusPending && dbStatus != engine.StatusInitializing {
		return dbg
	}
	live, err := u.orch.Status(ctx, dbg.ServiceID)
	if err != nil {
		return dbg
	}
	next := engine.ReconcileStatus(dbStatus, live)
	if string(next) == dbg.Status {
		return dbg
	}
	if updated, err := u.repo.UpdateDebugStatus(ctx, dbg.ID, dbg.ServiceID, string(next)); err == nil {
		return updated
	}
	dbg.Status = string(next)
	return dbg
}

func (u *usecase) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error) {
	p, err := u.repo.Update(ctx, id, ownerID, in)
	if p, err = mapErr(p, err); err != nil {
		return nil, err
	}
	var token string
	if in.Token != nil {
		token = *in.Token
	}
	if err := u.applyToken(ctx, p, ownerID, token); err != nil {
		return nil, err
	}
	return p, nil
}

// applyToken stores a non-blank Telegram bot token as the project's
// RELEASE_BOT_TOKEN environment variable and best-effort registers Telegram's
// webhook for it — mirrors Django's EnviromentModel.objects.update_or_create
// (bot.py:39) + core/apps/api/services/webhook.py's set_webhook(instance,
// "release"). A blank token is a no-op: Create/Update never force a token,
// only store one when given (dashboard's bot_token_required flow handles the
// tokenless path at release-deploy time instead).
func (u *usecase) applyToken(ctx context.Context, p *Project, ownerID uuid.UUID, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	if err := u.repo.SetReleaseBotToken(ctx, p.ID, ownerID, token); err != nil {
		return httpx.Internal(fmt.Errorf("store release bot token: %w", err))
	}
	setReleaseWebhook(token, p.ReleaseWebhookUUID)
	return nil
}

// setReleaseWebhook registers this project's stable release webhook route
// with Telegram for the given token. Best-effort by design (mirrors Django's
// set_webhook, which swallows httpx errors and logs — see webhook.py:43-50):
// a bad token or an unreachable Telegram API must never fail project
// create/update, since the release deploy path retries this same
// registration (release.Usecase.CreateRelease -> engine deploy) anyway.
func setReleaseWebhook(token string, releaseWebhookUUID uuid.UUID) {
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "api.botspace.uz"
	}
	hookURL := fmt.Sprintf("https://%s/api/bot-webhook/release/%s/", domain, releaseWebhookUUID.String())
	telegramURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook?url=%s", token, hookURL)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodPost, telegramURL, nil)
	if err != nil {
		log.Printf("project: build setWebhook request failed: %v", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("project: setWebhook call failed (will retry on release deploy): %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("project: setWebhook returned status %d", resp.StatusCode)
	}
}

// Delete ports Django's project delete, which tore down every running k8s
// deployment for the bot before removing the row (remove_service, called
// from the release/debug stop paths — see core/apps/api/services/project.py
// and the release/debug signal handlers). The Go port's soft-delete alone
// left release-<uuid>-*/debug-<uuid> Deployments (+ their -env Secret,
// -flow ConfigMap, bot-<webhookUUID> Service) running forever, since nothing
// else in the system ever calls orch.Stop for a deleted project.
//
// Container teardown must happen BEFORE the soft-delete: every owner-scoped
// lookup (ActiveReleaseServiceIDsForOwner, GetDebugForOwner) filters on
// deleted_at IS NULL, so once the project row is gone those service_ids can
// no longer be found. Best-effort like release.Usecase.StopRelease/
// StopDebug — an orchestrator error (or a service that's already stopped;
// engine.Orchestrator.Stop is idempotent, see kubernetes.go's
// ignoreNotFound) never blocks the delete itself.
func (u *usecase) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	u.stopRunningContainers(ctx, id, ownerID)

	if err := u.repo.SoftDelete(ctx, id, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("project.not_found", "project not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

// stopRunningContainers best-effort stops every release deployment plus the
// debug deployment for a project, ahead of Delete's soft-delete. Errors
// (including "project not found", which just means there's nothing to stop)
// are swallowed — Delete itself still reports 404 via SoftDelete right after.
func (u *usecase) stopRunningContainers(ctx context.Context, id, ownerID uuid.UUID) {
	if u.orch == nil {
		return
	}
	if serviceIDs, err := u.repo.ActiveReleaseServiceIDsForOwner(ctx, id, ownerID); err == nil {
		for _, serviceID := range serviceIDs {
			_ = u.orch.Stop(ctx, serviceID)
		}
	}
	if dbg, err := u.repo.GetDebugForOwner(ctx, id, ownerID); err == nil && dbg != nil && dbg.ServiceID != "" {
		_ = u.orch.Stop(ctx, dbg.ServiceID)
	}
}

func (u *usecase) List(ctx context.Context, f ListFilter) ([]Project, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.List(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.Count(ctx, f.OwnerID, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func clampPage(limit, offset int32) (int32, int32) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// AdminList returns a page of every project across every owner (admin-only).
func (u *usecase) AdminList(ctx context.Context, f AdminListFilter) ([]AdminProjectSummary, int64, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	items, err := u.repo.ListAdmin(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountAdmin(ctx, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// AdminGet returns any project's detail (owner + release history) regardless
// of who owns it (admin-only).
func (u *usecase) AdminGet(ctx context.Context, id uuid.UUID) (*AdminProjectDetail, error) {
	p, err := u.repo.GetAdmin(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("project.not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return p, nil
}

// AdminMonitoring returns a page of every project across every owner with
// its latest reported resource usage (admin-only).
func (u *usecase) AdminMonitoring(ctx context.Context, f AdminListFilter) ([]MonitoringProject, int64, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	items, err := u.repo.ListMonitoring(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountAdmin(ctx, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// mapErr translates a not-found into a 404 and any other error into a 500.
func mapErr(p *Project, err error) (*Project, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("project.not_found", "project not found")
		}
		return nil, httpx.Internal(err)
	}
	return p, nil
}
