package template

import (
	"encoding/json"
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AdminList godoc
// @Summary      List all templates across every author (admin)
// @Tags         template-admin
// @Produce      json
// @Security     BearerAuth
// @Param        search  query     string  false  "Filter by name/slug/description"
// @Param        status  query     string  false  "Filter by status: verified|pending"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  AdminTemplateListResponse
// @Router       /admin/templates [get]
func (h *Handler) AdminList(c *gin.Context) {
	limit, offset := pageParams(c)
	f := AdminTemplateListFilter{
		Search: c.Query("search"),
		Limit:  limit,
		Offset: offset,
	}
	switch c.Query("status") {
	case "verified":
		v := true
		f.IsVerify = &v
	case "pending":
		v := false
		f.IsVerify = &v
	}
	if raw := c.Query("is_verify"); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			f.IsVerify = &v
		}
	}
	items, total, err := h.uc.AdminList(c.Request.Context(), f)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminTemplateListResponse(items, total, limit, offset))
}

// AdminGet godoc
// @Summary      Get any template's full detail regardless of status (admin)
// @Tags         template-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Template id"
// @Success      200  {object}  AdminTemplateDetailResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/templates/{id} [get]
func (h *Handler) AdminGet(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	s, err := h.uc.AdminGet(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminTemplateDetailResponse(s))
}

// AdminCreate godoc
// @Summary      Create a template directly (admin, bypasses submission review)
// @Tags         template-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateAdminTemplateRequest  true  "Template payload"
// @Success      201   {object}  AdminTemplateActionResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /admin/templates [post]
func (h *Handler) AdminCreate(c *gin.Context) {
	var req CreateAdminTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	catIDs, err := parseUUIDs(req.CategoryIDs)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_category", err.Error()))
		return
	}
	featIDs, err := parseUUIDs(req.FeatureIDs)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_feature", err.Error()))
		return
	}
	var authorID, projectID *uuid.UUID
	if req.AuthorID != "" {
		id, err := uuid.Parse(req.AuthorID)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("template.invalid_author_id", "invalid author id"))
			return
		}
		authorID = &id
	}
	if req.ProjectID != "" {
		id, err := uuid.Parse(req.ProjectID)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("template.invalid_project_id", "invalid project id"))
			return
		}
		projectID = &id
	}
	t, err := h.uc.AdminCreate(c.Request.Context(), CreateAdminTemplateInput{
		AuthorID:         authorID,
		ProjectID:        projectID,
		Name:             req.Name,
		Slug:             req.Slug,
		Image:            req.Image,
		Video:            req.Video,
		ShortDescription: req.ShortDescription,
		Description:      req.Description,
		OriginalPrice:    req.OriginalPrice,
		Discount:         req.Discount,
		DiscountType:     req.DiscountType,
		DemoURL:          req.DemoURL,
		IsVerify:         req.IsVerify,
		RequiredEnvs:     req.RequiredEnvs,
		CategoryIDs:      catIDs,
		FeatureIDs:       featIDs,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toAdminTemplateActionResponse(t))
}

// AdminUpdate godoc
// @Summary      Update a template (admin)
// @Tags         template-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                      true  "Template id"
// @Param        body  body      UpdateAdminTemplateRequest  true  "Fields to update"
// @Success      200   {object}  AdminTemplateActionResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /admin/templates/{id} [patch]
func (h *Handler) AdminUpdate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req UpdateAdminTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	in := UpdateAdminTemplateInput{
		Name:             req.Name,
		Image:            req.Image,
		Video:            req.Video,
		ShortDescription: req.ShortDescription,
		Description:      req.Description,
		OriginalPrice:    req.OriginalPrice,
		Discount:         req.Discount,
		DiscountType:     req.DiscountType,
		DemoURL:          req.DemoURL,
		IsVerify:         req.IsVerify,
		RequiredEnvs:     req.RequiredEnvs,
	}
	if req.CategoryIDs != nil {
		ids, err := parseUUIDs(req.CategoryIDs)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("template.invalid_category", err.Error()))
			return
		}
		in.CategoryIDs = ids
	}
	if req.FeatureIDs != nil {
		ids, err := parseUUIDs(req.FeatureIDs)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("template.invalid_feature", err.Error()))
			return
		}
		in.FeatureIDs = ids
	}
	t, err := h.uc.AdminUpdate(c.Request.Context(), id, in)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminTemplateActionResponse(t))
}

// AdminUpdateFlow updates the linked source project's flow even when that
// project is soft-deleted. Template sources are intentionally hidden from the
// normal project list, while the internal feed still resolves their flow.
func (h *Handler) AdminUpdateFlow(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req UpdateAdminTemplateFlowRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Flow) == 0 {
		httpx.Error(c, httpx.BadRequest("request.malformed", "flow is required"))
		return
	}
	var flow map[string]any
	if err := json.Unmarshal(req.Flow, &flow); err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_flow", "flow must be a JSON object"))
		return
	}
	if _, ok := flow["nodes"].([]any); !ok {
		httpx.Error(c, httpx.BadRequest("template.invalid_flow", "flow.nodes must be an array"))
		return
	}
	if _, ok := flow["edges"].([]any); !ok {
		httpx.Error(c, httpx.BadRequest("template.invalid_flow", "flow.edges must be an array"))
		return
	}
	if err := h.uc.AdminUpdateFlow(c.Request.Context(), id, req.Flow); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, gin.H{"updated": true})
}

// AdminDelete godoc
// @Summary      Delete a template (admin)
// @Tags         template-admin
// @Security     BearerAuth
// @Param        id   path      string  true  "Template id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/templates/{id} [delete]
func (h *Handler) AdminDelete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.uc.AdminDelete(c.Request.Context(), id); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// AdminVerify godoc
// @Summary      Mark a template verified/published (admin)
// @Tags         template-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Template id"
// @Success      200  {object}  AdminTemplateActionResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/templates/{id}/verify [patch]
func (h *Handler) AdminVerify(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.uc.AdminSetVerify(c.Request.Context(), id, true)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminTemplateActionResponse(t))
}

// AdminReject godoc
// @Summary      Unverify/unpublish a template (admin)
// @Tags         template-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Template id"
// @Success      200  {object}  AdminTemplateActionResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/templates/{id}/reject [patch]
func (h *Handler) AdminReject(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.uc.AdminSetVerify(c.Request.Context(), id, false)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminTemplateActionResponse(t))
}
