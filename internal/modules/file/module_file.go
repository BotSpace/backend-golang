package file

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/media"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// internalTokenEnv is the shared engine<->backend secret. The engine derives a
// per-project HMAC bearer token from it, so the backend re-derives the same
// value to authenticate module-file callbacks.
const internalTokenEnv = "APP_INTERNAL_TOKEN"

// RegisterEngineRoutes mounts the Django-compatible, non-/v1 file endpoints the
// bot engine and its modules call directly, using their own HMAC token check
// (no user session). The engine hardcodes these exact paths:
//   - GET  /api/file/{uuid}/   — resolve a stored file to its public URL
//   - POST /api/module-file/   — a module uploads a produced blob (e.g. a
//     yt-dlp download) and gets back a uuid
//   - DELETE /api/module-file/{uuid}/
//
// Trailing slashes are stripped by Server.Router() before routing, so the
// no-slash forms registered here match both.
func RegisterEngineRoutes(r gin.IRouter, h *Handler) {
	r.GET("/api/file/:uuid", h.GetForEngine)
	r.POST("/api/module-file", h.UploadModuleFile)
	r.DELETE("/api/module-file/:uuid", h.DeleteModuleFile)
}

// verifyModuleToken authenticates a module/engine callback carrying
// `Authorization: Bearer <project_id>:<hex(HMAC-SHA256(APP_INTERNAL_TOKEN, project_id))>`
// and returns the scoped project id. Fails closed when the secret is unset.
func verifyModuleToken(c *gin.Context) (uuid.UUID, bool) {
	const prefix = "Bearer "
	auth := c.GetHeader("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		httpx.Error(c, httpx.Unauthorized("auth.missing_token", "missing bearer token"))
		return uuid.Nil, false
	}
	parts := strings.SplitN(strings.TrimPrefix(auth, prefix), ":", 2)
	if len(parts) != 2 {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token format"))
		return uuid.Nil, false
	}
	projID, providedHex := parts[0], parts[1]

	secret := os.Getenv(internalTokenEnv)
	if secret == "" {
		httpx.Error(c, httpx.Internal(errors.New("server misconfigured: "+internalTokenEnv+" is not set")))
		return uuid.Nil, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(projID))
	expectedHex := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(providedHex)), []byte(expectedHex)) != 1 {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	pid, err := uuid.Parse(projID)
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid project id"))
		return uuid.Nil, false
	}
	return pid, true
}

// GetForEngine resolves a file uuid to its public URL in the shape the engine's
// media handlers expect: {"status": true, "data": {"file": <url>, ...}}. Public
// (no auth), mirroring the /v1 retrieve — the uuid is the unguessable key.
func (h *Handler) GetForEngine(c *gin.Context) {
	uid, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": false, "data": gin.H{"detail": "invalid id"}})
		return
	}
	f, err := h.uc.GetFilePublic(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"status": false, "data": gin.H{"detail": "Not found."}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": true,
		"data": gin.H{
			"file":     media.URL(f.FilePath),
			"uuid":     f.UUID.String(),
			"filetype": f.FileType,
			"filesize": f.FileSize,
		},
	})
}

// UploadModuleFile stores a blob a module produced on behalf of a project and
// returns its uuid + public URL. Authenticated by the project HMAC token; the
// file row is stamped to the project's owner. Response carries the fields both
// at top level and under "data" so either engine parser variant resolves them.
func (h *Handler) UploadModuleFile(c *gin.Context) {
	projectID, ok := verifyModuleToken(c)
	if !ok {
		return
	}
	ownerID, err := h.uc.ProjectOwner(c.Request.Context(), projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}

	fh, err := c.FormFile("file")
	if err != nil {
		httpx.Error(c, httpx.BadRequest("file.missing", "file is required"))
		return
	}

	fileType := c.DefaultPostForm("filetype", TypeFile)
	if !ValidType(fileType) {
		fileType = TypeFile
	}

	var expiresAt *time.Time
	if raw := c.PostForm("ttl"); raw != "" {
		ttl, convErr := strconv.Atoi(raw)
		if convErr != nil {
			httpx.Error(c, httpx.BadRequest("file.invalid_ttl", "invalid ttl"))
			return
		}
		if ttl > 0 {
			t := time.Now().Add(time.Duration(ttl) * time.Second)
			expiresAt = &t
		}
	}

	if h.storage == nil {
		httpx.Error(c, httpx.Internal(fmt.Errorf("object storage not configured")))
		return
	}

	src, err := fh.Open()
	if err != nil {
		httpx.Error(c, httpx.Internal(fmt.Errorf("open upload: %w", err)))
		return
	}
	defer src.Close()

	key := objectKey(projectID, fh.Filename)
	contentType := fh.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := h.storage.WriteStream(c.Request.Context(), key, src, fh.Size, contentType); err != nil {
		httpx.Error(c, httpx.Internal(fmt.Errorf("upload to storage: %w", err)))
		return
	}

	f, err := h.uc.CreateFile(c.Request.Context(), CreateFileInput{
		OwnerID:      ownerID,
		ProjectID:    projectID,
		FilePath:     key,
		OriginalName: fh.Filename,
		FileSize:     fh.Size,
		FileType:     fileType,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		_ = h.storage.Delete(c.Request.Context(), key) // don't orphan the blob if the row failed
		httpx.Error(c, err)
		return
	}

	url := media.URL(f.FilePath)
	payload := gin.H{"uuid": f.UUID.String(), "file": url, "filetype": f.FileType, "filesize": f.FileSize}
	c.JSON(http.StatusCreated, gin.H{
		"status":   true,
		"uuid":     f.UUID.String(),
		"file":     url,
		"filetype": f.FileType,
		"filesize": f.FileSize,
		"data":     payload,
	})
}

// DeleteModuleFile removes a module-uploaded file (blob + metadata), scoped to
// the token's project owner.
func (h *Handler) DeleteModuleFile(c *gin.Context) {
	projectID, ok := verifyModuleToken(c)
	if !ok {
		return
	}
	uid, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("file.invalid_id", "invalid id"))
		return
	}
	ownerID, err := h.uc.ProjectOwner(c.Request.Context(), projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	f, err := h.uc.DeleteFile(c.Request.Context(), uid, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	if h.storage != nil && f != nil {
		_ = h.storage.Delete(c.Request.Context(), f.FilePath)
	}
	c.JSON(http.StatusOK, gin.H{"status": true})
}
