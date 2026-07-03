package plan

import (
	"encoding/json"
	"math"
	"time"
)

// planDisplay mirrors PlanModel.get_name_display() (the TextChoices label).
func planDisplay(name string) string {
	switch name {
	case NameFree:
		return "Free"
	case NamePro:
		return "Pro"
	case NameMax:
		return "Max"
	default:
		return name
	}
}

// statusDisplay mirrors UserSubscription.get_status_display().
func statusDisplay(status string) string {
	switch status {
	case StatusActive:
		return "Faol"
	case StatusSuspended:
		return "To'xtatilgan"
	case StatusGracePeriod:
		return "Imkoniyat muddati"
	case StatusCancelled:
		return "Bekor qilingan"
	default:
		return status
	}
}

// PlanResponse mirrors PlanSerializer's field list exactly.
type PlanResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	NameDisplay string          `json:"name_display"`
	Price       string          `json:"price"`
	MaxBots     int32           `json:"max_bots"`
	MaxTokens   int64           `json:"max_tokens"`
	Features    json.RawMessage `json:"features"`
	IsTop       bool            `json:"is_top"`
	Description string          `json:"description,omitempty"`
	IsActive    bool            `json:"is_active"`
	CreatedAt   time.Time       `json:"created_at"`
}

func toPlanResponse(p *Plan) PlanResponse {
	features := p.Features
	if len(features) == 0 {
		features = json.RawMessage("{}")
	}
	return PlanResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		NameDisplay: planDisplay(p.Name),
		Price:       p.Price,
		MaxBots:     p.MaxBots,
		MaxTokens:   p.MaxTokens,
		Features:    features,
		IsTop:       p.IsTop,
		Description: p.Description,
		IsActive:    p.IsActive,
		CreatedAt:   p.CreatedAt,
	}
}

func toPlanListResponse(plans []Plan) []PlanResponse {
	out := make([]PlanResponse, len(plans))
	for i := range plans {
		out[i] = toPlanResponse(&plans[i])
	}
	return out
}

// ===================== admin CRUD (Tarif boshqaruvi) =====================

// CreatePlanRequest is the admin create payload.
type CreatePlanRequest struct {
	Name            string          `json:"name" validate:"required,min=1,max=100"`
	Price           string          `json:"price" validate:"omitempty"`
	Description     string          `json:"description" validate:"omitempty"`
	IsTop           bool            `json:"is_top"`
	IsActive        bool            `json:"is_active"`
	SortOrder       int32           `json:"sort_order"`
	MaxBots         int32           `json:"max_bots" validate:"gte=0"`
	MaxTokens       int64           `json:"max_tokens" validate:"gte=0"`
	CachedTokenRate string          `json:"cached_token_rate" validate:"omitempty"`
	Features        json.RawMessage `json:"features" validate:"omitempty"`
}

// UpdatePlanRequest carries optional fields; omitted fields are left unchanged.
type UpdatePlanRequest struct {
	Name            *string         `json:"name" validate:"omitempty,min=1,max=100"`
	Price           *string         `json:"price" validate:"omitempty"`
	Description     *string         `json:"description" validate:"omitempty"`
	IsTop           *bool           `json:"is_top"`
	IsActive        *bool           `json:"is_active"`
	SortOrder       *int32          `json:"sort_order"`
	MaxBots         *int32          `json:"max_bots" validate:"omitempty,gte=0"`
	MaxTokens       *int64          `json:"max_tokens" validate:"omitempty,gte=0"`
	CachedTokenRate *string         `json:"cached_token_rate" validate:"omitempty"`
	Features        json.RawMessage `json:"features" validate:"omitempty"`
}

// SubscriptionResponse mirrors SubscriptionSerializer's field list.
type SubscriptionResponse struct {
	ID                   string       `json:"id"`
	Plan                 string       `json:"plan"`
	PlanDetails          PlanResponse `json:"plan_details"`
	Status               string       `json:"status"`
	StatusDisplay        string       `json:"status_display"`
	StartedAt            time.Time    `json:"started_at"`
	ExpiresAt            *time.Time   `json:"expires_at"`
	TokensUsedInput      int64        `json:"tokens_used_input"`
	TokensUsedOutput     int64        `json:"tokens_used_output"`
	TokensRemaining      int64        `json:"tokens_remaining"`
	IsTokenLimitExceeded bool         `json:"is_token_limit_exceeded"`
	BotsCount            int32        `json:"bots_count"`
	CanCreateBot         bool         `json:"can_create_bot"`
	TokenResetAt         time.Time    `json:"token_reset_at"`
	CreatedAt            time.Time    `json:"created_at"`
}

func toSubscriptionResponse(sub *Subscription, p *Plan) SubscriptionResponse {
	return SubscriptionResponse{
		ID:                   sub.ID.String(),
		Plan:                 p.ID.String(),
		PlanDetails:          toPlanResponse(p),
		Status:               sub.Status,
		StatusDisplay:        statusDisplay(sub.Status),
		StartedAt:            sub.StartedAt,
		ExpiresAt:            sub.ExpiresAt,
		TokensUsedInput:      sub.TokensUsedInput,
		TokensUsedOutput:     sub.TokensUsedOutput,
		TokensRemaining:      sub.TokensRemaining(p),
		IsTokenLimitExceeded: sub.IsTokenLimitExceeded(p),
		BotsCount:            sub.BotsCount,
		CanCreateBot:         sub.CanCreateBot(p),
		TokenResetAt:         sub.TokenResetAt,
		CreatedAt:            sub.CreatedAt,
	}
}

// UpgradeRequest is the /subscription/upgrade/ payload.
type UpgradeRequest struct {
	PlanID string `json:"plan_id" validate:"required,uuid"`
	Months int32  `json:"months" validate:"omitempty,oneof=1 3 6 12"`
}

// UpgradeResponse mirrors the dict returned by SubscriptionViewSet.upgrade.
type UpgradeResponse struct {
	Message      string               `json:"message"`
	PreviousPlan string               `json:"previous_plan"`
	NewPlan      string               `json:"new_plan"`
	Data         SubscriptionResponse `json:"data"`
}

func toUpgradeResponse(sub *Subscription, newPlan *Plan, previousPlanName string) UpgradeResponse {
	return UpgradeResponse{
		Message:      "Tarif muvaffaqiyatli yangilandi",
		PreviousPlan: planDisplay(previousPlanName),
		NewPlan:      planDisplay(newPlan.Name),
		Data:         toSubscriptionResponse(sub, newPlan),
	}
}

// UsageResponse mirrors the nested dict returned by
// SubscriptionViewSet.usage() field-for-field.
type UsageResponse struct {
	Plan             string          `json:"plan"`
	Status           string          `json:"status"`
	PlanName         string          `json:"plan_name"`
	TokensUsedInput  int64           `json:"tokens_used_input"`
	TokensUsedOutput int64           `json:"tokens_used_output"`
	TokensLimit      int64           `json:"tokens_limit"`
	TokensRemaining  int64           `json:"tokens_remaining"`
	BotsCount        int32           `json:"bots_count"`
	MaxBots          int32           `json:"max_bots"`
	UsagePercentage  float64         `json:"usage_percentage"`
	AITokenUsage     aiTokenUsageDTO `json:"ai_token_usage"`
	Tokens           tokensDTO       `json:"tokens"`
	Bots             botsDTO         `json:"bots"`
	TokenResetAt     time.Time       `json:"token_reset_at"`
	ExpiresAt        *time.Time      `json:"expires_at"`
}

type aiTokenUsageDTO struct {
	Limit        int64            `json:"limit"`
	Used         int64            `json:"used"`
	Remaining    int64            `json:"remaining"`
	InputUsed    int64            `json:"input_used"`
	OutputUsed   int64            `json:"output_used"`
	Plan         aiTokenUsagePlan `json:"plan"`
	Status       string           `json:"status"`
	TokenResetAt time.Time        `json:"token_reset_at"`
}

type aiTokenUsagePlan struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Display string `json:"display"`
}

type tokensDTO struct {
	Total      tokensTotalDTO `json:"total"`
	InputUsed  int64          `json:"input_used"`
	OutputUsed int64          `json:"output_used"`
}

type tokensTotalDTO struct {
	Used            int64   `json:"used"`
	Limit           int64   `json:"limit"`
	Remaining       int64   `json:"remaining"`
	UsagePercentage float64 `json:"usage_percentage"`
	LimitExceeded   bool    `json:"limit_exceeded"`
}

type botsDTO struct {
	Count     int32 `json:"count"`
	Limit     int32 `json:"limit"`
	CanCreate bool  `json:"can_create"`
}

func toUsageResponse(sub *Subscription, p *Plan) UsageResponse {
	used := sub.TokensUsedInput + sub.TokensUsedOutput
	remaining := sub.TokensRemaining(p)
	usagePct := 0.0
	if p.MaxTokens > 0 {
		usagePct = math.Round(float64(used)/float64(p.MaxTokens)*100*100) / 100
	}
	return UsageResponse{
		Plan:             planDisplay(p.Name),
		Status:           sub.Status,
		PlanName:         planDisplay(p.Name),
		TokensUsedInput:  sub.TokensUsedInput,
		TokensUsedOutput: sub.TokensUsedOutput,
		TokensLimit:      p.MaxTokens,
		TokensRemaining:  remaining,
		BotsCount:        sub.BotsCount,
		MaxBots:          p.MaxBots,
		UsagePercentage:  usagePct,
		AITokenUsage: aiTokenUsageDTO{
			Limit:      p.MaxTokens,
			Used:       used,
			Remaining:  remaining,
			InputUsed:  sub.TokensUsedInput,
			OutputUsed: sub.TokensUsedOutput,
			Plan: aiTokenUsagePlan{
				ID:      p.ID.String(),
				Name:    p.Name,
				Display: planDisplay(p.Name),
			},
			Status:       sub.Status,
			TokenResetAt: sub.TokenResetAt,
		},
		Tokens: tokensDTO{
			Total: tokensTotalDTO{
				Used:            used,
				Limit:           p.MaxTokens,
				Remaining:       remaining,
				UsagePercentage: usagePct,
				LimitExceeded:   p.MaxTokens > 0 && used >= p.MaxTokens,
			},
			InputUsed:  sub.TokensUsedInput,
			OutputUsed: sub.TokensUsedOutput,
		},
		Bots: botsDTO{
			Count:     sub.BotsCount,
			Limit:     p.MaxBots,
			CanCreate: sub.CanCreateBot(p),
		},
		TokenResetAt: sub.TokenResetAt,
		ExpiresAt:    sub.ExpiresAt,
	}
}

// ReportUsageRequest is the /subscription/usage/report/ payload. user_id and
// project_id are UUID strings (not ints) because this Go port uses UUID
// primary keys for users and projects throughout, unlike the Django bigint
// pks the original endpoint expected.
type ReportUsageRequest struct {
	UserID          string  `json:"user_id" validate:"required,uuid"`
	ProjectID       *string `json:"project_id" validate:"omitempty,uuid"`
	InputTokens     int64   `json:"input_tokens" validate:"gte=0"`
	OutputTokens    int64   `json:"output_tokens" validate:"gte=0"`
	CacheReadTokens int64   `json:"cache_read_tokens" validate:"gte=0"`
}

// ReportUsageResponse mirrors report_usage()'s success payload.
type ReportUsageResponse struct {
	UserID            string  `json:"user_id"`
	ProjectID         *string `json:"project_id"`
	InputTokensAdded  int64   `json:"input_tokens_added"`
	OutputTokensAdded int64   `json:"output_tokens_added"`
}
