package credential

import (
	"context"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// callOAuthCallback drives Handler.OAuthCallback exactly as gin's router
// would for GET /credentials/oauth/:provider/callback?..., with no
// Authorization header set — proving the route works from an unauthenticated
// browser redirect (see routes.go's comment on why it sits outside the
// JWT-guarded group).
func callOAuthCallback(h *Handler, provider string, query neturl.Values) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	target := "/credentials/oauth/" + provider + "/callback?" + query.Encode()
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Params = gin.Params{{Key: "provider", Value: provider}}
	h.OAuthCallback(c)
	return w
}

// TestHandlerOAuthCallback_HappyPath drives the real HTTP handler (not just
// the usecase) end to end against a fake token/userinfo server: mints a
// state via OAuthStart, completes it via the gin handler, and asserts both
// the HTML response (200, postMessage ok:true) and that a credential was
// actually persisted.
func TestHandlerOAuthCallback_HappyPath(t *testing.T) {
	t.Setenv("DJANGO_SECRET_KEY", "handler-oauth-callback-happy-secret")

	tokenHandler := jsonHandler(t, 200, map[string]any{
		"access_token": "handler-access-token", "refresh_token": "handler-refresh-token", "expires_in": 3600,
	})
	userinfoHandler := jsonHandler(t, 200, map[string]any{"email": "handler@example.com"})
	repo, cipher := newFakeRepo(), mustCipher(t)
	uc, closeSrv := newCallbackTestUsecase(t, repo, cipher, tokenHandler, userinfoHandler)
	defer closeSrv()

	h := NewHandler(uc, "https://admin.botspace.uz")

	ownerID := uuid.New()
	authorizeURL, err := uc.OAuthStart(context.Background(), ownerID, "google", "google_email")
	if err != nil {
		t.Fatalf("OAuthStart: %v", err)
	}
	state := stateFromAuthorizeURL(t, authorizeURL)

	w := callOAuthCallback(h, "google", neturl.Values{"code": {"fake-code"}, "state": {state}})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ok:true") {
		t.Fatalf("expected success postMessage payload in body, got: %s", body)
	}
	if !strings.Contains(body, "https://admin.botspace.uz") {
		t.Fatalf("expected panel URL in body, got: %s", body)
	}

	if got := len(repo.byID); got != 1 {
		t.Fatalf("expected 1 credential to be persisted by the handler call, got %d", got)
	}
}

// TestHandlerOAuthCallback_ErrorParam mirrors Google redirecting back with
// ?error=access_denied straight to the HTTP handler: expects a 400 HTML page
// carrying ok:false in its postMessage payload, and no attempt to reach the
// (here, non-configured) token endpoint.
func TestHandlerOAuthCallback_ErrorParam(t *testing.T) {
	uc, closeSrv := newCallbackTestUsecase(t, newFakeRepo(), mustCipher(t), nil, nil)
	defer closeSrv()
	h := NewHandler(uc, "https://admin.botspace.uz")

	w := callOAuthCallback(h, "google", neturl.Values{"error": {"access_denied"}})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "ok:false") {
		t.Fatalf("expected failure postMessage payload in body, got: %s", body)
	}
	if !strings.Contains(body, "access_denied") {
		t.Fatalf("expected the provider's error to be reflected in the page, got: %s", body)
	}
}

// TestHandlerOAuthCallback_DefaultPanelURL confirms NewHandler("") falls
// back to defaultOAuthPanelURL instead of producing an empty href/postMessage
// target.
func TestHandlerOAuthCallback_DefaultPanelURL(t *testing.T) {
	uc, closeSrv := newCallbackTestUsecase(t, newFakeRepo(), mustCipher(t), nil, nil)
	defer closeSrv()
	h := NewHandler(uc, "")

	w := callOAuthCallback(h, "google", neturl.Values{"error": {"access_denied"}})
	if !strings.Contains(w.Body.String(), defaultOAuthPanelURL) {
		t.Fatalf("expected default panel URL %q in body, got: %s", defaultOAuthPanelURL, w.Body.String())
	}
}
