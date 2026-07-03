package user

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

// Me godoc
// @Summary      Get current user
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  UserResponse
// @Failure      401  {object}  httpx.ErrorResponse
// @Router       /users/me [get]
func (h *Handler) Me(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	u, err := h.uc.GetByID(c.Request.Context(), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(u))
}

// Balance godoc
// @Summary      Get current user's balance
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  BalanceResponse
// @Failure      401  {object}  httpx.ErrorResponse
// @Router       /users/me/balance [get]
func (h *Handler) Balance(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	u, err := h.uc.GetByID(c.Request.Context(), uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, BalanceResponse{Balance: u.Balance, TemplateBalance: u.TemplateBalance})
}

// UpdateMe godoc
// @Summary      Update current user's profile
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      UpdateMeRequest  true  "Update payload"
// @Success      200   {object}  UserResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /users/me [patch]
func (h *Handler) UpdateMe(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	var req UpdateMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	u, err := h.uc.UpdateProfile(c.Request.Context(), uid, ProfileUpdate{
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Username:  req.Username,
		Email:     req.Email,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(u))
}

// ChangePassword godoc
// @Summary      Change current user's password
// @Tags         users
// @Accept       json
// @Security     BearerAuth
// @Param        body  body      ChangePasswordRequest  true  "Passwords"
// @Success      204
// @Failure      400  {object}  httpx.ErrorResponse
// @Router       /users/me/password [post]
func (h *Handler) ChangePassword(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	if err := h.uc.ChangePassword(c.Request.Context(), uid, req.OldPassword, req.NewPassword); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// List godoc
// @Summary      List users (admin)
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Param        search  query     string  false  "Filter by phone or name"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip (default 0)"
// @Success      200  {object}  ListResponse
// @Router       /users [get]
func (h *Handler) List(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	users, total, err := h.uc.List(c.Request.Context(), ListFilter{
		Search: c.Query("search"),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(users, total, int32(limit), int32(offset)))
}

// GetByID godoc
// @Summary      Get user by id, enriched with subscription/projects (admin)
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "User id"
// @Success      200  {object}  AdminDetailResponse
// @Router       /users/{id} [get]
func (h *Handler) GetByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("user.invalid_id", "invalid user id"))
		return
	}
	d, err := h.uc.GetAdminDetail(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAdminDetailResponse(d))
}

// SetRole godoc
// @Summary      Set user role (admin)
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string          true  "User id"
// @Param        body  body      SetRoleRequest  true  "New role"
// @Success      200  {object}  UserResponse
// @Router       /users/{id}/role [patch]
func (h *Handler) SetRole(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("user.invalid_id", "invalid user id"))
		return
	}
	var req SetRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	u, err := h.uc.SetRole(c.Request.Context(), id, req.Role)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(u))
}

// Ban godoc
// @Summary      Ban a user (admin) — sets is_active = false
// @Tags         users-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "User id"
// @Success      200  {object}  UserResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/users/{id}/ban [post]
func (h *Handler) Ban(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("user.invalid_id", "invalid user id"))
		return
	}
	u, err := h.uc.Ban(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(u))
}

// Unban godoc
// @Summary      Unban a user (admin) — sets is_active = true
// @Tags         users-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "User id"
// @Success      200  {object}  UserResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/users/{id}/unban [post]
func (h *Handler) Unban(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("user.invalid_id", "invalid user id"))
		return
	}
	u, err := h.uc.Unban(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(u))
}

// AdjustBalance godoc
// @Summary      Adjust a user's balance by a signed delta (admin), writing an audit history row
// @Tags         users-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                 true  "User id"
// @Param        body  body      BalanceAdjustRequest   true  "Signed delta + reason"
// @Success      200   {object}  UserResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Failure      409   {object}  httpx.ErrorResponse
// @Router       /admin/users/{id}/balance [post]
func (h *Handler) AdjustBalance(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("user.invalid_id", "invalid user id"))
		return
	}
	var req BalanceAdjustRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	u, err := h.uc.AdminAdjustBalance(c.Request.Context(), id, req.Delta, req.Reason)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(u))
}
