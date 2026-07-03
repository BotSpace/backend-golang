package authbot

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the authbot module under whatever group it's given.
// server.go calls this twice: once with the root router group, so the
// Telegram bot service keeps reaching /authbot/code, /authbot/codes/:id/
// message-id, /authbot/login directly (mirrors Django's authbot.urls,
// included at /authbot/, not under /api/); and once with the /api/v1 group,
// so the dashboard can reach the same endpoints at /api/v1/authbot/... (the
// only path shape the prod gateway routes to this backend, via its
// /api/v2 -> /api/v1 rewrite). All routes are public (AllowAny in Django):
// code issuance is only ever called by the trusted Telegram bot service, and
// login is the auth step itself.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	g := rg.Group("/authbot")
	{
		g.POST("/code", h.IssueCode)
		g.PATCH("/codes/:id/message-id", h.SetMessageID)
		g.POST("/login", h.Login)
		// Telegram calls this directly (Django: authbot.urls "webhook/",
		// path("webhook/", ...)). Registered both with and without the
		// trailing slash instead of relying on gin's RedirectTrailingSlash
		// (308) — Telegram's webhook caller is not guaranteed to follow
		// redirects. setWebhook must point at the root-mounted
		// /authbot/webhook/ (see server.go's dual RegisterRoutes call).
		g.POST("/webhook", h.Webhook)
		g.POST("/webhook/", h.Webhook)
	}
}
