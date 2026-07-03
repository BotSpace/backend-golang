package payment

import "time"

// ===================== transactions =====================

type TransactionResponse struct {
	ID        string    `json:"id"`
	User      string    `json:"user,omitempty"`
	UserPhone string    `json:"user_phone,omitempty"`
	Amount    string    `json:"amount"`
	Status    bool      `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toTransactionResponse(t *Transaction) TransactionResponse {
	return TransactionResponse{
		ID: t.ID.String(), User: t.UserID.String(), UserPhone: t.UserPhone,
		Amount: t.Amount, Status: t.Status, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

type TransactionListResponse struct {
	Items  []TransactionResponse `json:"items"`
	Total  int64                 `json:"total"`
	Limit  int32                 `json:"limit"`
	Offset int32                 `json:"offset"`
}

func toTransactionListResponse(items []Transaction, total int64, limit, offset int32) TransactionListResponse {
	out := make([]TransactionResponse, len(items))
	for i := range items {
		out[i] = toTransactionResponse(&items[i])
	}
	return TransactionListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// ===================== orders =====================

// CreateOrderRequest is the engine-facing bot payment order create payload
// (Django CreateOrderSerializer).
type CreateOrderRequest struct {
	ProjectID      string `json:"project_id" validate:"required,uuid"`
	PayerChatID    int64  `json:"payer_chat_id" validate:"required"`
	Amount         string `json:"amount" validate:"required"`
	Comment        string `json:"comment" validate:"omitempty,max=512"`
	Provider       string `json:"provider" validate:"omitempty,oneof=click paycue payme paynet uzum"`
	IdempotencyKey string `json:"idempotency_key" validate:"omitempty,max=128"`
	IsDebug        bool   `json:"is_debug"`
	PayerFirstName string `json:"payer_first_name" validate:"omitempty,max=255"`
	PayerLastName  string `json:"payer_last_name" validate:"omitempty,max=255"`
	PayerUsername  string `json:"payer_username" validate:"omitempty,max=255"`
}

// OrderCreateResponse mirrors order.py::order_create's response "data" object.
type OrderCreateResponse struct {
	OrderID        string `json:"order_id"`
	PayURL         string `json:"pay_url"`
	PayInstruction string `json:"pay_instruction"`
	Amount         string `json:"amount"`
	NetAmount      string `json:"net_amount"`
	Commission     string `json:"commission"`
}

func toOrderCreateResponse(r *OrderCreateResult) OrderCreateResponse {
	return OrderCreateResponse{
		OrderID:        r.Order.ID.String(),
		PayURL:         r.PayURL,
		PayInstruction: r.PayInstruction,
		Amount:         r.Order.Amount,
		NetAmount:      r.Order.NetAmount,
		Commission:     r.Order.Commission,
	}
}

// OrderResponse is the order status/detail projection (Django
// OrderStatusSerializer / EarningsOrderSerializer, merged).
type OrderResponse struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	ProjectName    string     `json:"project_name,omitempty"`
	Owner          string     `json:"owner,omitempty"`
	OwnerPhone     string     `json:"owner_phone,omitempty"`
	PayerChatID    int64      `json:"payer_chat_id"`
	PayerFirstName string     `json:"payer_first_name,omitempty"`
	PayerLastName  string     `json:"payer_last_name,omitempty"`
	PayerUsername  string     `json:"payer_username,omitempty"`
	Amount         string     `json:"amount"`
	Commission     string     `json:"commission"`
	NetAmount      string     `json:"net_amount"`
	Provider       string     `json:"provider"`
	Status         string     `json:"status"`
	ExternalID     string     `json:"external_id,omitempty"`
	Comment        string     `json:"comment,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
}

func toOrderResponse(o *Order) OrderResponse {
	return OrderResponse{
		ID:             o.ID.String(),
		ProjectID:      o.ProjectID.String(),
		ProjectName:    o.ProjectName,
		Owner:          o.OwnerID.String(),
		OwnerPhone:     o.OwnerPhone,
		PayerChatID:    o.PayerChatID,
		PayerFirstName: o.PayerFirstName,
		PayerLastName:  o.PayerLastName,
		PayerUsername:  o.PayerUsername,
		Amount:         o.Amount,
		Commission:     o.Commission,
		NetAmount:      o.NetAmount,
		Provider:       o.Provider,
		Status:         o.Status,
		ExternalID:     o.ExternalID,
		Comment:        o.Comment,
		CreatedAt:      o.CreatedAt,
		PaidAt:         o.PaidAt,
	}
}

// OrderListResponse is a paginated envelope for the admin-wide bot payment
// order listing (unlike EarningsResponse, which embeds orders inline for the
// owner dashboard).
type OrderListResponse struct {
	Items  []OrderResponse `json:"items"`
	Total  int64           `json:"total"`
	Limit  int32           `json:"limit"`
	Offset int32           `json:"offset"`
}

func toOrderListResponse(items []Order, total int64, limit, offset int32) OrderListResponse {
	out := make([]OrderResponse, len(items))
	for i := range items {
		out[i] = toOrderResponse(&items[i])
	}
	return OrderListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// ===================== earnings =====================

type ProjectEarningResponse struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	TotalEarned string `json:"total_earned"`
	Count       int64  `json:"count"`
}

// EarningsResponse mirrors views/order.py::EarningsView.list's flat payload.
type EarningsResponse struct {
	Balance         int64                    `json:"balance"`
	Withdrawable    string                   `json:"withdrawable"`
	TotalEarned     string                   `json:"total_earned"`
	TotalCommission string                   `json:"total_commission"`
	ByBot           []ProjectEarningResponse `json:"by_bot"`
	Orders          []OrderResponse          `json:"orders"`
	Count           int64                    `json:"count"`
	Limit           int32                    `json:"limit"`
	Offset          int32                    `json:"offset"`
}

func toEarningsResponse(r *EarningsResult, limit, offset int32) EarningsResponse {
	byBot := make([]ProjectEarningResponse, len(r.ByBot))
	for i, b := range r.ByBot {
		byBot[i] = ProjectEarningResponse{
			ProjectID: b.ProjectID.String(), ProjectName: b.ProjectName, TotalEarned: b.TotalEarned, Count: b.OrderCount,
		}
	}
	orders := make([]OrderResponse, len(r.Orders))
	for i := range r.Orders {
		orders[i] = toOrderResponse(&r.Orders[i])
	}
	return EarningsResponse{
		Balance: r.Balance, Withdrawable: r.Withdrawable, TotalEarned: r.TotalEarned, TotalCommission: r.TotalCommission,
		ByBot: byBot, Orders: orders, Count: r.Total, Limit: limit, Offset: offset,
	}
}

// ===================== history =====================

type HistoryResponse struct {
	ID        string    `json:"id"`
	Amount    string    `json:"amount"`
	Type      string    `json:"type"`
	Comment   string    `json:"comment,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toHistoryResponse(h *History) HistoryResponse {
	return HistoryResponse{ID: h.ID.String(), Amount: h.Amount, Type: h.Type, Comment: h.Comment, CreatedAt: h.CreatedAt}
}

type HistoryListResponse struct {
	Items  []HistoryResponse `json:"items"`
	Total  int64             `json:"total"`
	Limit  int32             `json:"limit"`
	Offset int32             `json:"offset"`
}

func toHistoryListResponse(items []History, total int64, limit, offset int32) HistoryListResponse {
	out := make([]HistoryResponse, len(items))
	for i := range items {
		out[i] = toHistoryResponse(&items[i])
	}
	return HistoryListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// ===================== withdrawals =====================

// CreateWithdrawalRequest mirrors serializers/withdrawal.py::CreateWithdrawalSerializer.
type CreateWithdrawalRequest struct {
	ProjectID  string `json:"project_id" validate:"omitempty,uuid"`
	Amount     string `json:"amount" validate:"required"`
	CardNumber string `json:"card_number" validate:"required"`
	CardOwner  string `json:"card_owner" validate:"required,max=255"`
}

type WithdrawalResponse struct {
	ID              string     `json:"id"`
	User            string     `json:"user,omitempty"`
	UserPhone       string     `json:"user_phone,omitempty"`
	ProjectID       string     `json:"project_id,omitempty"`
	ProjectName     string     `json:"project_name,omitempty"`
	Amount          string     `json:"amount"`
	CardNumber      string     `json:"card_number"`
	CardOwner       string     `json:"card_owner"`
	Status          string     `json:"status"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
}

func toWithdrawalResponse(w *WithdrawalRequest) WithdrawalResponse {
	resp := WithdrawalResponse{
		ID: w.ID.String(), User: w.UserID.String(), UserPhone: w.UserPhone,
		ProjectName: w.ProjectName, Amount: w.Amount, CardNumber: w.CardNumber, CardOwner: w.CardOwner,
		Status: w.Status, RejectionReason: w.RejectionReason, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
		ReviewedAt: w.ReviewedAt,
	}
	if w.ProjectID != nil {
		resp.ProjectID = w.ProjectID.String()
	}
	return resp
}

type WithdrawalListResponse struct {
	Items  []WithdrawalResponse `json:"items"`
	Total  int64                `json:"total"`
	Limit  int32                `json:"limit"`
	Offset int32                `json:"offset"`
}

func toWithdrawalListResponse(items []WithdrawalRequest, total int64, limit, offset int32) WithdrawalListResponse {
	out := make([]WithdrawalResponse, len(items))
	for i := range items {
		out[i] = toWithdrawalResponse(&items[i])
	}
	return WithdrawalListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// AdminRejectWithdrawalRequest mirrors administration/serializers/bot_withdrawals.py.
type AdminRejectWithdrawalRequest struct {
	RejectionReason string `json:"rejection_reason" validate:"required,max=500"`
}

// ===================== legacy payment link =====================

// CreatePaymentLinkRequest mirrors views/payment.py::CreatePaymentLinkSerializer.
type CreatePaymentLinkRequest struct {
	Provider string `json:"method" validate:"required,oneof=click payme"`
	Amount   string `json:"amount" validate:"required"`
}

type PaymentLinkResponse struct {
	Link string `json:"link"`
}
