package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	ctxRequestIDKey = "request_id"
	requestIDHeader = "X-Request-ID"
)

// RequestID assigns a unique id to each request (reusing an inbound
// X-Request-ID if present), stores it in the context, and echoes it back in
// the response header so logs and clients can correlate a request.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(ctxRequestIDKey, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

// RequestIDOf returns the request id stored by the RequestID middleware.
func RequestIDOf(c *gin.Context) string {
	if v, ok := c.Get(ctxRequestIDKey); ok {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}
