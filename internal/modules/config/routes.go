package config

import (
	"crypto/subtle"
	"os"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the config module:
//   - /admin/settings/{key}       admin/superuser only (JWT)
//   - /shared/config/public       AllowAny — flat ai_agent.* (no secrets)
//   - /shared/config/internal     X-Internal-Token — flat ai_agent.* incl. secrets
//
// The shared/config routes exist for agentv3
// (../agent/src/agent/integrations/backend.py fetch_public_config /
// fetch_internal_config) to read AI provider settings without a user JWT.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/admin/settings", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		g.GET("/:key", h.Get)
		g.PUT("/:key", h.Update)
	}

	shared := rg.Group("/shared/config")
	{
		shared.GET("/public", h.PublicSharedConfig)
		shared.GET("/internal", internalTokenAuth(), h.InternalSharedConfig)
	}
}

// internalTokenAuth gates the engine/agent-facing endpoint behind a shared
// X-Internal-Token header, compared against APP_INTERNAL_TOKEN in constant
// time — same convention as credential/plugin/webhook modules' own copies
// (no shared middleware for this exists yet in this codebase).
func internalTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := os.Getenv("APP_INTERNAL_TOKEN")
		provided := c.GetHeader("X-Internal-Token")
		if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			httpx.Error(c, httpx.Unauthorized("internal.unauthorized", "invalid internal token"))
			return
		}
		c.Next()
	}
}
