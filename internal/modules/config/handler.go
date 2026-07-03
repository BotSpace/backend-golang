package config

import (
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// Get godoc
// @Summary      Get a platform config entry (admin)
// @Tags         admin-settings
// @Produce      json
// @Security     BearerAuth
// @Param        key  path      string  true  "Config key (general, ai)"
// @Success      200  {object}  ConfigResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/settings/{key} [get]
func (h *Handler) Get(c *gin.Context) {
	e, err := h.uc.Get(c.Request.Context(), c.Param("key"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(e))
}

// Update godoc
// @Summary      Update a platform config entry (admin)
// @Tags         admin-settings
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key   path      string  true  "Config key (general, ai)"
// @Param        body  body      object  true  "Arbitrary JSON config value"
// @Success      200   {object}  ConfigResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /admin/settings/{key} [put]
func (h *Handler) Update(c *gin.Context) {
	actorID, err := uuid.Parse(middleware.UserID(c))
	if err != nil {
		httpx.Error(c, httpx.Unauthorized("auth.invalid_token", "invalid token"))
		return
	}
	body, err := c.GetRawData()
	if err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	e, err := h.uc.Set(c.Request.Context(), c.Param("key"), body, actorID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(e))
}

// PublicSharedConfig godoc
// @Summary  Public flat ai_agent.* config (no secrets) — used by agentv3
// @Tags     shared-config
// @Produce  json
// @Router   /shared/config/public [get]
func (h *Handler) PublicSharedConfig(c *gin.Context) {
	out, err := h.uc.SharedAIAgentConfig(c.Request.Context(), false)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, out)
}

// InternalSharedConfig godoc
// @Summary  Internal flat ai_agent.* config incl. secrets (X-Internal-Token) — used by agentv3
// @Tags     shared-config
// @Produce  json
// @Param    X-Internal-Token  header  string  true  "Internal service token"
// @Router   /shared/config/internal [get]
func (h *Handler) InternalSharedConfig(c *gin.Context) {
	out, err := h.uc.SharedAIAgentConfig(c.Request.Context(), true)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, out)
}
