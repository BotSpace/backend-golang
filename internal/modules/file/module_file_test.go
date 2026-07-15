package file

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func moduleToken(t *testing.T, projectID uuid.UUID, secret string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(projectID.String()))
	return "Bearer " + projectID.String() + ":" + hex.EncodeToString(mac.Sum(nil))
}

// newEngineRouter builds a gin router with the engine-facing routes plus a
// stand-in /api/v1 group and the paycue non-v1 route, so the test also proves
// RegisterEngineRoutes doesn't conflict with the real route tree (a gin panic
// at registration would fail the test).
func newEngineRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Group("/api/v1").GET("/file/:uuid", func(c *gin.Context) {})
	r.POST("/api/payment/paycue/update", func(c *gin.Context) {})
	RegisterEngineRoutes(r, h)
	return r
}

func TestUploadModuleFileAuth(t *testing.T) {
	t.Setenv(internalTokenEnv, "test-secret")
	repo := newFakeRepo()
	h := NewHandler(NewUsecase(repo), nil)
	r := newEngineRouter(h)

	cases := []struct {
		name, auth string
		want       int
	}{
		{"missing token", "", http.StatusUnauthorized},
		{"malformed token", "Bearer nope", http.StatusUnauthorized},
		{"bad hmac", "Bearer " + uuid.New().String() + ":deadbeef", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/module-file", strings.NewReader(""))
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// A valid token for an unknown project must pass auth and fail at owner lookup
// (404), proving the HMAC verification accepts a correctly derived token.
func TestUploadModuleFileValidTokenUnknownProject(t *testing.T) {
	t.Setenv(internalTokenEnv, "test-secret")
	repo := newFakeRepo()
	h := NewHandler(NewUsecase(repo), nil)
	r := newEngineRouter(h)

	pid := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/module-file", strings.NewReader(""))
	req.Header.Set("Authorization", moduleToken(t, pid, "test-secret"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
}

func TestGetForEngineShape(t *testing.T) {
	repo := newFakeRepo()
	owner := uuid.New()
	pid := uuid.New()
	repo.projects[pid] = owner
	f, err := repo.CreateFile(t.Context(), CreateFileInput{
		OwnerID: owner, ProjectID: pid, FilePath: "proj/vid.mp4", OriginalName: "vid.mp4",
		FileSize: 1234, FileType: TypeVideo,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(NewUsecase(repo), nil)
	r := newEngineRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/file/"+f.UUID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Status bool `json:"status"`
		Data   struct {
			File     string `json:"file"`
			UUID     string `json:"uuid"`
			Filetype string `json:"filetype"`
			Filesize int64  `json:"filesize"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Status || resp.Data.UUID != f.UUID.String() || resp.Data.Filetype != TypeVideo || resp.Data.Filesize != 1234 {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
	// file field is media.URL(FilePath); unconfigured media passes the key through.
	if resp.Data.File == "" {
		t.Fatalf("empty file url: %s", w.Body.String())
	}

	// Unknown uuid → status:false, 404.
	req2 := httptest.NewRequest(http.MethodGet, "/api/file/"+uuid.New().String(), nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("unknown uuid status = %d, want 404", w2.Code)
	}
}
