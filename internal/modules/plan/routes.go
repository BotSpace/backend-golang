package plan

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the plan/subscription module.
//
//   - GET  /plans                     public, no auth (mirrors PlanViewSet's
//     empty permission_classes).
//   - GET  /subscription              requires a user JWT.
//   - POST /subscription/upgrade      requires a user JWT.
//   - GET  /subscription/usage        requires a user JWT.
//   - POST /subscription/usage/report internal-token auth only (checked
//     inside the handler), deliberately mounted outside the JWT-auth group
//     so agentv3/worker can call it without a user token.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	rg.GET("/plans", h.ListPlans)

	sub := rg.Group("/subscription", middleware.Auth(verifier))
	{
		sub.GET("", h.Current)
		sub.POST("/upgrade", h.Upgrade)
		sub.GET("/usage", h.Usage)
	}

	rg.POST("/subscription/usage/report", h.ReportUsage)

	admin := rg.Group("/admin/plans", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		admin.GET("", h.AdminList)
		admin.POST("", h.AdminCreate)
		admin.PATCH("/:id", h.AdminUpdate)
		admin.DELETE("/:id", h.AdminDelete)
	}
}
