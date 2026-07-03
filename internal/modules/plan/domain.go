// Package plan ports Django's pricing-plan domain (core.apps.api.models.plan):
// a catalogue of pricing tiers (Plan) and one subscription per user
// (Subscription) that tracks monthly AI token usage and bot-creation quota.
package plan

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Plan names. Mirrors enums.plan.PlanType.
const (
	NameFree = "free"
	NamePro  = "pro"
	NameMax  = "max"
)

// Subscription statuses. Mirrors enums.plan.PlanStatus.
const (
	StatusActive      = "active"
	StatusSuspended   = "suspended"
	StatusGracePeriod = "grace_period" // 3 kunlik imkoniyat muddati
	StatusCancelled   = "cancelled"
)

// BillingMonthsDiscounts mirrors BILLING_MONTHS_DISCOUNTS in
// core/apps/api/serializers/project/relase.py: percentage discount applied to
// the monthly price for each allowed billing period.
var BillingMonthsDiscounts = map[int32]int32{1: 0, 3: 12, 6: 20, 12: 30}

// Plan is a pricing tier. Price and CachedTokenRate are NUMERIC columns
// rendered as decimal strings (see user.User.TemplateBalance for the same
// convention). MaxOutputTokens/MaxInputTokens are deprecated Django columns
// kept only for schema/history parity; no business logic reads them.
type Plan struct {
	ID              uuid.UUID
	Name            string
	Price           string
	Description     string
	IsTop           bool
	IsActive        bool
	SortOrder       int32
	MaxBots         int32
	MaxTokens       int64
	MaxOutputTokens int64
	MaxInputTokens  int64
	CachedTokenRate string
	Features        json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Subscription is a user's active plan assignment (Django UserSubscription).
// It never embeds the Plan; callers fetch it separately (repo.GetPlanByID)
// so a single row read stays cheap.
type Subscription struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	PlanID           uuid.UUID
	Status           string
	StartedAt        time.Time
	ExpiresAt        *time.Time
	TokensUsedInput  int64
	TokensUsedOutput int64
	TokenResetAt     time.Time
	BotsCount        int32
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// TokensRemaining mirrors UserSubscription.tokens_remaining(): the plan's
// monthly budget minus tokens already consumed, floored at zero.
func (s *Subscription) TokensRemaining(plan *Plan) int64 {
	remaining := plan.MaxTokens - (s.TokensUsedInput + s.TokensUsedOutput)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsTokenLimitExceeded mirrors UserSubscription.is_token_limit_exceeded().
func (s *Subscription) IsTokenLimitExceeded(plan *Plan) bool {
	return s.TokensRemaining(plan) <= 0
}

// CanCreateBot mirrors UserSubscription.can_create_bot(): 0 means unlimited.
func (s *Subscription) CanCreateBot(plan *Plan) bool {
	if plan.MaxBots == 0 {
		return true
	}
	return s.BotsCount < plan.MaxBots
}

// ReportUsageInput carries a token-usage report from the AI runner
// (agentv3/worker), authenticated out-of-band via an internal token header
// rather than a user JWT (see handler.go).
type ReportUsageInput struct {
	UserID          uuid.UUID
	ProjectID       *uuid.UUID
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
}

// CreatePlanInput is the admin create payload.
type CreatePlanInput struct {
	Name            string
	Price           string
	Description     string
	IsTop           bool
	IsActive        bool
	SortOrder       int32
	MaxBots         int32
	MaxTokens       int64
	CachedTokenRate string
	Features        json.RawMessage
}

// UpdatePlanInput carries optional fields; nil pointers are left unchanged.
type UpdatePlanInput struct {
	Name            *string
	Price           *string
	Description     *string
	IsTop           *bool
	IsActive        *bool
	SortOrder       *int32
	MaxBots         *int32
	MaxTokens       *int64
	CachedTokenRate *string
	Features        json.RawMessage
}

// Repository is the persistence port for both plan and subscription rows.
type Repository interface {
	ListActivePlans(ctx context.Context) ([]Plan, error)
	GetPlanByID(ctx context.Context, id uuid.UUID) (*Plan, error)
	GetActivePlanByID(ctx context.Context, id uuid.UUID) (*Plan, error)
	GetPlanByName(ctx context.Context, name string) (*Plan, error)

	GetSubscriptionByUserID(ctx context.Context, userID uuid.UUID) (*Subscription, error)
	// GetOrCreateSubscription creates a subscription on defaultPlanID if the
	// user has none yet; otherwise it returns the existing row untouched.
	GetOrCreateSubscription(ctx context.Context, userID, defaultPlanID uuid.UUID) (*Subscription, error)
	UpdateSubscriptionPlan(ctx context.Context, id, planID uuid.UUID, status string, expiresAt time.Time) (*Subscription, error)
	IncrementUsage(ctx context.Context, id uuid.UUID, inputDelta, outputDelta int64) error

	// Admin CRUD (Tarif boshqaruvi).
	ListAllPlans(ctx context.Context) ([]Plan, error)
	CreatePlan(ctx context.Context, in CreatePlanInput) (*Plan, error)
	UpdatePlan(ctx context.Context, id uuid.UUID, in UpdatePlanInput) (*Plan, error)
	DeletePlan(ctx context.Context, id uuid.UUID) error
}
