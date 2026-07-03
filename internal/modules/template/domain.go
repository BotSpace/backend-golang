// Package template ports Django's core.apps.template marketplace: a public
// template catalog, author submission review, buy/sell orders with commission
// split, and author withdrawal requests against users.template_balance.
package template

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Discount type (Django DiscountTypeEnum).
const (
	DiscountTypePercent = "percent"
	DiscountTypeAmount  = "amount"
)

// Submission review status (Django TemplateSubmissionStatusEnum).
const (
	SubmissionStatusPending  = "pending"
	SubmissionStatusApproved = "approved"
	SubmissionStatusRejected = "rejected"
)

// Order status (Django TemplateOrderStatusEnum).
const (
	OrderStatusPending = "pending"
	OrderStatusConfirm = "confirm"
	OrderStatusCancel  = "cancel"
)

// Withdrawal review status (Django WithdrawalStatusEnum).
const (
	WithdrawalStatusPending  = "pending"
	WithdrawalStatusApproved = "approved"
	WithdrawalStatusRejected = "rejected"
)

// AuthorCommissionPercent is the author's cut of a sale; the platform keeps the
// remaining 20%. Mirrors signals/order.py::OrderSignal (Decimal('0.80')).
const AuthorCommissionPercent = "0.80"

// Category and Feature are shared tags attached to templates (and submissions,
// before a template exists).
type Category struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Feature struct {
	ID        uuid.UUID
	Name      string
	Value     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Template is a published marketplace item. Categories/Features/LikesCount are
// populated by the usecase in a batch pass, not by the base repository row.
type Template struct {
	ID               uuid.UUID
	AuthorID         *uuid.UUID
	ProjectID        *uuid.UUID
	Name             string
	Slug             string
	Image            string
	Video            string
	Spec             json.RawMessage
	ShortDescription string
	Description      string
	OriginalPrice    string // NUMERIC(20,2) as decimal string
	Discount         string
	DiscountType     string
	Views            int64
	Version          int32
	IsVerify         bool
	DemoURL          string
	RequiredEnvs     json.RawMessage
	Categories       []Category
	Features         []Feature
	LikesCount       int64
	IsLiked          bool
	IsOrdered        bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Price applies the discount to OriginalPrice, mirroring TemplateModel.price.
func (t *Template) Price() string {
	return computePrice(t.OriginalPrice, t.Discount, t.DiscountType)
}

// computePrice mirrors the Django property: no discount -> original price;
// "amount" discount subtracts a flat sum; "percent" (default) subtracts a
// percentage of the original price. math/big.Rat keeps this exact (no float
// rounding) since money here is always a 2-decimal string.
func computePrice(originalPrice, discount, discountType string) string {
	orig, ok := new(big.Rat).SetString(originalPrice)
	if !ok {
		orig = new(big.Rat)
	}
	disc, ok := new(big.Rat).SetString(discount)
	if !ok || disc.Sign() == 0 {
		return orig.FloatString(2)
	}
	var price *big.Rat
	if discountType == DiscountTypeAmount {
		price = new(big.Rat).Sub(orig, disc)
	} else {
		hundred := big.NewRat(100, 1)
		price = new(big.Rat).Sub(orig, new(big.Rat).Quo(new(big.Rat).Mul(orig, disc), hundred))
	}
	if price.Sign() < 0 {
		price = new(big.Rat)
	}
	return price.FloatString(2)
}

// parseRat parses a decimal string into a big.Rat, returning nil if it isn't
// a valid number (mirrors big.Rat.SetString's ok flag).
func parseRat(s string) (*big.Rat, bool) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, false
	}
	return r, true
}

// commissionShare returns the author's cut (AuthorCommissionPercent) of a
// sale amount, as an exact 2-decimal string. Mirrors
// `instance.amount * Decimal('0.80')` in signals/order.py::OrderSignal.
func commissionShare(amount string) (string, error) {
	amt, ok := parseRat(amount)
	if !ok {
		return "", fmt.Errorf("invalid amount %q", amount)
	}
	pct, _ := parseRat(AuthorCommissionPercent)
	share := new(big.Rat).Mul(amt, pct)
	return share.FloatString(2), nil
}

// decimalToInt64 rounds a decimal money string to the nearest whole unit
// (half away from zero) for storage in an integer wallet column
// (users.balance). Exact big.Rat arithmetic - no float rounding.
func decimalToInt64(s string) (int64, error) {
	r, ok := parseRat(s)
	if !ok {
		return 0, fmt.Errorf("invalid decimal amount %q", s)
	}
	num := new(big.Int).Set(r.Num())
	den := r.Denom()
	half := new(big.Int).Rsh(den, 1)
	if num.Sign() >= 0 {
		num.Add(num, half)
	} else {
		num.Sub(num, half)
	}
	q := new(big.Int).Quo(num, den)
	if !q.IsInt64() {
		return 0, fmt.Errorf("amount %q out of range", s)
	}
	return q.Int64(), nil
}

// slugify turns a name into a URL-safe slug: lowercase ASCII letters/digits
// separated by single hyphens (mirrors django.utils.text.slugify closely
// enough for template names, which are plain human titles).
func slugify(name string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) && r <= unicode.MaxASCII, unicode.IsDigit(r) && r <= unicode.MaxASCII:
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// discountTypeOrDefault normalizes an empty/invalid discount type to percent.
func discountTypeOrDefault(t string) string {
	if t == DiscountTypeAmount {
		return DiscountTypeAmount
	}
	return DiscountTypePercent
}

// searchKeywords splits a free-text search query into lowercase keywords for
// the AI-friendly smart search (mirrors filters/template.py::filter_search).
// Returns nil (SQL NULL, meaning "no filter") for a blank query.
func searchKeywords(search string) []string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(search)))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// TemplateListFilter narrows and paginates the public catalog.
type TemplateListFilter struct {
	Search     string
	CategoryID *uuid.UUID
	FeatureID  *uuid.UUID
	MinPrice   string
	MaxPrice   string
	ViewerID   *uuid.UUID // populates IsLiked/IsOrdered when set
	Limit      int32
	Offset     int32
}

// ===================== admin-wide (cross-owner) =====================

// AdminTemplateListFilter narrows and paginates the admin-wide template
// listing (unlike TemplateListFilter, it is not restricted to is_verify=TRUE
// and can filter on verification status directly).
type AdminTemplateListFilter struct {
	Search   string
	IsVerify *bool // nil = no filter (both pending and verified)
	Limit    int32
	Offset   int32
}

// AdminTemplateSummary is a Template enriched with the author's display
// fields (joined from users), populated only by the admin listing/detail
// queries — this is the fix for the admin panel's "#undefined" author bug,
// which was caused by no admin endpoint existing at all, not by missing data.
type AdminTemplateSummary struct {
	Template
	AuthorName  string
	AuthorPhone string
}

// CreateAdminTemplateInput is the admin direct-create payload (bypasses the
// author submission/review flow entirely).
type CreateAdminTemplateInput struct {
	AuthorID         *uuid.UUID
	ProjectID        *uuid.UUID
	Name             string
	Slug             string // empty -> auto-generated from Name
	Image            string
	Video            string
	ShortDescription string
	Description      string
	OriginalPrice    string
	Discount         string
	DiscountType     string
	DemoURL          string
	IsVerify         bool
	RequiredEnvs     json.RawMessage
	CategoryIDs      []uuid.UUID
	FeatureIDs       []uuid.UUID
}

// UpdateAdminTemplateInput carries optional fields; nil pointers are left
// unchanged. Slug/author/project are immutable after creation (same
// convention as blog.UpdateInput).
type UpdateAdminTemplateInput struct {
	Name             *string
	Image            *string
	Video            *string
	ShortDescription *string
	Description      *string
	OriginalPrice    *string
	Discount         *string
	DiscountType     *string
	DemoURL          *string
	IsVerify         *bool
	RequiredEnvs     json.RawMessage
	CategoryIDs      []uuid.UUID // nil = leave as-is, non-nil (incl. empty) replaces
	FeatureIDs       []uuid.UUID
}

// Submission is a user's request to publish one of their projects as a
// template; an admin approves (creating a Template) or rejects it.
type Submission struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	ProjectID        uuid.UUID
	Name             string
	ShortDescription string
	Description      string
	Image            string
	Video            string
	DemoURL          string
	OriginalPrice    string
	Categories       []Category
	Features         []Feature
	Status           string
	RejectionReason  string
	TemplateID       *uuid.UUID
	ReviewedBy       *uuid.UUID
	ReviewedAt       *time.Time
	RequiredEnvs     json.RawMessage
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CreateSubmissionInput is the owner-scoped create payload.
type CreateSubmissionInput struct {
	UserID           uuid.UUID
	ProjectID        uuid.UUID
	Name             string
	ShortDescription string
	Description      string
	Image            string
	Video            string
	DemoURL          string
	OriginalPrice    string
	CategoryIDs      []uuid.UUID
	FeatureIDs       []uuid.UUID
	RequiredEnvs     json.RawMessage
}

// UpdateSubmissionInput carries optional fields; nil pointers are unchanged.
// Only allowed while the submission is still pending.
type UpdateSubmissionInput struct {
	Name             *string
	ShortDescription *string
	Description      *string
	Image            *string
	Video            *string
	DemoURL          *string
	OriginalPrice    *string
	RequiredEnvs     json.RawMessage
	CategoryIDs      []uuid.UUID // nil = leave as-is, non-nil (incl. empty) replaces
	FeatureIDs       []uuid.UUID
}

// Order is a buy request for a Template. Amount is snapshotted at creation
// time (Template.Price() at that moment); PaymentStatus flips true once paid
// (immediately for free templates); CommissionPaid guards the author payout
// from being applied twice.
type Order struct {
	ID             uuid.UUID
	UserID         *uuid.UUID
	TemplateID     *uuid.UUID
	Amount         string
	Status         string
	PaymentStatus  bool
	CommissionPaid bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Withdrawal is an author's request to cash out users.template_balance.
type Withdrawal struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	Amount          string
	CardNumber      string
	CardOwner       string
	Status          string
	RejectionReason string
	ReviewedBy      *uuid.UUID
	ReviewedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// UserPhone is populated only by the admin listing query (joins users);
	// empty on owner-scoped paths.
	UserPhone string
}

// Repository is the persistence port for the whole template domain. It stays
// one interface (like project/user) so the usecase depends on a single seam;
// repository.go groups the sqlc-backed implementation by sub-domain.
type Repository interface {
	// Categories & features (public read, admin create).
	ListCategories(ctx context.Context) ([]Category, error)
	CreateCategory(ctx context.Context, name string) (*Category, error)
	ListFeatures(ctx context.Context) ([]Feature, error)
	CreateFeature(ctx context.Context, name, value string) (*Feature, error)

	// Catalog.
	GetBySlug(ctx context.Context, slug string) (*Template, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Template, error)
	// GetBySlugAny is GetBySlug without the is_verify=TRUE restriction — used
	// only by the internal RAG feed (see internal_feed.go).
	GetBySlugAny(ctx context.Context, slug string) (*Template, error)
	// GetProjectFlow resolves a template's linked project's raw flow JSON,
	// unscoped by owner (internal/system-token callers only).
	GetProjectFlow(ctx context.Context, projectID uuid.UUID) (json.RawMessage, error)
	IncrementViews(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, f TemplateListFilter) ([]Template, error)
	Count(ctx context.Context, f TemplateListFilter) (int64, error)
	AttachExtras(ctx context.Context, items []Template, viewerID *uuid.UUID) error

	// Admin-wide catalog management (no is_verify=TRUE restriction).
	ListAdmin(ctx context.Context, f AdminTemplateListFilter) ([]AdminTemplateSummary, error)
	CountAdmin(ctx context.Context, f AdminTemplateListFilter) (int64, error)
	GetAdminByID(ctx context.Context, id uuid.UUID) (*AdminTemplateSummary, error)
	CreateAdmin(ctx context.Context, in CreateAdminTemplateInput) (*Template, error)
	UpdateAdmin(ctx context.Context, id uuid.UUID, in UpdateAdminTemplateInput) (*Template, error)
	DeleteAdmin(ctx context.Context, id uuid.UUID) error
	SetVerify(ctx context.Context, id uuid.UUID, verify bool) (*Template, error)

	// Likes.
	ToggleLike(ctx context.Context, templateID, userID uuid.UUID) (liked bool, err error)

	// Submissions.
	// ProjectOwnedByUser scopes a submission's source project to its owner
	// without importing the project module (SQL-only join on projects).
	ProjectOwnedByUser(ctx context.Context, projectID, ownerID uuid.UUID) (bool, error)
	CreateSubmission(ctx context.Context, in CreateSubmissionInput) (*Submission, error)
	GetSubmissionForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Submission, error)
	GetSubmission(ctx context.Context, id uuid.UUID) (*Submission, error)
	ListSubmissionsForOwner(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Submission, error)
	CountSubmissionsForOwner(ctx context.Context, ownerID uuid.UUID) (int64, error)
	ListSubmissionsAdmin(ctx context.Context, status string, limit, offset int32) ([]Submission, error)
	CountSubmissionsAdmin(ctx context.Context, status string) (int64, error)
	UpdateSubmission(ctx context.Context, id, ownerID uuid.UUID, in UpdateSubmissionInput) (*Submission, error)
	DeleteSubmission(ctx context.Context, id, ownerID uuid.UUID) error
	AttachSubmissionExtras(ctx context.Context, items []Submission) error
	// ApproveSubmission creates the published Template from the submission and
	// marks it approved, all inside one transaction.
	ApproveSubmission(ctx context.Context, id, reviewerID uuid.UUID, discount, discountType string) (*Submission, *Template, error)
	RejectSubmission(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Submission, error)

	// Orders / purchase.
	GetOrCreateOrder(ctx context.Context, userID, templateID uuid.UUID) (*Order, error)
	GetOrderForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Order, error)
	ListOrdersForOwner(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Order, error)
	CountOrdersForOwner(ctx context.Context, ownerID uuid.UUID) (int64, error)
	// Pay charges the buyer, credits the author's commission, marks the order
	// paid and grants ownership - all inside one transaction.
	Pay(ctx context.Context, id, buyerID uuid.UUID) (*Order, error)

	// Withdrawals.
	GetAuthorBalance(ctx context.Context, userID uuid.UUID) (string, error)
	CreateWithdrawal(ctx context.Context, userID uuid.UUID, amount, cardNumber, cardOwner string) (*Withdrawal, error)
	GetWithdrawalForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Withdrawal, error)
	GetWithdrawal(ctx context.Context, id uuid.UUID) (*Withdrawal, error)
	ListWithdrawalsForOwner(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Withdrawal, error)
	CountWithdrawalsForOwner(ctx context.Context, ownerID uuid.UUID) (int64, error)
	ListWithdrawalsAdmin(ctx context.Context, status string, limit, offset int32) ([]Withdrawal, error)
	CountWithdrawalsAdmin(ctx context.Context, status string) (int64, error)
	DeleteWithdrawal(ctx context.Context, id, ownerID uuid.UUID) error
	// ApproveWithdrawal deducts template_balance and marks approved; if the
	// balance is insufficient at approval time it rejects instead (matches
	// admin/withdrawal.py::approve_withdrawals).
	ApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*Withdrawal, error)
	RejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Withdrawal, error)
}
