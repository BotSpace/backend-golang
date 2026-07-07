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
		g.POST("/options/:resource", h.Options)
		g.GET("/:id", h.Get)
		g.PATCH("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}

	// OAuth callback: Google redirects the user's *browser* here after
	// consent — there is no Authorization header (this is not a dashboard
	// API call), so it CANNOT sit inside the JWT-guarded group above. The
	// caller is instead authenticated via the signed `state` query param
	// (see oauth_state.go / usecase.go's OAuthCallback). Mounted directly on
	// rg (not `g`) so it resolves to /credentials/oauth/:provider/callback
	// without the Auth middleware. In prod this is reached as
	// /api/v1/credentials/oauth/:provider/callback(/), which the gateway
	// exposes publicly as /api/v2/credentials/oauth/:provider/callback/ —
	// the exact redirect_uri OAuthStart hands to Google (oauth_providers.go).
	//
	// Both the trailing-slash and no-slash forms are registered explicitly
	// (not relying on gin's default RedirectTrailingSlash): gin's own 301
	// redirect targets the path as registered in ITS router — i.e. the
	// backend-internal /api/v1/... form — with no way to make it emit the
	// public /api/v2/... form the browser needs to keep going through the
	// gateway. A client hitting the trailing-slash-mismatched variant would
	// otherwise get redirected straight to a bare /api/v1/... URL, which the
	// gateway does NOT route to this backend (only /api/v2/... is), so it
	// falls through to the old Django deployment's still-live (and now
	// stale) OAuthCallbackView instead of here. Register both forms so no
	// redirect is ever needed.
	rg.GET("/credentials/oauth/:provider/callback", h.OAuthCallback)
	rg.GET("/credentials/oauth/:provider/callback/", h.OAuthCallback)

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
