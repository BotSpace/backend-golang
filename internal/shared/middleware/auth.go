package middleware

import (
	"strings"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
)

const (
	ctxUserIDKey = "user_id"
	ctxRoleKey   = "user_role"
	authHeader   = "Authorization"
	bearerPrefix = "Bearer "
)

// TokenVerifier is implemented by the auth module. Keeping it as an interface
// here avoids a circular import between middleware and auth.
type TokenVerifier interface {
	VerifyAccessToken(token string) (userID, role string, err error)
}

// Auth requires a valid bearer token and injects the user id + role into the
// context.
func Auth(v TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader(authHeader)
		if !strings.HasPrefix(raw, bearerPrefix) {
			httpx.Error(c, httpx.Unauthorized("auth.missing_token", "missing bearer token"))
			return
		}
		token := strings.TrimPrefix(raw, bearerPrefix)
		uid, role, err := v.VerifyAccessToken(token)
		if err != nil {
			httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid or expired token"))
			return
		}
		c.Set(ctxUserIDKey, uid)
		c.Set(ctxRoleKey, role)
		c.Next()
	}
}

// RequireRole allows the request only if the authenticated user's role is one
// of the given roles. Must be chained after Auth.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := Role(c)
		for _, r := range roles {
			if role == r {
				c.Next()
				return
			}
		}
		httpx.Error(c, httpx.Forbidden("auth.forbidden", "insufficient permissions"))
	}
}

// UserID extracts the authenticated user id set by Auth. It returns an empty
// string if Auth did not run before the handler.
func UserID(c *gin.Context) string {
	v, ok := c.Get(ctxUserIDKey)
	if !ok {
		return ""
	}
	uid, _ := v.(string)
	return uid
}

// Role extracts the authenticated user's role set by Auth.
func Role(c *gin.Context) string {
	v, ok := c.Get(ctxRoleKey)
	if !ok {
		return ""
	}
	role, _ := v.(string)
	return role
}
