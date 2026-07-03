package kyc

import (
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the kyc module. Self-service routes live under
// /kyc (any authenticated user); review routes live under /kyc-verifications
// and require the admin/superuser role.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	self := rg.Group("/kyc", middleware.Auth(verifier))
	{
		self.POST("", h.Submit)
		self.GET("/status", h.Status)
	}

	admin := rg.Group("/kyc-verifications", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		admin.GET("", h.AdminList)
		admin.GET("/:id", h.AdminGet)
		admin.PATCH("/:id/approve", h.Approve)
		admin.PATCH("/:id/reject", h.Reject)
	}
}
