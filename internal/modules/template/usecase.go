package template

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Repository sentinels, translated to HTTP errors by the usecase (mirrors
// internal/modules/user's ErrPhoneTaken/ErrInsufficientFunds pattern).
var (
	ErrSubmissionNotPending = errors.New("submission is not pending")
	ErrWithdrawalNotPending = errors.New("withdrawal is not pending")
	ErrTemplateUnavailable  = errors.New("order has no linked template")
	ErrInsufficientBalance  = errors.New("insufficient wallet balance")
)

// Usecase exposes every template-marketplace operation. It is split across
// usecase.go (catalog/categories/features/likes), usecase_submission.go and
// usecase_order.go (which also covers withdrawals) for readability; all three
// share the *usecase receiver defined here.
type Usecase interface {
	// Catalog (public).
	ListCategories(ctx context.Context) ([]Category, error)
	CreateCategory(ctx context.Context, name string) (*Category, error)
	ListFeatures(ctx context.Context) ([]Feature, error)
	CreateFeature(ctx context.Context, name, value string) (*Feature, error)
	GetBySlug(ctx context.Context, slug string, viewerID *uuid.UUID) (*Template, error)
	List(ctx context.Context, f TemplateListFilter) ([]Template, int64, error)
	ToggleLike(ctx context.Context, slug string, userID uuid.UUID) (bool, error)

	// Internal RAG feed (X-Internal-Token gated — see internal_feed.go).
	ListInternalFeed(ctx context.Context, onlyVerified bool) ([]FeedItem, error)
	GetInternalFeedItem(ctx context.Context, identifier string) (*FeedItem, error)

	// Admin-wide catalog management.
	AdminList(ctx context.Context, f AdminTemplateListFilter) ([]AdminTemplateSummary, int64, error)
	AdminGet(ctx context.Context, id uuid.UUID) (*AdminTemplateSummary, error)
	AdminCreate(ctx context.Context, in CreateAdminTemplateInput) (*Template, error)
	AdminUpdate(ctx context.Context, id uuid.UUID, in UpdateAdminTemplateInput) (*Template, error)
	AdminUpdateFlow(ctx context.Context, id uuid.UUID, flow json.RawMessage) error
	AdminDelete(ctx context.Context, id uuid.UUID) error
	AdminSetVerify(ctx context.Context, id uuid.UUID, verify bool) (*Template, error)

	// Submissions.
	CreateSubmission(ctx context.Context, in CreateSubmissionInput) (*Submission, error)
	GetSubmission(ctx context.Context, id, ownerID uuid.UUID) (*Submission, error)
	ListSubmissions(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Submission, int64, error)
	UpdateSubmission(ctx context.Context, id, ownerID uuid.UUID, in UpdateSubmissionInput) (*Submission, error)
	DeleteSubmission(ctx context.Context, id, ownerID uuid.UUID) error
	ListSubmissionsAdmin(ctx context.Context, status string, limit, offset int32) ([]Submission, int64, error)
	ApproveSubmission(ctx context.Context, id, reviewerID uuid.UUID, discount, discountType string) (*Submission, *Template, error)
	RejectSubmission(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Submission, error)

	// Orders / purchase.
	CreateOrder(ctx context.Context, userID, templateID uuid.UUID) (*Order, error)
	// ResolveForProjectCreate is the project module's entry point for
	// "create a project from a template" (project.Usecase.CreateFromTemplate
	// depends on this via project.TemplateResolver) — see its doc comment in
	// usecase_order.go for the full purchase-check/flow-fallback semantics.
	// Returns the flow and the raw `collections` spec array (nil when the
	// template has none) so the project module can seed CMS collections + data.
	ResolveForProjectCreate(ctx context.Context, idOrSlug string, buyerID uuid.UUID) (flow, collections json.RawMessage, err error)
	GetOrder(ctx context.Context, id, ownerID uuid.UUID) (*Order, error)
	ListOrders(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Order, int64, error)
	PayOrder(ctx context.Context, id, buyerID uuid.UUID) (*Order, error)

	// Withdrawals.
	CreateWithdrawal(ctx context.Context, userID uuid.UUID, amount, cardNumber, cardOwner string) (*Withdrawal, error)
	GetWithdrawal(ctx context.Context, id, ownerID uuid.UUID) (*Withdrawal, error)
	ListWithdrawals(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Withdrawal, int64, error)
	DeleteWithdrawal(ctx context.Context, id, ownerID uuid.UUID) error
	ListWithdrawalsAdmin(ctx context.Context, status string, limit, offset int32) ([]Withdrawal, int64, error)
	ApproveWithdrawal(ctx context.Context, id, reviewerID uuid.UUID) (*Withdrawal, error)
	RejectWithdrawal(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Withdrawal, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

func (u *usecase) ListCategories(ctx context.Context) ([]Category, error) {
	items, err := u.repo.ListCategories(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return items, nil
}

func (u *usecase) CreateCategory(ctx context.Context, name string) (*Category, error) {
	if name == "" {
		return nil, httpx.BadRequest("template.invalid_category", "name is required")
	}
	c, err := u.repo.CreateCategory(ctx, name)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return c, nil
}

func (u *usecase) ListFeatures(ctx context.Context) ([]Feature, error) {
	items, err := u.repo.ListFeatures(ctx)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return items, nil
}

func (u *usecase) CreateFeature(ctx context.Context, name, value string) (*Feature, error) {
	if name == "" {
		return nil, httpx.BadRequest("template.invalid_feature", "name is required")
	}
	f, err := u.repo.CreateFeature(ctx, name, value)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return f, nil
}

// GetBySlug returns a published template, bumps its view counter and
// attaches categories/features/likes (+ per-viewer flags when authenticated).
func (u *usecase) GetBySlug(ctx context.Context, slug string, viewerID *uuid.UUID) (*Template, error) {
	t, err := u.repo.GetBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.not_found", "template not found")
		}
		return nil, httpx.Internal(err)
	}
	if err := u.repo.IncrementViews(ctx, t.ID); err != nil {
		return nil, httpx.Internal(err)
	}
	t.Views++
	// AttachExtras mutates in place through the slice — attach onto a
	// one-element slice wrapping *t, then copy the result back into t. Passing
	// []Template{*t} directly and discarding it left IsOrdered (and IsLiked)
	// on the returned *t always false, even for a viewer who owns the
	// template: the detail endpoint (GET /template/list/:slug) kept showing
	// "Bepul olish" instead of the "Sizda bor" state the list already got
	// right via the same AttachExtras call in List below.
	items := []Template{*t}
	if err := u.repo.AttachExtras(ctx, items, viewerID); err != nil {
		return nil, httpx.Internal(err)
	}
	*t = items[0]
	return t, nil
}

// List returns a page of the public catalog plus the total match count.
func (u *usecase) List(ctx context.Context, f TemplateListFilter) ([]Template, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.List(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	if err := u.repo.AttachExtras(ctx, items, f.ViewerID); err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.Count(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// ToggleLike flips the authenticated user's like on a template (by slug).
func (u *usecase) ToggleLike(ctx context.Context, slug string, userID uuid.UUID) (bool, error) {
	t, err := u.repo.GetBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return false, httpx.NotFound("template.not_found", "template not found")
		}
		return false, httpx.Internal(err)
	}
	liked, err := u.repo.ToggleLike(ctx, t.ID, userID)
	if err != nil {
		return false, httpx.Internal(err)
	}
	return liked, nil
}
