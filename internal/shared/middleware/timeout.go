package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// Timeout bounds how long a request's context lives. Downstream calls that
// honor the context (pgx queries, http clients) are cancelled once it elapses,
// preventing a slow dependency from holding a connection open indefinitely.
// timeout <= 0 disables it.
func Timeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if timeout <= 0 {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
