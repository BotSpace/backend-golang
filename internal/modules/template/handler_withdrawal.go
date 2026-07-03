package template

import (
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
)

// CreateWithdrawal godoc
// @Summary      Request a withdrawal from my template earnings
// @Tags         template-withdrawal
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateWithdrawalRequest  true  "Withdrawal payload"
// @Success      201   {object}  WithdrawalResponse
// @Failure      409   {object}  httpx.ErrorResponse  "insufficient template balance"
// @Router       /template/withdrawals [post]
func (h *Handler) CreateWithdrawal(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	var req CreateWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	w, err := h.uc.CreateWithdrawal(c.Request.Context(), uid, req.Amount, req.CardNumber, req.CardOwner)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toWithdrawalResponse(w))
}

// ListWithdrawals godoc
// @Summary      List my withdrawal requests
// @Tags         template-withdrawal
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query     int  false  "Page size (default 20, max 100)"
// @Param        offset  query     int  false  "Rows to skip"
// @Success      200  {object}  WithdrawalListResponse
// @Router       /template/withdrawals [get]
func (h *Handler) ListWithdrawals(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListWithdrawals(c.Request.Context(), uid, limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalListResponse(items, total, limit, offset))
}

// GetWithdrawal godoc
// @Summary      Get one of my withdrawal requests
// @Tags         template-withdrawal
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Withdrawal id"
// @Success      200  {object}  WithdrawalResponse
// @Router       /template/withdrawals/{id} [get]
func (h *Handler) GetWithdrawal(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	w, err := h.uc.GetWithdrawal(c.Request.Context(), id, uid)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalResponse(w))
}

// DeleteWithdrawal godoc
// @Summary      Delete a pending withdrawal request
// @Tags         template-withdrawal
// @Security     BearerAuth
// @Param        id   path  string  true  "Withdrawal id"
// @Success      204
// @Router       /template/withdrawals/{id} [delete]
func (h *Handler) DeleteWithdrawal(c *gin.Context) {
	uid, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.uc.DeleteWithdrawal(c.Request.Context(), id, uid); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// ListWithdrawalsAdmin godoc
// @Summary      List all withdrawal requests (admin)
// @Tags         template-withdrawal
// @Produce      json
// @Security     BearerAuth
// @Param        status  query     string  false  "Filter by status (pending|approved|rejected)"
// @Param        limit   query     int     false  "Page size (default 20, max 100)"
// @Param        offset  query     int     false  "Rows to skip"
// @Success      200  {object}  WithdrawalListResponse
// @Router       /template/withdrawals/admin [get]
func (h *Handler) ListWithdrawalsAdmin(c *gin.Context) {
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListWithdrawalsAdmin(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalListResponse(items, total, limit, offset))
}

// ApproveWithdrawal godoc
// @Summary      Approve a pending withdrawal, deducting template_balance (admin)
// @Tags         template-withdrawal
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Withdrawal id"
// @Success      200  {object}  WithdrawalResponse
// @Router       /template/withdrawals/{id}/approve [patch]
func (h *Handler) ApproveWithdrawal(c *gin.Context) {
	reviewerID, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	w, err := h.uc.ApproveWithdrawal(c.Request.Context(), id, reviewerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalResponse(w))
}

// RejectWithdrawal godoc
// @Summary      Reject a pending withdrawal request (admin)
// @Tags         template-withdrawal
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "Withdrawal id"
// @Param        body  body      RejectRequest  true  "Rejection reason"
// @Success      200   {object}  WithdrawalResponse
// @Router       /template/withdrawals/{id}/reject [patch]
func (h *Handler) RejectWithdrawal(c *gin.Context) {
	reviewerID, ok := authUserID(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req RejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
		return
	}
	if details, err := validator.Struct(req); err != nil {
		httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
		return
	}
	w, err := h.uc.RejectWithdrawal(c.Request.Context(), id, reviewerID, req.RejectionReason)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalResponse(w))
}
