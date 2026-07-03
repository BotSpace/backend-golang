package release

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the release/debug/environment module. All routes
// require authentication; every operation is scoped to the caller (owner).
//
// Paths mirror Django's router-registered "relase"/"enviroment" viewsets
// (core/apps/api/urls.py) and the debug actions nested under ProjectView
// (core/apps/api/views/project.py: .debug/.stop/.info) — kept under
// /projects/:id/... here rather than importing the project module, per the
// porting guide's no-cross-module-import rule.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("", middleware.Auth(verifier))
	{
		relase := g.Group("/relase")
		relase.GET("", h.ListReleases)
		relase.POST("", h.CreateRelease)
		relase.GET("/:id", h.GetRelease)
		relase.GET("/:id/stop", h.StopRelease)

		env := g.Group("/enviroment")
		env.GET("", h.ListEnvironments)
		env.POST("", h.UpsertEnvironment)
		env.DELETE("/:key", h.DeleteEnvironment)

		debug := g.Group("/projects/:id")
		debug.GET("/debug", h.StartDebug)
		debug.GET("/debug/stop", h.StopDebug)
		debug.GET("/info", h.GetDebug)
	}
}
