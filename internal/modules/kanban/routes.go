package kanban

import (
	"crypto/subtle"
	"os"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/boards", middleware.Auth(verifier))
	{
		g.GET("", h.ListBoards)
		g.POST("", h.CreateBoard)
		g.GET("/:id", h.GetBoard)
		g.PUT("/:id", h.UpdateBoard)
		g.DELETE("/:id", h.ArchiveBoard)
		g.POST("/:id/projects", h.LinkProject)
		g.DELETE("/:id/projects/:project_id", h.UnlinkProject)
		g.POST("/:id/columns", h.CreateColumn)
		g.PUT("/:id/columns/:column_id", h.UpdateColumn)
		g.DELETE("/:id/columns/:column_id", h.DeleteColumn)
		g.POST("/:id/cards", h.CreateCard)
	}
	cards := rg.Group("/cards", middleware.Auth(verifier))
	{
		cards.GET("/:id", h.GetCard)
		cards.PUT("/:id", h.UpdateCard)
		cards.POST("/:id/move", h.MoveCard)
		cards.DELETE("/:id", h.ArchiveCard)
		cards.GET("/:id/activities", h.ListActivities)
		cards.GET("/:id/comments", h.ListComments)
		cards.POST("/:id/comments", h.CreateComment)
	}
	rg.POST("/internal/projects/:project_id/boards/:id/cards/upsert", internalTokenAuth(), h.InternalUpsertCard)
}

func internalTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected, provided := os.Getenv("APP_INTERNAL_TOKEN"), c.GetHeader("X-Internal-Token")
		if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
			httpx.Error(c, httpx.Unauthorized("internal.unauthorized", "invalid internal token"))
			return
		}
		c.Next()
	}
}
