package authbot

import (
	"net/http"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// IssueCode godoc
// @Summary      Issue a one-time login code for a Telegram identity
// @Description  Called by the Telegram bot service after a user shares their phone number, not by end users.
// @Tags         authbot
// @Accept       json
// @Produce      json
// @Param        body  body      IssueCodeRequest  true  "Telegram contact-share payload"
// @Success      200   {object}  IssueCodeResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /authbot/code [post]
func (h *Handler) IssueCode(c *gin.Context) {
	var req IssueCodeRequest
	if !bind(c, &req) {
		return
	}
	issued, err := h.uc.IssueCode(c.Request.Context(), IssueCodeInput{
		TelegramID: req.TelegramID,
		Phone:      req.Phone,
		FirstName:  req.FirstName,
		LastName:   req.LastName,
		Username:   req.Username,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, toIssueCodeResponse(issued))
}

// SetMessageID godoc
// @Summary      Record the Telegram message id that displays a code
// @Description  Called by the Telegram bot service right after it sends the code message.
// @Tags         authbot
// @Accept       json
// @Param        id    path  string               true  "Code id"
// @Param        body  body  SetMessageIDRequest  true  "Telegram message id"
// @Success      204
// @Failure      400  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /authbot/codes/{id}/message-id [patch]
func (h *Handler) SetMessageID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("authbot.invalid_id", "invalid code id"))
		return
	}
	var req SetMessageIDRequest
	if !bind(c, &req) {
		return
	}
	if err := h.uc.SetMessageID(c.Request.Context(), id, req.MessageID); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// Login godoc
// @Summary      Exchange a Telegram one-time code for app JWT tokens
// @Tags         authbot
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest  true  "One-time code"
// @Success      200   {object}  LoginResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /authbot/login [post]
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if !bind(c, &req) {
		return
	}
	result, err := h.uc.Login(c.Request.Context(), req.Code)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, http.StatusOK, toLoginResponse(result))
}
