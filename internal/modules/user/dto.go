package user

import "time"

// UserResponse is the public projection of a user. Password hash and internal
// flags are never exposed.
type UserResponse struct {
	ID              string     `json:"id"`
	Phone           string     `json:"phone"`
	FirstName       string     `json:"first_name"`
	LastName        string     `json:"last_name"`
	Username        string     `json:"username,omitempty"`
	Email           string     `json:"email,omitempty"`
	Role            string     `json:"role"`
	Balance         int64      `json:"balance"`
	TemplateBalance string     `json:"template_balance"`
	IsActive        bool       `json:"is_active"`
	IsKYCVerified   bool       `json:"is_kyc_verified"`
	ValidatedAt     *time.Time `json:"validated_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toResponse(u *User) UserResponse {
	return UserResponse{
		ID:              u.ID.String(),
		Phone:           u.Phone,
		FirstName:       u.FirstName,
		LastName:        u.LastName,
		Username:        u.Username,
		Email:           u.Email,
		Role:            u.Role,
		Balance:         u.Balance,
		TemplateBalance: u.TemplateBalance,
		IsActive:        u.IsActive,
		IsKYCVerified:   u.KYCVerifiedAt != nil,
		ValidatedAt:     u.ValidatedAt,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}
}

// BalanceResponse mirrors the legacy /user/balance/ payload.
type BalanceResponse struct {
	Balance         int64  `json:"balance"`
	TemplateBalance string `json:"template_balance"`
}

// UpdateMeRequest updates editable profile fields. All optional; omitted
// fields are left unchanged.
type UpdateMeRequest struct {
	FirstName *string `json:"first_name" validate:"omitempty,min=1,max=150"`
	LastName  *string `json:"last_name" validate:"omitempty,min=1,max=150"`
	Username  *string `json:"username" validate:"omitempty,max=150"`
	Email     *string `json:"email" validate:"omitempty,email"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" validate:"required"`
	NewPassword string `json:"new_password" validate:"required,min=8,max=72"`
}

type SetRoleRequest struct {
	Role string `json:"role" validate:"required,oneof=user admin superuser"`
}

// BalanceAdjustRequest is the admin "balansni tuzatish" payload. Delta is
// signed: positive credits, negative debits (guarded against overdraft by
// AddBalance).
type BalanceAdjustRequest struct {
	Delta  int64  `json:"delta" validate:"required"`
	Reason string `json:"reason" validate:"omitempty,max=500"`
}

// ListResponse is a paginated envelope for a user listing.
type ListResponse struct {
	Items  []UserResponse `json:"items"`
	Total  int64          `json:"total"`
	Limit  int32          `json:"limit"`
	Offset int32          `json:"offset"`
}

func toListResponse(users []User, total int64, limit, offset int32) ListResponse {
	items := make([]UserResponse, len(users))
	for i := range users {
		items[i] = toResponse(&users[i])
	}
	return ListResponse{Items: items, Total: total, Limit: limit, Offset: offset}
}

// ===================== admin detail enrichment =====================

type SubscriptionResponse struct {
	Plan             string     `json:"plan"`
	PlanName         string     `json:"plan_name"`
	Status           string     `json:"status"`
	StartedAt        *time.Time `json:"started_at"`
	ExpiresAt        *time.Time `json:"expires_at"`
	TokenResetAt     *time.Time `json:"token_reset_at"`
	BotsCount        int32      `json:"bots_count"`
	TokensUsedInput  int64      `json:"tokens_used_input"`
	TokensUsedOutput int64      `json:"tokens_used_output"`
	TokensUsedTotal  int64      `json:"tokens_used_total"`
	TokenLimit       int64      `json:"token_limit"`
	MaxBots          int32      `json:"max_bots"`
}

type ProjectBriefResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Username   string    `json:"username,omitempty"`
	IsTemplate bool      `json:"is_template"`
	CreatedAt  time.Time `json:"created_at"`
}

// AdminDetailResponse is the enriched admin-only user detail projection.
// purchased_templates/authored_templates ship as empty arrays with an
// accurate count alongside — the underlying per-row listing was judged out
// of scope for this pass (see the porting report).
type AdminDetailResponse struct {
	UserResponse
	Subscription       *SubscriptionResponse  `json:"subscription"`
	Projects           []ProjectBriefResponse `json:"projects"`
	ProjectsCount      int64                  `json:"projects_count"`
	PurchasedTemplates []struct{}             `json:"purchased_templates"`
	PurchasedCount     int64                  `json:"purchased_count"`
	AuthoredTemplates  []struct{}             `json:"authored_templates"`
	AuthoredCount      int64                  `json:"authored_count"`
}

func toAdminDetailResponse(d *AdminDetail) AdminDetailResponse {
	var sub *SubscriptionResponse
	if d.Subscription != nil {
		s := d.Subscription
		sub = &SubscriptionResponse{
			Plan: s.Plan, PlanName: s.PlanName, Status: s.Status,
			StartedAt: &s.StartedAt, ExpiresAt: s.ExpiresAt, TokenResetAt: &s.TokenResetAt,
			BotsCount: s.BotsCount, TokensUsedInput: s.TokensUsedInput, TokensUsedOutput: s.TokensUsedOutput,
			TokensUsedTotal: s.TokensUsedInput + s.TokensUsedOutput, TokenLimit: s.TokenLimit, MaxBots: s.MaxBots,
		}
	}
	projects := make([]ProjectBriefResponse, len(d.Projects))
	for i, p := range d.Projects {
		projects[i] = ProjectBriefResponse{
			ID: p.ID.String(), Name: p.Name, Username: p.Username,
			IsTemplate: p.IsTemplate, CreatedAt: p.CreatedAt,
		}
	}
	return AdminDetailResponse{
		UserResponse:       toResponse(&d.User),
		Subscription:       sub,
		Projects:           projects,
		ProjectsCount:      d.ProjectsCount,
		PurchasedTemplates: []struct{}{},
		PurchasedCount:     d.PurchasedCount,
		AuthoredTemplates:  []struct{}{},
		AuthoredCount:      d.AuthoredCount,
	}
}
