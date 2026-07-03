package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimit applies a per-client-IP token bucket (rps requests/second, burst
// bucket size). rps <= 0 disables the middleware entirely. Idle visitors are
// swept periodically so the map does not grow unbounded.
//
// ponytail: in-memory per-instance limiter. Behind multiple replicas each
// instance limits independently — swap for a Redis bucket if you need a global
// limit across the fleet.
func RateLimit(rps float64, burst int) gin.HandlerFunc {
	if rps <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	if burst < 1 {
		burst = 1
	}

	type visitor struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}
	var (
		mu       sync.Mutex
		visitors = make(map[string]*visitor)
	)

	go func() {
		for range time.Tick(time.Minute) {
			mu.Lock()
			for ip, v := range visitors {
				if time.Since(v.lastSeen) > 3*time.Minute {
					delete(visitors, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()

		mu.Lock()
		v, ok := visitors[ip]
		if !ok {
			v = &visitor{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
			visitors[ip] = v
		}
		v.lastSeen = time.Now()
		allowed := v.limiter.Allow()
		mu.Unlock()

		if !allowed {
			err := httpx.TooManyRequests("rate.limited", "too many requests")
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, httpx.ErrorResponse{
				Error: httpx.ErrorBody{Code: err.Code, Message: err.Message},
			})
			return
		}
		c.Next()
	}
}
