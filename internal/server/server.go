package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/botspace/backend/internal/modules/administration"
	"github.com/botspace/backend/internal/modules/auth"
	"github.com/botspace/backend/internal/modules/authbot"
	"github.com/botspace/backend/internal/modules/blog"
	"github.com/botspace/backend/internal/modules/cms"
	adminconfig "github.com/botspace/backend/internal/modules/config"
	"github.com/botspace/backend/internal/modules/credential"
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
	// Engine orchestrator: no-op stub until a real Docker/K8s client (or an
	// engine-golang HTTP client) is wired in. CRUD works; deploys stay pending.
	orch := engine.NewStub()

	// user module
	userRepo := user.NewPostgresRepository(s.pool)
	userUC := user.NewUsecase(userRepo)

	// auth module — depends on user usecase + jwt issuer + OTP + refresh store.
	refreshStore := auth.NewRefreshStore(s.pool)
	otpService := auth.NewOTPService(s.pool, auth.NewLogSender(s.log), s.cfg.OTP)
	authUC := auth.NewUsecase(userUC, tokens, otpService, refreshStore)
	auth.RegisterRoutes(v1, auth.NewHandler(authUC))

	// user routes need the verifier from auth
	user.RegisterRoutes(v1, user.NewHandler(userUC), tokens)

	// project module — owner-scoped CRUD, requires auth.
	projectUC := project.NewUsecase(project.NewPostgresRepository(s.pool))
	project.RegisterRoutes(v1, project.NewHandler(projectUC), tokens)

	// plan / subscription — plan enforcement reads/writes the user via userUC.
	planUC := plan.NewUsecase(plan.NewPostgresRepository(s.pool), userUC)
	plan.RegisterRoutes(v1, plan.NewHandler(planUC), tokens)

	// message broadcast — project-scoped.
	messageUC := message.NewUsecase(message.NewPostgresRepository(s.pool))
	message.RegisterRoutes(v1, message.NewHandler(messageUC), tokens)

	// credential — owner-scoped encrypted credentials + internal resolve.
	credentialUC := credential.NewUsecase(credential.NewPostgresRepository(s.pool), cipher)
	credential.RegisterRoutes(v1, credential.NewHandler(credentialUC), tokens)

	// webhook endpoints + managed bots.
	webhookUC := webhook.NewUsecase(webhook.NewPostgresRepository(s.pool), cipher)
	webhook.RegisterRoutes(v1, webhook.NewHandler(webhookUC), tokens)

	// file / resource / used.
	fileUC := file.NewUsecase(file.NewPostgresRepository(s.pool))
	file.RegisterRoutes(v1, file.NewHandler(fileUC), tokens)

	// release / environment / debug — deploys via the engine orchestrator.
	releaseUC := release.NewUsecase(release.NewPostgresRepository(s.pool), orch)
	release.RegisterRoutes(v1, release.NewHandler(releaseUC), tokens)

	// module/plugin catalog + per-project modules.
	pluginUC := plugin.NewUsecase(plugin.NewPostgresRepository(s.pool), orch, plugin.NewHTTPManifestFetcher(), nil)
	plugin.RegisterRoutes(v1, plugin.NewHandler(pluginUC), tokens)

	// template marketplace.
	templateUC := template.NewUsecase(template.NewPostgresRepository(s.pool))
	template.RegisterRoutes(v1, template.NewHandler(templateUC), tokens)

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

	// authbot — Telegram login flow, mounted at /authbot (not /api/v1).
	authbotUC := authbot.NewUsecase(
		authbot.NewPostgresRepository(s.pool),
		userUC,
		authbotTokenAdapter{tokens: tokens, store: refreshStore},
	)
	// authbot.RegisterRoutes adds its own "/authbot" group, so mount it on the
	// root router group (not a "/authbot" group) to avoid a doubled prefix.
	authbot.RegisterRoutes(&s.router.RouterGroup, authbot.NewHandler(authbotUC))

	// cms — collection/field schema in Postgres, documents in Mongo. If Mongo is
	// unreachable we skip the routes rather than fail the whole server, so the
	// rest of the API still serves.
	mongoClient, err := cms.NewMongoClient(ctx)
	if err != nil {
		s.log.Warn("mongo unavailable — cms routes disabled", logger.Err(err))
	} else {
		cmsUC := cms.NewUsecase(cms.NewPostgresRepository(s.pool), cms.NewMongoDocumentRepository(mongoClient))
		cms.RegisterRoutes(v1, cms.NewHandler(cmsUC), tokens)
	}

	// websocket module — hub runs until ctx is cancelled.
	hub := ws.NewHub()
	go hub.Run(ctx)
	ws.RegisterRoutes(v1, ws.NewHandler(hub, tokens, s.cfg.HTTP.CORSOrigins))
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
