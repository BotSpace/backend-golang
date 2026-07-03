package chat

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the chat CRM read endpoints under /chat, mirroring
// Django's ChatViewSet action URLs (core/apps/api/urls.py registers it at
// /api/chat/...). Both routes require authentication; ownership is enforced
// in Usecase.ResolveProjectForOwner (SQL join on projects.owner_id), same
// pattern as every other owner-scoped module.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/chat", middleware.Auth(verifier))
	{
		g.GET("/users/:project_id", h.ListUsers)
		g.GET("/messages/:project_id/:chat_id", h.ListMessages)
	}
}
