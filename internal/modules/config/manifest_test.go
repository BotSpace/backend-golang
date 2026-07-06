package config

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// stubVerifier satisfies middleware.TokenVerifier without ever being called —
// /shared/manifest/builtin-nodes is AllowAny (mounted outside any auth
// group), so no request in this test should reach it.
type stubVerifier struct{}

func (stubVerifier) VerifyAccessToken(string) (string, string, error) { return "", "", nil }

// TestBuiltinNodeManifests_ReturnsExpectedShape is a regression test for the
// HARD BLOCKER fixed here: agentv3's manifest_compiler/loader.py does a
// synchronous GET to this exact path at import time and crashes the whole
// process on a 404 or on an unexpected response shape. In production,
// Server.Router strips the trailing slash before routing, so the module route
// itself must handle the slashless path. It expects
// `{"manifests": [...]}` (a non-empty list of node manifest objects, each
// with at least a "type" key) — see loader.py's `_fetch_from_backend`.
func TestBuiltinNodeManifests_ReturnsExpectedShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(NewUsecase(newFakeRepo()))
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, h, stubVerifier{})

	resp := requestManifests(t, router, "/api/v1/shared/manifest/builtin-nodes")
	if len(resp.Manifests) == 0 {
		t.Fatal("expected a non-empty manifests list")
	}
	for i, m := range resp.Manifests {
		typ, ok := m["type"].(string)
		if !ok || typ == "" {
			t.Fatalf("manifests[%d] missing non-empty \"type\" key: %#v", i, m)
		}
	}

	// Spot-check one well-known built-in node type survives the round trip
	// byte-identically shaped to Django's canonical catalog.
	found := false
	for _, m := range resp.Manifests {
		if m["type"] == "CommandTriggerNode" {
			found = true
			if m["category"] != "triggers" {
				t.Fatalf("CommandTriggerNode.category = %v, want %q", m["category"], "triggers")
			}
		}
	}
	if !found {
		t.Fatal("expected CommandTriggerNode in the built-in manifest catalog")
	}
}

func requestManifests(t *testing.T, router *gin.Engine, path string) struct {
	Manifests []map[string]any `json:"manifests"`
} {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusMovedPermanently || w.Code == http.StatusTemporaryRedirect {
		t.Fatalf("%s redirected (status %d, Location %q) instead of being handled directly", path, w.Code, w.Header().Get("Location"))
	}
	if w.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body: %s", path, w.Code, w.Body.String())
	}

	var resp struct {
		Manifests []map[string]any `json:"manifests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
