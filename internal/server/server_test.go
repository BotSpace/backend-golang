package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
)

// newTrailingSlashTestHandler mirrors New()+Router(): a gin engine with
// RedirectTrailingSlash=false and CORS, wrapped by the same trailing-slash
// stripper Router() applies BEFORE gin routes. This exercises the real path a
// request takes in prod.
func newTrailingSlashTestHandler() http.Handler {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CORS([]string{"https://constructor.botspace.uz"}))
	r.RedirectTrailingSlash = false
	r.NoRoute(func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "not found"}) })
	r.GET("/api/v2/cms/projects/:id/collections", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.Param("id")})
	})
	// A route guarded by a middleware that aborts with an error body — used to
	// prove the response is written exactly ONCE even for a trailing-slash call.
	authed := r.Group("/api/v2/secure")
	authed.Use(func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
	})
	authed.GET("/thing", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if p := req.URL.Path; len(p) > 1 && strings.HasSuffix(p, "/") {
			if trimmed := strings.TrimRight(p, "/"); trimmed != "" {
				req.URL.Path = trimmed
			}
		}
		r.ServeHTTP(w, req)
	})
}

// TestTrailingSlashServesWithoutRedirect: a client calling a registered
// no-slash route WITH a trailing slash (Django convention) is served directly
// (200 + CORS header), never gin's default 301 (which is written before any
// middleware, so a cross-origin follow-up sees no CORS header and is blocked).
func TestTrailingSlashServesWithoutRedirect(t *testing.T) {
	h := newTrailingSlashTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/cms/projects/abc-123/collections/", nil)
	req.Header.Set("Origin", "https://constructor.botspace.uz")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://constructor.botspace.uz" {
		t.Fatalf("expected CORS header, got %q", got)
	}
	if got := rec.Body.String(); got != `{"id":"abc-123"}` {
		t.Fatalf("expected the matched route's handler to run, got %q", got)
	}
}

// TestTrailingSlashErrorBodyWrittenOnce is the regression for the double-JSON
// bug: a trailing-slash request to a route whose middleware aborts with an
// error must return a SINGLE JSON body, not two concatenated ones (the old
// NoRoute+HandleContext re-dispatch ran the middleware chain twice, producing
// `{"error":...}{"error":...}`).
func TestTrailingSlashErrorBodyWrittenOnce(t *testing.T) {
	h := newTrailingSlashTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/secure/thing/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":"invalid token"}`; got != want {
		t.Fatalf("expected a single error body %q, got %q", want, got)
	}
	if n := strings.Count(rec.Body.String(), `"error"`); n != 1 {
		t.Fatalf("expected exactly one error object, got %d (body: %s)", n, rec.Body.String())
	}
}

// TestNoTrailingSlashStillWorks: the no-slash form (as every route is
// registered) keeps working unchanged.
func TestNoTrailingSlashStillWorks(t *testing.T) {
	h := newTrailingSlashTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/cms/projects/abc-123/collections", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestUnknownPathWithTrailingSlashIs404: a path that doesn't exist even after
// stripping the trailing slash falls through to a real 404.
func TestUnknownPathWithTrailingSlashIs404(t *testing.T) {
	h := newTrailingSlashTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/does-not-exist/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestUnknownPathNoTrailingSlashIs404: sanity check for the plain no-slash 404.
func TestUnknownPathNoTrailingSlashIs404(t *testing.T) {
	h := newTrailingSlashTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
