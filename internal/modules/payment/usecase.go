package payment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Repository sentinel errors, translated to HTTP errors by the usecase.
var (
	ErrInsufficientFunds    = errors.New("insufficient balance")
	ErrWithdrawalNotPending = errors.New("withdrawal is not pending")
)

// defaultCommissionPercent mirrors services/commission.py _DEFAULT_COMMISSION_PERCENT.
const defaultCommissionPercent = 5

// CreateOrderInput is the engine-facing bot payment order create payload.
type CreateOrderInput struct {
	ProjectID      uuid.UUID
	PayerChatID    int64
	PayerFirstName string
	PayerLastName  string
	PayerUsername  string
	Amount         string
	Provider       string
	Comment        string
	IdempotencyKey string
	IsDebug        bool
}

// OrderCreateResult is the order-create response payload (Django order_create).
type OrderCreateResult struct {
	Order          *Order
	PayURL         string
	PayInstruction string
}

type PaycueWebhookInput struct {
	Action        string
	Amount        string
	TransactionID string
}

// EarningsResult is the owner earnings dashboard payload (Django EarningsView.list).
type EarningsResult struct {
	Balance         int64
	Withdrawable    string
	TotalEarned     string
	TotalCommission string
	ByBot           []ProjectEarning
	Orders          []Order
	Total           int64
}

// Usecase exposes the payment domain's business operations.
type Usecase interface {
	// Orders.
	CreateOrder(ctx context.Context, in CreateOrderInput) (*OrderCreateResult, error)
	GetOrderForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Order, error)
	// GetOrderInternal is the engine/internal-token view; projectID, when
	// non-nil, must match the order's project (IDOR guard, mirrors
	// order.py::order_status's project_id enforcement).
	GetOrderInternal(ctx context.Context, id uuid.UUID, projectID *uuid.UUID) (*Order, error)
	ListEarnings(ctx context.Context, f EarningsFilter) (*EarningsResult, error)

	// Transactions (read-only from the API; writes happen via settlement).
	ListTransactionsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]Transaction, int64, error)
	GetTransactionForUser(ctx context.Context, id, userID uuid.UUID) (*Transaction, error)
	// AdminListTransactions/AdminListOrders are admin-wide (all users/owners).
	AdminListTransactions(ctx context.Context, limit, offset int32) ([]Transaction, int64, error)
	AdminListOrders(ctx context.Context, status string, limit, offset int32) ([]Order, int64, error)

	// History.
	ListHistoryForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]History, int64, error)
	GetHistoryForUser(ctx context.Context, id, userID uuid.UUID) (*History, error)

	// Withdrawals.
	CreateWithdrawal(ctx context.Context, in CreateWithdrawalInput) (*WithdrawalRequest, error)
	GetWithdrawalForUser(ctx context.Context, id, userID uuid.UUID) (*WithdrawalRequest, error)
	ListWithdrawalsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]WithdrawalRequest, int64, error)
	CancelWithdrawal(ctx context.Context, id, userID uuid.UUID) error

	// Admin withdrawal review.
	AdminListWithdrawals(ctx context.Context, status string, limit, offset int32) ([]WithdrawalRequest, int64, error)
	AdminGetWithdrawal(ctx context.Context, id uuid.UUID) (*WithdrawalRequest, error)
	AdminApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*WithdrawalRequest, error)
	AdminRejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*WithdrawalRequest, error)

	// Gateway settlement — called by gateway_click.go / gateway_payme.go
	// after signature verification confirms a transaction's fate. Looks up
	// whether the transaction backs a bot PaymentOrder or stands alone
	// (legacy direct balance top-up) and settles accordingly. A nil *Order
	// return (with nil error) means the legacy transaction-only path ran.
	SettlePaidTransaction(ctx context.Context, transactionID uuid.UUID, externalID string) (*Order, error)
	SettleCancelledTransaction(ctx context.Context, transactionID uuid.UUID) (*Order, error)
	SettlePaycueTransaction(ctx context.Context, in PaycueWebhookInput) (*Order, error)
	GetTransactionByID(ctx context.Context, id uuid.UUID) (*Transaction, error)
	// SetTransactionExternalRef/GetTransactionByExternalRef back the Payme
	// gateway, which only carries its own opaque transaction id on
	// Perform/Cancel/Check calls (see domain.go's Repository doc).
	SetTransactionExternalRef(ctx context.Context, id uuid.UUID, ref string) error
	GetTransactionByExternalRef(ctx context.Context, ref string) (*Transaction, error)
	// ListTransactionsByExternalRefDateRange backs Payme's GetStatement RPC.
	ListTransactionsByExternalRefDateRange(ctx context.Context, from, to time.Time) ([]Transaction, error)

	// CreatePaymentLink is the legacy simple "top up my balance" endpoint
	// (Django views/payment.py::payment_link).
	CreatePaymentLink(ctx context.Context, userID uuid.UUID, provider, amount string) (string, error)
}

type usecase struct {
	repo              Repository
	notifier          Notifier
	commissionPercent int64
	revenueUserID     *uuid.UUID
	click             ClickGateway
	payme             PaymeGateway
	paycue            PaycueGateway
}

// NewUsecase wires the payment usecase. Gateway credentials and the optional
// Botspace commission-revenue account are read from the environment (see the
// task's KEY RULES): APP_CLICK_SERVICE_ID/APP_CLICK_MERCHANT_ID/
// APP_CLICK_SECRET_KEY, APP_PAYME_ID/APP_PAYME_KEY, plus
// APP_PAYMENT_COMMISSION_PERCENT (default 5) and APP_BOTSPACE_REVENUE_USER_ID
// (optional — commission is only credited when set, mirrors
// views/click.py::_credit_botspace's TODO Faza4).
func NewUsecase(repo Repository, notifier Notifier) Usecase {
	percent := int64(defaultCommissionPercent)
	if v := os.Getenv("APP_PAYMENT_COMMISSION_PERCENT"); v != "" {
		if p, err := strconv.ParseInt(v, 10, 64); err == nil {
			percent = p
		}
	}
	var revenueUserID *uuid.UUID
	if v := os.Getenv("APP_BOTSPACE_REVENUE_USER_ID"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			revenueUserID = &id
		}
	}
	return &usecase{
		repo:              repo,
		notifier:          notifier,
		commissionPercent: percent,
		revenueUserID:     revenueUserID,
		click:             NewClickGatewayFromEnv(),
		payme:             NewPaymeGatewayFromEnv(),
		paycue:            NewPaycueGatewayFromEnv(),
	}
}

// ===================== orders =====================

// CreateOrder mirrors views/order.py::order_create: idempotency reuse for a
// still-pending order, otherwise commission split + transaction + order +
// pay_url or pay_instruction.
func (u *usecase) CreateOrder(ctx context.Context, in CreateOrderInput) (*OrderCreateResult, error) {
	amountCents, err := parseMoneyCents(in.Amount)
	if err != nil {
		return nil, httpx.BadRequest("order.invalid_amount", "amount noto'g'ri formatda")
	}
	minCents, _ := parseMoneyCents(MinOrderAmountSom)
	if amountCents < minCents {
		return nil, httpx.BadRequest("order.amount_too_low", "minimal to'lov summasi 1000 so'm")
	}
	provider := in.Provider
	if provider == "" {
		provider = ProviderClick
	}

	if in.IdempotencyKey != "" {
		existing, err := u.repo.GetOrderByIdempotencyKey(ctx, in.IdempotencyKey)
		if err != nil && !errors.Is(err, database.ErrNotFound) {
			return nil, httpx.Internal(err)
		}
		if err == nil {
			if existing.Status == OrderStatusCreated {
				if existing.TransactionID == nil {
					txn, err := u.repo.CreateTransaction(ctx, CreateTransactionInput{
						ID: uuid.New(), UserID: existing.OwnerID, Amount: existing.Amount,
					})
					if err != nil {
						return nil, httpx.Internal(err)
					}
					updated, err := u.repo.AttachTransactionToOrder(ctx, existing.ID, txn.ID)
					if err != nil {
						return nil, httpx.Internal(err)
					}
					existing = updated
				}
				if existing.Provider == ProviderPaycue {
					if existing.ExternalID == "" {
						if err := u.repo.ClearOrderIdempotencyKey(ctx, existing.ID); err != nil && !errors.Is(err, database.ErrNotFound) {
							return nil, httpx.Internal(err)
						}
					} else {
						return &OrderCreateResult{Order: existing, PayURL: u.paycue.PayURL(existing.ExternalID)}, nil
					}
				} else {
					payURL := ""
					if existing.TransactionID != nil {
						payURL = u.payURLFor(existing.Provider, *existing.TransactionID, existing.Amount)
					}
					return &OrderCreateResult{Order: existing, PayURL: payURL, PayInstruction: existing.PayInstruction}, nil
				}
			}
			// Finalized order (paid/cancelled/refunded): free the key so a new
			// order can be created with it, mirroring order.py's else branch.
			if err := u.repo.ClearOrderIdempotencyKey(ctx, existing.ID); err != nil && !errors.Is(err, database.ErrNotFound) {
				return nil, httpx.Internal(err)
			}
		}
	}

	ownerID, projectName, err := u.repo.GetProjectOwner(ctx, in.ProjectID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("order.project_not_found", "loyiha topilmadi")
		}
		return nil, httpx.Internal(err)
	}

	orderAmount := in.Amount
	var paycueTx *PaycueTransaction
	if provider == ProviderPaycue {
		paycueTx, err = u.paycue.CreateTransaction(ctx, in.Amount)
		if err != nil {
			return nil, httpx.Internal(fmt.Errorf("create paycue transaction: %w", err))
		}
		orderAmount = paycueTx.Amount
	}

	commission, net, err := calculateCommission(orderAmount, u.commissionPercent)
	if err != nil {
		return nil, httpx.BadRequest("order.invalid_amount", "amount noto'g'ri formatda")
	}

	txn, err := u.repo.CreateTransaction(ctx, CreateTransactionInput{ID: uuid.New(), UserID: ownerID, Amount: orderAmount})
	if err != nil {
		return nil, httpx.Internal(err)
	}

	comment := in.Comment
	if comment == "" {
		comment = projectName
	}

	order, err := u.repo.CreateOrder(ctx, CreateOrderParams{
		ID:             uuid.New(),
		ProjectID:      in.ProjectID,
		OwnerID:        ownerID,
		PayerChatID:    in.PayerChatID,
		PayerFirstName: in.PayerFirstName,
		PayerLastName:  in.PayerLastName,
		PayerUsername:  in.PayerUsername,
		Amount:         orderAmount,
		Commission:     commission,
		NetAmount:      net,
		Provider:       provider,
		Comment:        comment,
		IdempotencyKey: in.IdempotencyKey,
		TransactionID:  &txn.ID,
		IsDebug:        in.IsDebug,
	})
	if err != nil {
		return nil, httpx.Internal(err)
	}

	if provider == ProviderPaycue {
		order, err = u.repo.SetOrderPayInstruction(ctx, order.ID, paycueTx.TransactionID, "")
		if err != nil {
			return nil, httpx.Internal(err)
		}
		return &OrderCreateResult{Order: order, PayURL: paycueTx.PayURL}, nil
	}

	return &OrderCreateResult{Order: order, PayURL: u.payURLFor(provider, txn.ID, orderAmount)}, nil
}

// payURLFor generates a gateway checkout URL. Providers without a
// generator (paycue/paynet/uzum) return "".
func (u *usecase) payURLFor(provider string, txnID uuid.UUID, amount string) string {
	switch provider {
	case ProviderClick:
		return u.click.PayURL(txnID, amount)
	case ProviderPayme:
		cents, err := parseMoneyCents(amount)
		if err != nil {
			return ""
		}
		return u.payme.PayURL(txnID, cents)
	default:
		return ""
	}
}

func (u *usecase) GetOrderForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Order, error) {
	o, err := u.repo.GetOrderForOwner(ctx, id, ownerID)
	return mapOrderErr(o, err)
}

func (u *usecase) GetOrderInternal(ctx context.Context, id uuid.UUID, projectID *uuid.UUID) (*Order, error) {
	o, err := u.repo.GetOrderByID(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("order.not_found", "order topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	if projectID != nil && o.ProjectID != *projectID {
		return nil, httpx.Forbidden("order.forbidden", "ruxsat yo'q")
	}
	return o, nil
}

func (u *usecase) ListEarnings(ctx context.Context, f EarningsFilter) (*EarningsResult, error) {
	f.Limit = clampLimit(f.Limit)
	f.Offset = clampOffset(f.Offset)

	balance, err := u.repo.GetUserBalance(ctx, f.OwnerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	totalEarned, totalCommission, err := u.repo.SumEarningsForOwner(ctx, f.OwnerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	reserved, err := u.repo.SumReservedWithdrawals(ctx, f.OwnerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	byBot, err := u.repo.EarningsByProject(ctx, f.OwnerID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	orders, err := u.repo.ListPaidOrdersForOwner(ctx, f)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	total, err := u.repo.CountPaidOrdersForOwner(ctx, f.OwnerID, f.ProjectID)
	if err != nil {
		return nil, httpx.Internal(err)
	}

	return &EarningsResult{
		Balance:         balance,
		Withdrawable:    subtractMoney(totalEarned, reserved),
		TotalEarned:     totalEarned,
		TotalCommission: totalCommission,
		ByBot:           byBot,
		Orders:          orders,
		Total:           total,
	}, nil
}

// ===================== transactions =====================

func (u *usecase) ListTransactionsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]Transaction, int64, error) {
	limit, offset = clampLimit(limit), clampOffset(offset)
	items, err := u.repo.ListTransactionsForUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountTransactionsForUser(ctx, userID)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) GetTransactionForUser(ctx context.Context, id, userID uuid.UUID) (*Transaction, error) {
	t, err := u.repo.GetTransactionForUser(ctx, id, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("transaction.not_found", "tranzaksiya topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return t, nil
}

func (u *usecase) GetTransactionByID(ctx context.Context, id uuid.UUID) (*Transaction, error) {
	t, err := u.repo.GetTransactionByID(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("transaction.not_found", "tranzaksiya topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return t, nil
}

func (u *usecase) AdminListTransactions(ctx context.Context, limit, offset int32) ([]Transaction, int64, error) {
	limit, offset = clampLimit(limit), clampOffset(offset)
	items, err := u.repo.ListTransactionsAdmin(ctx, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountTransactionsAdmin(ctx)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) AdminListOrders(ctx context.Context, status string, limit, offset int32) ([]Order, int64, error) {
	limit, offset = clampLimit(limit), clampOffset(offset)
	items, err := u.repo.ListOrdersAdmin(ctx, status, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountOrdersAdmin(ctx, status)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) SetTransactionExternalRef(ctx context.Context, id uuid.UUID, ref string) error {
	if err := u.repo.SetTransactionExternalRef(ctx, id, ref); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("transaction.not_found", "tranzaksiya topilmadi")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) GetTransactionByExternalRef(ctx context.Context, ref string) (*Transaction, error) {
	t, err := u.repo.GetTransactionByExternalRef(ctx, ref)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("transaction.not_found", "tranzaksiya topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return t, nil
}

func (u *usecase) ListTransactionsByExternalRefDateRange(ctx context.Context, from, to time.Time) ([]Transaction, error) {
	items, err := u.repo.ListTransactionsByExternalRefDateRange(ctx, from, to)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return items, nil
}

// ===================== history =====================

func (u *usecase) ListHistoryForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]History, int64, error) {
	limit, offset = clampLimit(limit), clampOffset(offset)
	items, err := u.repo.ListHistoryForUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountHistoryForUser(ctx, userID)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) GetHistoryForUser(ctx context.Context, id, userID uuid.UUID) (*History, error) {
	h, err := u.repo.GetHistoryForUser(ctx, id, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("history.not_found", "yozuv topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return h, nil
}

// ===================== withdrawals =====================

// CreateWithdrawal mirrors serializers/withdrawal.py::CreateWithdrawalSerializer:
// KYC required, minimum amount, bot ownership, withdrawable-balance check,
// card number format.
func (u *usecase) CreateWithdrawal(ctx context.Context, in CreateWithdrawalInput) (*WithdrawalRequest, error) {
	verified, err := u.repo.IsUserKYCVerified(ctx, in.UserID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if !verified {
		return nil, httpx.Forbidden("withdrawal.kyc_required", "pul yechish uchun avval verifikatsiyadan o'tishingiz kerak")
	}

	amountCents, err := parseMoneyCents(in.Amount)
	if err != nil {
		return nil, httpx.BadRequest("withdrawal.invalid_amount", "amount noto'g'ri formatda")
	}
	minCents, _ := parseMoneyCents(MinWithdrawalAmountSom)
	if amountCents < minCents {
		return nil, httpx.BadRequest("withdrawal.amount_too_low", "minimal yechish summasi 1000 so'm")
	}

	if in.ProjectID != nil {
		ownerID, _, err := u.repo.GetProjectOwner(ctx, *in.ProjectID)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, httpx.NotFound("withdrawal.project_not_found", "bot topilmadi")
			}
			return nil, httpx.Internal(err)
		}
		if ownerID != in.UserID {
			return nil, httpx.Forbidden("withdrawal.not_owner", "bu bot sizga tegishli emas")
		}
	}

	earned, err := u.repo.SumBotEarnedForOwner(ctx, in.UserID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	reserved, err := u.repo.SumReservedWithdrawals(ctx, in.UserID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	withdrawable := subtractMoney(earned, reserved)
	withdrawableCents, _ := parseMoneyCents(withdrawable)
	if amountCents > withdrawableCents {
		return nil, httpx.Conflict("withdrawal.insufficient_earnings",
			fmt.Sprintf("yechiladigan bot daromadi yetarli emas. Mavjud: %s so'm", withdrawable))
	}

	card := strings.ReplaceAll(in.CardNumber, " ", "")
	if !isValidCardNumber(card) {
		return nil, httpx.BadRequest("withdrawal.invalid_card", "karta raqami 16-19 ta raqamdan iborat bo'lishi kerak")
	}
	in.CardNumber = card

	wd, err := u.repo.CreateWithdrawal(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return wd, nil
}

func (u *usecase) GetWithdrawalForUser(ctx context.Context, id, userID uuid.UUID) (*WithdrawalRequest, error) {
	wd, err := u.repo.GetWithdrawalForUser(ctx, id, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("withdrawal.not_found", "so'rov topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return wd, nil
}

func (u *usecase) ListWithdrawalsForUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]WithdrawalRequest, int64, error) {
	limit, offset = clampLimit(limit), clampOffset(offset)
	items, err := u.repo.ListWithdrawalsForUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountWithdrawalsForUser(ctx, userID)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// CancelWithdrawal mirrors WithdrawalView.perform_destroy: only a still-
// pending request can be cancelled by its owner.
func (u *usecase) CancelWithdrawal(ctx context.Context, id, userID uuid.UUID) error {
	if err := u.repo.DeletePendingWithdrawal(ctx, id, userID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("withdrawal.not_found", "so'rov topilmadi yoki allaqachon ko'rib chiqilgan")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) AdminListWithdrawals(ctx context.Context, status string, limit, offset int32) ([]WithdrawalRequest, int64, error) {
	limit, offset = clampLimit(limit), clampOffset(offset)
	items, err := u.repo.ListWithdrawalsAdmin(ctx, status, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountWithdrawalsAdmin(ctx, status)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) AdminGetWithdrawal(ctx context.Context, id uuid.UUID) (*WithdrawalRequest, error) {
	wd, err := u.repo.GetWithdrawalByIDAdmin(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("withdrawal.not_found", "so'rov topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return wd, nil
}

// AdminApproveWithdrawal mirrors administration/views/bot_withdrawals.py::approve:
// deducts users.balance and marks approved atomically (repository.go's tx).
func (u *usecase) AdminApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*WithdrawalRequest, error) {
	wd, err := u.repo.ApproveWithdrawal(ctx, id, reviewerID)
	if err != nil {
		switch {
		case errors.Is(err, ErrWithdrawalNotPending):
			return nil, httpx.Conflict("withdrawal.not_pending", "so'rov allaqachon ko'rib chiqilgan")
		case errors.Is(err, ErrInsufficientFunds):
			return nil, httpx.Conflict("withdrawal.insufficient_balance", "foydalanuvchi balansi yetarli emas")
		case errors.Is(err, database.ErrNotFound):
			return nil, httpx.NotFound("withdrawal.not_found", "so'rov topilmadi")
		default:
			return nil, httpx.Internal(err)
		}
	}
	return wd, nil
}

func (u *usecase) AdminRejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*WithdrawalRequest, error) {
	wd, err := u.repo.RejectWithdrawal(ctx, id, reviewerID, reason)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.Conflict("withdrawal.not_pending", "so'rov allaqachon ko'rib chiqilgan yoki topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return wd, nil
}

// ===================== gateway settlement =====================

// SettlePaidTransaction mirrors views/click.py::ClickWebhookAPIView.successfully_payment
// (shared by the Payme perform-transaction path too): dispatch on whether the
// transaction backs a PaymentOrder or is a standalone legacy top-up.
func (u *usecase) SettlePaidTransaction(ctx context.Context, transactionID uuid.UUID, externalID string) (*Order, error) {
	order, err := u.repo.GetOrderByTransactionID(ctx, transactionID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return nil, httpx.Internal(err)
	}

	if order == nil {
		txn, err := u.repo.GetTransactionByID(ctx, transactionID)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, httpx.NotFound("transaction.not_found", "tranzaksiya topilmadi")
			}
			return nil, httpx.Internal(err)
		}
		amountSom, err := parseMoneySom(txn.Amount)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		if _, _, err := u.repo.ApplyBalanceDelta(ctx, BalanceDeltaInput{
			UserID: txn.UserID, DeltaSom: amountSom, Comment: "Balans to'ldirildi",
			IdempotencyKey: fmt.Sprintf("txn:credit:%s", txn.ID),
		}); err != nil {
			return nil, httpx.Internal(fmt.Errorf("credit transaction top-up: %w", err))
		}
		if err := u.repo.SetTransactionStatus(ctx, txn.ID, false, true); err != nil && !errors.Is(err, database.ErrNotFound) {
			return nil, httpx.Internal(err)
		}
		return nil, nil
	}

	if order.Status == OrderStatusPaid {
		return order, nil // already settled — idempotent no-op (retried webhook)
	}
	if order.Status != OrderStatusCreated {
		return order, nil // cancelled/refunded: ignore a stray late callback
	}

	updated, err := u.repo.MarkOrderPaid(ctx, order.ID, order.Commission, order.NetAmount, externalID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// Guarded by status='created'; a concurrent call already settled it.
			return u.repo.GetOrderByID(ctx, order.ID)
		}
		return nil, httpx.Internal(err)
	}

	netSom, err := parseMoneySom(updated.NetAmount)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if _, _, err := u.repo.ApplyBalanceDelta(ctx, BalanceDeltaInput{
		UserID:         updated.OwnerID,
		DeltaSom:       netSom,
		Comment:        fmt.Sprintf("Bot to'lovi: %s", firstNonEmpty(updated.Comment, updated.ID.String())),
		IdempotencyKey: fmt.Sprintf("payorder:credit:%s", updated.ID),
	}); err != nil {
		// The credit must not be silently dropped: surface it so the caller
		// can retry — MarkOrderPaid is now a no-op (status already 'paid').
		return nil, httpx.Internal(fmt.Errorf("credit order owner: %w", err))
	}

	if u.revenueUserID != nil {
		if commissionSom, err := parseMoneySom(updated.Commission); err == nil && commissionSom > 0 {
			_, _, _ = u.repo.ApplyBalanceDelta(ctx, BalanceDeltaInput{
				UserID:         *u.revenueUserID,
				DeltaSom:       commissionSom,
				Comment:        fmt.Sprintf("Botspace komissiya: order #%s", updated.ID),
				IdempotencyKey: fmt.Sprintf("payorder:fee:%s", updated.ID),
			})
		}
	}

	// Keep transaction.status in sync even on the order-linked path — Payme's
	// CheckTransaction/PerformTransaction (gateway_payme.go) read this field
	// directly to report state 1 (created) vs 2 (performed); without this the
	// transaction stayed "not performed" forever from Payme's point of view
	// even after the order was fully paid and the owner credited. Best-effort:
	// a guard failure (already true, e.g. a concurrent/retried settle) is not
	// an error.
	if err := u.repo.SetTransactionStatus(ctx, transactionID, false, true); err != nil && !errors.Is(err, database.ErrNotFound) {
		return nil, httpx.Internal(err)
	}

	_ = u.notifier.NotifyOrderPaid(ctx, updated) // best-effort; must never block the webhook response
	return updated, nil
}

func (u *usecase) SettlePaycueTransaction(ctx context.Context, in PaycueWebhookInput) (*Order, error) {
	if in.TransactionID == "" {
		return nil, httpx.BadRequest("paycue.invalid_transaction", "transaction_id majburiy")
	}
	order, err := u.repo.GetOrderByExternalID(ctx, in.TransactionID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("order.not_found", "order topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	if in.Amount != "" {
		if ok, err := sameMoneyAmount(order.Amount, in.Amount); err != nil {
			return nil, httpx.BadRequest("paycue.invalid_amount", "amount noto'g'ri formatda")
		} else if !ok {
			return nil, httpx.BadRequest("paycue.amount_mismatch", "amount order bilan mos emas")
		}
	}

	switch in.Action {
	case "confirm":
		if order.TransactionID == nil {
			return nil, httpx.Conflict("paycue.order_without_transaction", "order transaction bilan bog'lanmagan")
		}
		return u.SettlePaidTransaction(ctx, *order.TransactionID, in.TransactionID)
	case "cancel":
		if order.Status == OrderStatusCancelled {
			return order, nil
		}
		if order.Status == OrderStatusPaid {
			return order, nil
		}
		updated, err := u.repo.MarkOrderCancelled(ctx, order.ID)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return u.repo.GetOrderByID(ctx, order.ID)
			}
			return nil, httpx.Internal(err)
		}
		if order.TransactionID != nil {
			_ = u.repo.SetTransactionStatus(ctx, *order.TransactionID, true, false)
		}
		if err := u.repo.ClearOrderIdempotencyKey(ctx, updated.ID); err != nil && !errors.Is(err, database.ErrNotFound) {
			return nil, httpx.Internal(err)
		}
		return updated, nil
	default:
		return nil, httpx.BadRequest("paycue.invalid_action", "action noto'g'ri")
	}
}

// SettleCancelledTransaction mirrors views/click.py::cancelled_payment.
func (u *usecase) SettleCancelledTransaction(ctx context.Context, transactionID uuid.UUID) (*Order, error) {
	order, err := u.repo.GetOrderByTransactionID(ctx, transactionID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return nil, httpx.Internal(err)
	}

	if order == nil {
		txn, err := u.repo.GetTransactionByID(ctx, transactionID)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, httpx.NotFound("transaction.not_found", "tranzaksiya topilmadi")
			}
			return nil, httpx.Internal(err)
		}
		amountSom, err := parseMoneySom(txn.Amount)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		_, _, _ = u.repo.ApplyBalanceDelta(ctx, BalanceDeltaInput{
			UserID: txn.UserID, DeltaSom: -amountSom, Comment: "To'lov bekor qilindi",
			IdempotencyKey: fmt.Sprintf("txn:debit:%s", txn.ID),
		})
		_ = u.repo.SetTransactionStatus(ctx, txn.ID, true, false)
		return nil, nil
	}

	if order.Status == OrderStatusCancelled {
		return order, nil
	}
	if order.Status == OrderStatusPaid {
		if netSom, err := parseMoneySom(order.NetAmount); err == nil {
			_, _, _ = u.repo.ApplyBalanceDelta(ctx, BalanceDeltaInput{
				UserID:         order.OwnerID,
				DeltaSom:       -netSom,
				Comment:        fmt.Sprintf("Bot to'lovi qaytarildi: %s", firstNonEmpty(order.Comment, order.ID.String())),
				IdempotencyKey: fmt.Sprintf("payorder:debit:%s", order.ID),
			})
		}
	}

	updated, err := u.repo.MarkOrderCancelled(ctx, order.ID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return u.repo.GetOrderByID(ctx, order.ID)
		}
		return nil, httpx.Internal(err)
	}
	return updated, nil
}

// ===================== legacy payment link =====================

// CreatePaymentLink mirrors views/payment.py::payment_link — a simple direct
// balance top-up transaction (no PaymentOrder involved).
func (u *usecase) CreatePaymentLink(ctx context.Context, userID uuid.UUID, provider, amount string) (string, error) {
	cents, err := parseMoneyCents(amount)
	if err != nil || cents <= 0 {
		return "", httpx.BadRequest("payment.invalid_amount", "amount noto'g'ri formatda")
	}
	txn, err := u.repo.CreateTransaction(ctx, CreateTransactionInput{ID: uuid.New(), UserID: userID, Amount: amount})
	if err != nil {
		return "", httpx.Internal(err)
	}
	switch provider {
	case ProviderClick:
		return u.click.PayURL(txn.ID, amount), nil
	case ProviderPayme:
		return u.payme.PayURL(txn.ID, cents), nil
	default:
		return "", httpx.BadRequest("payment.unsupported_provider", "provider qo'llab-quvvatlanmaydi")
	}
}

// ===================== helpers =====================

func mapOrderErr(o *Order, err error) (*Order, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("order.not_found", "order topilmadi")
		}
		return nil, httpx.Internal(err)
	}
	return o, nil
}

func clampLimit(limit int32) int32 {
	if limit <= 0 || limit > 100 {
		return 20
	}
	return limit
}

func clampOffset(offset int32) int32 {
	if offset < 0 {
		return 0
	}
	return offset
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func isValidCardNumber(s string) bool {
	if len(s) < 16 || len(s) > 19 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// parseMoneyCents parses a NUMERIC(20,2)-shaped decimal string ("12345.67")
// into an integer count of hundredths (cents/tiyin). Used instead of
// float/Decimal arithmetic to keep money math exact.
func parseMoneyCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	frac := "00"
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			frac = frac[:2]
		}
		for len(frac) < 2 {
			frac += "0"
		}
	}
	wholeN, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q: %w", s, err)
	}
	fracN, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q: %w", s, err)
	}
	cents := wholeN*100 + fracN
	if neg {
		cents = -cents
	}
	return cents, nil
}

// formatMoneyCents is the inverse of parseMoneyCents.
func formatMoneyCents(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		s = "-" + s
	}
	return s
}

// parseMoneySom truncates a decimal so'm string to whole so'm (matches
// Django's `int(Decimal(amount))` used throughout services/balance.py).
func parseMoneySom(s string) (int64, error) {
	cents, err := parseMoneyCents(s)
	if err != nil {
		return 0, err
	}
	return cents / 100, nil
}

func sameMoneyAmount(a, b string) (bool, error) {
	ac, err := parseMoneyCents(a)
	if err != nil {
		return false, err
	}
	bc, err := parseMoneyCents(b)
	if err != nil {
		return false, err
	}
	return ac == bc, nil
}

// calculateCommission mirrors services/commission.py::calculate_commission
// (round-half-up on the commission, percent out of 100).
func calculateCommission(amount string, percent int64) (commission, net string, err error) {
	cents, err := parseMoneyCents(amount)
	if err != nil {
		return "", "", err
	}
	commissionCents := (cents*percent + 50) / 100
	netCents := cents - commissionCents
	return formatMoneyCents(commissionCents), formatMoneyCents(netCents), nil
}

// subtractMoney computes max(a-b, 0) for two decimal strings.
func subtractMoney(a, b string) string {
	ac, err := parseMoneyCents(a)
	if err != nil {
		ac = 0
	}
	bc, err := parseMoneyCents(b)
	if err != nil {
		bc = 0
	}
	diff := ac - bc
	if diff < 0 {
		diff = 0
	}
	return formatMoneyCents(diff)
}
