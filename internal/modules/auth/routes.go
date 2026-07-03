package auth

import "github.com/gin-gonic/gin"

func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	g := rg.Group("/auth")
	{
		g.POST("/register", h.Register)
		g.POST("/confirm", h.Confirm)
		g.POST("/resend", h.Resend)
		g.POST("/login", h.Login)
		g.POST("/refresh", h.Refresh)
		g.POST("/logout", h.Logout)
		g.POST("/reset-password", h.ResetStart)
		g.POST("/reset-password/confirm", h.ResetConfirm)
		g.POST("/reset-password/set", h.ResetSet)
	}
}
