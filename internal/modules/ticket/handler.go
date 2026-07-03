package ticket

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

// actorID extracts the authenticated user id, writing a 401 on failure. It
// is used both for the ticket owner and for the acting admin.
func actorID(c *gin.Context) (uuid.UUID, bool) {
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
		httpx.Error(c, httpx.BadRequest("ticket.invalid_id", "invalid ticket id"))
		return uuid.Nil, false
	}
	return id, true
}

func pageParams(c *gin.Context) (limit, offset int32) {
	l, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	o, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	return int32(l), int32(o)
}

// List godoc
// @Summary      List my tickets
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        status  query     string  false  "Filter by status (open, pending, closed)"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  ListResponse
// @Router       /tickets [get]
func (h *Handler) List(c *gin.Context) {
	ownerID, ok := actorID(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.List(c.Request.Context(), ListFilter{
		OwnerID: ownerID,
		Status:  c.Query("status"),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(items, total, limit, offset))
}

// Create godoc
// @Summary      Open a support ticket
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateTicketRequest  true  "Subject + first message"
// @Success      201   {object}  TicketResponse
// @Failure      400   {object}  httpx.ErrorResponse
// @Router       /tickets [post]
func (h *Handler) Create(c *gin.Context) {
	ownerID, ok := actorID(c)
	if !ok {
		return
	}
	var req CreateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	t, err := h.uc.Create(c.Request.Context(), CreateInput{
		UserID:        ownerID,
		Subject:       req.Subject,
		Message:       req.Message,
		AttachmentURL: req.AttachmentURL,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(t))
}

// Get godoc
// @Summary      Get a ticket with its full conversation
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Ticket id"
// @Success      200  {object}  TicketResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /tickets/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	ownerID, ok := actorID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(t))
}

// Reply godoc
// @Summary      Reply to my ticket (reopens it for admin attention)
// @Tags         tickets
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string        true  "Ticket id"
// @Param        body  body      ReplyRequest  true  "Message"
// @Success      201   {object}  TicketResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /tickets/{id}/reply [post]
func (h *Handler) Reply(c *gin.Context) {
	ownerID, ok := actorID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req ReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	t, err := h.uc.Reply(c.Request.Context(), id, ownerID, req.Message, req.AttachmentURL)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(t))
}

// Close godoc
// @Summary      Close my ticket
// @Tags         tickets
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Ticket id"
// @Success      200  {object}  TicketResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /tickets/{id}/close [post]
func (h *Handler) Close(c *gin.Context) {
	ownerID, ok := actorID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.uc.Close(c.Request.Context(), id, ownerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(t))
}

// AdminList godoc
// @Summary      List all tickets (admin)
// @Tags         tickets-admin
// @Produce      json
// @Security     BearerAuth
// @Param        status   query     string  false  "Filter by status (open, pending, closed)"
// @Param        user_id  query     string  false  "Filter by ticket owner"
// @Param        search   query     string  false  "Filter by subject"
// @Param        limit    query     int     false  "Page size (default 20, max 100)"
// @Param        offset   query     int     false  "Rows to skip"
// @Success      200  {object}  ListResponse
// @Router       /admin/tickets [get]
func (h *Handler) AdminList(c *gin.Context) {
	var userID *uuid.UUID
	if raw := c.Query("user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("ticket.invalid_user_id", "invalid user_id"))
			return
		}
		userID = &id
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.AdminList(c.Request.Context(), AdminListFilter{
		Status: c.Query("status"),
		UserID: userID,
		Search: c.Query("search"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toListResponse(items, total, limit, offset))
}

// AdminGet godoc
// @Summary      Get any ticket with its full conversation (admin)
// @Tags         tickets-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Ticket id"
// @Success      200  {object}  TicketResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/tickets/{id} [get]
func (h *Handler) AdminGet(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.uc.AdminGet(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(t))
}

// AdminRespond godoc
// @Summary      Respond to a ticket as support (admin)
// @Tags         tickets-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string        true  "Ticket id"
// @Param        body  body      ReplyRequest  true  "Message"
// @Success      201   {object}  TicketResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Router       /admin/tickets/{id}/respond [post]
func (h *Handler) AdminRespond(c *gin.Context) {
	adminID, ok := actorID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req ReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	t, err := h.uc.AdminRespond(c.Request.Context(), id, adminID, req.Message, req.AttachmentURL)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toResponse(t))
}

// AdminClose godoc
// @Summary      Close any ticket (admin)
// @Tags         tickets-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Ticket id"
// @Success      200  {object}  TicketResponse
// @Failure      404  {object}  httpx.ErrorResponse
// @Router       /admin/tickets/{id}/close [post]
func (h *Handler) AdminClose(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.uc.AdminClose(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toResponse(t))
}
