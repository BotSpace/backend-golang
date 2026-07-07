package payment

import (
	"crypto/subtle"
	"os"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts the payment module:
//   - /payment/*            owner-scoped, JWT auth
//   - /admin/payment/*      admin/superuser only, JWT auth
//   - /internal/payment/*   engine-facing, X-Internal-Token auth
//   - /payment/webhooks/*   provider webhooks, no platform auth — Click/Payme
//     verify their own request signature inside the handler.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier) {
	g := rg.Group("/payment", middleware.Auth(verifier))
	{
		g.GET("/transactions", h.ListTransactions)
		g.GET("/transactions/:id", h.GetTransaction)

		g.GET("/history", h.ListHistory)
		g.GET("/history/:id", h.GetHistory)

		g.GET("/earnings", h.GetEarnings)
		g.GET("/earnings/:id", h.GetOrder)

		g.GET("/withdrawals", h.ListWithdrawals)
		g.POST("/withdrawals", h.CreateWithdrawal)
		g.GET("/withdrawals/:id", h.GetWithdrawal)
		g.DELETE("/withdrawals/:id", h.CancelWithdrawal)

		g.POST("/link", h.CreatePaymentLink)
	}

	admin := rg.Group("/admin/payment", middleware.Auth(verifier), middleware.RequireRole("admin", "superuser"))
	{
		admin.GET("/withdrawals", h.AdminListWithdrawals)
		admin.GET("/withdrawals/:id", h.AdminGetWithdrawal)
		admin.PATCH("/withdrawals/:id/approve", h.AdminApproveWithdrawal)
		admin.PATCH("/withdrawals/:id/reject", h.AdminRejectWithdrawal)
		admin.GET("/transactions", h.AdminListTransactions)
		admin.GET("/orders", h.AdminListOrders)
	}

	internal := rg.Group("/internal/payment", internalTokenAuth())
	{
		internal.POST("/orders", h.InternalCreateOrder)
		internal.GET("/orders/:id/status", h.InternalOrderStatus)
	}

	// Provider webhooks: intentionally outside both Auth() and
	// internalTokenAuth() — Click/Payme sign every request themselves
	// (ClickWebhook/PaymeWebhook verify that signature before touching data).
	webhooks := rg.Group("/payment/webhooks")
	{
		webhooks.POST("/click", h.ClickWebhook)
		webhooks.POST("/payme", h.PaymeWebhook)
		webhooks.POST("/paycue", h.PaycueWebhook)
	}

	paycue := rg.Group("/payment/paycue")
	{
		paycue.POST("/update", h.PaycueWebhook)
		paycue.POST("/update/", h.PaycueWebhook)
	}
}

// internalTokenAuth gates the engine-facing endpoints behind a shared
// X-Internal-Token header, compared against APP_INTERNAL_TOKEN in constant
// time. Mirrors internal/modules/credential/routes.go's helper of the same
// name/contract (kept local — modules must not import each other).
func internalTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := os.Getenv("APP_INTERNAL_TOKEN")
		provided := c.GetHeader("X-Internal-Token")
		if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			httpx.Error(c, httpx.Unauthorized("internal.unauthorized", "invalid internal token"))
			return
		}
		c.Next()
	}
}
