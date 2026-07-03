package blog

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the blog module. "/blog" is public (published posts
// only, no auth); "/admin/blog" is unscoped full CRUD and requires an
// admin/superuser role.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	public := rg.Group("/blog")
	{
		public.GET("", h.PublicList)
		public.GET("/:slug", h.PublicGet)
	}

	admin := rg.Group("/admin/blog", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		admin.GET("", h.AdminList)
		admin.POST("", h.AdminCreate)
		admin.GET("/:id", h.AdminGet)
		admin.PATCH("/:id", h.AdminUpdate)
		admin.DELETE("/:id", h.AdminDelete)
	}
}
