package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// PublicCORS grants permissive Access-Control-Allow-Origin:* to requests whose
// path starts with one of the given prefixes — for UNAUTHENTICATED public
// endpoints (OAuth discovery / dynamic registration / token) that browser MCP
// clients reach from arbitrary origins. Register it BEFORE CORS so it owns
// those paths (including the OPTIONS preflight, which the main CORS would
// otherwise answer without an allow-origin header); all other paths fall
// through untouched. Safe as "*" because these endpoints carry no cookies —
// PKCE / the bearer header is the only credential.
func PublicCORS(prefixes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		matched := false
		for _, p := range prefixes {
			if strings.HasPrefix(path, p) {
				matched = true
				break
			}
		}
		if !matched {
			c.Next()
			return
		}
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Max-Age", "600")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// CORS applies a small, dependency-free CORS policy. allowedOrigins of
// ["*"] allows any origin; otherwise the request Origin must match exactly.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowAll := len(allowedOrigins) == 1 && allowedOrigins[0] == "*"
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if allowAll {
				c.Header("Access-Control-Allow-Origin", "*")
			} else if _, ok := allowed[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", strings.Join([]string{
			"Authorization", "Content-Type", requestIDHeader,
		}, ", "))
		c.Header("Access-Control-Max-Age", "600")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
