package user

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Roles recognised by the RBAC middleware. Mirrors accounts.RoleChoice.
const (
	RoleUser      = "user"
	RoleAdmin     = "admin"
	RoleSuperuser = "superuser"
)

// User is the core entity. Persistence (repository.go) and transport (dto.go)
// concerns live elsewhere. Phone is the login identifier; a user is pending
// until ValidatedAt is set by OTP confirmation.
type User struct {
	ID              uuid.UUID
	Phone           string
	FirstName       string
	LastName        string
	Username        string
	Email           string
	PasswordHash    string
	Role            string
	Balance         int64
	TemplateBalance string // NUMERIC(20,2) rendered as a decimal string
	IsActive        bool
	ValidatedAt     *time.Time
	KYCVerifiedAt   *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// TokensUsedTotal is the user's AI-agent token consumption (subscription
	// tokens_used_input + tokens_used_output). Only the admin list query
	// (Repository.List) populates it; every other read leaves it 0.
	TokensUsedTotal int64
}

// IsValidated reports whether the phone has passed OTP confirmation.
func (u *User) IsValidated() bool { return u.ValidatedAt != nil }

// ListFilter narrows and paginates a user listing (admin). All fields are
// optional — a zero value means "don't filter on this". Search matches phone,
// name or email (case-insensitive). Ordering is a whitelisted sort key (see
// repository.orderClause); an unknown value falls back to newest-first.
type ListFilter struct {
	Search       string
	Role         string
	IsActive     *bool
	IsStaff      *bool
	Plan         string
	JoinedAfter  *time.Time
	JoinedBefore *time.Time
	BalanceMin   *int64
	BalanceMax   *int64
	Ordering     string
	Limit        int32
	Offset       int32
}

// ProfileUpdate carries optional profile fields; nil pointers are left as-is.
type ProfileUpdate struct {
	FirstName *string
	LastName  *string
	Username  *string
	Email     *string
}

// Subscription is a compact plan-usage summary for the admin user detail
// page (cross-module read of plan/user_subscription — this module owns no
// plan data of its own).
type Subscription struct {
	Plan             string
	PlanName         string
	Status           string
	StartedAt        time.Time
	ExpiresAt        *time.Time
	TokenResetAt     time.Time
	BotsCount        int32
	TokensUsedInput  int64
	TokensUsedOutput int64
	TokenLimit       int64
	MaxBots          int32
}

// ProjectBrief is a compact project row for the admin user detail page's
// project list (cross-module read of projects).
type ProjectBrief struct {
	ID         uuid.UUID
	Name       string
	Username   string
	IsTemplate bool
	CreatedAt  time.Time
}

// Repository is the persistence port. The usecase depends only on this
// interface, keeping storage swappable (postgres, in-memory tests).
type Repository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetByPhone(ctx context.Context, phone string) (*User, error)
	// GetByEmail is used by the Google sign-in flow (auth module) to find an
	// existing account by its verified Google email. Only called with a
	// non-empty email — see GetUserByEmail in queries/users.sql for the
	// no-unique-constraint caveat.
	GetByEmail(ctx context.Context, email string) (*User, error)
	MarkValidated(ctx context.Context, id uuid.UUID) (*User, error)
	UpdateProfile(ctx context.Context, id uuid.UUID, p ProfileUpdate) (*User, error)
	UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error
	UpdateRole(ctx context.Context, id uuid.UUID, role string) (*User, error)
	AddBalance(ctx context.Context, id uuid.UUID, delta int64) (*User, error)
	// SetActive toggles the admin ban/unban flag (is_active = FALSE blocks login).
	SetActive(ctx context.Context, id uuid.UUID, active bool) (*User, error)
	// RecordBalanceHistory appends an audit row for an admin-initiated balance
	// adjustment, independent of AddBalance which is what actually moves the
	// money — the two are called together by the usecase.
	RecordBalanceHistory(ctx context.Context, userID uuid.UUID, delta int64, comment string) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]User, error)
	Count(ctx context.Context, f ListFilter) (int64, error)

	// GetSubscription returns nil (no error) when the user has no
	// subscription row yet — a brand-new account before its default plan is
	// provisioned.
	GetSubscription(ctx context.Context, userID uuid.UUID) (*Subscription, error)
	ListProjectsBrief(ctx context.Context, userID uuid.UUID) ([]ProjectBrief, error)
	CountProjectsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountPurchasedTemplates(ctx context.Context, userID uuid.UUID) (int64, error)
	CountAuthoredTemplates(ctx context.Context, userID uuid.UUID) (int64, error)
}
