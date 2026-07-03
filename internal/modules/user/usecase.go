package user

import (
	"context"
	"errors"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Repository sentinels, translated to HTTP errors by the usecase.
var (
	ErrPhoneTaken        = errors.New("phone already registered")
	ErrInsufficientFunds = errors.New("insufficient balance")
)

// Usecase exposes business operations for the user module. Other modules
// (e.g. auth) consume it via this interface, not the concrete type.
type Usecase interface {
	Register(ctx context.Context, in RegisterInput) (*User, error)
	Authenticate(ctx context.Context, phone, password string) (*User, error)
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetByPhone(ctx context.Context, phone string) (*User, error)
	MarkValidated(ctx context.Context, id uuid.UUID) (*User, error)
	UpdateProfile(ctx context.Context, id uuid.UUID, p ProfileUpdate) (*User, error)
	ChangePassword(ctx context.Context, id uuid.UUID, oldPassword, newPassword string) error
	ResetPassword(ctx context.Context, phone, newPassword string) error
	SetRole(ctx context.Context, id uuid.UUID, role string) (*User, error)
	AddBalance(ctx context.Context, id uuid.UUID, delta int64) (*User, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, f ListFilter) ([]User, int64, error)

	// Ban/Unban toggle is_active (admin). AdminAdjustBalance applies a signed
	// delta and records an audit history row in one call (admin panel's
	// "balansni tuzatish" action).
	Ban(ctx context.Context, id uuid.UUID) (*User, error)
	Unban(ctx context.Context, id uuid.UUID) (*User, error)
	AdminAdjustBalance(ctx context.Context, id uuid.UUID, delta int64, reason string) (*User, error)

	// GetAdminDetail enriches the base user with subscription/project/
	// template-marketplace summaries for the admin user detail page.
	GetAdminDetail(ctx context.Context, id uuid.UUID) (*AdminDetail, error)
}

// AdminDetail is the enriched admin-only projection of a user: the base
// User plus subscription, project count/list, and template marketplace
// participation counts (cross-module reads — this module owns none of that
// data, only queries it read-only, mirroring the administration module's
// dashboard-stats pattern).
type AdminDetail struct {
	User
	Subscription   *Subscription
	Projects       []ProjectBrief
	ProjectsCount  int64
	PurchasedCount int64
	AuthoredCount  int64
}

// RegisterInput creates (or refreshes, if still pending) a user by phone.
type RegisterInput struct {
	Phone     string
	FirstName string
	LastName  string
	Password  string
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

// Register creates a pending user (validated_at is NULL until OTP confirm).
// Re-registering a phone that already confirmed is rejected; a still-pending
// phone is allowed to re-submit its details (upsert in SQL).
func (u *usecase) Register(ctx context.Context, in RegisterInput) (*User, error) {
	if existing, err := u.repo.GetByPhone(ctx, in.Phone); err == nil && existing.IsValidated() {
		return nil, httpx.Conflict("user.phone_taken", "phone number already registered")
	} else if err != nil && !errors.Is(err, database.ErrNotFound) {
		return nil, httpx.Internal(err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	newUser := &User{
		ID:           uuid.New(),
		Phone:        in.Phone,
		FirstName:    in.FirstName,
		LastName:     in.LastName,
		PasswordHash: string(hash),
		Role:         RoleUser,
	}
	if err := u.repo.Create(ctx, newUser); err != nil {
		if errors.Is(err, ErrPhoneTaken) {
			return nil, httpx.Conflict("user.phone_taken", "phone number already registered")
		}
		return nil, httpx.Internal(err)
	}
	return newUser, nil
}

// Authenticate verifies phone + password for the JWT login flow. It requires a
// confirmed (validated) account.
func (u *usecase) Authenticate(ctx context.Context, phone, password string) (*User, error) {
	found, err := u.repo.GetByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.Unauthorized("auth.invalid_credentials", "invalid phone or password")
		}
		return nil, httpx.Internal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(found.PasswordHash), []byte(password)) != nil {
		return nil, httpx.Unauthorized("auth.invalid_credentials", "invalid phone or password")
	}
	if !found.IsValidated() {
		return nil, httpx.Forbidden("auth.not_validated", "phone number not confirmed")
	}
	return found, nil
}

func (u *usecase) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
	return u.get(u.repo.GetByID(ctx, id))
}

func (u *usecase) GetByPhone(ctx context.Context, phone string) (*User, error) {
	return u.get(u.repo.GetByPhone(ctx, phone))
}

func (u *usecase) MarkValidated(ctx context.Context, id uuid.UUID) (*User, error) {
	return u.get(u.repo.MarkValidated(ctx, id))
}

func (u *usecase) UpdateProfile(ctx context.Context, id uuid.UUID, p ProfileUpdate) (*User, error) {
	return u.get(u.repo.UpdateProfile(ctx, id, p))
}

// ChangePassword verifies the current password before setting the new one.
func (u *usecase) ChangePassword(ctx context.Context, id uuid.UUID, oldPassword, newPassword string) error {
	usr, err := u.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("user.not_found", "user not found")
		}
		return httpx.Internal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(usr.PasswordHash), []byte(oldPassword)) != nil {
		return httpx.BadRequest("auth.invalid_password", "current password is incorrect")
	}
	return u.setPassword(ctx, id, newPassword)
}

// ResetPassword sets a new password after an OTP-verified reset (no old
// password needed — the caller already proved phone ownership).
func (u *usecase) ResetPassword(ctx context.Context, phone, newPassword string) error {
	usr, err := u.repo.GetByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("user.not_found", "user not found")
		}
		return httpx.Internal(err)
	}
	return u.setPassword(ctx, usr.ID, newPassword)
}

func (u *usecase) setPassword(ctx context.Context, id uuid.UUID, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return httpx.Internal(err)
	}
	if err := u.repo.UpdatePassword(ctx, id, string(hash)); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("user.not_found", "user not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) SetRole(ctx context.Context, id uuid.UUID, role string) (*User, error) {
	switch role {
	case RoleUser, RoleAdmin, RoleSuperuser:
	default:
		return nil, httpx.BadRequest("user.invalid_role", "role must be user, admin, or superuser")
	}
	return u.get(u.repo.UpdateRole(ctx, id, role))
}

// AddBalance applies a signed delta; a negative delta that would overdraw is
// rejected with 409.
func (u *usecase) AddBalance(ctx context.Context, id uuid.UUID, delta int64) (*User, error) {
	usr, err := u.repo.AddBalance(ctx, id, delta)
	if err != nil {
		if errors.Is(err, ErrInsufficientFunds) {
			return nil, httpx.Conflict("user.insufficient_funds", "insufficient balance")
		}
		return nil, httpx.Internal(err)
	}
	return usr, nil
}

// List returns a page of users plus the total count matching the filter.
func (u *usecase) List(ctx context.Context, f ListFilter) ([]User, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	users, err := u.repo.List(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.Count(ctx, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return users, total, nil
}

// Ban sets is_active = FALSE, blocking the user's login (Authenticate checks
// IsValidated, not IsActive, today — this only affects future login-flow
// wiring that reads IsActive; the flag is still the correct source of truth
// for the admin panel's ban toggle).
func (u *usecase) Ban(ctx context.Context, id uuid.UUID) (*User, error) {
	return u.get(u.repo.SetActive(ctx, id, false))
}

func (u *usecase) Unban(ctx context.Context, id uuid.UUID) (*User, error) {
	return u.get(u.repo.SetActive(ctx, id, true))
}

// AdminAdjustBalance applies a signed delta to a user's balance and records
// an audit history row for it. The balance change (AddBalance, guarded
// against overdraft) always happens first and is the source of truth; if the
// history write fails afterwards the balance change is not rolled back —
// same trade-off internal/modules/template.Pay makes for its commission
// credit — a 500 here still tells the admin something needs a look.
func (u *usecase) AdminAdjustBalance(ctx context.Context, id uuid.UUID, delta int64, reason string) (*User, error) {
	usr, err := u.AddBalance(ctx, id, delta)
	if err != nil {
		return nil, err
	}
	if err := u.repo.RecordBalanceHistory(ctx, id, delta, reason); err != nil {
		return nil, httpx.Internal(err)
	}
	return usr, nil
}

func (u *usecase) Delete(ctx context.Context, id uuid.UUID) error {
	if err := u.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("user.not_found", "user not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

// GetAdminDetail loads the base user then fans out to subscription/project/
// template summaries. A missing subscription (brand-new account) is not an
// error — Subscription stays nil.
func (u *usecase) GetAdminDetail(ctx context.Context, id uuid.UUID) (*AdminDetail, error) {
	usr, err := u.get(u.repo.GetByID(ctx, id))
	if err != nil {
		return nil, err
	}
	sub, err := u.repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	projects, err := u.repo.ListProjectsBrief(ctx, id)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	projectsCount, err := u.repo.CountProjectsForUser(ctx, id)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	purchasedCount, err := u.repo.CountPurchasedTemplates(ctx, id)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	authoredCount, err := u.repo.CountAuthoredTemplates(ctx, id)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return &AdminDetail{
		User:           *usr,
		Subscription:   sub,
		Projects:       projects,
		ProjectsCount:  projectsCount,
		PurchasedCount: purchasedCount,
		AuthoredCount:  authoredCount,
	}, nil
}

// get maps a repository (user, err) into HTTP-aware errors.
func (u *usecase) get(usr *User, err error) (*User, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("user.not_found", "user not found")
		}
		return nil, httpx.Internal(err)
	}
	return usr, nil
}
