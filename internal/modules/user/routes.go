package user

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the user module. The auth verifier is injected so the
// module stays decoupled from the auth package.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/users", middleware.Auth(verifier))
	{
		// self-service: any authenticated user
		g.GET("/me", h.Me)
		g.PATCH("/me", h.UpdateMe)
		g.GET("/me/balance", h.Balance)
		g.POST("/me/password", h.ChangePassword)

		// admin-only management
		admin := g.Group("", middleware.RequireRole(RoleAdmin, RoleSuperuser))
		admin.GET("", h.List)
		admin.GET("/:id", h.GetByID)
		admin.PATCH("/:id/role", h.SetRole)
	}

	// Admin-panel-facing actions, mounted at the top-level /admin/users (not
	// nested under /users) to match the admin panel's expected routing —
	// same pattern as project's /admin/projects and template's /admin/templates.
	adminUsers := rg.Group("/admin/users", middleware.Auth(verifier), middleware.RequireRole(RoleAdmin, RoleSuperuser))
	{
		adminUsers.POST("/:id/ban", h.Ban)
		adminUsers.POST("/:id/unban", h.Unban)
		adminUsers.POST("/:id/balance", h.AdjustBalance)
	}
}
