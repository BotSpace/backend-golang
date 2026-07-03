package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// stubVerifier satisfies middleware.TokenVerifier without ever being called —
// /subscription/usage/report is deliberately mounted outside the JWT-auth
// group (see routes.go), so no request in this test should reach it.
type stubVerifier struct{}

func (stubVerifier) VerifyAccessToken(string) (string, string, error) { return "", "", nil }

// newReportUsageTestRouter builds a real gin router with RegisterRoutes, so
// these tests drive the actual HTTP layer (header extraction, JSON binding)
// rather than calling the handler func directly.
func newReportUsageTestRouter(t *testing.T) (*gin.Engine, uuid.UUID) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	repo := newFakeRepo()
	repo.addPlan(Plan{ID: uuid.New(), Name: NameFree, Price: "0", IsActive: true, MaxBots: 1, MaxTokens: 1_000_000})
	users := newFakeUsers()
	userID := uuid.New()
	users.balances[userID] = 0
	uc := NewUsecase(repo, users)
	// Ensure the user has a subscription row before ReportUsage looks it up.
	if _, _, err := uc.Current(context.Background(), userID); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	h := NewHandler(uc)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, h, stubVerifier{})
	return router, userID
}

func doReportUsage(t *testing.T, router *gin.Engine, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscription/usage/report", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestReportUsage_ReadsAppInternalTokenEnvVar is a regression test: this
// handler used to read INTERNAL_API_TOKEN (a literal port of the Django env
// var name), which is never set on this deployment, so the endpoint always
// 500'd regardless of what token was sent. Every other module (config,
// credential, plugin, webhook) reads APP_INTERNAL_TOKEN — this asserts the
// plan module now does too.
func TestReportUsage_ReadsAppInternalTokenEnvVar(t *testing.T) {
	router, userID := newReportUsageTestRouter(t)
	body, err := json.Marshal(ReportUsageRequest{UserID: userID.String(), InputTokens: 10, OutputTokens: 5})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("INTERNAL_API_TOKEN set but APP_INTERNAL_TOKEN unset still 500s", func(t *testing.T) {
		t.Setenv("INTERNAL_API_TOKEN", "some-legacy-value")
		w := doReportUsage(t, router, "some-legacy-value", body)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (APP_INTERNAL_TOKEN still unset); body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("APP_INTERNAL_TOKEN set + correct token succeeds", func(t *testing.T) {
		t.Setenv("APP_INTERNAL_TOKEN", "test-secret")
		w := doReportUsage(t, router, "test-secret", body)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
		}
		var resp ReportUsageResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.UserID != userID.String() || resp.InputTokensAdded != 10 || resp.OutputTokensAdded != 5 {
			t.Fatalf("unexpected response: %#v", resp)
		}
	})

	t.Run("APP_INTERNAL_TOKEN set + wrong token is rejected, not 500", func(t *testing.T) {
		t.Setenv("APP_INTERNAL_TOKEN", "test-secret")
		w := doReportUsage(t, router, "wrong-token", body)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 for a bad token; body: %s", w.Code, w.Body.String())
		}
	})
}
