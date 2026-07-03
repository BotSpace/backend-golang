package template

import (
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateOrder godoc
// @Summary      Order a template (returns the existing order if already placed)
// @Tags         template-order
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateOrderRequest  true  "Template to order"
// @Success      201   {object}  OrderResponse
// @Router       /template/orders [post]
func (h *Handler) CreateOrder(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	templateID, err := uuid.Parse(req.TemplateID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("template.invalid_template_id", "invalid template_id"))
		return
	}
	o, err := h.uc.CreateOrder(c.Request.Context(), uid, templateID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toOrderResponse(o))
}

// ListOrders godoc
// @Summary      List my template orders
// @Tags         template-order
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int  false  "Page size (default 20, max 100)"
// @Param        offset  query     int  false  "Rows to skip"
// @Success      200  {object}  OrderListResponse
// @Router       /template/orders [get]
func (h *Handler) ListOrders(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListOrders(c.Request.Context(), uid, limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toOrderListResponse(items, total, limit, offset))
}

// GetOrder godoc
// @Summary      Get one of my template orders
// @Tags         template-order
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Order id"
// @Success      200  {object}  OrderResponse
// @Router       /template/orders/{id} [get]
func (h *Handler) GetOrder(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	o, err := h.uc.GetOrder(c.Request.Context(), id, uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toOrderResponse(o))
}

// PayOrder godoc
// @Summary      Pay for a template order from the caller's wallet balance
// @Tags         template-order
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Order id"
// @Success      200  {object}  OrderResponse
// @Failure      409  {object}  httpx.ErrorResponse  "insufficient wallet balance"
// @Router       /template/orders/{id}/pay [post]
func (h *Handler) PayOrder(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	o, err := h.uc.PayOrder(c.Request.Context(), id, uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toOrderResponse(o))
}
