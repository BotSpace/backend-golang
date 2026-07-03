package payment

import (
	"strconv"

	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/botspace/backend/internal/shared/middleware"
	"github.com/botspace/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	uc    Usecase
	click ClickGateway
	payme PaymeGateway
}

// NewHandler builds the payment HTTP handler. click/payme are separate
// gateway value structs (not derived from uc) purely for webhook signature
// verification — the pay_url generation itself happens inside the usecase.
func NewHandler(uc Usecase) *Handler {
	return &Handler{uc: uc, click: NewClickGatewayFromEnv(), payme: NewPaymeGatewayFromEnv()}
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

// pathID parses the :id path param, writing a 400 on failure.
func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, httpx.BadRequest("payment.invalid_id", "invalid id"))
		return uuid.Nil, false
	}
	return id, true
}

func pageParams(c *gin.Context) (int32, int32) {
	limit, _ := strconv.ParseInt(c.Query("limit"), 10, 32)
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 32)
	return int32(limit), int32(offset)
}

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

// ===================== transactions =====================

// ListTransactions godoc
// @Summary      List my gateway transactions
// @Tags         payment-transactions
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  TransactionListResponse
// @Router       /payment/transactions [get]
func (h *Handler) ListTransactions(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListTransactionsForUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toTransactionListResponse(items, total, limit, offset))
}

// GetTransaction godoc
// @Summary      Get one of my gateway transactions
// @Tags         payment-transactions
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Transaction id"
// @Success      200  {object}  TransactionResponse
// @Router       /payment/transactions/{id} [get]
func (h *Handler) GetTransaction(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.uc.GetTransactionForUser(c.Request.Context(), id, userID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toTransactionResponse(t))
}

// AdminListTransactions godoc
// @Summary      [admin] List all gateway transactions (all users)
// @Tags         payment-admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  TransactionListResponse
// @Router       /admin/payment/transactions [get]
func (h *Handler) AdminListTransactions(c *gin.Context) {
	limit, offset := pageParams(c)
	items, total, err := h.uc.AdminListTransactions(c.Request.Context(), limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toTransactionListResponse(items, total, limit, offset))
}

// AdminListOrders godoc
// @Summary      [admin] List all bot payment orders (all owners)
// @Tags         payment-admin
// @Produce      json
// @Security     BearerAuth
// @Param        status  query     string  false  "Filter by status (created/paid/cancelled/refunded)"
// @Success      200  {object}  OrderListResponse
// @Router       /admin/payment/orders [get]
func (h *Handler) AdminListOrders(c *gin.Context) {
	limit, offset := pageParams(c)
	items, total, err := h.uc.AdminListOrders(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toOrderListResponse(items, total, limit, offset))
}

// ===================== history =====================

// ListHistory godoc
// @Summary      List my balance ledger entries
// @Tags         payment-history
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  HistoryListResponse
// @Router       /payment/history [get]
func (h *Handler) ListHistory(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListHistoryForUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toHistoryListResponse(items, total, limit, offset))
}

// GetHistory godoc
// @Summary      Get one balance ledger entry
// @Tags         payment-history
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "History id"
// @Success      200  {object}  HistoryResponse
// @Router       /payment/history/{id} [get]
func (h *Handler) GetHistory(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	hist, err := h.uc.GetHistoryForUser(c.Request.Context(), id, userID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toHistoryResponse(hist))
}

// ===================== earnings / orders =====================

// GetEarnings godoc
// @Summary      My bot earnings dashboard (balance + paid orders)
// @Tags         payment-earnings
// @Produce      json
// @Security     BearerAuth
// @Param        project_id  query     string  false  "Filter by bot"
// @Param        limit       query     int     false  "Page size (default 20, max 100)"
// @Param        offset      query     int     false  "Rows to skip"
// @Success      200  {object}  EarningsResponse
// @Router       /payment/earnings [get]
func (h *Handler) GetEarnings(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	f := EarningsFilter{OwnerID: userID, Limit: limit, Offset: offset}
	if raw := c.Query("project_id"); raw != "" {
		pid, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("payment.invalid_project_id", "invalid project_id"))
			return
		}
		f.ProjectID = &pid
	}
	result, err := h.uc.ListEarnings(c.Request.Context(), f)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toEarningsResponse(result, limit, offset))
}

// GetOrder godoc
// @Summary      Get one of my bot payment orders (any status)
// @Tags         payment-earnings
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Order id"
// @Success      200  {object}  OrderResponse
// @Router       /payment/earnings/{id} [get]
func (h *Handler) GetOrder(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	o, err := h.uc.GetOrderForOwner(c.Request.Context(), id, userID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toOrderResponse(o))
}

// ===================== withdrawals =====================

// ListWithdrawals godoc
// @Summary      List my bot-income withdrawal requests
// @Tags         payment-withdrawals
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  WithdrawalListResponse
// @Router       /payment/withdrawals [get]
func (h *Handler) ListWithdrawals(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	items, total, err := h.uc.ListWithdrawalsForUser(c.Request.Context(), userID, limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalListResponse(items, total, limit, offset))
}

// CreateWithdrawal godoc
// @Summary      Request a bot-income withdrawal
// @Tags         payment-withdrawals
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateWithdrawalRequest  true  "Withdrawal payload"
// @Success      201   {object}  WithdrawalResponse
// @Failure      403   {object}  httpx.ErrorResponse  "KYC not verified"
// @Failure      409   {object}  httpx.ErrorResponse  "Insufficient withdrawable earnings"
// @Router       /payment/withdrawals [post]
func (h *Handler) CreateWithdrawal(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	var req CreateWithdrawalRequest
	if !bind(c, &req) {
		return
	}
	in := CreateWithdrawalInput{
		UserID: userID, Amount: req.Amount, CardNumber: req.CardNumber, CardOwner: req.CardOwner,
	}
	if req.ProjectID != "" {
		pid, err := uuid.Parse(req.ProjectID)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("payment.invalid_project_id", "invalid project_id"))
			return
		}
		in.ProjectID = &pid
	}
	wd, err := h.uc.CreateWithdrawal(c.Request.Context(), in)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toWithdrawalResponse(wd))
}

// GetWithdrawal godoc
// @Summary      Get one of my withdrawal requests
// @Tags         payment-withdrawals
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Withdrawal id"
// @Success      200  {object}  WithdrawalResponse
// @Router       /payment/withdrawals/{id} [get]
func (h *Handler) GetWithdrawal(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	wd, err := h.uc.GetWithdrawalForUser(c.Request.Context(), id, userID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalResponse(wd))
}

// CancelWithdrawal godoc
// @Summary      Cancel one of my still-pending withdrawal requests
// @Tags         payment-withdrawals
// @Security     BearerAuth
// @Param        id   path      string  true  "Withdrawal id"
// @Success      204
// @Router       /payment/withdrawals/{id} [delete]
func (h *Handler) CancelWithdrawal(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.uc.CancelWithdrawal(c.Request.Context(), id, userID); err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.NoContent(c)
}

// ===================== admin withdrawal review =====================

// AdminListWithdrawals godoc
// @Summary      [admin] List all bot-income withdrawal requests
// @Tags         payment-admin
// @Produce      json
// @Security     BearerAuth
// @Param        status  query     string  false  "Filter by status (pending/approved/rejected)"
// @Success      200  {object}  WithdrawalListResponse
// @Router       /admin/payment/withdrawals [get]
func (h *Handler) AdminListWithdrawals(c *gin.Context) {
	limit, offset := pageParams(c)
	items, total, err := h.uc.AdminListWithdrawals(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalListResponse(items, total, limit, offset))
}

// AdminGetWithdrawal godoc
// @Summary      [admin] Get one withdrawal request
// @Tags         payment-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Withdrawal id"
// @Success      200  {object}  WithdrawalResponse
// @Router       /admin/payment/withdrawals/{id} [get]
func (h *Handler) AdminGetWithdrawal(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	wd, err := h.uc.AdminGetWithdrawal(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalResponse(wd))
}

// AdminApproveWithdrawal godoc
// @Summary      [admin] Approve a withdrawal — deducts users.balance
// @Tags         payment-admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Withdrawal id"
// @Success      200  {object}  WithdrawalResponse
// @Failure      409  {object}  httpx.ErrorResponse
// @Router       /admin/payment/withdrawals/{id}/approve [patch]
func (h *Handler) AdminApproveWithdrawal(c *gin.Context) {
	reviewerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	wd, err := h.uc.AdminApproveWithdrawal(c.Request.Context(), id, reviewerID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalResponse(wd))
}

// AdminRejectWithdrawal godoc
// @Summary      [admin] Reject a withdrawal
// @Tags         payment-admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                        true  "Withdrawal id"
// @Param        body  body      AdminRejectWithdrawalRequest  true  "Rejection reason"
// @Success      200   {object}  WithdrawalResponse
// @Router       /admin/payment/withdrawals/{id}/reject [patch]
func (h *Handler) AdminRejectWithdrawal(c *gin.Context) {
	reviewerID, ok := owner(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req AdminRejectWithdrawalRequest
	if !bind(c, &req) {
		return
	}
	wd, err := h.uc.AdminRejectWithdrawal(c.Request.Context(), id, reviewerID, req.RejectionReason)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toWithdrawalResponse(wd))
}

// ===================== legacy payment link =====================

// CreatePaymentLink godoc
// @Summary      Create a simple balance top-up pay link (Click/Payme)
// @Tags         payment
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreatePaymentLinkRequest  true  "Provider + amount"
// @Success      200   {object}  PaymentLinkResponse
// @Router       /payment/link [post]
func (h *Handler) CreatePaymentLink(c *gin.Context) {
	userID, ok := owner(c)
	if !ok {
		return
	}
	var req CreatePaymentLinkRequest
	if !bind(c, &req) {
		return
	}
	link, err := h.uc.CreatePaymentLink(c.Request.Context(), userID, req.Provider, req.Amount)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, PaymentLinkResponse{Link: link})
}

// ===================== internal (engine-facing) =====================

// InternalCreateOrder godoc
// @Summary      [internal] Engine creates a bot payment order
// @Tags         payment-internal
// @Accept       json
// @Produce      json
// @Param        X-Internal-Token  header    string              true  "Internal service token"
// @Param        body              body      CreateOrderRequest  true  "Order payload"
// @Success      201  {object}  OrderCreateResponse
// @Router       /internal/payment/orders [post]
func (h *Handler) InternalCreateOrder(c *gin.Context) {
	var req CreateOrderRequest
	if !bind(c, &req) {
		return
	}
	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		httpx.Error(c, httpx.BadRequest("order.invalid_project_id", "invalid project_id"))
		return
	}
	result, err := h.uc.CreateOrder(c.Request.Context(), CreateOrderInput{
		ProjectID: projectID, PayerChatID: req.PayerChatID, PayerFirstName: req.PayerFirstName,
		PayerLastName: req.PayerLastName, PayerUsername: req.PayerUsername, Amount: req.Amount,
		Provider: req.Provider, Comment: req.Comment, IdempotencyKey: req.IdempotencyKey, IsDebug: req.IsDebug,
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 201, toOrderCreateResponse(result))
}

// InternalOrderStatus godoc
// @Summary      [internal] Engine reads a bot payment order's status
// @Tags         payment-internal
// @Produce      json
// @Param        X-Internal-Token  header    string  true   "Internal service token"
// @Param        id                path      string  true   "Order id"
// @Param        project_id        query     string  false  "Enforced if present (IDOR guard)"
// @Success      200  {object}  OrderResponse
// @Router       /internal/payment/orders/{id}/status [get]
func (h *Handler) InternalOrderStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var projectID *uuid.UUID
	if raw := c.Query("project_id"); raw != "" {
		pid, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(c, httpx.BadRequest("order.invalid_project_id", "invalid project_id"))
			return
		}
		projectID = &pid
	}
	o, err := h.uc.GetOrderInternal(c.Request.Context(), id, projectID)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	httpx.OK(c, 200, toOrderResponse(o))
}
