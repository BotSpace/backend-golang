package template

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

// authUserID extracts and parses the authenticated user id, writing a 401 and
// returning ok=false when it is missing/invalid.
func authUserID(c *gin.Context) (uuid.UUID, bool) {
	uid, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return uuid.Nil, false
	}
	return uid, true
}

// viewerID returns the caller's id when optionalAuth resolved one, else nil
// (public catalog routes never require a token).
func viewerID(c *gin.Context) *uuid.UUID {
	uid, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		return nil
	}
	return &uid
}

func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_id", "invalid id"))
		return uuid.Nil, false
	}
	return id, true
}

func pageParams(c *gin.Context) (int32, int32) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	return int32(limit), int32(offset)
}

// ===================== categories & features =====================

// ListCategories godoc
// @Summary      List template categories
// @Tags         template
// @Produce      json
// @Success      200  {array}  CategoryResponse
// @Router       /template/category [get]
func (h *Handler) ListCategories(c *gin.Context) {
	items, err := h.uc.ListCategories(c.Request.Context())
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toCategoryResponses(items))
}

// CreateCategory godoc
// @Summary      Create a template category (admin)
// @Tags         template
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateCategoryRequest  true  "Category payload"
// @Success      201   {object}  CategoryResponse
// @Router       /template/category [post]
func (h *Handler) CreateCategory(c *gin.Context) {
	var req CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	cat, err := h.uc.CreateCategory(c.Request.Context(), req.Name)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toCategoryResponse(cat))
}

// ListFeatures godoc
// @Summary      List template features
// @Tags         template
// @Produce      json
// @Success      200  {array}  FeatureResponse
// @Router       /template/feature [get]
func (h *Handler) ListFeatures(c *gin.Context) {
	items, err := h.uc.ListFeatures(c.Request.Context())
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toFeatureResponses(items))
}

// CreateFeature godoc
// @Summary      Create a template feature (admin)
// @Tags         template
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateFeatureRequest  true  "Feature payload"
// @Success      201   {object}  FeatureResponse
// @Router       /template/feature [post]
func (h *Handler) CreateFeature(c *gin.Context) {
	var req CreateFeatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	f, err := h.uc.CreateFeature(c.Request.Context(), req.Name, req.Value)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toFeatureResponse(f))
}

// ===================== catalog =====================

// List godoc
// @Summary      List published templates (AI-friendly smart search)
// @Tags         template
// @Produce      json
// @Param        search      query     string  false  "Keyword search over name/description/categories/features"
// @Param        category_id query     string  false  "Filter by category id"
// @Param        feature_id  query     string  false  "Filter by feature id"
// @Param        min_price   query     string  false  "Minimum price"
// @Param        max_price   query     string  false  "Maximum price"
// @Param        limit       query     int     false  "Page size (default 20, max 100)"
// @Param        offset      query     int     false  "Rows to skip"
// @Success      200  {object}  TemplateListResponse
// @Router       /template/list [get]
func (h *Handler) List(c *gin.Context) {
	limit, offset := pageParams(c)
	f := TemplateListFilter{
		Search:   c.Query("search"),
		MinPrice: c.Query("min_price"),
		MaxPrice: c.Query("max_price"),
		ViewerID: viewerID(c),
		Limit:    limit,
		Offset:   offset,
	}
	if raw := c.Query("category_id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			f.CategoryID = &id
		}
	}
	if raw := c.Query("feature_id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			f.FeatureID = &id
		}
	}
	items, total, err := h.uc.List(c.Request.Context(), f)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toTemplateListResponse(items, total, limit, offset))
}

// GetBySlug godoc
// @Summary      Get a published template by slug (bumps view count)
// @Tags         template
// @Produce      json
// @Param        slug  path      string  true  "Template slug"
// @Success      200   {object}  TemplateResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /template/list/{slug} [get]
func (h *Handler) GetBySlug(c *gin.Context) {
	t, err := h.uc.GetBySlug(c.Request.Context(), c.Param("slug"), viewerID(c))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toTemplateResponse(t))
}

// ToggleLike godoc
// @Summary      Like/unlike a template
// @Tags         template
// @Produce      json
// @Security     BearerAuth
// @Param        slug  path      string  true  "Template slug"
// @Success      200   {object}  LikeResponse
// @Router       /template/list/{slug}/like [post]
func (h *Handler) ToggleLike(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	liked, err := h.uc.ToggleLike(c.Request.Context(), c.Param("slug"), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, LikeResponse{Liked: liked})
}
