package project

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the project module. All routes require authentication;
// every operation is scoped to the caller (owner).
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/projects", middleware.Auth(verifier))
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.POST("/create-from-template", h.CreateFromTemplate)
		g.GET("/:id", h.Get)
		g.GET("/:id/collab-token", h.CollabToken)
		g.PATCH("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}

	admin := rg.Group("/admin", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		admin.GET("/projects", h.AdminList)
		admin.GET("/projects/:id", h.AdminGet)
		admin.GET("/monitoring", h.AdminMonitoring)
		admin.GET("/monitoring/:id/history", h.AdminMonitoringHistory)
	}
}
