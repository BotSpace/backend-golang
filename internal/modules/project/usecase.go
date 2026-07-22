package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/botspace/backend/internal/modules/cms"
	"github.com/botspace/backend/internal/modules/release"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Usecase exposes owner-scoped project operations, plus admin-wide
// (unscoped) listing/detail/monitoring reachable only behind an admin route.
type Usecase interface {
	Create(ctx context.Context, in CreateInput) (*Project, error)
	// CreateFromTemplate seeds a brand-new project from a marketplace
	// template's flow — see its doc comment below for the full port notes.
	CreateFromTemplate(ctx context.Context, in CreateFromTemplateInput) (*Project, error)
	Get(ctx context.Context, id, ownerID uuid.UUID) (*Project, error)
	Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error)
	Delete(ctx context.Context, id, ownerID uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]Project, int64, error)

	AdminList(ctx context.Context, f AdminListFilter) ([]AdminProjectSummary, int64, error)
	AdminGet(ctx context.Context, id uuid.UUID) (*AdminProjectDetail, error)
	Monitoring(ctx context.Context, id, ownerID uuid.UUID) (*ProjectMonitoring, error)
	MonitoringHistory(ctx context.Context, id, ownerID uuid.UUID, window string) (*MonitoringHistory, error)
	AdminMonitoring(ctx context.Context, f AdminListFilter) (*MonitoringPage, error)
	AdminMonitoringHistory(ctx context.Context, id uuid.UUID, window string) (*MonitoringHistory, error)

	// IssueCollabToken mints the short-lived RS256 token the constructor
	// presents to the standalone `websocket` service to join this project's
	// real-time collaboration room — see collab_token.go's doc comment for
	// why this deliberately signs with a different key than this backend's
	// own auth tokens. 404s (via mapErr) exactly like Get when id isn't
	// owned by ownerID.
	IssueCollabToken(ctx context.Context, id, ownerID uuid.UUID) (token, room string, err error)
}

// TemplateResolver is the minimal template-module surface CreateFromTemplate
// needs — defined narrowly (same convention as CollectionProvisioner below)
// so this package depends only on the read-only "resolve a purchased
// template into a flow" step, not the whole marketplace usecase.
// template.Usecase satisfies this interface structurally.
type TemplateResolver interface {
	ResolveForProjectCreate(ctx context.Context, idOrSlug string, buyerID uuid.UUID) (flow, collections json.RawMessage, err error)
}

// ReleaseOrchestrator is the minimal release-module surface
// CreateFromTemplate needs: writing arbitrary env vars and mirroring
// Django's create_from_template mode branch (release: create+deploy the
// first release; debug: start the tokenless debug container immediately).
// Same narrow-interface convention as CollectionProvisioner/TemplateResolver.
// release.Usecase satisfies this interface structurally.
type ReleaseOrchestrator interface {
	UpsertEnvironment(ctx context.Context, in release.UpsertEnvironmentInput) (*release.Environment, error)
	CreateRelease(ctx context.Context, projectID, ownerID uuid.UUID) (*release.Release, error)
	StartDebug(ctx context.Context, projectID, ownerID uuid.UUID) (*release.Debug, error)
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
	InsertDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, docs []map[string]any) ([]map[string]any, error)
}

// ManagedBotTokenResolver is the minimal webhook-module surface Create/
// CreateFromTemplate need to resolve a manager-bot-wizard-created bot's
// decrypted token by id, owner-scoped — same narrow-interface convention as
// TemplateResolver/ReleaseOrchestrator/CollectionProvisioner above.
// webhook.Usecase satisfies this interface structurally. A NotFound-shaped
// error (see httpx.NotFound) is expected when the id doesn't exist or isn't
// owned by the caller — resolveManagedBotToken passes it straight through.
type ManagedBotTokenResolver interface {
	ResolveManagedBotToken(ctx context.Context, managedBotID, ownerID uuid.UUID) (string, error)
}

type MetricsProvider interface {
	ProjectMetrics(ctx context.Context) (map[uuid.UUID]LiveMetrics, bool, error)
}

type MetricsHistoryProvider interface {
	ProjectHistory(ctx context.Context, projectID uuid.UUID, window string) (*MonitoringHistory, bool, error)
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
	// tmpl is nil-able: nil means CreateFromTemplate always fails with a 500
	// (there is genuinely nothing it can do without a template resolver —
	// unlike cms/orch, this isn't an optional side effect).
	tmpl TemplateResolver
	// rel is nil-able: nil means CreateFromTemplate skips applying envs and
	// deploying the first release/debug container — the project row + flow
	// + default collections still get created, just not launched.
	rel ReleaseOrchestrator
	// botResolver/testBotToken are nil/empty-able, same optional-dependency
	// convention as orch/cms/tmpl/rel above: nil resolver or an empty
	// testBotToken simply means Get never populates BotMode/BotUsername/
	// BotLink (see populateBotLink). Wired via NewUsecaseWithBotLinks.
	botResolver  BotUsernameResolver
	testBotToken string
	// managedBot is nil-able, same optional-dependency convention as the
	// fields above: nil simply means a non-empty CreateInput.ManagedBotID /
	// CreateFromTemplateInput.ManagedBotID fails with a clear 500 instead of
	// silently being ignored (see resolveManagedBotToken) — wired via
	// NewUsecaseWithManagedBotResolver.
	managedBot ManagedBotTokenResolver
	// collabSigner is nil-able: nil means IssueCollabToken fails with a
	// clear 500 ("collab token signing key not configured") instead of
	// silently minting a token the websocket service can never verify — see
	// collab_token.go. Wired via NewUsecaseWithCollabSigner.
	collabSigner *CollabTokenSigner
	metrics      MetricsProvider
}

// NewUsecase wires the project usecase. orch may be nil — Get then skips
// live debug-status reconciliation and Delete skips stopping containers.
// cmsUC may be nil (including a nil cms.Usecase, which satisfies
// CollectionProvisioner too) — Create then skips provisioning default
// collections. tmplUC/relUC may also be nil — see the usecase struct's field
// comments for what CreateFromTemplate then skips.
func NewUsecase(repo Repository, orch engine.Orchestrator, cmsUC CollectionProvisioner, tmplUC TemplateResolver, relUC ReleaseOrchestrator) Usecase {
	return &usecase{repo: repo, orch: orch, cms: cmsUC, tmpl: tmplUC, rel: relUC}
}

// NewUsecaseWithBotLinks is NewUsecase plus BotMode/BotUsername/BotLink
// resolution on Get — see populateBotLink's doc comment for the release-vs-
// debug logic. testBotToken is the platform's shared TEST_BOT_TOKEN
// (config.Engine.TestBotToken / APP_ENGINE_TEST_BOT_TOKEN), used for
// tokenless (debug) projects — every debug bot is reached through this one
// shared Telegram bot's ?start=<debug_webhook_uuid> deep-link.
func NewUsecaseWithBotLinks(repo Repository, orch engine.Orchestrator, cmsUC CollectionProvisioner, tmplUC TemplateResolver, relUC ReleaseOrchestrator, botResolver BotUsernameResolver, testBotToken string) Usecase {
	return &usecase{repo: repo, orch: orch, cms: cmsUC, tmpl: tmplUC, rel: relUC, botResolver: botResolver, testBotToken: testBotToken}
}

// NewUsecaseWithManagedBotResolver is NewUsecaseWithBotLinks plus resolving
// CreateInput/CreateFromTemplateInput's ManagedBotID into a decrypted
// Telegram token through managedBot (typically the webhook module's
// Usecase, which structurally satisfies ManagedBotTokenResolver) — see
// resolveManagedBotToken. managedBot may be nil, same optional-dependency
// convention as every other field here — see the usecase struct's managedBot
// field comment for what's skipped then.
func NewUsecaseWithManagedBotResolver(repo Repository, orch engine.Orchestrator, cmsUC CollectionProvisioner, tmplUC TemplateResolver, relUC ReleaseOrchestrator, botResolver BotUsernameResolver, testBotToken string, managedBot ManagedBotTokenResolver) Usecase {
	return &usecase{repo: repo, orch: orch, cms: cmsUC, tmpl: tmplUC, rel: relUC, botResolver: botResolver, testBotToken: testBotToken, managedBot: managedBot}
}

// NewUsecaseWithCollabSigner wraps an already-built Usecase (as returned by
// NewUsecase/NewUsecaseWithBotLinks/NewUsecaseWithManagedBotResolver) with a
// CollabTokenSigner for IssueCollabToken — a separate wrapper (rather than a
// growing constructor parameter list) so callers that don't need
// collab-token aren't forced to thread a nil signer through every existing
// constructor call site. signer may be nil (e.g. the PRIVATE_KEY env this
// backend needs isn't configured yet) — IssueCollabToken then fails loudly
// instead of silently minting an unverifiable token, see collab_token.go.
func NewUsecaseWithCollabSigner(uc Usecase, signer *CollabTokenSigner) Usecase {
	u, ok := uc.(*usecase)
	if !ok {
		// No other Usecase implementation exists in this codebase outside
		// tests, and tests construct *usecase directly — this would only
		// fire from a programming error.
		panic("project: NewUsecaseWithCollabSigner requires a Usecase built by this package's own constructors")
	}
	clone := *u
	clone.collabSigner = signer
	return &clone
}

func NewUsecaseWithMetrics(uc Usecase, metrics MetricsProvider) Usecase {
	u, ok := uc.(*usecase)
	if !ok {
		panic("project: NewUsecaseWithMetrics requires a Usecase built by this package's own constructors")
	}
	clone := *u
	clone.metrics = metrics
	return &clone
}

func (u *usecase) Create(ctx context.Context, in CreateInput) (*Project, error) {
	// Runtime capacity is created lazily with the first bot/project, never at
	// account registration. Ensure it before inserting the project so a failed
	// cluster operation cannot leave a bot row whose promised runtime is absent.
	if ensurer, ok := u.orch.(engine.OwnerRuntimeEnsurer); ok {
		if _, err := ensurer.EnsureOwnerRuntime(ctx, in.OwnerID); err != nil {
			return nil, httpx.Internal(fmt.Errorf("ensure owner runtime: %w", err))
		}
	}
	p, err := u.repo.Create(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	token := in.Token
	if strings.TrimSpace(token) == "" && strings.TrimSpace(in.ManagedBotID) != "" {
		if token, err = u.resolveManagedBotToken(ctx, in.ManagedBotID, p.OwnerID); err != nil {
			return nil, err
		}
	}
	if err := u.applyToken(ctx, p, p.OwnerID, token); err != nil {
		return nil, err
	}
	u.provisionDefaultCollections(ctx, p)
	return p, nil
}

// resolveManagedBotToken parses managedBotIDStr as a UUID and resolves it to
// its decrypted Telegram token via the injected ManagedBotTokenResolver —
// mirrors Django's ProjectView._attach_managed_bot / create_from_template's
// managed_bot_id branch (ManagedBotModel.objects.get(id=..., owner=...) +
// get_token()). Returns a 400 for a malformed id, whatever the resolver
// reports (typically a 404) when the bot doesn't exist or isn't owned by
// ownerID, and a 500 when no resolver is wired at all.
func (u *usecase) resolveManagedBotToken(ctx context.Context, managedBotIDStr string, ownerID uuid.UUID) (string, error) {
	if u.managedBot == nil {
		return "", httpx.Internal(errors.New("project: managed bot resolver not configured"))
	}
	managedBotID, err := uuid.Parse(strings.TrimSpace(managedBotIDStr))
	if err != nil {
		return "", httpx.BadRequest("project.managed_bot_invalid_id", "managed_bot_id noto'g'ri formatda")
	}
	return u.managedBot.ResolveManagedBotToken(ctx, managedBotID, ownerID)
}

// CreateFromTemplateInput is the template-seeded create payload — mirrors
// Django's CreateProjectFromTemplate serializer (project.py:391-448), minus
// Credentials (see CreateFromTemplate's doc comment for why).
type CreateFromTemplateInput struct {
	OwnerID uuid.UUID
	// Template is the template's id (UUID) or slug — TemplateResolver tries
	// both, same as the internal RAG feed's GetInternalFeedItem.
	Template string
	Name     string
	// Token is optional, same semantics as CreateInput.Token — ignored
	// entirely when Mode is "debug" (mirrors Django never wiring
	// RELEASE_BOT_TOKEN on the tokenless debug branch).
	Token string
	// ManagedBotID is an alternative to Token, same semantics as
	// CreateInput.ManagedBotID — also ignored entirely when Mode is "debug".
	// mode "release" requires one of Token/ManagedBotID (see
	// Handler.CreateFromTemplate's validation, mirroring Django's
	// CreateProjectFromTemplate.validate).
	ManagedBotID string
	// Mode is "release" (default, deploys the first release immediately) or
	// "debug" (tokenless, starts the debug container immediately instead).
	Mode string
	Envs map[string]string
}

// CreateFromTemplate ports ProjectView.create_from_template
// (core/apps/api/views/project.py:446-575): resolve+purchase-check the
// template (via TemplateResolver, which also loads its flow), seed a
// brand-new project with that flow through the ordinary Create path above —
// so it gets the exact same default users/chat CMS collections +
// RELEASE_BOT_TOKEN wiring a normal create gets, nothing duplicated — apply
// any requested env vars, then either deploy the first release (mode
// "release", the default) or start the tokenless debug container (mode
// "debug"), matching Django's mode branch.
//
// Deliberately NOT ported (kept simple per the task's own instruction):
//   - the `credentials` map / spec-driven collections+modules install
//     (Django's apply_spec_to_project + replace_placeholders_in_flow). A
//     template flow with unresolved "cred:<key>" placeholders deploys as-is
//     (an inert credential_id field) rather than failing outright.
func (u *usecase) CreateFromTemplate(ctx context.Context, in CreateFromTemplateInput) (*Project, error) {
	if u.tmpl == nil {
		return nil, httpx.Internal(errors.New("project: template resolver not configured"))
	}
	flow, collections, err := u.tmpl.ResolveForProjectCreate(ctx, in.Template, in.OwnerID)
	if err != nil {
		return nil, err
	}

	mode := in.Mode
	if mode == "" {
		mode = "release"
	}
	token := in.Token
	managedBotID := in.ManagedBotID
	if mode == "debug" {
		// debug never wires a token (or a managed bot), mirrors Django's
		// tokenless branch — the platform TEST_BOT_TOKEN answers instead.
		token = ""
		managedBotID = ""
	}

	p, err := u.Create(ctx, CreateInput{
		OwnerID:      in.OwnerID,
		Name:         in.Name,
		Flow:         flow,
		Token:        token,
		ManagedBotID: managedBotID,
	})
	if err != nil {
		return nil, err
	}

	// Seed the template's CMS collections (schema + demo documents) so a bot
	// built from a template arrives with its `services`/`orders`/... data, not
	// just the flow. u.Create already provisioned the default users/chat system
	// collections; those are skipped here (get-or-create). Best-effort like the
	// default provisioning — a CMS hiccup must not fail an otherwise-good create.
	u.provisionTemplateCollections(ctx, p, collections)

	if u.rel == nil {
		return p, nil
	}

	// Env application is best-effort like provisionDefaultCollections above
	// — one bad KEY must never abort an otherwise-successful create.
	for key, value := range in.Envs {
		if _, envErr := u.rel.UpsertEnvironment(ctx, release.UpsertEnvironmentInput{
			ProjectID: p.ID, OwnerID: in.OwnerID, UserID: in.OwnerID, Key: key, Value: value,
		}); envErr != nil {
			zap.L().Warn("project create-from-template env failed",
				logger.String("key", key),
				logger.String("project_id", p.ID.String()),
				logger.Err(envErr),
			)
		}
	}

	// Unlike envs, the deploy itself propagates its error (matches Django
	// re-raising ApiException as a 503) — the whole point of create-from-
	// template is a running bot, so a deploy failure must not look like
	// success even though the project row now exists (same orphan-on-error
	// tradeoff Django accepts here).
	if mode == "debug" {
		if _, derr := u.rel.StartDebug(ctx, p.ID, in.OwnerID); derr != nil {
			return nil, derr
		}
	} else {
		if _, rerr := u.rel.CreateRelease(ctx, p.ID, in.OwnerID); rerr != nil {
			return nil, rerr
		}
	}

	// Re-fetch so the response reflects the just-deployed release/debug
	// state (Create's own return value predates the deploy above).
	return u.Get(ctx, p.ID, in.OwnerID)
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
			zap.L().Warn("project create default collection failed",
				logger.String("collection", def.name),
				logger.String("project_id", p.ID.String()),
				logger.Err(err),
			)
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
				zap.L().Warn("project add default collection field failed",
					logger.String("field", f.name),
					logger.String("collection", def.name),
					logger.String("project_id", p.ID.String()),
					logger.Err(err),
				)
			}
		}
	}
}

// templateCollectionSpec is one entry of a template spec's `collections` array
// (see template.extractSpecCollections / Django's build_spec_from_project).
type templateCollectionSpec struct {
	Name         string                    `json:"name"`
	Label        string                    `json:"label"`
	Description  string                    `json:"description"`
	PrimaryField string                    `json:"primary_field"`
	System       bool                      `json:"system"`
	Fields       []templateCollectionField `json:"fields"`
	Data         []map[string]any          `json:"data"`
}

type templateCollectionField struct {
	Name       string          `json:"name"`
	Label      string          `json:"label"`
	Kind       string          `json:"kind"`
	Required   bool            `json:"required"`
	Order      int32           `json:"order"`
	Default    json.RawMessage `json:"default"`
	Validators json.RawMessage `json:"validators"`
}

// provisionTemplateCollections seeds a from-template project with the template's
// own CMS collections — schema, fields and demo `data` documents — so the bot
// arrives with its content (e.g. the QR-menu template's `services`/`orders`),
// not just the flow that reads from them.
//
// Get-or-create per collection: any slug that already exists (the default
// users/chat provisioned by Create, or a re-run) is skipped, mirroring
// provisionDefaultCollections. Best-effort throughout — a CMS failure logs a
// warning and moves on; it must never fail an otherwise-successful create.
func (u *usecase) provisionTemplateCollections(ctx context.Context, p *Project, raw json.RawMessage) {
	if u.cms == nil || len(raw) == 0 {
		return
	}
	var colls []templateCollectionSpec
	if err := json.Unmarshal(raw, &colls); err != nil {
		zap.L().Warn("project create-from-template: invalid collections spec",
			logger.String("project_id", p.ID.String()),
			logger.Err(err),
		)
		return
	}

	for _, col := range colls {
		if col.Name == "" {
			continue
		}
		if _, err := u.cms.GetCollection(ctx, p.OwnerID, p.ID, col.Name); err == nil {
			continue // already provisioned (users/chat, or a re-run)
		}
		name := col.Label
		if name == "" {
			name = col.Name
		}
		if _, err := u.cms.CreateCollection(ctx, p.OwnerID, p.ID, cms.CreateCollectionInput{
			Name:         name,
			Slug:         col.Name,
			Description:  col.Description,
			PrimaryField: col.PrimaryField,
			System:       col.System,
		}); err != nil {
			zap.L().Warn("project create-from-template collection failed",
				logger.String("collection", col.Name),
				logger.String("project_id", p.ID.String()),
				logger.Err(err),
			)
			continue
		}
		for i, f := range col.Fields {
			fc := cms.FieldCreate{
				Name:     f.Name,
				Label:    f.Label,
				Type:     f.Kind,
				Required: f.Required,
				Order:    f.Order,
			}
			if nonNullJSON(f.Default) {
				fc.Default = f.Default
			}
			if nonNullJSON(f.Validators) {
				fc.Validators = f.Validators
			}
			if _, err := u.cms.AddField(ctx, p.OwnerID, p.ID, col.Name, fc); err != nil {
				zap.L().Warn("project create-from-template field failed",
					logger.String("field", f.Name),
					logger.String("collection", col.Name),
					logger.String("project_id", p.ID.String()),
					logger.Int("field_index", i),
					logger.Err(err),
				)
			}
		}
		if len(col.Data) > 0 {
			if _, err := u.cms.InsertDocuments(ctx, p.OwnerID, p.ID, col.Name, col.Data); err != nil {
				zap.L().Warn("project create-from-template documents failed",
					logger.String("collection", col.Name),
					logger.Int("count", len(col.Data)),
					logger.String("project_id", p.ID.String()),
					logger.Err(err),
				)
			}
		}
	}
}

// nonNullJSON reports whether a raw JSON value is present and not literal null,
// so we only forward a field's default/validators when the template set them.
func nonNullJSON(r json.RawMessage) bool {
	t := strings.TrimSpace(string(r))
	return t != "" && t != "null"
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
	if dbg, err := u.repo.GetDebugForOwner(ctx, id, ownerID); err == nil && dbg != nil {
		p.Debug = u.reconcileDebug(ctx, dbg)
	}
	u.populateBotLink(ctx, p)
	return p, nil
}

// IssueCollabToken mirrors Django's ProjectViewSet.collab_token action
// (core/apps/api/views/project.py): GetForOwner's own WHERE owner_id = $2
// 404s a non-owning caller exactly like Django's get_object()-via-
// get_queryset() ownership filter, so no separate ownership check is
// needed here.
func (u *usecase) IssueCollabToken(ctx context.Context, id, ownerID uuid.UUID) (string, string, error) {
	p, err := u.repo.GetForOwner(ctx, id, ownerID)
	if p, err = mapErr(p, err); err != nil {
		return "", "", err
	}
	if u.collabSigner == nil {
		return "", "", httpx.Internal(errors.New(
			"collab token signing key not configured: set PRIVATE_KEY (the same RSA key as k8s secret jwt-secret) on this deployment",
		))
	}
	// projectRef mirrors Django's token["project_id"] = project.id (its
	// integer pk) whenever this project has one (legacy_id, backfilled from
	// Django) — the standalone websocket service's room ("project_<id>")
	// must match what Django's collab_persist_worker.py's int(project_id)
	// expects. A project created after the Go cutover has no legacy_id, so
	// falls back to its own uuid: live relay between connected clients
	// still works (the room is just a string), only the Postgres
	// flow-persistence side then no-ops (collab_persist_worker's int()
	// parse fails and it silently drops that op — see its _decode).
	var projectRef any
	if p.LegacyID != nil {
		projectRef = *p.LegacyID
	} else {
		projectRef = p.ID.String()
	}
	token, err := u.collabSigner.Sign(ownerID, projectRef)
	if err != nil {
		return "", "", httpx.Internal(fmt.Errorf("sign collab token: %w", err))
	}
	return token, fmt.Sprintf("project_%v", projectRef), nil
}

// populateBotLink best-effort fills BotMode/BotUsername/BotLink — mirrors
// Django's create/create_from_template bot_link addition (core/apps/api/
// views/project.py:210-216, 571-575), extended to the plain GET (see
// NewUsecaseWithBotLinks's doc comment for why). A RELEASE_BOT_TOKEN takes
// precedence — the project has its own bot, mode "release", a plain
// https://telegram.me/<username> link. Otherwise, when the platform TEST_BOT_TOKEN
// is configured, mode "debug": the shared test bot's username with the
// required ?start=<debug_webhook_uuid> deep-link, since a bare t.me link to
// the shared test bot can't route to any one specific project (Django's
// comment on the same fix: "bot loyihani tanimaydi" without it). Any
// resolver/lookup failure just leaves these three fields empty rather than
// failing the whole Get — same best-effort convention as reconcileDebug.
func (u *usecase) populateBotLink(ctx context.Context, p *Project) {
	if u.botResolver == nil {
		return
	}
	if token, err := u.repo.GetReleaseBotToken(ctx, p.ID); err == nil && strings.TrimSpace(token) != "" {
		if username, uerr := u.botResolver.ResolveUsername(ctx, token); uerr == nil && username != "" {
			p.BotMode = "release"
			p.BotUsername = username
			p.BotLink = fmt.Sprintf("https://telegram.me/%s", username)
		}
		return
	}
	if u.testBotToken == "" {
		return
	}
	username, err := u.botResolver.ResolveUsername(ctx, u.testBotToken)
	if err != nil || username == "" {
		return
	}
	p.BotMode = "debug"
	p.BotUsername = username
	p.BotLink = fmt.Sprintf("https://telegram.me/%s?start=%s", username, p.DebugWebhookUUID.String())
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
	// A running debug runtime follows the draft flow. In the user-supervisor
	// model StartDebug is a Redis-backed hot swap (not a pod redeploy), so the
	// constructor's ordinary save becomes visible without pressing reload.
	if len(in.Flow) > 0 && u.rel != nil {
		if dbg, dbgErr := u.repo.GetDebugForOwner(ctx, id, ownerID); dbgErr == nil && dbg != nil && (dbg.Status == "running" || dbg.Status == "initializing" || dbg.Status == "pending") {
			if _, reloadErr := u.rel.StartDebug(ctx, id, ownerID); reloadErr != nil {
				return nil, reloadErr
			}
		}
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
		zap.L().Warn("project build setWebhook request failed", logger.Err(err))
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		zap.L().Warn("project setWebhook call failed", logger.Err(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		zap.L().Warn("project setWebhook returned non-200", logger.Int("status", resp.StatusCode))
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
	total, err := u.repo.CountAdmin(ctx, f)
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
// maxMonitoringSortRows caps how many projects a resource sort loads into memory
// to order globally before paginating. Far above the real project count; a
// runaway backstop, not a functional limit.
const maxMonitoringSortRows = 10000

func (u *usecase) AdminMonitoring(ctx context.Context, f AdminListFilter) (*MonitoringPage, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	total, err := u.repo.CountAdmin(ctx, f)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	// A resource sort must order the whole set before paginating (the metric is
	// a per-project subquery, not a real column), so load all matching rows and
	// slice in Go; otherwise page in SQL as before.
	resourceSort := isResourceOrdering(f.Ordering)
	listFilter := f
	if resourceSort {
		listFilter.Limit, listFilter.Offset = maxMonitoringSortRows, 0
	}
	items, err := u.repo.ListMonitoring(ctx, listFilter)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	available := false
	if u.metrics != nil {
		live, ok, err := u.metrics.ProjectMetrics(ctx)
		if err == nil && ok {
			available = true
			for i := range items {
				if m, exists := live[items[i].ID]; exists {
					items[i].CPUCores = m.CPUMilli
					items[i].MemoryBytes = m.MemoryBytes
				}
			}
		}
	}

	// Sort by the live-overlaid values (matches what the UI shows) then paginate.
	if resourceSort {
		sortMonitoringByResource(items, f.Ordering)
		items = pageSlice(items, f.Offset, f.Limit)
	}

	return &MonitoringPage{Items: items, Total: total, PrometheusAvailable: available}, nil
}

// isResourceOrdering reports whether the ordering key selects a resource metric
// sort handled in Go (vs the default created_at DESC done in SQL).
func isResourceOrdering(o string) bool {
	switch o {
	case "cpu_asc", "cpu_desc", "ram_asc", "ram_desc", "disk_asc", "disk_desc", "net_asc", "net_desc":
		return true
	}
	return false
}

func sortMonitoringByResource(items []MonitoringProject, ordering string) {
	field := strings.TrimSuffix(strings.TrimSuffix(ordering, "_desc"), "_asc")
	desc := strings.HasSuffix(ordering, "_desc")
	value := func(m *MonitoringProject) int64 {
		switch field {
		case "cpu":
			return int64(m.CPUCores)
		case "ram":
			return int64(m.MemoryBytes)
		case "disk":
			return m.DiskBytes
		case "net":
			return m.NetBytes
		}
		return 0
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := value(&items[i]), value(&items[j])
		if a == b {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		if desc {
			return a > b
		}
		return a < b
	})
}

func pageSlice(items []MonitoringProject, offset, limit int32) []MonitoringProject {
	start := int(offset)
	if start > len(items) {
		start = len(items)
	}
	end := start + int(limit)
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func (u *usecase) Monitoring(ctx context.Context, id, ownerID uuid.UUID) (*ProjectMonitoring, error) {
	if _, err := mapErr(u.repo.GetForOwner(ctx, id, ownerID)); err != nil {
		return nil, err
	}
	out := &ProjectMonitoring{}
	if u.metrics == nil {
		return out, nil
	}
	live, ok, err := u.metrics.ProjectMetrics(ctx)
	if err != nil || !ok {
		return out, nil
	}
	out.PrometheusAvailable = true
	if m, exists := live[id]; exists {
		out.CPUCores = float64(m.CPUMilli) / 1000
		out.MemoryBytes = int64(m.MemoryBytes)
		out.NetworkRXBps = m.NetworkRXBps
		out.NetworkTXBps = m.NetworkTXBps
	}
	return out, nil
}

func (u *usecase) MonitoringHistory(ctx context.Context, id, ownerID uuid.UUID, window string) (*MonitoringHistory, error) {
	if _, err := mapErr(u.repo.GetForOwner(ctx, id, ownerID)); err != nil {
		return nil, err
	}
	return u.projectHistory(ctx, id, window), nil
}

func (u *usecase) AdminMonitoringHistory(ctx context.Context, id uuid.UUID, window string) (*MonitoringHistory, error) {
	return u.projectHistory(ctx, id, window), nil
}

// projectHistory assembles the monitoring time-series: CPU/RAM/network from the
// Prometheus provider (when available), and Disk from the hourly disksnap
// snapshots — Prometheus has no filesystem metric for the engine pods, so its
// disk range query is always empty and the Disk chart would otherwise be blank.
func (u *usecase) projectHistory(ctx context.Context, id uuid.UUID, window string) *MonitoringHistory {
	history := &MonitoringHistory{}
	if provider, ok := u.metrics.(MetricsHistoryProvider); ok && provider != nil {
		if h, available, err := provider.ProjectHistory(ctx, id, window); err == nil && h != nil {
			history = h
			history.PrometheusAvailable = available
		}
	}
	since := time.Now().Add(-historyWindowDuration(window))
	if disk, err := u.repo.DiskHistory(ctx, id, since); err == nil {
		history.Disk = disk
	}
	return history
}

func historyWindowDuration(window string) time.Duration {
	switch window {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
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
