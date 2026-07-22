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

func TestKanbanRoutesHappyPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, boardID, projectID, columnID, cardID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repo := &stubRepo{board: &Board{ID: boardID, OwnerID: owner}, card: &Card{ID: cardID, BoardID: boardID, ColumnID: columnID, Title: "Lead"}, createdCard: true}
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), NewHandler(NewUsecase(repo, nil)), tokenVerifier{user: owner.String()})
	t.Setenv("APP_INTERNAL_TOKEN", "internal-secret")

	tests := []struct {
		method, path, body string
		want               int
		internal           bool
	}{
		{http.MethodGet, "/api/v1/boards", "", 200, false},
		{http.MethodGet, "/api/v1/boards/archived", "", 200, false},
		{http.MethodGet, "/api/v1/boards/" + boardID.String(), "", 200, false},
		{http.MethodPut, "/api/v1/boards/" + boardID.String(), `{"name":"Sales","icon":"kanban","color":"#fff","card_type_label":"Lead","field_schema":[]}`, 200, false},
		{http.MethodDelete, "/api/v1/boards/" + boardID.String(), "", 204, false},
		{http.MethodPost, "/api/v1/boards/" + boardID.String() + "/restore", "", 200, false},
		{http.MethodPost, "/api/v1/boards/" + boardID.String() + "/projects", `{"project_id":"` + projectID.String() + `"}`, 204, false},
		{http.MethodDelete, "/api/v1/boards/" + boardID.String() + "/projects/" + projectID.String(), "", 204, false},
		{http.MethodPost, "/api/v1/boards/" + boardID.String() + "/columns", `{"name":"New"}`, 201, false},
		{http.MethodPut, "/api/v1/boards/" + boardID.String() + "/columns/" + columnID.String(), `{"name":"New"}`, 200, false},
		{http.MethodPost, "/api/v1/boards/" + boardID.String() + "/columns/reorder", `{"column_ids":["` + columnID.String() + `"]}`, 200, false},
		{http.MethodDelete, "/api/v1/boards/" + boardID.String() + "/columns/" + columnID.String(), "", 204, false},
		{http.MethodPost, "/api/v1/boards/" + boardID.String() + "/cards", `{"column_id":"` + columnID.String() + `","title":"Lead"}`, 201, false},
		{http.MethodGet, "/api/v1/boards/" + boardID.String() + "/cards/archived", "", 200, false},
		{http.MethodGet, "/api/v1/cards/" + cardID.String(), "", 200, false},
		{http.MethodPut, "/api/v1/cards/" + cardID.String(), `{"title":"Updated"}`, 200, false},
		{http.MethodPost, "/api/v1/cards/" + cardID.String() + "/move", `{"column_id":"` + columnID.String() + `","position":0}`, 200, false},
		{http.MethodDelete, "/api/v1/cards/" + cardID.String(), "", 200, false},
		{http.MethodPost, "/api/v1/cards/" + cardID.String() + "/restore", "", 200, false},
		{http.MethodGet, "/api/v1/cards/" + cardID.String() + "/activities", "", 200, false},
		{http.MethodGet, "/api/v1/cards/" + cardID.String() + "/comments", "", 200, false},
		{http.MethodPost, "/api/v1/cards/" + cardID.String() + "/comments", `{"body":"Hello"}`, 201, false},
		{http.MethodPost, "/api/v1/internal/projects/" + projectID.String() + "/boards/" + boardID.String() + "/cards/upsert", `{"column_id":"` + columnID.String() + `","title":"Lead"}`, 201, true},
		{http.MethodPost, "/api/v1/internal/projects/" + projectID.String() + "/boards/" + boardID.String() + "/cards/mutate", `{"operation":"upsert","column_id":"` + columnID.String() + `","title":"Lead"}`, 201, true},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.internal {
				req.Header.Set("X-Internal-Token", "internal-secret")
			} else {
				req.Header.Set("Authorization", "Bearer ok")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestKanbanRoutesRejectInvalidIDsAndInternalAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router.Group("/api/v1"), NewHandler(NewUsecase(&stubRepo{}, nil)), tokenVerifier{user: uuid.NewString()})
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/boards/not-a-uuid", http.StatusBadRequest},
		{"/api/v1/internal/projects/" + uuid.NewString() + "/boards/" + uuid.NewString() + "/cards/upsert", http.StatusUnauthorized},
	} {
		method := http.MethodGet
		if strings.Contains(tc.path, "/internal/") {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, tc.path, nil)
		if strings.HasPrefix(tc.path, "/api/v1/boards/") {
			req.Header.Set("Authorization", "Bearer ok")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("path=%s status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}
}

var _ middleware.TokenVerifier = tokenVerifier{}
