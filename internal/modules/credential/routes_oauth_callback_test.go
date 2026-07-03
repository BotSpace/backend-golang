package credential

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// stubVerifier satisfies middleware.TokenVerifier without ever being called
// by this test — the callback route intentionally sits outside the
// JWT-guarded group, so no request here should reach it.
type stubVerifier struct{}

func (stubVerifier) VerifyAccessToken(string) (string, string, error) { return "", "", nil }

// TestRegisterRoutes_OAuthCallbackNoTrailingSlashRedirect is a regression
// test for a real prod incident: oauthCallbackURL (and Google Console's
// registered redirect_uri) always ends in a trailing slash
// (".../callback/"), but the route was first registered without one. Gin's
// default RedirectTrailingSlash then 301-redirected to the path AS
// REGISTERED IN GIN'S OWN ROUTER — i.e. the backend-internal /api/v1/...
// form — which the prod gateway does not route to this backend (only
// /api/v2/... is), so the browser silently fell through to the old Django
// deployment's still-live callback instead of this one. This test drives
// the real gin router (RegisterRoutes), not just the handler function
// directly, because calling the handler directly (as the other tests in
// this package do) bypasses gin's routing layer entirely and would never
// have caught this.
func TestRegisterRoutes_OAuthCallbackNoTrailingSlashRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc, closeSrv := newCallbackTestUsecase(t, newFakeRepo(), mustCipher(t), nil, nil)
	defer closeSrv()
	h := NewHandler(uc, "https://admin.botspace.uz")

	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, h, stubVerifier{})

	for _, path := range []string{
		"/api/v1/credentials/oauth/google/callback/",
		"/api/v1/credentials/oauth/google/callback",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path+"?error=access_denied", nil)
			router.ServeHTTP(w, req)

			if w.Code == http.StatusMovedPermanently || w.Code == http.StatusTemporaryRedirect {
				t.Fatalf("%s: got a redirect (status %d, Location %q) instead of being handled directly — "+
					"this is exactly the bug that sent OAuth callbacks to the wrong backend",
					path, w.Code, w.Header().Get("Location"))
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400 (error=access_denied path); body: %s", path, w.Code, w.Body.String())
			}
		})
	}
}
