package plan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/botspace/backend/internal/modules/user"
	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Postgres error codes checked by the admin plan CRUD (mirrors
// internal/modules/user's uniqueViolation const of the same contract).
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation
}

// daysPerBillingMonth mirrors Django's `timedelta(days=months * 30)`.
const daysPerBillingMonth = 30

// Usecase exposes plan/subscription business operations.
type Usecase interface {
	ListActivePlans(ctx context.Context) ([]Plan, error)
	// Current returns the caller's subscription, creating a FREE one on
	// first access (mirrors _get_or_create_subscription).
	Current(ctx context.Context, userID uuid.UUID) (*Subscription, *Plan, error)
	Usage(ctx context.Context, userID uuid.UUID) (*Subscription, *Plan, error)
	// Upgrade switches the caller onto a new plan for the given billing
	// period, charging the discounted total price from their balance.
	// Returns the updated subscription, the new plan, and the previous
	// plan's name (for the response message).
	Upgrade(ctx context.Context, userID, planID uuid.UUID, months int32) (*Subscription, *Plan, string, error)
	// ReportUsage records AI token consumption for a user (called by the
	// internal-token-authenticated endpoint) and returns the billable input
	// token count actually applied (after the cached-token discount).
	ReportUsage(ctx context.Context, in ReportUsageInput) (billableInput int64, err error)

	// Admin CRUD (Tarif boshqaruvi) — every plan regardless of is_active.
	AdminListPlans(ctx context.Context) ([]Plan, error)
	AdminCreatePlan(ctx context.Context, in CreatePlanInput) (*Plan, error)
	AdminUpdatePlan(ctx context.Context, id uuid.UUID, in UpdatePlanInput) (*Plan, error)
	AdminDeletePlan(ctx context.Context, id uuid.UUID) error

	// ExpireSubscriptions transitions active subscriptions past expires_at
	// into grace_period, and grace_period subscriptions past their 3-day
	// grace window into suspended. Called by the scheduler
	// (internal/shared/scheduler), not an HTTP route — see the queries'
	// ExpireActiveSubscriptions/SuspendGracePeriodSubscriptions doc comment
	// for why this is net-new Go-side logic, not a Django port. Returns
	// (movedToGrace, movedToSuspended, error).
	ExpireSubscriptions(ctx context.Context) (int, int, error)
}

type usecase struct {
	repo  Repository
	users user.Usecase
}

// NewUsecase wires the plan module. users is the shared user module usecase,
// used only to read/charge the caller's balance during an upgrade (mirrors
// Django's core.apps.payment.services.balance.subtract_balance call).
func NewUsecase(repo Repository, users user.Usecase) Usecase {
	return &usecase{repo: repo, users: users}
}

func (u *usecase) ListActivePlans(ctx context.Context) ([]Plan, error) {
	plans, err := u.repo.ListActivePlans(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return plans, nil
}

func (u *usecase) Current(ctx context.Context, userID uuid.UUID) (*Subscription, *Plan, error) {
	return u.getOrCreateSubscription(ctx, userID)
}

func (u *usecase) ExpireSubscriptions(ctx context.Context) (int, int, error) {
	toGrace, err := u.repo.ExpireActiveSubscriptions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("expire active subscriptions: %w", err)
	}
	suspended, err := u.repo.SuspendGracePeriodSubscriptions(ctx)
	if err != nil {
		return len(toGrace), 0, fmt.Errorf("suspend grace-period subscriptions: %w", err)
	}
	return len(toGrace), len(suspended), nil
}

func (u *usecase) Usage(ctx context.Context, userID uuid.UUID) (*Subscription, *Plan, error) {
	return u.getOrCreateSubscription(ctx, userID)
}

// getOrCreateSubscription mirrors _get_or_create_subscription: fetch the
// user's subscription, or seed one on the FREE plan if this is their first
// visit. The FREE plan lookup only happens on the (rare) creation path, kept
// off the hot "already subscribed" path.
func (u *usecase) getOrCreateSubscription(ctx context.Context, userID uuid.UUID) (*Subscription, *Plan, error) {
	sub, err := u.repo.GetSubscriptionByUserID(ctx, userID)
	if err != nil {
		if !errors.Is(err, database.ErrNotFound) {
			return nil, nil, httpx.Internal(err)
		}
		freePlan, ferr := u.repo.GetPlanByName(ctx, NameFree)
		if ferr != nil {
			if errors.Is(ferr, database.ErrNotFound) {
				return nil, nil, httpx.Internal(fmt.Errorf("FREE plan topilmadi. Admin bilan bog'laning"))
			}
			return nil, nil, httpx.Internal(ferr)
		}
		sub, err = u.repo.GetOrCreateSubscription(ctx, userID, freePlan.ID)
		if err != nil {
			return nil, nil, httpx.Internal(err)
		}
		if sub.PlanID == freePlan.ID {
			return sub, freePlan, nil
		}
		// Lost a get-or-create race against a concurrent upgrade; fall through
		// to fetch whatever plan actually ended up attached.
	}

	plan, err := u.repo.GetPlanByID(ctx, sub.PlanID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, nil, httpx.Internal(fmt.Errorf("subscription plan %s not found", sub.PlanID))
		}
		return nil, nil, httpx.Internal(err)
	}
	return sub, plan, nil
}

func (u *usecase) Upgrade(
	ctx context.Context, userID, planID uuid.UUID, months int32,
) (*Subscription, *Plan, string, error) {
	sub, oldPlan, err := u.getOrCreateSubscription(ctx, userID)
	if err != nil {
		return nil, nil, "", err
	}

	if _, ok := BillingMonthsDiscounts[months]; !ok {
		return nil, nil, "", httpx.BadRequest(
			"subscription.invalid_months", "noto'g'ri muddat. Quyidagilardan birini tanlang: 1, 3, 6, 12",
		)
	}

	newPlan, err := u.repo.GetActivePlanByID(ctx, planID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, nil, "", httpx.BadRequest("plan.not_found", "tarif topilmadi yoki nofaol")
		}
		return nil, nil, "", httpx.Internal(err)
	}
	if newPlan.ID == sub.PlanID {
		return nil, nil, "", httpx.Conflict("subscription.already_subscribed", "siz allaqachon ushbu tarifdasiz")
	}

	totalPrice, err := calculateTotalPrice(newPlan.Price, months)
	if err != nil {
		return nil, nil, "", httpx.Internal(err)
	}

	// Charge first (atomic, guarded against overdraft by the user module),
	// then flip the plan. If the plan flip fails we refund best-effort so a
	// user is never charged without receiving the plan.
	if totalPrice > 0 {
		if _, err := u.users.AddBalance(ctx, userID, -totalPrice); err != nil {
			return nil, nil, "", err
		}
	}

	expiresAt := time.Now().AddDate(0, 0, int(months)*daysPerBillingMonth)
	updated, err := u.repo.UpdateSubscriptionPlan(ctx, sub.ID, newPlan.ID, StatusActive, expiresAt)
	if err != nil {
		if totalPrice > 0 {
			_, _ = u.users.AddBalance(ctx, userID, totalPrice) // best-effort refund
		}
		return nil, nil, "", httpx.Internal(err)
	}

	return updated, newPlan, oldPlan.Name, nil
}

func (u *usecase) ReportUsage(ctx context.Context, in ReportUsageInput) (int64, error) {
	if _, err := u.users.GetByID(ctx, in.UserID); err != nil {
		return 0, err // already httpx.NotFound / httpx.Internal
	}

	sub, plan, err := u.getOrCreateSubscription(ctx, in.UserID)
	if err != nil {
		return 0, err
	}

	// Cached tokens are billed at the plan's discounted rate; never let
	// cache_read exceed the reported input.
	cacheRead := in.CacheReadTokens
	if cacheRead < 0 {
		cacheRead = 0
	}
	if cacheRead > in.InputTokens {
		cacheRead = in.InputTokens
	}
	uncached := in.InputTokens - cacheRead

	rate, err := strconv.ParseFloat(plan.CachedTokenRate, 64)
	if err != nil {
		rate = 0
	}
	billableInput := uncached + int64(math.Round(float64(cacheRead)*rate))

	if err := u.repo.IncrementUsage(ctx, sub.ID, billableInput, in.OutputTokens); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return 0, httpx.NotFound("subscription.not_found", "obuna topilmadi")
		}
		return 0, httpx.Internal(err)
	}
	return billableInput, nil
}

// ===================== admin CRUD =====================

// AdminListPlans returns every plan (active and inactive) for the admin
// "Tarif turlari" management page.
func (u *usecase) AdminListPlans(ctx context.Context) ([]Plan, error) {
	plans, err := u.repo.ListAllPlans(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return plans, nil
}

func (u *usecase) AdminCreatePlan(ctx context.Context, in CreatePlanInput) (*Plan, error) {
	if in.Name == "" {
		return nil, httpx.BadRequest("plan.name_required", "name is required")
	}
	if in.Price == "" {
		in.Price = "0"
	}
	if in.CachedTokenRate == "" {
		in.CachedTokenRate = "0"
	}
	p, err := u.repo.CreatePlan(ctx, in)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, httpx.Conflict("plan.name_taken", "bu nomli tarif allaqachon mavjud")
		}
		return nil, httpx.Internal(err)
	}
	return p, nil
}

func (u *usecase) AdminUpdatePlan(ctx context.Context, id uuid.UUID, in UpdatePlanInput) (*Plan, error) {
	p, err := u.repo.UpdatePlan(ctx, id, in)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("plan.not_found", "tarif topilmadi")
		}
		if isUniqueViolation(err) {
			return nil, httpx.Conflict("plan.name_taken", "bu nomli tarif allaqachon mavjud")
		}
		return nil, httpx.Internal(err)
	}
	return p, nil
}

// AdminDeletePlan removes a plan. A plan with active subscriptions cannot be
// deleted (user_subscription.plan_id is ON DELETE RESTRICT) — surfaced as a
// 409 instead of a raw FK violation.
func (u *usecase) AdminDeletePlan(ctx context.Context, id uuid.UUID) error {
	if err := u.repo.DeletePlan(ctx, id); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("plan.not_found", "tarif topilmadi")
		}
		if isForeignKeyViolation(err) {
			return httpx.Conflict("plan.in_use", "bu tarifga obuna bo'lgan foydalanuvchilar bor, avval ularni ko'chiring")
		}
		return httpx.Internal(err)
	}
	return nil
}

// calculateTotalPrice mirrors calculate_total_price() in
// core/apps/api/serializers/project/relase.py: apply the billing-period
// discount to the monthly price, round to whole currency units, multiply by
// the number of months.
func calculateTotalPrice(monthlyPrice string, months int32) (int64, error) {
	price, err := strconv.ParseFloat(monthlyPrice, 64)
	if err != nil {
		return 0, fmt.Errorf("parse plan price %q: %w", monthlyPrice, err)
	}
	discount := BillingMonthsDiscounts[months]
	discountedMonthly := math.Round(price * (1 - float64(discount)/100))
	return int64(discountedMonthly) * int64(months), nil
}
