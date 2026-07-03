package webhook

import (
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const internalTokenHeader = "X-Internal-Token"
const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

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

// MintEndpoint godoc
// @Summary      Mint (or reuse) a node's webhook URL
// @Tags         webhook
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      MintRequest  true  "Mint payload"
// @Success      200   {object}  MintResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /webhook/endpoint [post]
func (h *Handler) MintEndpoint(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req MintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	projectID, err := uuid.Parse(req.Project)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("webhook.invalid_project", "invalid project id"))
		return
	}
	var chatID int64
	if req.ChatID != nil {
		chatID = *req.ChatID
	}
	e, err := h.uc.MintEndpoint(c.Request.Context(), MintInput{
		OwnerID:   ownerID,
		ProjectID: projectID,
		NodeID:    req.NodeID,
		ChatID:    chatID,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toMintResponse(e))
}

// ResolveToken godoc
// @Summary      Resolve a webhook token to its routing info (internal)
// @Tags         webhook
// @Produce      json
// @Param        token  path      string  true  "Webhook token"
// @Param        X-Internal-Token  header  string  true  "Internal service token"
// @Success      200  {object}  ResolveResponse
// @Failure      401  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /internal/webhook-hook/{token} [get]
func (h *Handler) ResolveToken(c *gin.Context) {
	token, err := uuid.Parse(c.Param("token"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("webhook.invalid_token", "invalid token"))
		return
	}
	hook, err := h.uc.ResolveToken(c.Request.Context(), c.GetHeader(internalTokenHeader), token)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResolveResponse(hook))
}

// ListManagedBots godoc
// @Summary      List my managed bots
// @Tags         managed-bot
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}  ManagedBotResponse
// @Router       /managed-bot [get]
func (h *Handler) ListManagedBots(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	bots, err := h.uc.ListManagedBots(c.Request.Context(), ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toManagedBotListResponse(bots))
}

// ManagedBotConfig godoc
// @Summary      Wizard deep-link configuration
// @Tags         managed-bot
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  ManagedBotConfigResponse
// @Router       /managed-bot/config [get]
func (h *Handler) ManagedBotConfig(c *gin.Context) {
	if _, ok := owner(c); !ok {
		return
	}
	httpx.OK(c, 200, toManagedBotConfigResponse(h.uc.ManagedBotConfig()))
}

// ManagedBotClaim godoc
// @Summary      Pre-claim a wizard bot username before it is created
// @Tags         managed-bot
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  ManagedBotClaimRequest  true  "Claim payload"
// @Success      200   {object}  map[string]bool
// @Router       /managed-bot/claim [post]
func (h *Handler) ManagedBotClaim(c *gin.Context) {
	ownerID, ok := owner(c)
	if !ok {
		return
	}
	var req ManagedBotClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	if err := h.uc.ClaimManagedBotUsername(ownerID, req.Username); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, gin.H{"ok": true})
}

// ManagedBotWebhook godoc
// @Summary      Manager bot Telegram webhook (managed_bot updates)
// @Tags         managed-bot
// @Accept       json
// @Param        secret  path  string  true  "Webhook path secret"
// @Success      200
// @Failure      403
// @Router       /managed-bot/webhook/{secret} [post]
func (h *Handler) ManagedBotWebhook(c *gin.Context) {
	secret := c.Param("secret")
	var body managedBotWebhookBody
	// Best-effort bind: an unparsable body still needs to 200 most paths
	// Telegram cares about (ack), so a bind error just yields an empty
	// payload rather than a hard failure.
	_ = c.ShouldBindJSON(&body)

	err := h.uc.HandleManagedBotWebhook(c.Request.Context(), secret, c.GetHeader(telegramSecretHeader), toWebhookPayload(body))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.Status(200)
}
