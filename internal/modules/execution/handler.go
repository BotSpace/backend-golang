package execution

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct{ uc Usecase }

func NewHandler(uc Usecase) *Handler { return &Handler{uc: uc} }

// List godoc
// @Summary      List workflow executions for a project
// @Description  Returns owner-scoped execution history ordered newest first.
// @Tags         executions
// @Produce      json
// @Security     BearerAuth
// @Param        project_id query string true  "Project ID (uuid)"
// @Param        status     query string false "running, waiting, succeeded, failed, cancelled"
// @Param        runtime_mode query string false "debug or release"
// @Param        limit      query int    false "Page size (default 50, max 200)"
// @Param        offset     query int    false "Page offset"
// @Success      200 {array} Response
// @Failure      400 {object} httpx.ErrorResponse
// @Failure      401 {object} httpx.ErrorResponse
// @Failure      500 {object} httpx.ErrorResponse
// @Router       /executions [get]
func (h *Handler) List(c *gin.Context) {
	ownerID, ok := authenticatedUserID(c)
	if !ok {
		return
	}
	projectID, err := uuid.Parse(c.Query("project_id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("execution.invalid_project_id", "project_id is required and must be a uuid"))
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, err := h.uc.List(c.Request.Context(), ListFilter{
		ProjectID: projectID, OwnerID: ownerID, Status: Status(c.Query("status")),
		RuntimeMode: c.Query("runtime_mode"),
		Limit:       int32(limit), Offset: int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(items))
}

// Get godoc
// @Summary      Get one workflow execution and its node runs
// @Tags         executions
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Execution ID (uuid)"
// @Success      200 {object} DetailResponse
// @Failure      400 {object} httpx.ErrorResponse
// @Failure      401 {object} httpx.ErrorResponse
// @Failure      404 {object} httpx.ErrorResponse
// @Failure      500 {object} httpx.ErrorResponse
// @Router       /executions/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	ownerID, ok := authenticatedUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("execution.invalid_id", "execution id must be a uuid"))
		return
	}
	item, nodes, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, DetailResponse{Execution: toResponse(*item), NodeRuns: toNodeRunResponse(nodes)})
}

func authenticatedUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return id, true
}
