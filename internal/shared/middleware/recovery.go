package middleware

import (
	"fmt"
	"runtime/debug"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/logger"
	"github.com/gin-gonic/gin"
)

// Recovery converts panics into AppError(500) responses while logging the
// stack trace for debugging. Unlike gin.Recovery it goes through the standard
// httpx error response shape.
func Recovery(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("panic: %v", r)
				log.Error("panic recovered",
					logger.String("request_id", RequestIDOf(c)),
					logger.Err(err),
					logger.String("stack", string(debug.Stack())),
				)
				httpx.Error(c, httpx.Internal(err))
			}
		}()
		c.Next()
	}
}
