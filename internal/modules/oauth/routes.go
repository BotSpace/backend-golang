package oauth

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the MCP OAuth authorization server at bare, un-prefixed
// paths (no /api/v1) so the URLs match what the discovery document advertises
// and what MCP clients / the dashboard consent page already call — same
// root-mount rationale as auth's /auth/token/exchange and authbot.
//
// Only slashless forms are registered: the server's Router() wrapper strips a
// trailing slash before gin routes, so /auth/oauth/token/ resolves here too.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	rg.GET("/.well-known/oauth-authorization-server", h.Metadata)

	oa := rg.Group("/auth/oauth")
	{
		oa.POST("/register", h.Register)
		oa.GET("/authorize/meta", h.AuthorizeMeta)
		oa.POST("/token", h.Token)

		// approve is the only authenticated endpoint — it binds the code to the
		// logged-in user (UUID) taken from the Bearer JWT.
		authed := oa.Group("", middleware.Auth(verifier))
		authed.POST("/authorize/approve", h.Approve)
	}
}
