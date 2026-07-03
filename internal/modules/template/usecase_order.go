package template

import (
	"context"
	"errors"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// ===================== orders / purchase =====================

// CreateOrder returns the buyer's existing order for a template if one
// exists, otherwise creates one (mirrors views/order.py::TemplateOrderView).
// Free templates (price 0) are auto-confirmed and granted immediately; paid
// ones stay pending until PayOrder charges the buyer's wallet.
func (u *usecase) CreateOrder(ctx context.Context, userID, templateID uuid.UUID) (*Order, error) {
	t, err := u.repo.GetByID(ctx, templateID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.not_found", "template not found")
		}
		return nil, httpx.Internal(err)
	}
	if !t.IsVerify {
		return nil, httpx.NotFound("template.not_found", "template not found")
	}
	o, err := u.repo.GetOrCreateOrder(ctx, userID, templateID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return o, nil
}

func (u *usecase) GetOrder(ctx context.Context, id, ownerID uuid.UUID) (*Order, error) {
	o, err := u.repo.GetOrderForOwner(ctx, id, ownerID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.order_not_found", "order not found")
		}
		return nil, httpx.Internal(err)
	}
	return o, nil
}

func (u *usecase) ListOrders(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Order, int64, error) {
	limit, offset = clampPage(limit, offset)
	items, err := u.repo.ListOrdersForOwner(ctx, ownerID, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountOrdersForOwner(ctx, ownerID)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// PayOrder charges the buyer's wallet balance and credits the template
// author's commission (AuthorCommissionPercent). Already-paid orders return
// unchanged (idempotent). Mirrors signals/order.py::OrderSignal's commission
// split, triggered here explicitly instead of on a Django post_save signal.
func (u *usecase) PayOrder(ctx context.Context, id, buyerID uuid.UUID) (*Order, error) {
	o, err := u.repo.Pay(ctx, id, buyerID)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrNotFound):
			return nil, httpx.NotFound("template.order_not_found", "order not found")
		case errors.Is(err, ErrInsufficientBalance):
			return nil, httpx.Conflict("template.insufficient_balance", "insufficient wallet balance")
		case errors.Is(err, ErrTemplateUnavailable):
			return nil, httpx.Conflict("template.unavailable", "the ordered template is no longer available")
		default:
			return nil, httpx.Internal(err)
		}
	}
	return o, nil
}

// ===================== withdrawals =====================

// CreateWithdrawal validates the amount against the caller's current
// template_balance before filing the request (mirrors
// CreateTemplateWithdrawalSerializer.validate_amount/validate_card_number).
func (u *usecase) CreateWithdrawal(ctx context.Context, userID uuid.UUID, amount, cardNumber, cardOwner string) (*Withdrawal, error) {
	amt, ok := parseRat(amount)
	if !ok || amt.Sign() <= 0 {
		return nil, httpx.BadRequest("template.invalid_amount", "amount must be greater than 0")
	}
	if err := validateCardNumber(cardNumber); err != nil {
		return nil, httpx.BadRequest("template.invalid_card_number", err.Error())
	}
	if cardOwner == "" {
		return nil, httpx.BadRequest("template.invalid_card_owner", "card_owner is required")
	}

	balanceStr, err := u.repo.GetAuthorBalance(ctx, userID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	balance, _ := parseRat(balanceStr)
	if balance == nil || amt.Cmp(balance) > 0 {
		return nil, httpx.Conflict("template.insufficient_template_balance", "insufficient template balance")
	}

	w, err := u.repo.CreateWithdrawal(ctx, userID, amount, cardNumber, cardOwner)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return w, nil
}

func (u *usecase) GetWithdrawal(ctx context.Context, id, ownerID uuid.UUID) (*Withdrawal, error) {
	w, err := u.repo.GetWithdrawalForOwner(ctx, id, ownerID)
	return mapWithdrawalErr(w, err)
}

func (u *usecase) ListWithdrawals(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Withdrawal, int64, error) {
	limit, offset = clampPage(limit, offset)
	items, err := u.repo.ListWithdrawalsForOwner(ctx, ownerID, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountWithdrawalsForOwner(ctx, ownerID)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) DeleteWithdrawal(ctx context.Context, id, ownerID uuid.UUID) error {
	existing, err := u.repo.GetWithdrawalForOwner(ctx, id, ownerID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("template.withdrawal_not_found", "withdrawal not found")
		}
		return httpx.Internal(err)
	}
	if existing.Status != WithdrawalStatusPending {
		return httpx.Conflict("template.withdrawal_not_pending", "cannot delete a reviewed withdrawal")
	}
	if err := u.repo.DeleteWithdrawal(ctx, id, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("template.withdrawal_not_found", "withdrawal not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) ListWithdrawalsAdmin(ctx context.Context, status string, limit, offset int32) ([]Withdrawal, int64, error) {
	limit, offset = clampPage(limit, offset)
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

// ApproveWithdrawal deducts template_balance and marks approved; if the
// balance is no longer sufficient it is rejected instead (mirrors
// admin/withdrawal.py::approve_withdrawals - never returns a hard error for
// that case, matching Django's fallback-to-rejection behavior).
func (u *usecase) ApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*Withdrawal, error) {
	w, err := u.repo.ApproveWithdrawal(ctx, id, reviewerID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.withdrawal_not_found", "withdrawal not found")
		}
		if errors.Is(err, ErrWithdrawalNotPending) {
			return nil, httpx.Conflict("template.withdrawal_not_pending", "withdrawal already reviewed")
		}
		return nil, httpx.Internal(err)
	}
	return w, nil
}

func (u *usecase) RejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Withdrawal, error) {
	if reason == "" {
		return nil, httpx.BadRequest("template.rejection_reason_required", "rejection_reason is required")
	}
	w, err := u.repo.RejectWithdrawal(ctx, id, reviewerID, reason)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.withdrawal_not_found", "withdrawal not found or already reviewed")
		}
		return nil, httpx.Internal(err)
	}
	return w, nil
}

func mapWithdrawalErr(w *Withdrawal, err error) (*Withdrawal, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.withdrawal_not_found", "withdrawal not found")
		}
		return nil, httpx.Internal(err)
	}
	return w, nil
}

// validateCardNumber mirrors CreateTemplateWithdrawalSerializer.validate_card_number:
// digits only (spaces stripped first), 13-19 characters (standard PAN range).
func validateCardNumber(raw string) error {
	digits := make([]rune, 0, len(raw))
	for _, r := range raw {
		if r == ' ' {
			continue
		}
		if r < '0' || r > '9' {
			return errors.New("card number must contain only digits")
		}
		digits = append(digits, r)
	}
	if len(digits) < 13 || len(digits) > 19 {
		return errors.New("card number must be between 13 and 19 digits")
	}
	return nil
}
