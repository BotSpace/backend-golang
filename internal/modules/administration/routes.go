package administration

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the administration module under /admin. Every route
// requires an authenticated admin or superuser — this whole module is
// admin-panel-only, unlike user-facing modules such as user/project.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/admin", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		g.GET("/audit-log", h.ListAuditLog)
		g.GET("/audit-log/:id", h.GetAuditLog)
		g.GET("/stats/dashboard", h.DashboardStats)
	}
}
