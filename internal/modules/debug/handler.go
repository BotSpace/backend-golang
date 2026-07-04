package debug

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler { return &Handler{uc: uc} }

// ListErrors godoc
// @Summary  List recent flow/node debug errors for a project
// @Tags     debug
// @Security BearerAuth
// @Param    project_id  query  string  true   "project id"
// @Param    limit       query  int     false  "max rows (default 100, max 500)"
// @Router   /debug/errors [get]
func (h *Handler) ListErrors(c *gin.Context) {
	ownerID, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return
	}
	projectID, err := uuid.Parse(c.Query("project_id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("debug.invalid_project_id", "project_id is required and must be a uuid"))
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)

	items, err := h.uc.ListErrors(c.Request.Context(), ownerID, projectID, int32(limit))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toErrorsResponse(items))
}

// ListContext godoc
// @Summary  Latest flow-context (variables) snapshot per active debug chat
// @Tags     debug
// @Security BearerAuth
// @Param    project_id  query  string  true  "project id"
// @Router   /debug/context [get]
func (h *Handler) ListContext(c *gin.Context) {
	ownerID, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return
	}
	projectID, err := uuid.Parse(c.Query("project_id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("debug.invalid_project_id", "project_id is required and must be a uuid"))
		return
	}

	items, err := h.uc.ListContext(c.Request.Context(), ownerID, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toContextResponse(items))
}
