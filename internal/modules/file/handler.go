package file

import (
	"fmt"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/media"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/storagex"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// objectKey builds the bucket-relative storage key for an uploaded blob:
// "<projectID>/<random>.<ext>". This is persisted as File.FilePath and is the
// same key the cleanup worker deletes from B2 and internal/shared/media turns
// into a public URL ("<STORAGE_URL>/<bucket>/<key>").
func objectKey(projectID uuid.UUID, originalName string) string {
	return path.Join(projectID.String(), uuid.New().String()+path.Ext(originalName))
}

type Handler struct {
	uc      Usecase
	storage *storagex.Storage
}

func NewHandler(uc Usecase, storage *storagex.Storage) *Handler {
	return &Handler{uc: uc, storage: storage}
}

// owner extracts the authenticated user id, writing a 401 on failure.
func owner(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return id, true
}

// pathUUID parses a named path param as a uuid, writing a 400 on failure.
func pathUUID(c *gin.Context, name, code string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		httpx.Error(c, httpx.BadRequest(code, "invalid id"))
		return uuid.Nil, false
	}
	return id, true
}

// Upload godoc
// @Summary      Upload a file
// @Tags         file
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        file        formData  file    true   "Blob to upload"
// @Param        project_id  formData  string  true   "Owning project id"
// @Param        filetype    formData  string  false  "file|photo|video|audio (default file)"
// @Param        ttl         formData  int     false  "Seconds until expiry; 0/absent = forever"
// @Success      201  {object}  FileResponse
// @Failure      400  {object}  httpx.ErrorResponse
// @Router       /file [post]
func (h *Handler) Upload(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}

	req := UploadFileRequest{
		ProjectID: c.PostForm("project_id"),
		FileType:  c.DefaultPostForm("filetype", TypeFile),
	}
	if raw := c.PostForm("ttl"); raw != "" {
		ttl, err := strconv.Atoi(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("file.invalid_ttl", "invalid ttl"))
			return
		}
		req.TTL = ttl
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("file.invalid_project_id", "invalid project id"))
		return
	}

	fh, err := c.FormFile("file")
	if err != nil {
		httpx.Error(c, httpx.BadRequest("file.missing", "file is required"))
		return
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
	// Stream straight to object storage instead of buffering the whole (up to
	// tens of MB) blob in memory, so concurrent uploads don't balloon the heap.
	if err := h.storage.WriteStream(c.Request.Context(), key, src, fh.Size, contentType); err != nil {
		httpx.Error(c, httpx.Internal(fmt.Errorf("upload to storage: %w", err)))
		return
	}

	var expiresAt *time.Time
	if req.TTL > 0 {
		t := time.Now().Add(time.Duration(req.TTL) * time.Second)
		expiresAt = &t
	}

	f, err := h.uc.CreateFile(c.Request.Context(), CreateFileInput{
		OwnerID:      ownerID,
		ProjectID:    projectID,
		FilePath:     key,
		OriginalName: fh.Filename,
		FileSize:     fh.Size,
		FileType:     req.FileType,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		_ = h.storage.Delete(c.Request.Context(), key) // don't leave an orphaned blob if the metadata row failed
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toFileResponse(f))
}

// Get godoc
// @Summary      Get file metadata (public)
// @Tags         file
// @Produce      json
// @Param        uuid  path      string  true  "File uuid"
// @Success      200  {object}  FileResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /file/{uuid} [get]
func (h *Handler) Get(c *gin.Context) {
	uid, ok := pathUUID(c, "uuid", "file.invalid_id")
	if !ok {
		return
	}
	f, err := h.uc.GetFilePublic(c.Request.Context(), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toFileResponse(f))
}

// Download godoc
// @Summary      Download the raw file blob (public)
// @Tags         file
// @Produce      application/octet-stream
// @Param        uuid  path      string  true  "File uuid"
// @Success      200  {file}    binary
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /file/{uuid}/download [get]
func (h *Handler) Download(c *gin.Context) {
	uid, ok := pathUUID(c, "uuid", "file.invalid_id")
	if !ok {
		return
	}
	f, err := h.uc.GetFilePublic(c.Request.Context(), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	// Blobs live in the public B2 media bucket; redirect to the object URL
	// instead of proxying bytes through the API.
	c.Redirect(http.StatusFound, media.URL(f.FilePath))
}

// List godoc
// @Summary      List my files
// @Tags         file
// @Produce      json
// @Security     BearerAuth
// @Param        project_id  query     string  false  "Filter by project"
// @Param        filetype    query     string  false  "Filter by type: file|photo|video|audio"
// @Param        limit       query     int     false  "Page size (default 20, max 100)"
// @Param        offset      query     int     false  "Rows to skip"
// @Success      200  {object}  FileListResponse
// @Router       /file [get]
func (h *Handler) List(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)

	var projectID *uuid.UUID
	if raw := c.Query("project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("file.invalid_project_id", "invalid project id"))
			return
		}
		projectID = &id
	}
	var filetype *string
	if raw := c.Query("filetype"); raw != "" {
		filetype = &raw
	}

	items, total, err := h.uc.ListFiles(c.Request.Context(), ListFileFilter{
		OwnerID:   ownerID,
		ProjectID: projectID,
		FileType:  filetype,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toFileListResponse(items, total, int32(limit), int32(offset)))
}

// Update godoc
// @Summary      Update a file's type
// @Tags         file
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        uuid  path      string             true  "File uuid"
// @Param        body  body      UpdateFileRequest  true  "Fields to update"
// @Success      200   {object}  FileResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /file/{uuid} [patch]
func (h *Handler) Update(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	uid, ok := pathUUID(c, "uuid", "file.invalid_id")
	if !ok {
		return
	}
	var req UpdateFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	f, err := h.uc.UpdateFile(c.Request.Context(), uid, ownerID, UpdateFileInput{FileType: req.FileType})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toFileResponse(f))
}

// Delete godoc
// @Summary      Delete a file
// @Tags         file
// @Security     BearerAuth
// @Param        uuid  path      string  true  "File uuid"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /file/{uuid} [delete]
func (h *Handler) Delete(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	uid, ok := pathUUID(c, "uuid", "file.invalid_id")
	if !ok {
		return
	}
	f, err := h.uc.DeleteFile(c.Request.Context(), uid, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	// Best-effort blob cleanup; the metadata row is already gone, so a failed
	// storage delete here must not fail the request.
	if h.storage != nil {
		_ = h.storage.Delete(c.Request.Context(), f.FilePath)
	}
	httpx.NoContent(c)
}

// CreateResource godoc
// @Summary      Report a resource usage sample
// @Tags         resource
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateResourceRequest  true  "Resource payload"
// @Success      201   {object}  ResourceResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /resource [post]
func (h *Handler) CreateResource(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req CreateResourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("resource.invalid_project_id", "invalid project id"))
		return
	}
	res, err := h.uc.CreateResource(c.Request.Context(), CreateResourceInput{
		OwnerID:   ownerID,
		ProjectID: projectID,
		Hostname:  req.Hostname,
		RAM:       req.RAM,
		CPU:       req.CPU,
		NetRX:     req.NetRX,
		NetTX:     req.NetTX,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResourceResponse(res))
}

// GetResource godoc
// @Summary      Get a resource sample
// @Tags         resource
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Resource id"
// @Success      200  {object}  ResourceResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /resource/{id} [get]
func (h *Handler) GetResource(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id", "resource.invalid_id")
	if !ok {
		return
	}
	res, err := h.uc.GetResource(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResourceResponse(res))
}

// ListResources godoc
// @Summary      List my resource samples
// @Tags         resource
// @Produce      json
// @Security     BearerAuth
// @Param        project_id  query     string  false  "Filter by project"
// @Param        limit       query     int     false  "Page size (default 20, max 100)"
// @Param        offset      query     int     false  "Rows to skip"
// @Success      200  {object}  ResourceListResponse
// @Router       /resource [get]
func (h *Handler) ListResources(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)

	var projectID *uuid.UUID
	if raw := c.Query("project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("resource.invalid_project_id", "invalid project id"))
			return
		}
		projectID = &id
	}

	items, total, err := h.uc.ListResources(c.Request.Context(), ListResourceFilter{
		OwnerID:   ownerID,
		ProjectID: projectID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResourceListResponse(items, total, int32(limit), int32(offset)))
}

// Used godoc
// @Summary      Aggregated usage (storage/ram/cpu/network) for a project or the whole account
// @Tags         used
// @Produce      json
// @Security     BearerAuth
// @Param        project_id  path      string  false  "Project id; omit to aggregate across every owned project"
// @Success      200  {object}  UsedResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /used [get]
// @Router       /used/{project_id} [get]
func (h *Handler) Used(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var projectID *uuid.UUID
	if raw := c.Param("project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("used.invalid_project_id", "invalid project id"))
			return
		}
		projectID = &id
	}
	stats, err := h.uc.Used(c.Request.Context(), ownerID, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toUsedResponse(stats))
}
