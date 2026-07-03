package ws

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the WebSocket endpoint at GET /ws. Auth is handled
// inside the handshake (query-param/header token), not via gin middleware,
// because the token arrives in the URL for browser clients.
func RegisterRoutes(rg *gin.RouterGroup, h *Handler) {
	rg.GET("/ws", h.Connect)
}
