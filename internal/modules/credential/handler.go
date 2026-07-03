package credential

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
		httpx.Error(c, httpx.BadRequest("credential.invalid_id", "invalid credential id"))
		return uuid.Nil, false
	}
	return id, true
}

// List godoc
// @Summary      List my credentials
// @Tags         credentials
// @Produce      json
// @Security     BearerAuth
// @Param        type_key  query     string  false  "Filter by credential type"
// @Param        limit     query     int     false  "Page size (default 20, max 100)"
// @Param        offset    query     int     false  "Rows to skip"
// @Success      200  {object}  ListResponse
// @Router       /credentials [get]
func (h *Handler) List(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.List(c.Request.Context(), ListFilter{
		OwnerID: ownerID,
		TypeKey: c.Query("type_key"),
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(items, total, int32(limit), int32(offset)))
}

// Types godoc
// @Summary      List credential types
// @Tags         credentials
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}  TypeDef
// @Router       /credentials/types [get]
func (h *Handler) Types(c *gin.Context) {
	if _, ok := owner(c); !ok {
		return
	}
	httpx.OK(c, 200, h.uc.ListTypes(c.Request.Context()))
}

// AdminTypes godoc
// @Summary      List credential types (admin, "Credential turlari" page)
// @Tags         credentials-admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}  TypeDef
// @Router       /admin/credential-types [get]
func (h *Handler) AdminTypes(c *gin.Context) {
	httpx.OK(c, 200, h.uc.ListTypes(c.Request.Context()))
}

// Create godoc
// @Summary      Create a credential
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateRequest  true  "Credential payload"
// @Success      201   {object}  CredentialResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /credentials [post]
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
	d, err := h.uc.Create(c.Request.Context(), CreateData{
		OwnerID: ownerID,
		TypeKey: req.TypeKey,
		Name:    req.Name,
		Data:    req.Data,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(d))
}

// Get godoc
// @Summary      Get a credential (masked data)
// @Tags         credentials
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Credential id"
// @Success      200  {object}  CredentialResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /credentials/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	d, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(d))
}

// Update godoc
// @Summary      Update a credential
// @Tags         credentials
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Credential id"
// @Param        body  body      UpdateRequest  true  "Fields to update"
// @Success      200   {object}  CredentialResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /credentials/{id} [patch]
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
	d, err := h.uc.Update(c.Request.Context(), id, ownerID, UpdateData{
		Name: req.Name,
		Data: req.Data,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(d))
}

// Delete godoc
// @Summary      Delete a credential (soft delete)
// @Tags         credentials
// @Security     BearerAuth
// @Param        id   path      string  true  "Credential id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /credentials/{id} [delete]
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

// InternalByID godoc
// @Summary      Resolve a credential by id (decrypted, engine-only)
// @Tags         credentials-internal
// @Produce      json
// @Param        X-Internal-Token  header    string  true  "Internal service token"
// @Param        X-Project-Id      header    string  false "Project id — cross-tenant owner guard"
// @Param        id                path      string  true  "Credential id"
// @Success      200  {object}  InternalResponse
// @Failure      403  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /internal/credentials/{id} [get]
func (h *Handler) InternalByID(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	projectID, ok := optionalProjectID(c)
	if !ok {
		return
	}
	d, err := h.uc.InternalGetByID(c.Request.Context(), id, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toInternalResponse(d))
}

// InternalByName godoc
// @Summary      Resolve a credential by name (decrypted, engine-only)
// @Tags         credentials-internal
// @Produce      json
// @Param        X-Internal-Token  header    string  true  "Internal service token"
// @Param        X-Project-Id      header    string  true  "Project id — the owner is derived from it"
// @Param        name              query     string  true  "Credential name"
// @Success      200  {object}  InternalResponse
// @Failure      400  {object}  httpx.ErrorResponse
// @Failure      403  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /internal/credentials/by-name [get]
func (h *Handler) InternalByName(c *gin.Context) {
	name := c.Query("name")
	if name == "" {
		httpx.Error(c, httpx.BadRequest("credential.name_required", "name query param required"))
		return
	}
	projectID, err := uuid.Parse(c.GetHeader("X-Project-Id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("credential.project_id_required", "X-Project-Id header required"))
		return
	}
	d, err := h.uc.InternalGetByName(c.Request.Context(), name, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toInternalResponse(d))
}

// optionalProjectID parses the X-Project-Id header when present; an absent
// header is fine (nil, true), a malformed one is a 400 (nil, false).
func optionalProjectID(c *gin.Context) (*uuid.UUID, bool) {
	raw := c.GetHeader("X-Project-Id")
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("credential.invalid_project_id", "invalid X-Project-Id header"))
		return nil, false
	}
	return &id, true
}
