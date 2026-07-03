package administration

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc Usecase
}

func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc}
}

// ListAuditLog godoc
// @Summary      List admin audit log entries (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        action    query     string  false  "Filter by action (substring match)"
// @Param        target    query     string  false  "Filter by target (substring match)"
// @Param        actor_id  query     string  false  "Filter by actor user id"
// @Param        limit     query     int     false  "Page size (default 20, max 100)"
// @Param        offset    query     int     false  "Rows to skip (default 0)"
// @Success      200  {object}  AuditLogListResponse
// @Router       /admin/audit-log [get]
func (h *Handler) ListAuditLog(c *gin.Context) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)

	var actorID *uuid.UUID
	if raw := c.Query("actor_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("audit.invalid_actor_id", "invalid actor id"))
			return
		}
		actorID = &id
	}

	items, total, err := h.uc.ListAuditLog(c.Request.Context(), ListFilter{
		Action:  c.Query("action"),
		Target:  c.Query("target"),
		ActorID: actorID,
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAuditLogListResponse(items, total, int32(limit), int32(offset)))
}

// GetAuditLog godoc
// @Summary      Get an audit log entry by id (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Audit log id"
// @Success      200  {object}  AuditLogResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/audit-log/{id} [get]
func (h *Handler) GetAuditLog(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("audit.invalid_id", "invalid audit log id"))
		return
	}
	entry, err := h.uc.GetAuditLog(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toAuditLogResponse(entry))
}

// DashboardStats godoc
// @Summary      Admin dashboard KPIs (admin)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  DashboardStatsResponse
// @Router       /admin/stats/dashboard [get]
func (h *Handler) DashboardStats(c *gin.Context) {
	stats, err := h.uc.DashboardStats(c.Request.Context())
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toDashboardStatsResponse(stats))
}
