package ws

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Handler authenticates the handshake and upgrades the connection.
type Handler struct {
	hub      *Hub
	verifier middleware.TokenVerifier
	upgrader websocket.Upgrader
}

func NewHandler(hub *Hub, verifier middleware.TokenVerifier, allowedOrigins []string) *Handler {
	return &Handler{
		hub:      hub,
		verifier: verifier,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     originChecker(allowedOrigins),
		},
	}
}

// Connect authenticates via the access token, then upgrades to WebSocket.
//
// Browsers cannot set the Authorization header on a WebSocket, so the token is
// read from the `token` query param, falling back to the header for non-browser
// clients. Auth happens BEFORE the upgrade so failures return a normal 401.
//
// @Summary      Open a WebSocket connection
// @Description  Authenticated WebSocket. Pass the access token via ?token= or the Authorization header. Echoes/broadcasts text messages to all connected clients.
// @Tags         ws
// @Param        token  query     string  false  "Access token (browsers can't send the Authorization header)"
// @Success      101
// @Failure      401  {object}  httpx.ErrorResponse
// @Router       /ws [get]
func (h *Handler) Connect(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		token = strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	}
	uid, role, err := h.verifier.VerifyAccessToken(token)
	if err != nil || uid == "" {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid or missing token"))
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade already wrote the HTTP error response
	}

	client := &Client{
		hub:    h.hub,
		conn:   conn,
		send:   make(chan []byte, sendBuffer),
		userID: uid,
		role:   role,
		rooms:  make(map[string]struct{}),
	}
	h.hub.register <- client

	go client.writePump()
	go client.readPump()
}

// originChecker validates the handshake Origin against the configured list.
// ["*"] (or empty) allows any origin; otherwise the Origin must match exactly.
func originChecker(allowed []string) func(*http.Request) bool {
	if len(allowed) == 0 || (len(allowed) == 1 && allowed[0] == "*") {
		return func(*http.Request) bool { return true }
	}
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[o] = struct{}{}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false // a browser always sends Origin; reject if absent
		}
		if _, ok := set[origin]; ok {
			return true
		}
		// tolerate trailing-slash / case differences in the host
		if u, err := url.Parse(origin); err == nil {
			_, ok := set[u.Scheme+"://"+u.Host]
			return ok
		}
		return false
	}
}
