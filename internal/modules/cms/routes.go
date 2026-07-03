package cms

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the cms module. All routes require authentication;
// every operation is scoped to the caller's project (owner-checked in SQL).
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/cms/projects/:project_id/collections", middleware.Auth(verifier))
	{
		g.GET("", h.ListCollections)
		g.POST("", h.CreateCollection)
		g.GET("/:slug", h.GetCollection)
		g.PATCH("/:slug", h.UpdateCollection)
		g.DELETE("/:slug", h.DeleteCollection)

		g.POST("/:slug/fields", h.AddField)
		g.PATCH("/:slug/fields/:field_name", h.UpdateField)
		g.DELETE("/:slug/fields/:field_name", h.RemoveField)

		g.GET("/:slug/documents", h.FindDocuments)
		g.POST("/:slug/documents", h.InsertDocument)
		g.GET("/:slug/documents/count", h.CountDocuments)
		g.GET("/:slug/documents/:doc_id", h.FindOneDocument)
		g.PATCH("/:slug/documents/:doc_id", h.UpdateDocument)
		g.DELETE("/:slug/documents/:doc_id", h.DeleteDocument)

		g.POST("/:slug/export", h.CreateExportTask)
		g.GET("/:slug/export/:task_id", h.GetExportTaskStatus)
	}
}
