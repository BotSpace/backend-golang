package template

import (
	"strings"

	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// AdminRole/SuperuserRole mirror user.RoleAdmin/RoleSuperuser as plain string
// constants - the guide asks modules not to import each other, so this module
// re-declares the two role names it needs for RequireRole.
const (
	adminRole     = "admin"
	superuserRole = "superuser"
)

// RegisterRoutes mounts the whole template marketplace under /template,
// mirroring core/apps/template/urls.py's router prefix.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/template")

	// Internal RAG feed — X-Internal-Token gated, not JWT auth. Mounted
	// before the public group so its static "/internal/..." segments never
	// have to compete with "/list/:slug"-style param routes.
	internal := g.Group("/internal", internalTokenAuth())
	{
		internal.GET("/list", h.InternalList)
		internal.GET("/:identifier", h.InternalGet)
	}

	// Public catalog - optionalAuth resolves is_liked/is_ordered when a valid
	// token is present, but never rejects an anonymous request.
	public := g.Group("", optionalAuth(verifier))
	{
		public.GET("/list", h.List)
		public.GET("/list/:slug", h.GetBySlug)
		public.GET("/category", h.ListCategories)
		public.GET("/feature", h.ListFeatures)
	}

	// Authenticated actions.
	auth := g.Group("", middleware.Auth(verifier))
	{
		auth.POST("/list/:slug/like", h.ToggleLike)

		auth.POST("/submissions", h.CreateSubmission)
		auth.GET("/submissions", h.ListSubmissions)
		auth.GET("/submissions/:id", h.GetSubmission)
		auth.PATCH("/submissions/:id", h.UpdateSubmission)
		auth.DELETE("/submissions/:id", h.DeleteSubmission)

		auth.POST("/orders", h.CreateOrder)
		auth.GET("/orders", h.ListOrders)
		auth.GET("/orders/:id", h.GetOrder)
		auth.POST("/orders/:id/pay", h.PayOrder)

		auth.POST("/withdrawals", h.CreateWithdrawal)
		auth.GET("/withdrawals", h.ListWithdrawals)
		auth.GET("/withdrawals/:id", h.GetWithdrawal)
		auth.DELETE("/withdrawals/:id", h.DeleteWithdrawal)
	}

	// Admin-only management.
	admin := g.Group("", middleware.Auth(verifier), middleware.RequireRole(adminRole, superuserRole))
	{
		admin.POST("/category", h.CreateCategory)
		admin.POST("/feature", h.CreateFeature)

		admin.GET("/submissions/admin", h.ListSubmissionsAdmin)
		admin.PATCH("/submissions/:id/approve", h.ApproveSubmission)
		admin.PATCH("/submissions/:id/reject", h.RejectSubmission)

		admin.GET("/withdrawals/admin", h.ListWithdrawalsAdmin)
		admin.PATCH("/withdrawals/:id/approve", h.ApproveWithdrawal)
		admin.PATCH("/withdrawals/:id/reject", h.RejectWithdrawal)
	}

	// Admin-wide template catalog management, mounted at the top-level
	// /admin/templates (not nested under /template) to match the admin
	// panel's routing convention — same pattern as project's /admin/projects.
	adminTemplates := rg.Group("/admin/templates", middleware.Auth(verifier), middleware.RequireRole(adminRole, superuserRole))
	{
		adminTemplates.GET("", h.AdminList)
		adminTemplates.POST("", h.AdminCreate)
		adminTemplates.GET("/:id", h.AdminGet)
		adminTemplates.PATCH("/:id", h.AdminUpdate)
		adminTemplates.DELETE("/:id", h.AdminDelete)
		adminTemplates.PATCH("/:id/verify", h.AdminVerify)
		adminTemplates.PATCH("/:id/reject", h.AdminReject)
	}
}

// optionalAuth resolves the caller's user id when a valid bearer token is
// present, without ever blocking the request (mirrors DRF AllowAny views that
// still read request.user when a token happens to be attached, e.g.
// BaseTemplateSerializer.get_is_order). It sets the same context key
// middleware.UserID reads ("user_id"), so downstream handlers can use
// authUserID/viewerID interchangeably regardless of which auth middleware ran.
func optionalAuth(v middleware.TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if strings.HasPrefix(raw, "Bearer ") {
			if uid, role, err := v.VerifyAccessToken(strings.TrimPrefix(raw, "Bearer ")); err == nil {
				c.Set("user_id", uid)
				c.Set("user_role", role)
			}
		}
		c.Next()
	}
}
