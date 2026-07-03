package ticket

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the ticket module. "/tickets" is owner-scoped (any
// authenticated user manages their own tickets); "/admin/tickets" is
// unscoped and requires an admin/superuser role.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/tickets", middleware.Auth(verifier))
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.GET("/:id", h.Get)
		g.POST("/:id/reply", h.Reply)
		g.POST("/:id/close", h.Close)
	}

	admin := rg.Group("/admin/tickets", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		admin.GET("", h.AdminList)
		admin.GET("/:id", h.AdminGet)
		admin.POST("/:id/respond", h.AdminRespond)
		admin.POST("/:id/close", h.AdminClose)
	}
}
