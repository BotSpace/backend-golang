package debug

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts GET /api/v1/debug/errors and /api/v1/debug/context —
// both owner-scoped (JWT). Every path is registered with AND without a
// trailing slash — mirroring Django's own defensive double-route
// (core/apps/api/urls.py registers "debug/errors/" and "debug/errors" as
// separate named routes) — because the constructor calls these with a
// trailing slash + query string, and relying on gin's RedirectTrailingSlash
// 301 here would re-issue the request cross-origin, potentially dropping
// the Authorization header behind a browser CORS preflight.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/debug", middleware.Auth(verifier))
	{
		g.GET("/errors", h.ListErrors)
		g.GET("/errors/", h.ListErrors)
		g.GET("/context", h.ListContext)
		g.GET("/context/", h.ListContext)
		g.POST("/run-node", h.RunNode)
		g.POST("/run-node/", h.RunNode)
	}
}
