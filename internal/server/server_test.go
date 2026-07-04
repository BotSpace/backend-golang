package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// newTrailingSlashTestRouter builds a minimal engine with the exact same
// RedirectTrailingSlash=false + NoRoute(trailingSlashFallback) wiring New()
// applies, plus the same CORS middleware, so the CORS-on-redispatch
// assertion below actually exercises the middleware-chain-reuse behavior
// HandleContext relies on (see trailingSlashFallback's doc comment).
func newTrailingSlashTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CORS([]string{"https://constructor.botspace.uz"}))
	r.RedirectTrailingSlash = false
	r.NoRoute(trailingSlashFallback(r))
	r.GET("/api/v2/cms/projects/:id/collections", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.Param("id")})
	})
	return r
}

// TestTrailingSlashFallbackServesWithoutRedirect is the Fix 1 regression
// test: a client calling a registered no-slash route WITH a trailing slash
// (Django convention — the constructor/agent do this) must get served
// directly (200 + CORS headers), never gin's default 301
// RedirectTrailingSlash (which is written before any middleware runs, so a
// cross-origin request following it sees no Access-Control-Allow-Origin
// header and the browser blocks it outright instead of following the
// redirect).
func TestTrailingSlashFallbackServesWithoutRedirect(t *testing.T) {
	r := newTrailingSlashTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/cms/projects/abc-123/collections/", nil)
	req.Header.Set("Origin", "https://constructor.botspace.uz")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://constructor.botspace.uz" {
		t.Fatalf("expected CORS header on the re-dispatched response, got %q", got)
	}
	if got := rec.Body.String(); got != `{"id":"abc-123"}` {
		t.Fatalf("expected the matched route's handler to actually run, got %q", got)
	}
}

// TestNoTrailingSlashStillWorks: the no-slash form (as every route is
// literally registered) must keep working unchanged.
func TestNoTrailingSlashStillWorks(t *testing.T) {
	r := newTrailingSlashTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/cms/projects/abc-123/collections", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestUnknownPathWithTrailingSlashIs404NotRedirect: a path that doesn't
// exist even after stripping the trailing slash must fall through to a real
// 404, never loop back into NoRoute again (the stripped path no longer ends
// in "/", so trailingSlashFallback's suffix check stops it).
func TestUnknownPathWithTrailingSlashIs404NotRedirect(t *testing.T) {
	r := newTrailingSlashTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/does-not-exist/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestUnknownPathNoTrailingSlashIs404: sanity check that the plain no-slash
// 404 path (no fallback involved at all) still behaves normally.
func TestUnknownPathNoTrailingSlashIs404(t *testing.T) {
	r := newTrailingSlashTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/does-not-exist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
