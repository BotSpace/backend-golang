package template

import (
	"crypto/subtle"
	"os"
	"strings"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
)

// InternalList godoc
// @Summary      List every template with its flow JSON (agent RAG feed)
// @Tags         template-internal
// @Produce      json
// @Param        X-Internal-Token  header  string  true   "Internal service token"
// @Param        only_verified     query   bool    false  "default true"
// @Success      200  {object}  FeedListResponse
// @Failure      403  {object}  httpx.ErrorResponse
// @Router       /template/internal/list [get]
func (h *Handler) InternalList(c *gin.Context) {
	onlyVerified := strings.ToLower(c.DefaultQuery("only_verified", "true")) != "false"
	items, err := h.uc.ListInternalFeed(c.Request.Context(), onlyVerified)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, FeedListResponse{Results: items})
}

// InternalGet godoc
// @Summary      Get one template (by id or slug) with its flow JSON
// @Tags         template-internal
// @Produce      json
// @Param        X-Internal-Token  header  string  true  "Internal service token"
// @Param        identifier        path    string  true  "Template id (uuid) or slug"
// @Success      200  {object}  FeedItem
// @Failure      403  {object}  httpx.ErrorResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /template/internal/{identifier} [get]
func (h *Handler) InternalGet(c *gin.Context) {
	item, err := h.uc.GetInternalFeedItem(c.Request.Context(), c.Param("identifier"))
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, item)
}

// internalTokenAuth gates the RAG feed behind the shared X-Internal-Token
// header — identical check to internal/modules/credential's
// internalTokenAuth(), duplicated per PORTING_GUIDE.md rule #4 (no
// cross-module import).
func internalTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := os.Getenv("APP_INTERNAL_TOKEN")
		provided := c.GetHeader("X-Internal-Token")
		if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			httpx.Error(c, httpx.Unauthorized("internal.unauthorized", "invalid internal token"))
			return
		}
		c.Next()
	}
}
