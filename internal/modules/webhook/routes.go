package webhook

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the webhook module.
//
//   - POST /webhook/endpoint             owner-only; mints/reuses a node's webhook URL.
//   - GET  /internal/webhook-hook/:token no bearer auth; gated by X-Internal-Token inside the handler
//     (used by the Go webhook-hook forwarder, not end users).
//   - GET  /managed-bot                  owner-only; lists the caller's managed bots.
//   - GET  /managed-bot/config           owner-only; wizard deep-link config.
//   - POST /managed-bot/claim            owner-only; pre-claims a wizard bot username.
//   - POST /managed-bot/webhook/:secret  no bearer auth; gated by the Telegram secret token inside the handler.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	auth := middleware.Auth(verifier)

	rg.POST("/webhook/endpoint", auth, h.MintEndpoint)
	rg.GET("/internal/webhook-hook/:token", h.ResolveToken)

	mb := rg.Group("/managed-bot")
	{
		mb.GET("", auth, h.ListManagedBots)
		mb.GET("/config", auth, h.ManagedBotConfig)
		mb.POST("/claim", auth, h.ManagedBotClaim)
		mb.POST("/webhook/:secret", h.ManagedBotWebhook)
	}
}
