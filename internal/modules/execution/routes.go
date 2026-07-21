package execution

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/executions", middleware.Auth(verifier))
	g.GET("", h.List)
	g.GET("/", h.List)
	g.GET("/:id", h.Get)
	g.GET("/:id/", h.Get)
}
