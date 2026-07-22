package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTimeoutSkipsEventStreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, accept string
		wantDeadline bool
	}{
		{name: "json request", accept: "application/json", wantDeadline: true},
		{name: "event stream", accept: "text/event-stream", wantDeadline: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(Timeout(time.Second))
			router.GET("/events", func(c *gin.Context) {
				_, hasDeadline := c.Request.Context().Deadline()
				if hasDeadline != tc.wantDeadline {
					c.Status(http.StatusInternalServerError)
					return
				}
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/events", nil)
			req.Header.Set("Accept", tc.accept)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusNoContent {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}
