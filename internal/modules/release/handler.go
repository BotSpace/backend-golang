package release

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// logQueryParams reads the shared tail/since_seconds query params for the
// two log-snapshot endpoints, matching Django's defaults (tail=200, no
// since_seconds restriction) and silently falling back on parse errors
// (mirrors the `except (TypeError, ValueError): tail = 200` in project.py).
func logQueryParams(c *gin.Context) (tailLines, sinceSeconds int) {
	tailLines = 200
	if v, err := strconv.Atoi(c.Query("tail")); err == nil {
		tailLines = v
	}
	if v, err := strconv.Atoi(c.Query("since_seconds")); err == nil {
		sinceSeconds = v
	}
	return tailLines, sinceSeconds
}

type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
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

// pathID parses the :id path param, writing a 400 on failure.
func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("release.invalid_id", "invalid id"))
		return uuid.Nil, false
	}
	return id, true
}

// queryProjectID reads the mandatory ?project= filter, matching Django's
// ReleaseView/EnviromentView get_queryset() which 400s when it is missing.
func queryProjectID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.Query("project")
	if raw == "" {
		httpx.Error(c, httpx.BadRequest("project.required", "project is required"))
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("project.invalid_id", "invalid project id"))
		return uuid.Nil, false
	}
	return id, true
}

// ---- Release ----------------------------------------------------------------

// ListReleases godoc
// @Summary      List releases for a project
// @Tags         relase
// @Produce      json
// @Security     BearerAuth
// @Param        project  query     string  true  "Project id"
// @Success      200  {array}  ReleaseResponse
// @Router       /relase [get]
func (h *Handler) ListReleases(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := queryProjectID(c)
	if !ok {
		return
	}
	items, err := h.uc.ListReleases(c.Request.Context(), projectID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toReleaseListResponse(items))
}

// CreateRelease godoc
// @Summary      Create and deploy a new release
// @Tags         relase
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateReleaseRequest  true  "Target project"
// @Success      201   {object}  ReleaseResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /relase [post]
func (h *Handler) CreateRelease(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req CreateReleaseRequest
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
		httpx.Error(c, httpx.BadRequest("project.invalid_id", "invalid project id"))
		return
	}
	rel, err := h.uc.CreateRelease(c.Request.Context(), projectID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toReleaseResponse(rel))
}

// GetRelease godoc
// @Summary      Get a release (flow included)
// @Tags         relase
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Release id"
// @Success      200  {object}  ReleaseResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /relase/{id} [get]
func (h *Handler) GetRelease(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	rel, err := h.uc.GetRelease(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toReleaseResponse(rel))
}

// StopRelease godoc
// @Summary      Stop a running release
// @Tags         relase
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Release id"
// @Success      200  {object}  ReleaseResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /relase/{id}/stop [get]
func (h *Handler) StopRelease(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	rel, err := h.uc.StopRelease(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toReleaseResponse(rel))
}

// GetReleaseLogs godoc
// @Summary      Tail a release's live pod log (non-streaming snapshot)
// @Tags         relase
// @Produce      json
// @Security     BearerAuth
// @Param        id             path   string  true   "Release id"
// @Param        tail           query  int     false  "trailing lines, default 200, max 2000"
// @Param        since_seconds  query  int     false  "only lines newer than N seconds"
// @Success      200  {object}  LogSnapshotResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /relase/{id}/logs [get]
func (h *Handler) GetReleaseLogs(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	tail, since := logQueryParams(c)
	snap, err := h.uc.GetReleaseLogs(c.Request.Context(), id, ownerID, tail, since)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toLogSnapshotResponse(snap))
}

// ---- Debug --------------------------------------------------------------------

// StartDebug godoc
// @Summary      Start (or restart) a project's debug container
// @Tags         debug
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Project id"
// @Success      200  {object}  DebugResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /projects/{id}/debug [get]
func (h *Handler) StartDebug(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathID(c)
	if !ok {
		return
	}
	dbg, err := h.uc.StartDebug(c.Request.Context(), projectID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toDebugResponse(dbg))
}

// StopDebug godoc
// @Summary      Stop a project's debug container
// @Tags         debug
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Project id"
// @Success      200  {object}  DebugResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /projects/{id}/debug/stop [get]
func (h *Handler) StopDebug(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathID(c)
	if !ok {
		return
	}
	dbg, err := h.uc.StopDebug(c.Request.Context(), projectID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toDebugResponse(dbg))
}

// GetDebug godoc
// @Summary      Get a project's debug container status
// @Tags         debug
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Project id"
// @Success      200  {object}  DebugResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /projects/{id}/info [get]
func (h *Handler) GetDebug(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathID(c)
	if !ok {
		return
	}
	dbg, err := h.uc.GetDebug(c.Request.Context(), projectID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toDebugResponse(dbg))
}

// GetDebugLogs godoc
// @Summary      Tail a project's debug container pod log (non-streaming snapshot)
// @Tags         debug
// @Produce      json
// @Security     BearerAuth
// @Param        id             path   string  true   "Project id"
// @Param        tail           query  int     false  "trailing lines, default 200, max 2000"
// @Param        since_seconds  query  int     false  "only lines newer than N seconds"
// @Success      200  {object}  LogSnapshotResponse
// @Router       /projects/{id}/debug/logs [get]
func (h *Handler) GetDebugLogs(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := pathID(c)
	if !ok {
		return
	}
	tail, since := logQueryParams(c)
	snap, err := h.uc.GetDebugLogs(c.Request.Context(), projectID, ownerID, tail, since)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toLogSnapshotResponse(snap))
}

// ---- Environment ----------------------------------------------------------------

// ListEnvironments godoc
// @Summary      List environment variables for a project
// @Tags         enviroment
// @Produce      json
// @Security     BearerAuth
// @Param        project  query     string  true  "Project id"
// @Success      200  {array}  EnvironmentResponse
// @Router       /enviroment [get]
func (h *Handler) ListEnvironments(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := queryProjectID(c)
	if !ok {
		return
	}
	items, err := h.uc.ListEnvironments(c.Request.Context(), projectID, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toEnvironmentListResponse(items))
}

// UpsertEnvironment godoc
// @Summary      Create or update an environment variable (upsert on key+project)
// @Tags         enviroment
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      UpsertEnvironmentRequest  true  "Environment payload"
// @Success      200   {object}  EnvironmentResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /enviroment [post]
func (h *Handler) UpsertEnvironment(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req UpsertEnvironmentRequest
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
		httpx.Error(c, httpx.BadRequest("project.invalid_id", "invalid project id"))
		return
	}
	env, err := h.uc.UpsertEnvironment(c.Request.Context(), UpsertEnvironmentInput{
		ProjectID: projectID,
		OwnerID:   ownerID,
		UserID:    ownerID,
		Key:       req.Key,
		Value:     req.Value,
		Secret:    req.Secret,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toEnvironmentResponse(env))
}

// DeleteEnvironment godoc
// @Summary      Delete an environment variable
// @Tags         enviroment
// @Security     BearerAuth
// @Param        key      path      string  true  "Environment key"
// @Param        project  query     string  true  "Project id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /enviroment/{key} [delete]
func (h *Handler) DeleteEnvironment(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := queryProjectID(c)
	if !ok {
		return
	}
	key := c.Param("key")
	if key == "" {
		httpx.Error(c, httpx.BadRequest("enviroment.invalid_key", "key is required"))
		return
	}
	if err := h.uc.DeleteEnvironment(c.Request.Context(), key, projectID, ownerID); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}
