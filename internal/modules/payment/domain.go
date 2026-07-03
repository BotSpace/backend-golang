// Package payment ports the Django payment app: bot payment orders
// (Click/Payme/Paycue collection), the gateway transaction ledger, bot
// income withdrawal requests, and the balance ledger (history). See
// ../../../PORTING_GUIDE.md for the module layout this package follows.
package payment

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Provider is the payment gateway that collected an order (Django
// PaymentMethosEnum).
const (
	ProviderClick  = "click"
	ProviderPaycue = "paycue"
	ProviderPayme  = "payme"
	ProviderPaynet = "paynet"
	ProviderUzum   = "uzum"
)

// Order status (Django PaymentOrderStatusEnum).
const (
	OrderStatusCreated   = "created"
	OrderStatusPaid      = "paid"
	OrderStatusCancelled = "cancelled"
	OrderStatusRefunded  = "refunded"
)

// Withdrawal review status (Django WithdrawalStatusEnum).
const (
	WithdrawalStatusPending  = "pending"
	WithdrawalStatusApproved = "approved"
	WithdrawalStatusRejected = "rejected"
)

// History ledger entry kind.
const (
	HistoryIncome  = "income"
	HistoryExpense = "expense"
)

// MinOrderAmountSom is the smallest amount a bot payment order accepts
// (Django CreateOrderSerializer.amount min_value).
const MinOrderAmountSom = "1000"

// MinWithdrawalAmountSom mirrors Django serializers/withdrawal.py MIN_WITHDRAWAL.
const MinWithdrawalAmountSom = "1000"

// Transaction is the gateway ledger row Click/Payme address via
// merchant_trans_id / account.order_id. It is created for every order and
// also stands alone for the legacy direct balance top-up flow.
type Transaction struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Amount         string // NUMERIC(20,2) rendered as a decimal string
	Status         bool   // true once the gateway confirmed payment
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time

	// UserPhone is populated only by the admin-wide listing (joins users).
	UserPhone string
}

// Order is a bot payment order: engine -> create -> pay_url -> payer pays ->
// webhook -> owner net credit + Botspace commission.
type Order struct {
	ID             uuid.UUID
	ProjectID      uuid.UUID
	OwnerID        uuid.UUID
	PayerChatID    int64
	PayerFirstName string
	PayerLastName  string
	PayerUsername  string
	Amount         string
	Commission     string
	NetAmount      string
	Provider       string
	Status         string
	ExternalID     string
	Comment        string
	IdempotencyKey string
	TransactionID  *uuid.UUID
	PaidAt         *time.Time
	IsDebug        bool
	PayInstruction string
	CreatedAt      time.Time
	UpdatedAt      time.Time

	// ProjectName/OwnerPhone are populated only by the admin-wide listing
	// (joins projects/users); empty on owner-scoped paths.
	ProjectName string
	OwnerPhone  string
}

// WithdrawalRequest is a bot-income payout request. amount is deducted from
// the shared users.balance only when an admin approves it.
type WithdrawalRequest struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	ProjectID       *uuid.UUID
	Amount          string
	CardNumber      string
	CardOwner       string
	Status          string
	RejectionReason string
	ReviewedBy      *uuid.UUID
	ReviewedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// UserPhone/ProjectName are populated only by admin-facing queries that
	// join users/projects; empty on the owner-scoped paths.
	UserPhone   string
	ProjectName string
}

// History is a balance ledger entry (income/expense) backing every
// users.balance mutation made by this domain.
type History struct {
	ID             uuid.UUID
	UserID         *uuid.UUID
	Amount         string
	Comment        string
	Type           string
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ProjectEarning is one row of the owner's by-bot income breakdown.
type ProjectEarning struct {
	ProjectID   uuid.UUID
	ProjectName string
	TotalEarned string
	OrderCount  int64
}

// CreateTransactionInput creates a gateway ledger row.
type CreateTransactionInput struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Amount         string
	IdempotencyKey string
}

// CreateOrderParams is the fully-resolved persistence payload for a new
// order (commission/net already computed, transaction already created).
type CreateOrderParams struct {
	ID             uuid.UUID
	ProjectID      uuid.UUID
	OwnerID        uuid.UUID
	PayerChatID    int64
	PayerFirstName string
	PayerLastName  string
	PayerUsername  string
	Amount         string
	Commission     string
	NetAmount      string
	Provider       string
	Comment        string
	IdempotencyKey string
	TransactionID  *uuid.UUID
	IsDebug        bool
}

// CreateWithdrawalInput is the owner-scoped withdrawal-create payload.
type CreateWithdrawalInput struct {
	UserID     uuid.UUID
	ProjectID  *uuid.UUID
	Amount     string
	CardNumber string
	CardOwner  string
}

// BalanceDeltaInput applies a signed so'm delta to users.balance, recording
// a History row first so retried callers (webhook redelivery, admin double
// click) are idempotent no-ops when IdempotencyKey repeats.
type BalanceDeltaInput struct {
	UserID         uuid.UUID
	DeltaSom       int64 // positive=income, negative=expense
	Comment        string
	IdempotencyKey string
}

// EarningsFilter paginates the owner's paid-order listing, optionally
// narrowed to one bot.
type EarningsFilter struct {
	OwnerID   uuid.UUID
	ProjectID *uuid.UUID
	Limit     int32
	Offset    int32
}

// Repository is the persistence port. The usecase depends only on this
// interface; pgRepo (repository.go) is the sqlc-backed implementation.
type Repository interface {
	// Transactions.
	CreateTransaction(ctx context.Context, in CreateTransactionInput) (*Transaction, error)
	GetTransactionByID(ctx context.Context, id uuid.UUID) (*Transaction, error)
	GetTransactionForUser(ctx context.Context, id, userID uuid.UUID) (*Transaction, error)
	ListTransactionsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]Transaction, error)
	CountTransactionsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	// ListTransactionsAdmin/CountTransactionsAdmin back the admin-wide
	// transactions listing (all users, not owner-scoped).
	ListTransactionsAdmin(ctx context.Context, limit, offset int32) ([]Transaction, error)
	CountTransactionsAdmin(ctx context.Context) (int64, error)
	SetTransactionStatus(ctx context.Context, id uuid.UUID, from, to bool) error
	// SetTransactionExternalRef/GetTransactionByExternalRef let a gateway
	// (Payme) index our transaction row by its own opaque transaction id —
	// see queries/payment.sql's SetPaymentTransactionExternalRef doc.
	SetTransactionExternalRef(ctx context.Context, id uuid.UUID, ref string) error
	GetTransactionByExternalRef(ctx context.Context, ref string) (*Transaction, error)
	// ListTransactionsByExternalRefDateRange backs Payme's GetStatement RPC
	// (payme/views/base.py::get_statement): every transaction the Payme
	// gateway itself created (has an external ref) within [from, to].
	ListTransactionsByExternalRefDateRange(ctx context.Context, from, to time.Time) ([]Transaction, error)

	// Orders.
	CreateOrder(ctx context.Context, in CreateOrderParams) (*Order, error)
	GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, error)
	GetOrderForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Order, error)
	GetOrderByIdempotencyKey(ctx context.Context, key string) (*Order, error)
	GetOrderByTransactionID(ctx context.Context, txnID uuid.UUID) (*Order, error)
	AttachTransactionToOrder(ctx context.Context, orderID, txnID uuid.UUID) (*Order, error)
	ClearOrderIdempotencyKey(ctx context.Context, orderID uuid.UUID) error
	SetOrderPayInstruction(ctx context.Context, orderID uuid.UUID, externalID, payInstruction string) (*Order, error)
	MarkOrderPaid(ctx context.Context, id uuid.UUID, commission, netAmount, externalID string) (*Order, error)
	MarkOrderCancelled(ctx context.Context, id uuid.UUID) (*Order, error)
	ListPaidOrdersForOwner(ctx context.Context, f EarningsFilter) ([]Order, error)
	CountPaidOrdersForOwner(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error)
	// ListOrdersAdmin/CountOrdersAdmin back the admin-wide bot payment order
	// listing (all owners, any status, not owner-scoped).
	ListOrdersAdmin(ctx context.Context, status string, limit, offset int32) ([]Order, error)
	CountOrdersAdmin(ctx context.Context, status string) (int64, error)
	SumEarningsForOwner(ctx context.Context, ownerID uuid.UUID) (totalEarned, totalCommission string, err error)
	EarningsByProject(ctx context.Context, ownerID uuid.UUID) ([]ProjectEarning, error)
	SumBotEarnedForOwner(ctx context.Context, ownerID uuid.UUID) (string, error)

	// Project ownership lookup (reuses the shared projects table; this
	// module owns no project data of its own).
	GetProjectOwner(ctx context.Context, projectID uuid.UUID) (ownerID uuid.UUID, projectName string, err error)

	// History (read-only listing; writes only happen via ApplyBalanceDelta).
	ListHistoryForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]History, error)
	CountHistoryForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	GetHistoryForUser(ctx context.Context, id, userID uuid.UUID) (*History, error)

	// Withdrawals.
	CreateWithdrawal(ctx context.Context, in CreateWithdrawalInput) (*WithdrawalRequest, error)
	GetWithdrawalByID(ctx context.Context, id uuid.UUID) (*WithdrawalRequest, error)
	GetWithdrawalForUser(ctx context.Context, id, userID uuid.UUID) (*WithdrawalRequest, error)
	ListWithdrawalsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]WithdrawalRequest, error)
	CountWithdrawalsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	DeletePendingWithdrawal(ctx context.Context, id, userID uuid.UUID) error
	ListWithdrawalsAdmin(ctx context.Context, status string, limit, offset int32) ([]WithdrawalRequest, error)
	CountWithdrawalsAdmin(ctx context.Context, status string) (int64, error)
	GetWithdrawalByIDAdmin(ctx context.Context, id uuid.UUID) (*WithdrawalRequest, error)
	ApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*WithdrawalRequest, error)
	RejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*WithdrawalRequest, error)
	SumReservedWithdrawals(ctx context.Context, userID uuid.UUID) (string, error)

	// Cross-cutting: balance + KYC live on the shared users table (owned by
	// the user/kyc modules' migrations; this module only reads/writes the
	// columns through the already-generated sqlc queries).
	GetUserBalance(ctx context.Context, userID uuid.UUID) (int64, error)
	IsUserKYCVerified(ctx context.Context, userID uuid.UUID) (bool, error)
	// ApplyBalanceDelta records a History row (idempotent on IdempotencyKey)
	// and, only if the History insert actually happened, applies the delta
	// to users.balance. Returns applied=false when the key was already
	// consumed by a previous call (safe no-op).
	ApplyBalanceDelta(ctx context.Context, in BalanceDeltaInput) (applied bool, newBalance int64, err error)
}

// Notifier informs the bot engine that an order finished (paid/cancelled) so
// a paused payment node can resume the flow.
//
// TODO: implement the real HTTP call — see
// core/apps/payment/services/notify.py for the Django contract:
//
//	POST http://bot-{release_or_debug_webhook_uuid}:8000/payment-callback
//	{order_id, chat_id, amount, net, commission, status, comment, provider}
//	X-Internal-Token: <APP_INTERNAL_TOKEN>
//
// Wire a real implementation in server.go once the engine-side contract
// (Faza 2 pause/resume) lands; a failed notify must never fail the webhook.
type Notifier interface {
	NotifyOrderPaid(ctx context.Context, order *Order) error
}

// noopNotifier is the default Notifier: it does nothing and never errors.
type noopNotifier struct{}

func (noopNotifier) NotifyOrderPaid(context.Context, *Order) error { return nil }

// NewNoopNotifier returns a Notifier that does nothing (see the Notifier
// TODO above for the real engine callback).
func NewNoopNotifier() Notifier { return noopNotifier{} }
