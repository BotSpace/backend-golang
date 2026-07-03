package file

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the file/resource/used module. File metadata reads
// (Get, Download) are intentionally public — mirrors Django's AllowAny
// retrieve, since file URLs are embedded directly into bot messages/CMS
// content that end users open unauthenticated. Every other route is
// owner-scoped and requires authentication.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	auth := middleware.Auth(verifier)

	f := rg.Group("/file")
	{
		f.GET("", auth, h.List)
		f.POST("", auth, h.Upload)
		f.GET("/:uuid", h.Get)
		f.GET("/:uuid/download", h.Download)
		f.PATCH("/:uuid", auth, h.Update)
		f.DELETE("/:uuid", auth, h.Delete)
	}

	r := rg.Group("/resource", auth)
	{
		r.GET("", h.ListResources)
		r.POST("", h.CreateResource)
		r.GET("/:id", h.GetResource)
	}

	rg.GET("/used", auth, h.Used)
	rg.GET("/used/:project_id", auth, h.Used)
}
