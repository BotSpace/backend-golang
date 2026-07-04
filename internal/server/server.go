package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	"github.com/botspace/backend/internal/modules/file"
	"github.com/botspace/backend/internal/modules/kyc"
	"github.com/botspace/backend/internal/modules/message"
	"github.com/botspace/backend/internal/modules/payment"
	"github.com/botspace/backend/internal/modules/plan"
	"github.com/botspace/backend/internal/modules/plugin"
	"github.com/botspace/backend/internal/modules/project"
	"github.com/botspace/backend/internal/modules/release"
	"github.com/botspace/backend/internal/modules/template"
	"github.com/botspace/backend/internal/modules/ticket"
	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/modules/webhook"
	"github.com/botspace/backend/internal/modules/ws"
	"github.com/botspace/backend/internal/shared/buildinfo"
	"github.com/botspace/backend/internal/shared/config"
	"github.com/botspace/backend/internal/shared/crypto"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/botspace/backend/internal/shared/logger"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/redisq"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// Server wires the dependency graph. Adding a new module is a single block
// in registerRoutes — repository, usecase, handler, routes.
type Server struct {
	cfg    *config.Config
	log    *logger.Logger
	pool   *pgxpool.Pool
	router *gin.Engine
	tokens *auth.TokenIssuer
}

// New builds the server. ctx governs background workers (e.g. the WebSocket
// hub + Mongo client), which stop when it is cancelled — pass the
// root/shutdown context. It loads the RS256 key pair and fails if the keys are
// missing/invalid.
func New(ctx context.Context, cfg *config.Config, log *logger.Logger, pool *pgxpool.Pool) (*Server, error) {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
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
		middleware.CORS(cfg.HTTP.CORSOrigins),
		middleware.RateLimit(cfg.HTTP.RateLimitRPS, cfg.HTTP.RateLimitBurst),
		middleware.BodyLimit(cfg.HTTP.MaxBodyBytes),
		middleware.Timeout(cfg.HTTP.RequestTimeout),
	)

	s := &Server{cfg: cfg, log: log, pool: pool, router: r, tokens: tokens}
	s.registerRoutes(ctx)
	return s, nil
}

func (s *Server) Router() http.Handler { return s.router }

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

	// release / environment / debug — deploys via the engine orchestrator.
	// Built ahead of the project module below so
	// project.Usecase.CreateFromTemplate can apply envs + deploy the first
	// release/debug container through it (nil would just mean
	// CreateFromTemplate skips that deploy step, same degrade-gracefully
	// convention as cmsUC above).
	releaseUC := release.NewUsecase(release.NewPostgresRepository(s.pool), orch)
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
	project.RegisterRoutes(v1, project.NewHandler(projectUC), tokens)

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
	file.RegisterRoutes(v1, file.NewHandler(fileUC), tokens)

	// module/plugin catalog + per-project modules.
	pluginUC := plugin.NewUsecase(plugin.NewPostgresRepository(s.pool), orch, plugin.NewHTTPManifestFetcher(), nil)
	plugin.RegisterRoutes(v1, plugin.NewHandler(pluginUC), tokens)

	// payment — orders, transactions, withdrawals, Click/Payme webhooks.
	paymentUC := payment.NewUsecase(payment.NewPostgresRepository(s.pool), payment.NewNoopNotifier())
	payment.RegisterRoutes(v1, payment.NewHandler(paymentUC), tokens)

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

	// websocket module — hub runs until ctx is cancelled.
	hub := ws.NewHub()
	go hub.Run(ctx)
	ws.RegisterRoutes(v1, ws.NewHandler(hub, tokens, s.cfg.HTTP.CORSOrigins))

	// Redis Stream consumer — engine-golang -> backend WRITE path (bot-user
	// upsert, flow/debug errors, token usage, ...). Reuses the same
	// engine.redis_host/port/password config already used to inject
	// REDIS_ADDR into release/debug engine pods, since it's the exact same
	// Redis. Skipped (not fatal) when Redis isn't reachable, same defensive
	// pattern as the Mongo/Kubernetes wiring above — see
	// docs/redis-stream-events.md for the full contract.
	s.startEventConsumer(ctx, debugUC)
}

const (
	eventsStreamName = "backend:events"
	eventsGroupName  = "backend-golang"
)

// startEventConsumer runs the redisq consumer in a background goroutine
// until ctx is cancelled. See docs/redis-stream-events.md for the event
// contract (XADD shape, registered "task" names, JSON args per task).
func (s *Server) startEventConsumer(ctx context.Context, debugUC debugmod.Usecase) {
	addr := s.cfg.Engine.RedisAddr()
	if addr == "" {
		s.log.Warn("redisq: engine.redis_host not configured — engine event consumer disabled")
		return
	}
	consumer := redisq.NewConsumer(addr, s.cfg.Engine.RedisPassword, eventsStreamName, eventsGroupName, "backend-1", s.log)

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
		return debugUC.RecordError(ctx, debugmod.RecordErrorInput{
			ProjectRef: payload.ProjectID,
			ChatID:     payload.ChatID,
			NodeID:     payload.NodeID,
			NodeType:   payload.NodeType,
			Level:      payload.Level,
			Message:    payload.Message,
			Detail:     payload.Detail,
			EventTS:    eventTS,
		})
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
		return debugUC.RecordNodeEvent(ctx, debugmod.RecordNodeEventInput{
			ProjectRef: payload.ProjectID,
			ChatID:     payload.ChatID,
			NodeID:     payload.NodeID,
			NodeType:   payload.NodeType,
			Status:     payload.Status,
			Error:      payload.Error,
			RunID:      payload.RunID,
			Seq:        payload.Seq,
			EventTS:    eventTS,
		})
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
		return debugUC.RecordContext(ctx, debugmod.RecordContextEventInput{
			ProjectRef: payload.ProjectID,
			ChatID:     payload.ChatID,
			Variables:  payload.Variables,
			EventTS:    eventTS,
		})
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
	ProjectID string `json:"project_id"`
	ChatID    int64  `json:"chat_id"`
	NodeID    string `json:"node_id"`
	NodeType  string `json:"node_type"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	TS        int64  `json:"ts"`
	RunID     int64  `json:"run_id"`
	Seq       int64  `json:"seq"`
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
