package authbot

import (
	"net/http"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc  *Usecase
	bot BotAPI
}

// NewHandler wires the authbot HTTP handlers. bot is the Telegram Bot API
// client used only by Webhook (IssueCode/SetMessageID/Login don't need it);
// pass NewHTTPBotAPI(os.Getenv("BOT_TOKEN")) in production, a fake BotAPI in
// tests.
func NewHandler(uc *Usecase, bot BotAPI) *Handler {
	return &Handler{uc: uc, bot: bot}
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

// Webhook godoc
// @Summary      Telegram webhook for the botmother_authbot login bot
// @Description  Called by Telegram itself (not a client of this API). Unauthenticated, matching Django's TelegramWebhookView (AllowAny, no secret token check). Always acks 200 so Telegram doesn't retry-storm a transient failure; errors are logged and, where Django did the same, echoed back to the chat.
// @Tags         authbot
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]string
// @Router       /authbot/webhook [post]
func (h *Handler) Webhook(c *gin.Context) {
	var update TelegramUpdate
	if err := c.ShouldBindJSON(&update); err != nil {
		httpx.Error(c, httpx.BadRequest("authbot.malformed_update", "invalid telegram update"))
		return
	}
	h.processUpdate(c.Request.Context(), update)
	httpx.OK(c, http.StatusOK, gin.H{"status": "ok"})
}
