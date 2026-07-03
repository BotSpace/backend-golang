package message

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
		httpx.Error(c, httpx.BadRequest("message.invalid_id", "invalid message id"))
		return uuid.Nil, false
	}
	return id, true
}

// optionalProjectQuery parses the ?project_id= query param when present; an
// absent value is fine (nil, true), a malformed one is a 400 (nil, false).
func optionalProjectQuery(c *gin.Context) (*uuid.UUID, bool) {
	raw := c.Query("project_id")
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("message.invalid_project_id", "invalid project_id query param"))
		return nil, false
	}
	return &id, true
}

// List godoc
// @Summary      List my broadcast messages
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Param        project_id  query     string  false  "Filter by project id"
// @Param        limit       query     int     false  "Page size (default 20, max 100)"
// @Param        offset      query     int     false  "Rows to skip"
// @Success      200  {object}  ListResponse
// @Router       /messages [get]
func (h *Handler) List(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	projectID, ok := optionalProjectQuery(c)
	if !ok {
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.List(c.Request.Context(), ListFilter{
		OwnerID:   ownerID,
		ProjectID: projectID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(items, total, int32(limit), int32(offset)))
}

// Create godoc
// @Summary      Create (queue) a broadcast message
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateRequest  true  "Message payload"
// @Success      201   {object}  MessageResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /messages [post]
func (h *Handler) Create(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req CreateRequest
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
		httpx.Error(c, httpx.BadRequest("message.invalid_project_id", "invalid project_id"))
		return
	}
	m, err := h.uc.Create(c.Request.Context(), CreateInput{
		OwnerID:     ownerID,
		ProjectID:   projectID,
		Caption:     req.Caption,
		MessageType: req.MessageType,
		FileID:      req.FileID,
		ButtonType:  req.ButtonType,
		Buttons:     req.Buttons,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(m))
}

// Get godoc
// @Summary      Get a broadcast message
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Message id"
// @Success      200  {object}  MessageResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /messages/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	m, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(m))
}

// Update godoc
// @Summary      Update a broadcast message
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Message id"
// @Param        body  body      UpdateRequest  true  "Fields to update"
// @Success      200   {object}  MessageResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /messages/{id} [patch]
func (h *Handler) Update(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	m, err := h.uc.Update(c.Request.Context(), id, ownerID, UpdateInput{
		Caption:     req.Caption,
		MessageType: req.MessageType,
		FileID:      req.FileID,
		ButtonType:  req.ButtonType,
		Buttons:     req.Buttons,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(m))
}

// Delete godoc
// @Summary      Delete a broadcast message
// @Tags         messages
// @Security     BearerAuth
// @Param        id   path      string  true  "Message id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /messages/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), id, ownerID); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}
