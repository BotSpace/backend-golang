package auth

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts /auth/*. verifier is optional: pass nil to skip
// mounting the JWT-authenticated personal-access-token CRUD (api-tokens) —
// used by the root mount in server.go, which only needs the public
// /token/exchange route reachable at a bare "/auth/..." path (MCP clients
// call agent's mcp/auth.py, which hits backend_base_url + "/auth/token/exchange/"
// directly, with no /api/v1 or /api/v2 prefix — same rationale as the
// authbot dual-mount, see server.go).
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/auth")
	{
		g.POST("/register", h.Register)
		g.POST("/confirm", h.Confirm)
		g.POST("/resend", h.Resend)
		g.POST("/login", h.Login)
		g.POST("/google", h.GoogleLogin)
		g.POST("/refresh", h.Refresh)
		g.POST("/logout", h.Logout)
		g.POST("/reset-password", h.ResetStart)
		g.POST("/reset-password/confirm", h.ResetConfirm)
		g.POST("/reset-password/set", h.ResetSet)

		// POST /auth/token/exchange — "bsp_..." personal access token -> JWT
		// pair. Public (the token itself is the credential); MCP clients call
		// this. Mirrors Django's AllowAny TokenExchangeView.
		g.POST("/token/exchange", h.TokenExchange)

		if verifier != nil {
			authed := g.Group("", middleware.Auth(verifier))
			authed.POST("/api-tokens", h.CreateAPIToken)
			authed.GET("/api-tokens", h.ListAPITokens)
			authed.DELETE("/api-tokens/:id", h.DeleteAPIToken)
		}
	}
}
