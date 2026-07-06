package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeUsecase struct {
	users    UsersPage
	messages MessagesPage
	err      error

	gotOwner     uuid.UUID
	gotProjectID string
	gotChatID    int64
	gotLimit     int64
	gotSkip      int64
}

func (u *fakeUsecase) ListUsers(_ context.Context, ownerID uuid.UUID, rawProjectID string, limit, skip int64) (UsersPage, error) {
	u.gotOwner = ownerID
	u.gotProjectID = rawProjectID
	u.gotLimit = limit
	u.gotSkip = skip
	return u.users, u.err
}

func (u *fakeUsecase) ListMessages(_ context.Context, ownerID uuid.UUID, rawProjectID string, chatID int64, limit, skip int64) (MessagesPage, error) {
	u.gotOwner = ownerID
	u.gotProjectID = rawProjectID
	u.gotChatID = chatID
	u.gotLimit = limit
	u.gotSkip = skip
	return u.messages, u.err
}

func newHandlerTestRouter(ownerID string, uc Usecase) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if ownerID != "" {
		r.Use(func(c *gin.Context) {
			c.Set("user_id", ownerID)
			c.Next()
		})
	}
	h := NewHandler(uc)
	r.GET("/chat/users/:project_id", h.ListUsers)
	r.GET("/chat/messages/:project_id/:chat_id", h.ListMessages)
	return r
}

func TestListUsersHandler_Success(t *testing.T) {
	ownerID := uuid.New()
	uc := &fakeUsecase{
		users: UsersPage{
			Items: []UserWithStats{{User: map[string]any{"chat_id": float64(7)}}},
			Total: 1,
		},
	}
	r := newHandlerTestRouter(ownerID.String(), uc)

	req := httptest.NewRequest(http.MethodGet, "/chat/users/project-1?limit=10&skip=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if uc.gotOwner != ownerID || uc.gotProjectID != "project-1" || uc.gotLimit != 10 || uc.gotSkip != 5 {
		t.Fatalf("unexpected usecase input: %#v", uc)
	}
	var resp ListUsersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 1 || resp.Count != 1 || len(resp.Users) != 1 || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestListUsersHandler_UsecaseError(t *testing.T) {
	uc := &fakeUsecase{err: httpx.NotFound("project.not_found", "project not found")}
	r := newHandlerTestRouter(uuid.NewString(), uc)

	req := httptest.NewRequest(http.MethodGet, "/chat/users/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestListMessagesHandler_Success(t *testing.T) {
	ownerID := uuid.New()
	uc := &fakeUsecase{
		messages: MessagesPage{
			Items: []map[string]any{{"text": "hello"}},
			Total: 1,
		},
	}
	r := newHandlerTestRouter(ownerID.String(), uc)

	req := httptest.NewRequest(http.MethodGet, "/chat/messages/project-1/123?limit=20&skip=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if uc.gotOwner != ownerID || uc.gotProjectID != "project-1" || uc.gotChatID != 123 || uc.gotLimit != 20 || uc.gotSkip != 2 {
		t.Fatalf("unexpected usecase input: %#v", uc)
	}
}

func TestListMessagesHandler_InvalidChatID(t *testing.T) {
	r := newHandlerTestRouter(uuid.NewString(), &fakeUsecase{})

	req := httptest.NewRequest(http.MethodGet, "/chat/messages/project-1/not-int", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestListUsersHandler_InvalidAuthContext(t *testing.T) {
	r := newHandlerTestRouter("", &fakeUsecase{err: errors.New("must not be called")})

	req := httptest.NewRequest(http.MethodGet, "/chat/users/project-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
}
