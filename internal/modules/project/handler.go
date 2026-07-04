package project

import (
	"strconv"
	"strings"

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
		httpx.Error(c, httpx.BadRequest("project.invalid_id", "invalid project id"))
		return uuid.Nil, false
	}
	return id, true
}

// List godoc
// @Summary      List my projects
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        search  query     string  false  "Filter by name"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  ListResponse
// @Router       /projects [get]
func (h *Handler) List(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.List(c.Request.Context(), ListFilter{
		OwnerID: ownerID,
		Search:  c.Query("search"),
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(items, total, int32(limit), int32(offset)))
}

// Create godoc
// @Summary      Create a project
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateRequest  true  "Project payload"
// @Success      201   {object}  ProjectResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /projects [post]
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
	p, err := h.uc.Create(c.Request.Context(), CreateInput{
		OwnerID:      ownerID,
		Name:         req.Name,
		Description:  req.Description,
		Username:     req.Username,
		Flow:         req.Flow,
		IsTemplate:   req.IsTemplate,
		Token:        req.Token,
		ManagedBotID: req.ManagedBotID,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(p))
}

// CreateFromTemplate godoc
// @Summary      Create a project seeded from a purchased marketplace template
// @Description  Requires the caller to already hold a paid order for the
// @Description  template (POST /template/orders — free templates are
// @Description  auto-confirmed there). mode=release (default) requires
// @Description  token; mode=debug deploys the tokenless debug container
// @Description  instead and ignores token entirely.
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateFromTemplateRequest  true  "Template + create payload"
// @Success      201  {object}  ProjectResponse
// @Failure      400  {object}  httpx.ErrorResponse  "validation failed / template not purchased / token required"
// @Failure      404  {object}  httpx.ErrorResponse  "template not found"
// @Router       /projects/create-from-template [post]
func (h *Handler) CreateFromTemplate(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req CreateFromTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = "release"
	}
	if mode == "release" && strings.TrimSpace(req.Token) == "" && strings.TrimSpace(req.ManagedBotID) == "" {
		httpx.Error(c, httpx.BadRequest(
			"project.token_required",
			"token yoki managed_bot_id majburiy (yoki mode=debug tanlang)",
		))
		return
	}
	p, err := h.uc.CreateFromTemplate(c.Request.Context(), CreateFromTemplateInput{
		OwnerID:      ownerID,
		Template:     req.Template,
		Name:         req.Name,
		Token:        req.Token,
		ManagedBotID: req.ManagedBotID,
		Mode:         mode,
		Envs:         req.Envs,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(p))
}

// Get godoc
// @Summary      Get a project
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Project id"
// @Success      200  {object}  ProjectResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /projects/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	p, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(p))
}

// Update godoc
// @Summary      Update a project
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Project id"
// @Param        body  body      UpdateRequest  true  "Fields to update"
// @Success      200   {object}  ProjectResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /projects/{id} [patch]
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
	p, err := h.uc.Update(c.Request.Context(), id, ownerID, UpdateInput{
		Name:        req.Name,
		Description: req.Description,
		Username:    req.Username,
		Flow:        req.Flow,
		Token:       req.Token,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(p))
}

// Delete godoc
// @Summary      Delete a project (soft delete)
// @Tags         projects
// @Security     BearerAuth
// @Param        id   path      string  true  "Project id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /projects/{id} [delete]
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

// ===================== admin-wide (cross-owner) =====================

// AdminList godoc
// @Summary      List all projects across every owner (admin)
// @Tags         projects-admin
// @Produce      json
// @Security     BearerAuth
// @Param        search  query     string  false  "Filter by name"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  AdminProjectListResponse
// @Router       /admin/projects [get]
func (h *Handler) AdminList(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.AdminList(c.Request.Context(), AdminListFilter{
		Search: c.Query("search"),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminProjectListResponse(items, total, int32(limit), int32(offset)))
}

// AdminGet godoc
// @Summary      Get any project's detail regardless of owner (admin)
// @Tags         projects-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Project id"
// @Success      200  {object}  AdminProjectDetailResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/projects/{id} [get]
func (h *Handler) AdminGet(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	p, err := h.uc.AdminGet(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminProjectDetailResponse(p))
}

// AdminMonitoring godoc
// @Summary      Per-project resource usage snapshot across every owner (admin)
// @Tags         projects-admin
// @Produce      json
// @Security     BearerAuth
// @Param        search  query     string  false  "Filter by name"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  MonitoringListResponse
// @Router       /admin/monitoring [get]
func (h *Handler) AdminMonitoring(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.AdminMonitoring(c.Request.Context(), AdminListFilter{
		Search: c.Query("search"),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toMonitoringListResponse(items, total, int32(limit), int32(offset)))
}
