package credential

import (
	"crypto/subtle"
	"os"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the credential module: owner-scoped CRUD (JWT auth)
// plus the engine-only internal resolve endpoints (X-Internal-Token auth).
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/credentials", middleware.Auth(verifier))
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.GET("/types", h.Types)
		g.POST("/oauth/:provider/start", h.OAuthStart)
		g.GET("/:id", h.Get)
		g.PATCH("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}

	internal := rg.Group("/internal/credentials", internalTokenAuth())
	{
		internal.GET("/by-name", h.InternalByName)
		internal.GET("/:id", h.InternalByID)
	}

	// Admin panel's "Credential turlari" page. The registry (registry.go) is
	// code-defined, not a DB table, so this is a read-only list — there is no
	// dynamic type CRUD to expose (create/update/delete a type means editing
	// registry.go and redeploying).
	adminTypes := rg.Group("/admin/credential-types", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	adminTypes.GET("", h.AdminTypes)
}

// internalTokenAuth gates the engine-facing endpoints behind a shared
// X-Internal-Token header, compared against APP_INTERNAL_TOKEN in constant
// time (the Go counterpart of Django's hmac.compare_digest check). The env
// var is read per-request so it can be rotated without a redeploy of this
// module's callers; an unset token always rejects (fail closed).
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
