package chat

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

func owner(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return id, true
}

// pagingParams reads limit/skip query params, matching Django's defaults
// (limit=50, skip=0) and silently falling back on parse errors.
func pagingParams(c *gin.Context) (limit, skip int64) {
	limit, skip = 50, 0
	if v, err := strconv.ParseInt(c.Query("limit"), 10, 64); err == nil {
		limit = v
	}
	if v, err := strconv.ParseInt(c.Query("skip"), 10, 64); err == nil {
		skip = v
	}
	return limit, skip
}

// ListUsers godoc
// @Summary      List a project's chat users with last-message stats
// @Tags         chat
// @Produce      json
// @Security     BearerAuth
// @Param        project_id  path   string  true   "Project id (Django legacy pk or uuid)"
// @Param        limit       query  int     false  "default 50"
// @Param        skip        query  int     false  "default 0"
// @Success      200  {object}  ListUsersResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /chat/users/{project_id} [get]
func (h *Handler) ListUsers(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	limit, skip := pagingParams(c)
	page, err := h.uc.ListUsers(c.Request.Context(), ownerID, c.Param("project_id"), limit, skip)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListUsersResponse(page, limit, skip))
}

// ListMessages godoc
// @Summary      List a chat_id's message history for a project
// @Tags         chat
// @Produce      json
// @Security     BearerAuth
// @Param        project_id  path   string  true   "Project id (Django legacy pk or uuid)"
// @Param        chat_id     path   int     true   "Telegram chat id"
// @Param        limit       query  int     false  "default 50"
// @Param        skip        query  int     false  "default 0"
// @Success      200  {object}  ListMessagesResponse
// @Failure      400  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /chat/messages/{project_id}/{chat_id} [get]
func (h *Handler) ListMessages(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	chatID, err := strconv.ParseInt(c.Param("chat_id"), 10, 64)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("chat.invalid_chat_id", "chat_id must be an integer"))
		return
	}
	limit, skip := pagingParams(c)
	page, err := h.uc.ListMessages(c.Request.Context(), ownerID, c.Param("project_id"), chatID, limit, skip)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListMessagesResponse(page, limit, skip))
}
