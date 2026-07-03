package plugin

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the module catalog, project-module management,
// CI/webhook, and internal relay endpoints.
//
// Route ownership note: "/projects/:id/..." subroutes are additive to
// whatever the project module registers on "/projects/:id" — gin requires the
// wildcard name ("id") to match across modules sharing that path segment.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	// Catalog: browsing needs auth; mutations need admin/superuser.
	admin := middleware.RequireRole("admin", "superuser")
	mg := rg.Group("/modules", middleware.Auth(verifier))
	{
		mg.GET("", h.ListModules)
		mg.GET("/:slug", h.GetModule)
		mg.POST("", admin, h.CreateModule)
		mg.PATCH("/:slug", admin, h.UpdateModule)
		mg.DELETE("/:slug", admin, h.DeleteModule)
		mg.POST("/:slug/refresh-manifest", admin, h.RefreshManifest)
	}
	// CI/webhook hooks: no user session, own token/signature checks inside.
	rg.POST("/modules/:slug/redeploy", h.Redeploy)
	rg.POST("/modules/github-webhook", h.GithubWebhook)

	// Project-scoped module management (ownership enforced in SQL).
	pg := rg.Group("/projects/:id", middleware.Auth(verifier))
	{
		pg.GET("/modules", h.ListProjectModules)
		pg.POST("/modules", h.EnableModule)
		pg.DELETE("/modules/:slug", h.DisableModule)
		pg.GET("/module-manifests", h.ModuleManifests)
		pg.GET("/node-manifests", h.NodeManifests)
		pg.GET("/modules-for-engine", h.ModulesForEngine)
	}

	// Internal relay + module-scoped file endpoints — own token checks.
	rg.POST("/module-trigger/:project_uuid", h.ModuleTrigger)
	rg.POST("/module-file", h.UploadModuleFile)
	rg.DELETE("/module-file/:uuid", h.DeleteModuleFile)
}
