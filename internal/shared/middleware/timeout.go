package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Timeout bounds how long a request's context lives. Downstream calls that
// honor the context (pgx queries, http clients) are cancelled once it elapses,
// preventing a slow dependency from holding a connection open indefinitely.
// timeout <= 0 disables it.
func Timeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Streaming responses own their connection lifetime. Applying the normal
		// request timeout creates a delivery gap every time EventSource reconnects
		// because Redis Pub/Sub does not retain messages for offline subscribers.
		if timeout <= 0 || strings.Contains(c.GetHeader("Accept"), "text/event-stream") {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
