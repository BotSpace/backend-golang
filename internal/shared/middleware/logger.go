package middleware

import (
	"time"

	"github.com/botspace/backend/internal/shared/logger"
	"github.com/gin-gonic/gin"
)

// healthPaths are polled frequently by orchestrators; logging them is noise.
var healthPaths = map[string]struct{}{"/healthz": {}, "/readyz": {}}

// Logger logs each request with method/path/status/duration using zap.
func Logger(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := healthPaths[c.Request.URL.Path]; ok {
			c.Next()
			return
		}

		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		if raw != "" {
			path = path + "?" + raw
		}

		fields := []logger.Field{
			logger.String("request_id", RequestIDOf(c)),
			logger.String("method", c.Request.Method),
			logger.String("path", path),
			logger.Int("status", c.Writer.Status()),
			logger.String("client_ip", c.ClientIP()),
			logger.Duration("latency", latency),
		}

		// Surface the underlying cause of failures (e.g. the DB error wrapped
		// in httpx.Internal), which is otherwise only stored in c.Errors and
		// never logged.
		if err := c.Errors.Last(); err != nil {
			fields = append(fields, logger.Err(err.Err))
		}

		switch {
		case c.Writer.Status() >= 500:
			log.Error("http request", fields...)
		case c.Writer.Status() >= 400:
			log.Warn("http request", fields...)
		default:
			log.Info("http request", fields...)
		}
	}
}
