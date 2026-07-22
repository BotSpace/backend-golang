package kanban

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type tokenVerifier struct{ user string }

func (v tokenVerifier) VerifyAccessToken(string) (string, string, error) { return v.user, "user", nil }

func TestCreateBoardHandlerHappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner := uuid.New()
	repo := &stubRepo{}
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, NewHandler(NewUsecase(repo, nil)), tokenVerifier{user: owner.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/boards", strings.NewReader(`{"name":"Sales","field_schema":[]}`))
	req.Header.Set("Authorization", "Bearer ok")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateBoardHandlerRejectsMalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterRoutes(v1, NewHandler(NewUsecase(&stubRepo{}, nil)), tokenVerifier{user: uuid.NewString()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/boards", strings.NewReader(`{`))
	req.Header.Set("Authorization", "Bearer ok")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

var _ middleware.TokenVerifier = tokenVerifier{}
