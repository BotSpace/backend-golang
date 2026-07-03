package authbot

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the authbot module. Unlike every other module it is
// NOT nested under /api/v1 — pass the root router group so the final paths
// are /authbot/code, /authbot/codes/:id/message-id, /authbot/login (mirrors
// Django's authbot.urls, which is included at /authbot/, not under /api/).
// All routes are public (AllowAny in Django): code issuance is only ever
// called by the trusted Telegram bot service, and login is the auth step
// itself.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	g := rg.Group("/authbot")
	{
		g.POST("/code", h.IssueCode)
		g.PATCH("/codes/:id/message-id", h.SetMessageID)
		g.POST("/login", h.Login)
	}
}
