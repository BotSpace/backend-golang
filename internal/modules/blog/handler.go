package blog

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler exposes both the public (published-only) blog endpoints and the
// admin CRUD endpoints over the same Usecase.
type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// authorID extracts the authenticated admin's id, writing a 401 on failure.
func authorID(c *gin.Context) (uuid.UUID, bool) {
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
		httpx.Error(c, httpx.BadRequest("blog.invalid_id", "invalid blog post id"))
		return uuid.Nil, false
	}
	return id, true
}

// PublicList godoc
// @Summary      List published blog posts
// @Tags         blog
// @Produce      json
// @Param        search  query     string  false  "Filter by title/excerpt"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  PublicListResponse
// @Router       /blog [get]
func (h *Handler) PublicList(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	items, total, err := h.uc.ListPublished(c.Request.Context(), PublicListFilter{
		Search: c.Query("search"),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toPublicListResponse(items, total, int32(limit), int32(offset)))
}

// PublicGet godoc
// @Summary      Get a published blog post by slug
// @Tags         blog
// @Produce      json
// @Param        slug  path      string  true  "Post slug"
// @Success      200  {object}  PublicDetailResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /blog/{slug} [get]
func (h *Handler) PublicGet(c *gin.Context) {
	p, err := h.uc.GetPublishedBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toPublicDetail(p))
}

// AdminList godoc
// @Summary      List blog posts (admin, drafts included)
// @Tags         admin-blog
// @Produce      json
// @Security     BearerAuth
// @Param        search        query     string  false  "Filter by title/slug/excerpt"
// @Param        is_published  query     bool    false  "Filter by publication state"
// @Param        limit         query     int     false  "Page size (default 20, max 100)"
// @Param        offset        query     int     false  "Rows to skip"
// @Success      200  {object}  AdminListResponse
// @Router       /admin/blog [get]
func (h *Handler) AdminList(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	f := ListFilter{
		Search: c.Query("search"),
		Limit:  int32(limit),
		Offset: int32(offset),
	}
	if raw := c.Query("is_published"); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			f.IsPublished = &v
		}
	}
	items, total, err := h.uc.ListAdmin(c.Request.Context(), f)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminListResponse(items, total, int32(limit), int32(offset)))
}

// AdminGet godoc
// @Summary      Get a blog post by id (admin)
// @Tags         admin-blog
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Post id"
// @Success      200  {object}  AdminBlogPostResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/blog/{id} [get]
func (h *Handler) AdminGet(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	p, err := h.uc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminResponse(p))
}

// AdminCreate godoc
// @Summary      Create a blog post (admin)
// @Tags         admin-blog
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateRequest  true  "Post payload"
// @Success      201   {object}  AdminBlogPostResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /admin/blog [post]
func (h *Handler) AdminCreate(c *gin.Context) {
	adminID, ok := authorID(c)
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
		Title:       req.Title,
		Excerpt:     req.Excerpt,
		Content:     req.Content,
		IsPublished: req.IsPublished,
		AuthorID:    adminID,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toAdminResponse(p))
}

// AdminUpdate godoc
// @Summary      Update a blog post (admin)
// @Tags         admin-blog
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Post id"
// @Param        body  body      UpdateRequest  true  "Fields to update"
// @Success      200   {object}  AdminBlogPostResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /admin/blog/{id} [patch]
func (h *Handler) AdminUpdate(c *gin.Context) {
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
	p, err := h.uc.Update(c.Request.Context(), id, UpdateInput{
		Title:       req.Title,
		Excerpt:     req.Excerpt,
		Content:     req.Content,
		IsPublished: req.IsPublished,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminResponse(p))
}

// AdminDelete godoc
// @Summary      Delete a blog post (admin)
// @Tags         admin-blog
// @Security     BearerAuth
// @Param        id   path      string  true  "Post id"
// @Success      204
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/blog/{id} [delete]
func (h *Handler) AdminDelete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), id); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}
