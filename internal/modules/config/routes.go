package config

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the config module under /admin/settings. Every route
// requires an authenticated admin or superuser.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/admin/settings", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		g.GET("/:key", h.Get)
		g.PUT("/:key", h.Update)
	}
}
