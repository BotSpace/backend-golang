package auth

import (
	"net/http"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	uc *Usecase
}

func NewHandler(uc *Usecase) *Handler {
	return &Handler{uc: uc}
}

// bind decodes and validates a JSON body, writing the error response itself.
// Returns ok=false when the caller should stop.
func bind[T any](c *gin.Context, req *T) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return false
	}
	if details, err := validator.Struct(*req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return false
	}
	return true
}

// Register godoc
// @Summary      Register (step 1) — create pending user and send OTP
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      RegisterRequest  true  "Registration payload"
// @Success      202   {object}  MessageResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Failure      409   {object}  httpx.ErrorResponse
// @Router       /auth/register [post]
func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if !bind(c, &req) {
		return
	}
	if err := h.uc.Register(c.Request.Context(), req); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusAccepted, MessageResponse{Detail: "confirmation code sent to " + req.Phone})
}

// Confirm godoc
// @Summary      Confirm registration (step 2) — verify OTP, return tokens
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      ConfirmRequest  true  "Phone + code"
// @Success      200   {object}  TokenResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /auth/confirm [post]
func (h *Handler) Confirm(c *gin.Context) {
	var req ConfirmRequest
	if !bind(c, &req) {
		return
	}
	tokens, err := h.uc.Confirm(c.Request.Context(), req)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, tokens)
}

// Resend godoc
// @Summary      Resend the confirmation code
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      PhoneRequest  true  "Phone"
// @Success      202   {object}  MessageResponse
// @Failure      429   {object}  httpx.ErrorResponse
// @Router       /auth/resend [post]
func (h *Handler) Resend(c *gin.Context) {
	var req PhoneRequest
	if !bind(c, &req) {
		return
	}
	if err := h.uc.Resend(c.Request.Context(), req.Phone); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusAccepted, MessageResponse{Detail: "confirmation code sent to " + req.Phone})
}

// Login godoc
// @Summary      Login by phone + password
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest  true  "Login payload"
// @Success      200   {object}  TokenResponse
// @Failure      401   {object}  httpx.ErrorResponse
// @Failure      403   {object}  httpx.ErrorResponse
// @Router       /auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if !bind(c, &req) {
		return
	}
	tokens, err := h.uc.Login(c.Request.Context(), req)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, tokens)
}

// Refresh godoc
// @Summary      Refresh access token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      RefreshRequest  true  "Refresh payload"
// @Success      200   {object}  TokenResponse
// @Failure      401   {object}  httpx.ErrorResponse
// @Router       /auth/refresh [post]
func (h *Handler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if !bind(c, &req) {
		return
	}
	tokens, err := h.uc.Refresh(c.Request.Context(), req)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, tokens)
}

// Logout godoc
// @Summary      Logout — revoke a refresh token
// @Tags         auth
// @Accept       json
// @Param        body  body      RefreshRequest  true  "Refresh token to revoke"
// @Success      204
// @Router       /auth/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	var req RefreshRequest
	if !bind(c, &req) {
		return
	}
	if err := h.uc.Logout(c.Request.Context(), req); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// ResetStart godoc
// @Summary      Password reset (step 1) — send OTP to a known phone
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      PhoneRequest  true  "Phone"
// @Success      202   {object}  MessageResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /auth/reset-password [post]
func (h *Handler) ResetStart(c *gin.Context) {
	var req PhoneRequest
	if !bind(c, &req) {
		return
	}
	if err := h.uc.ResetStart(c.Request.Context(), req.Phone); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusAccepted, MessageResponse{Detail: "confirmation code sent to " + req.Phone})
}

// ResetConfirm godoc
// @Summary      Password reset (step 2) — verify OTP, return reset token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      ConfirmRequest  true  "Phone + code"
// @Success      200   {object}  ResetTokenResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /auth/reset-password/confirm [post]
func (h *Handler) ResetConfirm(c *gin.Context) {
	var req ConfirmRequest
	if !bind(c, &req) {
		return
	}
	token, err := h.uc.ResetConfirm(c.Request.Context(), req)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, ResetTokenResponse{Token: token})
}

// ResetSet godoc
// @Summary      Password reset (step 3) — set new password with reset token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      ResetSetRequest  true  "Reset token + new password"
// @Success      204
// @Failure      401   {object}  httpx.ErrorResponse
// @Router       /auth/reset-password/set [post]
func (h *Handler) ResetSet(c *gin.Context) {
	var req ResetSetRequest
	if !bind(c, &req) {
		return
	}
	if err := h.uc.ResetSet(c.Request.Context(), req); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}
