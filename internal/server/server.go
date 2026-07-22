package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/botspace/backend/internal/modules/administration"
	"github.com/botspace/backend/internal/modules/auth"
	"github.com/botspace/backend/internal/modules/authbot"
	"github.com/botspace/backend/internal/modules/blog"
	"github.com/botspace/backend/internal/modules/chat"
	"github.com/botspace/backend/internal/modules/cms"
	adminconfig "github.com/botspace/backend/internal/modules/config"
	"github.com/botspace/backend/internal/modules/credential"
	debugmod "github.com/botspace/backend/internal/modules/debug"
	executionmod "github.com/botspace/backend/internal/modules/execution"
	"github.com/botspace/backend/internal/modules/file"
	"github.com/botspace/backend/internal/modules/kyc"
	"github.com/botspace/backend/internal/modules/message"
	"github.com/botspace/backend/internal/modules/oauth"
	"github.com/botspace/backend/internal/modules/payment"
	"github.com/botspace/backend/internal/modules/plan"
	"github.com/botspace/backend/internal/modules/plugin"
	"github.com/botspace/backend/internal/modules/project"
	"github.com/botspace/backend/internal/modules/release"
	"github.com/botspace/backend/internal/modules/template"
	"github.com/botspace/backend/internal/modules/ticket"
	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/modules/webhook"
	"github.com/botspace/backend/internal/shared/buildinfo"
	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/logger"
	"github.com/botspace/backend/internal/shared/media"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/redisq"
	"github.com/botspace/backend/internal/shared/storagex"
	"github.com/botspace/backend/internal/shared/wsevents"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// Server wires the dependency graph. Adding a new module is a single block
// in registerRoutes — repository, usecase, handler, routes.
type Server struct {
	cfg       *config.Config
	log       *logger.Logger
	pool      *pgxpool.Pool
	router    *gin.Engine
	tokens    *auth.TokenIssuer
	fileStore *storagex.Storage
}

// New builds the server. ctx governs background workers (e.g. the WebSocket
// hub + Mongo client), which stop when it is cancelled — pass the
// root/shutdown context. It loads the RS256 key pair and fails if the keys are
// missing/invalid.
func New(ctx context.Context, cfg *config.Config, log *logger.Logger, pool *pgxpool.Pool) (*Server, error) {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Public object-storage base for turning stored media keys into full URLs
	// (template covers, avatars, …). Process-wide; set once here.
	media.Configure(cfg.Storage.URL, cfg.Storage.BucketMedia)

	// Object-storage client for writing/reading uploaded blobs to the B2 media
	// bucket. nil (no credentials configured) makes the file handler return a
	// clear config error instead of silently writing to ephemeral local disk.
	var (
		fileStore *storagex.Storage
		err       error
	)
	if cfg.Storage.URL != "" && cfg.Storage.ID != "" && cfg.Storage.Key != "" {
		fileStore, err = storagex.New(cfg.Storage.URL, cfg.Storage.ID, cfg.Storage.Key, cfg.Storage.Region, cfg.Storage.BucketMedia)
		if err != nil {
			return nil, fmt.Errorf("init storage client: %w", err)
		}
	} else {
		log.Warn("object storage credentials not configured; file uploads will be rejected")
	}

	priv, pub, err := auth.LoadKeys(cfg.JWT, cfg.Env)
	if err != nil {
		return nil, fmt.Errorf("load jwt keys: %w", err)
	}
	tokens := auth.NewTokenIssuer(priv, pub, cfg.JWT)

	r := gin.New()
	r.Use(
		middleware.RequestID(),
		middleware.Recovery(log),
		middleware.Logger(log),
		// Public OAuth discovery/registration/token endpoints must be reachable
		// by browser MCP clients from ANY origin — runs before the allowlist
		// CORS below so it answers their cross-origin preflights.
		middleware.PublicCORS("/.well-known/oauth-authorization-server", "/auth/oauth"),
		middleware.CORS(cfg.HTTP.CORSOrigins),
		middleware.RateLimit(cfg.HTTP.RateLimitRPS, cfg.HTTP.RateLimitBurst),
		middleware.BodyLimit(cfg.HTTP.MaxBodyBytes),
		middleware.Timeout(cfg.HTTP.RequestTimeout),
	)

	// Clients (constructor, agent) call almost every endpoint WITH a trailing
	// slash (Django convention) while routes here are registered WITHOUT one.
	// gin's default RedirectTrailingSlash 301s to the no-slash form, but that
	// redirect is written in the tree-lookup phase BEFORE any middleware (incl.
	// CORS) runs, so a cross-origin request following it gets a 301 with no
	// Access-Control-Allow-Origin and the browser blocks it. We disable it and
	// instead strip the trailing slash in Router() BEFORE gin routes, so "/foo/"
	// matches "/foo" in a SINGLE middleware pass. (The previous approach —
	// NoRoute + gin.HandleContext re-dispatch — re-ran the whole middleware
	// chain and double-wrote the response body: two concatenated JSON errors
	// like `{"error":...missing_token}{"error":...invalid_token}`.)
	r.RedirectTrailingSlash = false
	r.NoRoute(func(c *gin.Context) {
		httpx.Error(c, httpx.NotFound("not_found", "route not found"))
	})

	s := &Server{cfg: cfg, log: log, pool: pool, router: r, tokens: tokens, fileStore: fileStore}
	s.registerRoutes(ctx)
	return s, nil
}

// Router returns the HTTP handler. It wraps the gin engine to strip a trailing
// slash from the request path BEFORE gin routes, so "/foo/" matches the "/foo"
// route in a SINGLE middleware pass — avoiding both gin's CORS-less 301 and the
// double-middleware-execution (duplicate concatenated error bodies) a NoRoute
// re-dispatch would cause. "/" and already-slash-less paths pass through as-is.
func (s *Server) Router() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; len(p) > 1 && strings.HasSuffix(p, "/") {
			if trimmed := strings.TrimRight(p, "/"); trimmed != "" {
				r.URL.Path = trimmed
			}
		}
		s.router.ServeHTTP(w, r)
	})
}

func (s *Server) registerRoutes(ctx context.Context) {
	// Liveness: process is up. Cheap, never touches the DB.
	s.router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": buildinfo.Version})
	})

	// Readiness: dependencies are reachable. Pings the DB so an orchestrator
	// stops routing traffic when Postgres is down.
	s.router.GET("/readyz", func(c *gin.Context) {
		if err := s.pool.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "db": "down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Swagger UI: /swagger/index.html — disabled in production by default.
	if s.cfg.Env != "production" {
		s.router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	v1 := s.router.Group("/api/v1")

	tokens := s.tokens

	// Shared infrastructure -------------------------------------------------
	// Secrets-at-rest cipher (credential data, bot tokens). A missing/short key
	// is fatal only when a module actually needs it; here we log and let the
	// affected modules surface errors at request time.
	cipher, err := crypto.FromEnv()
	if err != nil {
		s.log.Warn("crypto key not set — credential/webhook secrets disabled", logger.Err(err))
	}
	// Engine orchestrator: real KubernetesOrchestrator when engine.mode=k8s
	// (or, if unset, whenever cfg.Env=="production" — this backend now runs
	// as a pod in ns botmother and can reach the cluster API directly).
	// Falls back to the no-op Stub if the cluster is unreachable (local dev
	// without a kubeconfig) so CRUD keeps working with deploys left pending.
	orch := s.buildOrchestrator()

	// user module
	userRepo := user.NewPostgresRepository(s.pool)
	userUC := user.NewUsecase(userRepo)

	// auth module — depends on user usecase + jwt issuer + OTP + refresh store.
	refreshStore := auth.NewRefreshStore(s.pool)
	otpService := auth.NewOTPService(s.pool, auth.NewLogSender(s.log), s.cfg.OTP)
	// Google sign-in is enabled iff APP_GOOGLE_CLIENT_ID is set; a nil
	// verifier makes /auth/google fail with a clear config error instead of
	// a 404 or panic (see auth.Usecase.GoogleLogin).
	var googleVerifier auth.GoogleVerifier
	if s.cfg.Google.ClientID != "" {
		googleVerifier = auth.NewGoogleVerifier(s.cfg.Google.ClientID)
	} else {
		s.log.Warn("APP_GOOGLE_CLIENT_ID not set — /auth/google will reject every request")
	}
	patStore := auth.NewPATStore(s.pool)
	authUC := auth.NewUsecase(userUC, tokens, otpService, refreshStore, googleVerifier, patStore)
	authHandler := auth.NewHandler(authUC)
	auth.RegisterRoutes(v1, authHandler, tokens)
	// Root-mounted POST /auth/token/exchange only (not the whole auth
	// group): agent/src/agent/mcp/auth.py's _exchange_bsp() calls
	// backend_base_url + "/auth/token/exchange/" with no /api/v1 or /api/v2
	// prefix — same "trusted caller bypasses the gateway" rationale as
	// authbot's dual mount below, but narrower (register/login/etc. are NOT
	// re-exposed at root, only this one public, credential-is-the-auth
	// endpoint).
	s.router.POST("/auth/token/exchange", authHandler.TokenExchange)

	// MCP OAuth authorization server — root-mounted at bare /auth/oauth/* and
	// /.well-known/oauth-authorization-server (the URLs the discovery document
	// advertises and MCP clients / the dashboard consent page call). Migrated
	// off the legacy Django `web` server, whose SimpleJWT auth 500'd on the
	// UUID user_id these Go tokens carry.
	oauthUC := oauth.NewUsecase(oauth.NewStore(s.pool), authUC, oauth.Config{
		Issuer:       s.cfg.MCPOAuth.Issuer,
		AuthorizeURL: s.cfg.MCPOAuth.AuthorizeURL,
		AccessTTL:    s.cfg.JWT.AccessTTL,
		CodeTTL:      s.cfg.MCPOAuth.CodeTTL,
	})
	oauth.RegisterRoutes(&s.router.RouterGroup, oauth.NewHandler(oauthUC), tokens)

	// user routes need the verifier from auth
	user.RegisterRoutes(v1, user.NewHandler(userUC), tokens)

	// cms — collection/field schema in Postgres, documents in Mongo. If Mongo is
	// unreachable we skip the routes rather than fail the whole server, so the
	// rest of the API still serves. Built ahead of the project module below so
	// project.Usecase.Create can provision each new project's default system
	// collections through it (nil cmsUC just means that provisioning is
	// skipped, same degrade-gracefully-without-Mongo pattern as the routes).
	var cmsUC cms.Usecase
	mongoClient, err := cms.NewMongoClient(ctx)
	if err != nil {
		s.log.Warn("mongo unavailable — cms routes disabled", logger.Err(err))
	} else {
		cmsUC = cms.NewUsecase(cms.NewPostgresRepository(s.pool), cms.NewMongoDocumentRepository(mongoClient), s.cfg.Engine.MongoDBName)
		cms.RegisterRoutes(v1, cms.NewHandler(cmsUC), tokens)
	}

	// Real-time WS events (release_status/debug_status/node_execution/
	// context/error) are relayed to the constructor by the SEPARATE
	// `websocket` service (JscorpTech/websocket) — this backend only
	// PUBLISHes to the Redis channel it Watches (see internal/shared/
	// wsevents's doc comment). Reuses the same engine Redis the redisq
	// consumer below already talks to (APP_ENGINE_REDIS_*); nil (nothing
	// published, no-op) when that Redis isn't configured — same
	// degrade-gracefully convention as the rest of this function.
	var wsPub wsevents.Publisher
	if addr := s.cfg.Engine.RedisAddr(); addr != "" {
		wsPub = wsevents.NewRedisPublisher(addr, s.cfg.Engine.RedisPassword, s.log)
	} else {
		s.log.Warn("wsevents: engine.redis_host not configured — release/debug WS events disabled")
	}

	// Module usecase is also an input to the engine runtime-asset builder.
	// Routes are registered later with the other catalog endpoints.
	pluginUC := plugin.NewUsecase(plugin.NewPostgresRepository(s.pool), orch, plugin.NewHTTPManifestFetcher(), nil)
	runtimeAssets := func(ctx context.Context, projectID, ownerID uuid.UUID) ([]byte, []byte, error) {
		schemaMap := map[string]map[string]string{}
		if cmsUC != nil {
			collections, err := cmsUC.ListCollections(ctx, ownerID, projectID)
			if err != nil {
				return nil, nil, err
			}
			for _, collection := range collections {
				fields := make(map[string]string, len(collection.Fields))
				for _, field := range collection.Fields {
					fields[field.Name] = field.Type
				}
				schemaMap[collection.Slug] = fields
				// Older flows may reference the display name instead of the slug.
				if collection.Name != collection.Slug {
					schemaMap[collection.Name] = fields
				}
			}
		}
		schemasJSON, err := json.Marshal(schemaMap)
		if err != nil {
			return nil, nil, err
		}
		modules, err := pluginUC.ModulesForEngine(ctx, projectID, ownerID)
		if err != nil {
			return nil, nil, err
		}
		modulesJSON, err := json.Marshal(struct {
			Modules []plugin.EngineModuleInfo `json:"modules"`
		}{Modules: modules})
		if err != nil {
			return nil, nil, err
		}
		return schemasJSON, modulesJSON, nil
	}

	// release / environment / debug — deploys via the engine orchestrator.
	// Built ahead of the project module below so
	// project.Usecase.CreateFromTemplate can apply envs + deploy the first
	// release/debug container through it (nil would just mean
	// CreateFromTemplate skips that deploy step, same degrade-gracefully
	// convention as cmsUC above). NewUsecaseWithEventPublisher (over plain
	// NewUsecase) wires wsPub so every release/debug status transition
	// broadcasts to the constructor — see release/ws_events.go.
	releaseUC := release.NewUsecaseWithRuntimeAssets(release.NewPostgresRepository(s.pool), orch, wsPub, runtimeAssets)
	release.RegisterRoutes(v1, release.NewHandler(releaseUC), tokens)

	// template marketplace. Built ahead of the project module below so
	// project.Usecase.CreateFromTemplate can resolve+purchase-check a
	// template's flow through it.
	templateUC := template.NewUsecase(template.NewPostgresRepository(s.pool))
	template.RegisterRoutes(v1, template.NewHandler(templateUC), tokens)

	// webhook endpoints + managed bots. Built ahead of the project module
	// below (moved up from its former spot further down) so
	// project.Usecase.Create/CreateFromTemplate can resolve a managed_bot_id
	// into its decrypted token through it (see webhookUC's use as a
	// project.ManagedBotTokenResolver just below).
	webhookUC := webhook.NewUsecase(webhook.NewPostgresRepository(s.pool), cipher)
	webhook.RegisterRoutes(v1, webhook.NewHandler(webhookUC), tokens)

	// project module — owner-scoped CRUD, requires auth. orch is passed so
	// Get can live-reconcile the nested debug status (see project.usecase.Get
	// / release status parity fix) and Delete can stop running containers;
	// cmsUC lets Create provision default system CMS collections; templateUC/
	// releaseUC let CreateFromTemplate resolve a template and deploy the
	// resulting project. NewUsecaseWithManagedBotResolver (over plain
	// NewUsecase/NewUsecaseWithBotLinks) wires a getMe-backed bot_link/
	// bot_username/bot_mode resolver + the platform's shared TEST_BOT_TOKEN,
	// so a tokenless (debug) project's GET carries the
	// ?start=<debug_webhook_uuid> deep-link the dashboard's launch wizard
	// needs to open the shared test bot (see project.usecase.populateBotLink)
	// — plus webhookUC as the ManagedBotTokenResolver so Create/
	// CreateFromTemplate can resolve a managed_bot_id into its decrypted
	// Telegram token (see project.usecase.resolveManagedBotToken).
	projectUC := project.NewUsecaseWithManagedBotResolver(
		project.NewPostgresRepository(s.pool), orch, cmsUC, templateUC, releaseUC,
		project.NewTelegramUsernameResolver(), s.cfg.Engine.TestBotToken, webhookUC,
	)
	projectUC = project.NewUsecaseWithMetrics(projectUC, project.NewPrometheusMetricsProvider(s.cfg.Monitor.PrometheusURL))
	// collab-token (GET /projects/:id/collab-token) needs to sign with the
	// SAME RSA private key Django's SIGNING_KEY uses (k8s secret jwt-secret,
	// env PRIVATE_KEY) — NOT this backend's own auth signing key
	// (backend-golang-jwt) — so the standalone websocket service (which
	// verifies against jwt-secret's PUBLIC_KEY) accepts it. See
	// project/collab_token.go's doc comment. A nil signer (PRIVATE_KEY
	// unset) makes IssueCollabToken fail loudly per-request rather than
	// silently mint a token nothing can verify.
	if collabSigner, err := project.NewCollabTokenSignerFromEnv(0); err != nil {
		s.log.Warn("collab-token signer disabled — PRIVATE_KEY set but unparseable", logger.Err(err))
	} else {
		if collabSigner == nil {
			s.log.Warn("collab-token signer disabled — PRIVATE_KEY env not set (mount k8s secret jwt-secret to enable)")
		}
		projectUC = project.NewUsecaseWithCollabSigner(projectUC, collabSigner)
	}
	project.RegisterRoutes(v1, project.NewHandlerWithPublisher(projectUC, wsPub), tokens)

	// plan / subscription — plan enforcement reads/writes the user via userUC.
	planUC := plan.NewUsecase(plan.NewPostgresRepository(s.pool), userUC)
	plan.RegisterRoutes(v1, plan.NewHandler(planUC), tokens)

	// message broadcast — project-scoped.
	messageUC := message.NewUsecase(message.NewPostgresRepository(s.pool))
	message.RegisterRoutes(v1, message.NewHandler(messageUC), tokens)

	// credential — owner-scoped encrypted credentials + internal resolve.
	credentialUC := credential.NewUsecase(credential.NewPostgresRepository(s.pool), cipher, credential.OAuthConfig{
		GoogleClientID:     s.cfg.Google.OAuthClientID,
		GoogleClientSecret: s.cfg.Google.OAuthClientSecret,
		CallbackBaseURL:    s.cfg.Google.OAuthCallbackBase,
		PanelURL:           s.cfg.Google.OAuthPanelURL,
	})
	credential.RegisterRoutes(v1, credential.NewHandler(credentialUC, s.cfg.Google.OAuthPanelURL), tokens)

	// file / resource / used.
	fileUC := file.NewUsecase(file.NewPostgresRepository(s.pool))
	fileHandler := file.NewHandler(fileUC, s.fileStore)
	file.RegisterRoutes(v1, fileHandler, tokens)
	// Engine-facing (Django-compat, non-/v1) file + module-file endpoints the
	// bot engine and its modules call directly with an HMAC project token.
	file.RegisterEngineRoutes(s.router, fileHandler)

	// module/plugin catalog + per-project modules.
	plugin.RegisterRoutes(v1, plugin.NewHandler(pluginUC), tokens)

	// payment — orders, transactions, withdrawals, Click/Payme webhooks.
	paymentRepo := payment.NewPostgresRepository(s.pool)
	paymentUC := payment.NewUsecase(paymentRepo, payment.NewHTTPNotifier(paymentRepo, s.cfg.Engine.InternalToken))
	paymentHandler := payment.NewHandler(paymentUC)
	payment.RegisterRoutes(v1, paymentHandler, tokens)
	s.router.POST("/api/payment/paycue/update", paymentHandler.PaycueWebhook)
	s.router.POST("/api/payment/paycue/update/", paymentHandler.PaycueWebhook)

	// tickets — support.
	ticketUC := ticket.NewUsecase(ticket.NewPostgresRepository(s.pool))
	ticket.RegisterRoutes(v1, ticket.NewHandler(ticketUC), tokens)

	// blog — public + admin.
	blogUC := blog.NewUsecase(blog.NewPostgresRepository(s.pool))
	blog.RegisterRoutes(v1, blog.NewHandler(blogUC), tokens)

	// administration — admin-only audit log + dashboard stats.
	adminUC := administration.NewUsecase(administration.NewPostgresRepository(s.pool))
	administration.RegisterRoutes(v1, administration.NewHandler(adminUC), tokens)

	// KYC verification.
	kycUC := kyc.NewUsecase(kyc.NewPostgresRepository(s.pool))
	kyc.RegisterRoutes(v1, kyc.NewHandler(kycUC), tokens)

	// config — admin-editable general/AI platform settings.
	configUC := adminconfig.NewUsecase(adminconfig.NewPostgresRepository(s.pool))
	adminconfig.RegisterRoutes(v1, adminconfig.NewHandler(configUC), tokens)

	// authbot — Telegram login flow. Dual-mounted:
	//   - root /authbot/*        — called directly by the trusted Telegram
	//     bot service (unchanged, bypasses the gateway's /api/v2 rewrite).
	//   - /api/v1/authbot/*      — called by the dashboard, which only ever
	//     hits paths starting with /api/v1 (rewritten to /api/v2 -> this
	//     backend in prod). Without this second mount the dashboard's
	//     "/authbot/login" call resolved to the OLD Django backend (only
	//     /api/v2/* is routed to this service in prod), breaking Telegram
	//     login with a bogus "Method GET not allowed" Django error.
	// authbot.RegisterRoutes adds its own "/authbot" group under whatever
	// group it's given, so the root mount uses &s.router.RouterGroup (not a
	// "/authbot" group) to avoid a doubled prefix, and the v1 mount reuses
	// the same handler/usecase instance.
	authbotUC := authbot.NewUsecase(
		authbot.NewPostgresRepository(s.pool),
		userUC,
		authbotTokenAdapter{tokens: tokens, store: refreshStore},
	)
	// The Telegram Bot API client backing POST /authbot/webhook/ (Django:
	// management.commands.base.bot, built from env BOT_TOKEN — this backend
	// now runs the botmother_authbot bot logic itself instead of Django).
	authbotHandler := authbot.NewHandler(authbotUC, authbot.NewHTTPBotAPI(os.Getenv("BOT_TOKEN")))
	authbot.RegisterRoutes(&s.router.RouterGroup, authbotHandler)
	authbot.RegisterRoutes(v1, authbotHandler)

	// chat — read-only CRM views (bot end-users + message history) over the
	// same shared Mongo botspace_cms database, "users"/"chat" collections
	// engine-golang writes directly (see internal/modules/chat's package doc
	// for why this has its own small Mongo client instead of importing cms).
	// Same degrade-gracefully-without-Mongo pattern as cms above.
	chatMongo, err := chat.NewMongoDB(ctx, s.cfg.Engine.MongoDBName)
	if err != nil {
		s.log.Warn("mongo unavailable — chat routes disabled", logger.Err(err))
	} else {
		chatUC := chat.NewUsecase(chat.NewPostgresRepository(s.pool), chatMongo)
		chat.RegisterRoutes(v1, chat.NewHandler(chatUC), tokens)
	}

	// debug — persisted flow/node error events (see internal/modules/debug).
	// Read side (GET /api/v1/debug/errors) is owner-scoped HTTP; the write
	// side is the redisq consumer wired below, not an HTTP endpoint (engine
	// -> backend writes are Redis Stream events, not internal HTTP POSTs —
	// see docs/redis-stream-events.md). "debug.context_event" only caches
	// (Redis SET, no Postgres table — see the doc), so wire a context cache
	// off the same engine Redis when configured; without it RecordContext
	// just no-ops (degrade-gracefully-without-Redis, same pattern as the
	// Mongo/Kubernetes wiring below).
	debugRepo := debugmod.NewPostgresRepository(s.pool)
	var debugUC debugmod.Usecase
	if addr := s.cfg.Engine.RedisAddr(); addr != "" {
		debugUC = debugmod.NewUsecaseWithContextCache(debugRepo, debugmod.NewRedisContextCache(addr, s.cfg.Engine.RedisPassword))
	} else {
		debugUC = debugmod.NewUsecase(debugRepo)
	}
	debugmod.RegisterRoutes(v1, debugmod.NewHandler(debugUC), tokens)

	// execution — durable per-update and per-node lifecycle history. The write
	// side shares the ordered Redis Stream used by debug events; the read side
	// is owner-scoped and independent from debug mode.
	executionUC := executionmod.NewUsecase(executionmod.NewPostgresRepository(s.pool))
	executionmod.RegisterRoutes(v1, executionmod.NewHandler(executionUC), tokens)

	// Redis Stream consumer — engine-golang -> backend WRITE path (bot-user
	// upsert, flow/debug errors, token usage, ...). Reuses the same
	// engine.redis_host/port/password config already used to inject
	// REDIS_ADDR into release/debug engine pods, since it's the exact same
	// Redis. Skipped (not fatal) when Redis isn't reachable, same defensive
	// pattern as the Mongo/Kubernetes wiring above — see
	// docs/redis-stream-events.md for the full contract. wsPub (same
	// publisher wired into releaseUC above) lets this also forward
	// node_execution/context/error events to the constructor's WS room —
	// see the consumer.Register bodies below.
	s.startEventConsumer(ctx, debugUC, executionUC, wsPub)
}

const (
	eventsStreamName = "backend:events"
	eventsGroupName  = "backend-golang"
)

// startEventConsumer runs the redisq consumer in a background goroutine
// until ctx is cancelled. See docs/redis-stream-events.md for the event
// contract (XADD shape, registered "task" names, JSON args per task). wsPub
// is nil-able (see wsevents.Publisher) — every consumer.Register body below
// forwards its event to the constructor's ws-hub AFTER the Postgres/Redis
// persistence succeeds, via resolveDebugEventOwner + wsevents.UserRoom (the
// ws-hub's plain access token has no project_id claim, so it always sits in
// a "user_<id>" room — publishing to "project_<ref>" as this used to do
// meant the event never reached it, see internal/shared/wsevents doc
// comment). Each event's own "project" field keeps the engine's raw
// payload.ProjectID as-is (that's what the constructor's client-side filter
// compares against project.id).
func (s *Server) startEventConsumer(ctx context.Context, debugUC debugmod.Usecase, executionUC executionmod.Usecase, wsPub wsevents.Publisher) {
	addr := s.cfg.Engine.RedisAddr()
	if addr == "" {
		s.log.Warn("redisq: engine.redis_host not configured — engine event consumer disabled")
		return
	}
	consumer := redisq.NewConsumer(addr, s.cfg.Engine.RedisPassword, eventsStreamName, eventsGroupName, "backend-1", s.log)

	consumer.Register("execution.lifecycle", func(ctx context.Context, args json.RawMessage) error {
		var payload executionLifecyclePayload
		if err := json.Unmarshal(args, &payload); err != nil {
			return fmt.Errorf("decode execution.lifecycle: %w", err)
		}
		executionID, err := uuid.Parse(payload.ExecutionID)
		if err != nil {
			return fmt.Errorf("decode execution.lifecycle execution_id: %w", err)
		}
		eventTS := time.Now().UTC()
		if payload.TS != 0 {
			eventTS = time.Unix(payload.TS, 0).UTC()
		}
		if payload.Status == string(executionmod.StatusRunning) {
			projectID, err := debugUC.ResolveProjectRef(ctx, payload.ProjectID)
			if err != nil {
				return err
			}
			var updateID *int64
			if payload.TelegramUpdateID != 0 {
				updateID = &payload.TelegramUpdateID
			}
			return executionUC.Start(ctx, executionmod.StartInput{
				ID: executionID, ProjectID: projectID, ChatID: payload.ChatID,
				TelegramUpdateID: updateID, RunID: payload.RunID,
				TriggerType: payload.TriggerType, RuntimeMode: payload.RuntimeMode,
				FlowHash: payload.FlowHash, EventTS: eventTS,
			})
		}
		return executionUC.Finish(ctx, executionmod.FinishInput{
			ID: executionID, Status: executionmod.Status(payload.Status),
			Error: payload.Error, EventTS: eventTS,
		})
	})

	consumer.Register("debug.error_event", func(ctx context.Context, args json.RawMessage) error {
		var payload debugErrorEventPayload
		if err := json.Unmarshal(args, &payload); err != nil {
			return fmt.Errorf("decode debug.error_event: %w", err)
		}
		var eventTS *time.Time
		if payload.TS != 0 {
			t := time.Unix(payload.TS, 0).UTC()
			eventTS = &t
		}
		if err := debugUC.RecordError(ctx, debugmod.RecordErrorInput{
			ProjectRef: payload.ProjectID,
			ChatID:     payload.ChatID,
			NodeID:     payload.NodeID,
			NodeType:   payload.NodeType,
			Level:      payload.Level,
			Message:    payload.Message,
			Detail:     payload.Detail,
			EventTS:    eventTS,
		}); err != nil {
			return err
		}
		if wsPub != nil {
			if room, project, ok := s.wsForwardTarget(ctx, debugUC, "debug.error_event", payload.ProjectID); ok {
				level := payload.Level
				if level == "" {
					level = "error" // core/domain/ws.py ErrorEvent's default
				}
				wsPub.Publish(ctx, room, wsevents.EventError, map[string]any{
					"project":   project,
					"chat_id":   payload.ChatID,
					"node_id":   payload.NodeID,
					"node_type": payload.NodeType,
					"level":     level,
					"message":   payload.Message,
					"detail":    payload.Detail,
					"ts":        payload.TS,
				})
			}
		}
		return nil
	})

	consumer.Register("debug.node_event", func(ctx context.Context, args json.RawMessage) error {
		var payload debugNodeEventPayload
		if err := json.Unmarshal(args, &payload); err != nil {
			return fmt.Errorf("decode debug.node_event: %w", err)
		}
		var eventTS *time.Time
		if payload.TS != 0 {
			t := time.Unix(payload.TS, 0).UTC()
			eventTS = &t
		}
		if err := debugUC.RecordNodeEvent(ctx, debugmod.RecordNodeEventInput{
			ProjectRef: payload.ProjectID,
			ChatID:     payload.ChatID,
			NodeID:     payload.NodeID,
			NodeType:   payload.NodeType,
			Status:     payload.Status,
			Error:      payload.Error,
			RunID:      payload.RunID,
			Seq:        payload.Seq,
			EventTS:    eventTS,
		}); err != nil {
			return err
		}
		// Live canvas feedback must not depend on the durable execution ledger.
		// Publish first; if the idempotent ledger write below fails, Redis keeps
		// the event pending and retries it after restart without freezing the UI.
		if wsPub != nil {
			if room, project, ok := s.wsForwardTarget(ctx, debugUC, "debug.node_event", payload.ProjectID); ok {
				wsPub.Publish(ctx, room, wsevents.EventNodeExecution, nodeExecutionWSPayload(project, payload))
			}
		}
		if payload.ExecutionID != "" {
			executionID, err := uuid.Parse(payload.ExecutionID)
			if err != nil {
				return fmt.Errorf("decode debug.node_event execution_id: %w", err)
			}
			status := executionmod.Status(payload.Status)
			switch payload.Status {
			case "success":
				status = executionmod.StatusSucceeded
			case "error":
				status = executionmod.StatusFailed
			}
			if err := executionUC.RecordNodeRun(ctx, executionmod.NodeRunInput{
				ExecutionID: executionID, NodeID: payload.NodeID, NodeType: payload.NodeType,
				Attempt: payload.Attempt, Status: status, Error: payload.Error,
				Sequence: payload.Seq, EventTS: eventTSOrNow(eventTS),
			}); err != nil {
				return err
			}
		}
		return nil
	})

	consumer.Register("debug.context_event", func(ctx context.Context, args json.RawMessage) error {
		var payload debugContextEventPayload
		if err := json.Unmarshal(args, &payload); err != nil {
			return fmt.Errorf("decode debug.context_event: %w", err)
		}
		var eventTS *time.Time
		if payload.TS != 0 {
			t := time.Unix(payload.TS, 0).UTC()
			eventTS = &t
		}
		if err := debugUC.RecordContext(ctx, debugmod.RecordContextEventInput{
			ProjectRef: payload.ProjectID,
			ChatID:     payload.ChatID,
			Variables:  payload.Variables,
			EventTS:    eventTS,
		}); err != nil {
			return err
		}
		if wsPub != nil {
			if room, project, ok := s.wsForwardTarget(ctx, debugUC, "debug.context_event", payload.ProjectID); ok {
				wsPub.Publish(ctx, room, wsevents.EventContext, map[string]any{
					"project":   project,
					"chat_id":   payload.ChatID,
					"variables": payload.Variables,
					"ts":        payload.TS,
				})
			}
		}
		return nil
	})

	go func() {
		s.log.Info("redisq: engine event consumer starting",
			logger.String("stream", eventsStreamName), logger.String("group", eventsGroupName))
		if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("redisq: consumer stopped", logger.Err(err))
		}
		_ = consumer.Close()
	}()
}

// resolveDebugEventOwner resolves the engine's raw project_id reference
// (debugmod.Usecase.ResolveProjectRef — legacy int pk as a string, or Django
// project.uuid) down to this backend's internal project row, then to that
// project's owner_id, so the caller can publish to wsevents.UserRoom(owner).
// Best-effort: any resolution failure (unknown ref, project gone) is
// returned as-is so callers just skip the WS forward, matching the
// try/except-swallowing convention of every other WS publish in this repo.
// resolveDebugEvent maps an engine event's raw project ref (the Django `uuid`
// column) to BOTH this backend's internal project id (the `id` column — what
// the constructor knows as project.id and filters WS events on) and the owner
// id (the ws-hub's room is user_<owner>). Returning the resolved id is what
// lets the "project" field in the forwarded payload match project.id, instead
// of leaking the raw uuid-column ref which the client would filter out.
func resolveDebugEvent(ctx context.Context, debugUC debugmod.Usecase, projectRef string) (projectID, ownerID uuid.UUID, err error) {
	projectID, err = debugUC.ResolveProjectRef(ctx, projectRef)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	ownerID, err = debugUC.GetProjectOwner(ctx, projectID)
	return projectID, ownerID, err
}

// wsForwardTarget resolves the engine's raw project ref to the WS room
// (user_<owner>) + the project-id string for the payload, LOGGING the outcome
// so the engine→backend:events→WS path is fully observable end to end. On a
// resolve failure it logs the reason and returns ok=false — the forward is
// skipped explicitly, never silently swallowed (which is what used to hide
// "project.not_found" while node colors / variables / errors never arrived).
func (s *Server) wsForwardTarget(ctx context.Context, debugUC debugmod.Usecase, task, projectRef string) (room, project string, ok bool) {
	projectID, ownerID, err := resolveDebugEvent(ctx, debugUC, projectRef)
	if err != nil {
		s.log.Warn("ws forward skipped: project resolve failed",
			logger.String("task", task),
			logger.String("project_ref", projectRef),
			logger.Err(err))
		return "", "", false
	}
	room = wsevents.UserRoom(ownerID)
	project = projectID.String()
	s.log.Info("ws forward",
		logger.String("task", task),
		logger.String("room", room),
		logger.String("project", project))
	return room, project, true
}

// debugErrorEventPayload is the "args" shape for the "debug.error_event"
// redis stream task — see docs/redis-stream-events.md. project_id is the
// engine's raw project reference (Django legacy int pk as a string, or
// Django project.uuid), resolved server-side by debug.Usecase.ResolveProjectRef.
type debugErrorEventPayload struct {
	ProjectID string `json:"project_id"`
	ChatID    int64  `json:"chat_id"`
	NodeID    string `json:"node_id"`
	NodeType  string `json:"node_type"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Detail    string `json:"detail"`
	TS        int64  `json:"ts"`
}

// debugNodeEventPayload is the "args" shape for the "debug.node_event"
// redis stream task — see docs/redis-stream-events.md.
type debugNodeEventPayload struct {
	ProjectID   string `json:"project_id"`
	ChatID      int64  `json:"chat_id"`
	NodeID      string `json:"node_id"`
	NodeType    string `json:"node_type"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	TS          int64  `json:"ts"`
	RunID       int64  `json:"run_id"`
	Seq         int64  `json:"seq"`
	ExecutionID string `json:"execution_id"`
	Attempt     int32  `json:"attempt"`
}

func nodeExecutionWSPayload(project string, payload debugNodeEventPayload) map[string]any {
	return map[string]any{
		"project":      project,
		"chat_id":      payload.ChatID,
		"node_id":      payload.NodeID,
		"node_type":    payload.NodeType,
		"status":       payload.Status,
		"error":        payload.Error,
		"ts":           payload.TS,
		"run_id":       payload.RunID,
		"seq":          payload.Seq,
		"execution_id": payload.ExecutionID,
		"attempt":      payload.Attempt,
	}
}

// executionLifecyclePayload is emitted at the boundary of one inbound update.
// Running events carry project metadata; terminal/waiting events only need the
// stable execution id.
type executionLifecyclePayload struct {
	ExecutionID      string `json:"execution_id"`
	ProjectID        string `json:"project_id"`
	ChatID           int64  `json:"chat_id"`
	TelegramUpdateID int64  `json:"telegram_update_id"`
	RunID            int64  `json:"run_id"`
	TriggerType      string `json:"trigger_type"`
	RuntimeMode      string `json:"runtime_mode"`
	FlowHash         string `json:"flow_hash"`
	Status           string `json:"status"`
	Error            string `json:"error"`
	TS               int64  `json:"ts"`
}

func eventTSOrNow(ts *time.Time) time.Time {
	if ts != nil {
		return *ts
	}
	return time.Now().UTC()
}

// debugContextEventPayload is the "args" shape for the "debug.context_event"
// redis stream task — see docs/redis-stream-events.md. Variables is kept as
// raw JSON: this backend only caches it (Redis SET, no schema), never
// interprets its shape.
type debugContextEventPayload struct {
	ProjectID string          `json:"project_id"`
	ChatID    int64           `json:"chat_id"`
	Variables json.RawMessage `json:"variables"`
	TS        int64           `json:"ts"`
}

// buildOrchestrator picks the engine.Orchestrator implementation: the real
// Kubernetes client when cfg.Engine.Mode says so (explicitly, or implicitly
// via cfg.Env=="production"), falling back to the no-op Stub whenever the
// cluster isn't reachable (e.g. local dev without a kubeconfig) so the API
// still serves CRUD — deploys just report as pending instead of erroring.
func (s *Server) buildOrchestrator() engine.Orchestrator {
	wantK8s := s.cfg.Engine.Mode == "k8s" || (s.cfg.Engine.Mode == "" && s.cfg.Env == "production")
	if !wantK8s {
		s.log.Info("engine orchestrator: stub", logger.String("engine_mode", s.cfg.Engine.Mode))
		return engine.NewStub()
	}

	orch, err := engine.NewKubernetesOrchestrator(engine.KubernetesConfig{
		Namespace:          s.cfg.Engine.Namespace,
		ImageRepository:    s.cfg.Engine.ImageRepository,
		ImageTag:           s.cfg.Engine.ImageTag,
		RegistrySecretName: s.cfg.Engine.RegistrySecretName,
		APIURL:             s.cfg.Engine.APIURL,
		InternalToken:      s.cfg.Engine.InternalToken,
		TestBotToken:       s.cfg.Engine.TestBotToken,
		RedisAddr:          s.cfg.Engine.RedisAddr(),
		RedisPassword:      s.cfg.Engine.RedisPassword,
		MongoAddr:          s.cfg.Engine.MongoAddr,
		MongoDBName:        s.cfg.Engine.MongoDBName,
	})
	if err != nil {
		s.log.Warn("engine orchestrator: kubernetes unavailable — falling back to stub", logger.Err(err))
		return engine.NewStub()
	}
	s.log.Info("engine orchestrator: kubernetes",
		logger.String("namespace", s.cfg.Engine.Namespace),
		logger.String("image", s.cfg.Engine.Image()),
	)
	return orch
}

// authbotTokenAdapter adapts the auth TokenIssuer + refresh store to the
// authbot module's TokenIssuer port, so authbot never imports package auth.
type authbotTokenAdapter struct {
	tokens *auth.TokenIssuer
	store  auth.RefreshStore
}

func (a authbotTokenAdapter) IssueTokens(ctx context.Context, userID uuid.UUID, role string) (authbot.TokenPair, error) {
	access, accessExp, err := a.tokens.NewAccessToken(userID, role)
	if err != nil {
		return authbot.TokenPair{}, err
	}
	refresh, jti, refreshExp, err := a.tokens.NewRefreshToken(userID)
	if err != nil {
		return authbot.TokenPair{}, err
	}
	if err := a.store.Save(ctx, jti, userID, refreshExp); err != nil {
		return authbot.TokenPair{}, err
	}
	return authbot.TokenPair{
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
		TokenType:        "Bearer",
	}, nil
}
