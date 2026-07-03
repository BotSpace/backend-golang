package debug

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts GET /api/v1/debug/errors — owner-scoped (JWT).
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/debug", middleware.Auth(verifier))
	{
		g.GET("/errors", h.ListErrors)
	}
}
