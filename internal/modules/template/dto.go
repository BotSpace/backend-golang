package template

import (
	"encoding/json"

	"fmt"
	"github.com/botspace/backend/internal/shared/media"
	"time"

	"github.com/google/uuid"
)

// ===================== categories & features =====================

type CategoryResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func toCategoryResponse(c *Category) CategoryResponse {
	return CategoryResponse{ID: c.ID.String(), Name: c.Name}
}

func toCategoryResponses(items []Category) []CategoryResponse {
	out := make([]CategoryResponse, len(items))
	for i := range items {
		out[i] = toCategoryResponse(&items[i])
	}
	return out
}

type FeatureResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

func toFeatureResponse(f *Feature) FeatureResponse {
	return FeatureResponse{ID: f.ID.String(), Name: f.Name, Value: f.Value}
}

func toFeatureResponses(items []Feature) []FeatureResponse {
	out := make([]FeatureResponse, len(items))
	for i := range items {
		out[i] = toFeatureResponse(&items[i])
	}
	return out
}

type CreateCategoryRequest struct {
	Name string `json:"name" validate:"required,min=1,max=255"`
}

type CreateFeatureRequest struct {
	Name  string `json:"name" validate:"required,min=1,max=255"`
	Value string `json:"value" validate:"omitempty"`
}

// ===================== templates =====================

// TemplateResponse is the public projection of a Template. flow/spec internals
// are intentionally never exposed here (mirrors views/internal.py's comment:
// public endpoints hide flow for payload size and IP protection).
type TemplateResponse struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Slug             string             `json:"slug"`
	Image            string             `json:"image"`
	Video            string             `json:"video,omitempty"`
	ShortDescription string             `json:"short_description"`
	Description      string             `json:"description"`
	Price            string             `json:"price"`
	OriginalPrice    string             `json:"original_price"`
	Discount         string             `json:"discount"`
	DiscountType     string             `json:"discount_type"`
	Views            int64              `json:"views"`
	LikesCount       int64              `json:"likes_count"`
	IsLiked          bool               `json:"is_liked"`
	IsOrdered        bool               `json:"is_ordered"`
	Categories       []CategoryResponse `json:"categories"`
	Features         []FeatureResponse  `json:"features"`
	DemoURL          string             `json:"demo_url,omitempty"`
	RequiredEnvs     json.RawMessage    `json:"required_envs"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

func toTemplateResponse(t *Template) TemplateResponse {
	return TemplateResponse{
		ID:               t.ID.String(),
		Name:             t.Name,
		Slug:             t.Slug,
		Image:            media.URL(t.Image),
		Video:            media.URL(t.Video),
		ShortDescription: t.ShortDescription,
		Description:      t.Description,
		Price:            t.Price(),
		OriginalPrice:    t.OriginalPrice,
		Discount:         t.Discount,
		DiscountType:     t.DiscountType,
		Views:            t.Views,
		LikesCount:       t.LikesCount,
		IsLiked:          t.IsLiked,
		IsOrdered:        t.IsOrdered,
		Categories:       toCategoryResponses(t.Categories),
		Features:         toFeatureResponses(t.Features),
		DemoURL:          t.DemoURL,
		RequiredEnvs:     rawOrEmptyArray(t.RequiredEnvs),
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
	}
}

type TemplateListResponse struct {
	Items  []TemplateResponse `json:"items"`
	Total  int64              `json:"total"`
	Limit  int32              `json:"limit"`
	Offset int32              `json:"offset"`
}

func toTemplateListResponse(items []Template, total int64, limit, offset int32) TemplateListResponse {
	out := make([]TemplateResponse, len(items))
	for i := range items {
		out[i] = toTemplateResponse(&items[i])
	}
	return TemplateListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

type LikeResponse struct {
	Liked bool `json:"liked"`
}

// ===================== admin-wide catalog =====================

// verifyStatus renders is_verify as the "verified"/"pending" string the admin
// panel's status badge expects, alongside the raw boolean.
func verifyStatus(isVerify bool) string {
	if isVerify {
		return "verified"
	}
	return "pending"
}

// AdminTemplateResponse is the admin-wide listing projection: unlike
// TemplateResponse it always includes the author (name + phone, resolved via
// a join — fixes the admin panel's "#undefined" author bug) and the real
// verification status/flag instead of being restricted to published items.
type AdminTemplateResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	Image         string    `json:"image,omitempty"`
	AuthorID      *string   `json:"author_id,omitempty"`
	AuthorName    string    `json:"author_name,omitempty"`
	AuthorPhone   string    `json:"author_phone,omitempty"`
	Status        string    `json:"status"` // "verified" | "pending"
	IsVerify      bool      `json:"is_verify"`
	Views         int64     `json:"views"`
	Price         string    `json:"price"`
	OriginalPrice string    `json:"original_price"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toAdminTemplateResponse(s *AdminTemplateSummary) AdminTemplateResponse {
	t := &s.Template
	return AdminTemplateResponse{
		ID:            t.ID.String(),
		Name:          t.Name,
		Slug:          t.Slug,
		Image:         media.URL(t.Image),
		AuthorID:      uuidPtrToStrPtr(t.AuthorID),
		AuthorName:    s.AuthorName,
		AuthorPhone:   s.AuthorPhone,
		Status:        verifyStatus(t.IsVerify),
		IsVerify:      t.IsVerify,
		Views:         t.Views,
		Price:         t.Price(),
		OriginalPrice: t.OriginalPrice,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
	}
}

// AdminTemplateListResponse is a paginated envelope for the admin listing.
type AdminTemplateListResponse struct {
	Items  []AdminTemplateResponse `json:"items"`
	Total  int64                   `json:"total"`
	Limit  int32                   `json:"limit"`
	Offset int32                   `json:"offset"`
}

func toAdminTemplateListResponse(items []AdminTemplateSummary, total int64, limit, offset int32) AdminTemplateListResponse {
	out := make([]AdminTemplateResponse, len(items))
	for i := range items {
		out[i] = toAdminTemplateResponse(&items[i])
	}
	return AdminTemplateListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// AdminTemplateDetailResponse is the full admin detail projection — every
// field the admin edit form needs, plus the author/status enrichment.
type AdminTemplateDetailResponse struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Slug             string             `json:"slug"`
	Image            string             `json:"image"`
	Video            string             `json:"video,omitempty"`
	AuthorID         *string            `json:"author_id,omitempty"`
	AuthorName       string             `json:"author_name,omitempty"`
	AuthorPhone      string             `json:"author_phone,omitempty"`
	ProjectID        *string            `json:"project_id,omitempty"`
	ShortDescription string             `json:"short_description"`
	Description      string             `json:"description"`
	Price            string             `json:"price"`
	OriginalPrice    string             `json:"original_price"`
	Discount         string             `json:"discount"`
	DiscountType     string             `json:"discount_type"`
	Status           string             `json:"status"`
	IsVerify         bool               `json:"is_verify"`
	Views            int64              `json:"views"`
	LikesCount       int64              `json:"likes_count"`
	Categories       []CategoryResponse `json:"categories"`
	Features         []FeatureResponse  `json:"features"`
	DemoURL          string             `json:"demo_url,omitempty"`
	RequiredEnvs     json.RawMessage    `json:"required_envs"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

func toAdminTemplateDetailResponse(s *AdminTemplateSummary) AdminTemplateDetailResponse {
	t := &s.Template
	return AdminTemplateDetailResponse{
		ID:               t.ID.String(),
		Name:             t.Name,
		Slug:             t.Slug,
		Image:            media.URL(t.Image),
		Video:            media.URL(t.Video),
		AuthorID:         uuidPtrToStrPtr(t.AuthorID),
		AuthorName:       s.AuthorName,
		AuthorPhone:      s.AuthorPhone,
		ProjectID:        uuidPtrToStrPtr(t.ProjectID),
		ShortDescription: t.ShortDescription,
		Description:      t.Description,
		Price:            t.Price(),
		OriginalPrice:    t.OriginalPrice,
		Discount:         t.Discount,
		DiscountType:     t.DiscountType,
		Status:           verifyStatus(t.IsVerify),
		IsVerify:         t.IsVerify,
		Views:            t.Views,
		LikesCount:       t.LikesCount,
		Categories:       toCategoryResponses(t.Categories),
		Features:         toFeatureResponses(t.Features),
		DemoURL:          t.DemoURL,
		RequiredEnvs:     rawOrEmptyArray(t.RequiredEnvs),
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
	}
}

// AdminTemplateActionResponse is returned by the admin create/update/verify/
// reject endpoints: the full TemplateResponse plus the status/is_verify
// fields the admin panel needs (TemplateResponse itself omits is_verify —
// the public catalog is always pre-filtered to is_verify=TRUE, so it never
// needed the field; admin actions do).
type AdminTemplateActionResponse struct {
	TemplateResponse
	Status   string `json:"status"`
	IsVerify bool   `json:"is_verify"`
}

func toAdminTemplateActionResponse(t *Template) AdminTemplateActionResponse {
	return AdminTemplateActionResponse{
		TemplateResponse: toTemplateResponse(t),
		Status:           verifyStatus(t.IsVerify),
		IsVerify:         t.IsVerify,
	}
}

// CreateAdminTemplateRequest is the admin direct-create payload (bypasses
// the author submission/review flow).
type CreateAdminTemplateRequest struct {
	AuthorID         string          `json:"author_id" validate:"omitempty,uuid"`
	ProjectID        string          `json:"project_id" validate:"omitempty,uuid"`
	Name             string          `json:"name" validate:"required,min=1,max=255"`
	Slug             string          `json:"slug" validate:"omitempty,max=255"`
	Image            string          `json:"image" validate:"omitempty"`
	Video            string          `json:"video" validate:"omitempty"`
	ShortDescription string          `json:"short_description" validate:"omitempty"`
	Description      string          `json:"description" validate:"omitempty"`
	OriginalPrice    string          `json:"original_price" validate:"omitempty"`
	Discount         string          `json:"discount" validate:"omitempty"`
	DiscountType     string          `json:"discount_type" validate:"omitempty,oneof=percent amount"`
	DemoURL          string          `json:"demo_url" validate:"omitempty"`
	IsVerify         bool            `json:"is_verify"`
	CategoryIDs      []string        `json:"category_ids" validate:"omitempty,dive,uuid"`
	FeatureIDs       []string        `json:"feature_ids" validate:"omitempty,dive,uuid"`
	RequiredEnvs     json.RawMessage `json:"required_envs" validate:"omitempty"`
}

// UpdateAdminTemplateRequest carries optional fields; omitted fields are
// left unchanged. Slug/author/project are immutable after creation.
type UpdateAdminTemplateRequest struct {
	Name             *string         `json:"name" validate:"omitempty,min=1,max=255"`
	Image            *string         `json:"image" validate:"omitempty"`
	Video            *string         `json:"video" validate:"omitempty"`
	ShortDescription *string         `json:"short_description" validate:"omitempty"`
	Description      *string         `json:"description" validate:"omitempty"`
	OriginalPrice    *string         `json:"original_price" validate:"omitempty"`
	Discount         *string         `json:"discount" validate:"omitempty"`
	DiscountType     *string         `json:"discount_type" validate:"omitempty,oneof=percent amount"`
	DemoURL          *string         `json:"demo_url" validate:"omitempty"`
	IsVerify         *bool           `json:"is_verify"`
	CategoryIDs      []string        `json:"category_ids" validate:"omitempty,dive,uuid"`
	FeatureIDs       []string        `json:"feature_ids" validate:"omitempty,dive,uuid"`
	RequiredEnvs     json.RawMessage `json:"required_envs" validate:"omitempty"`
}

type UpdateAdminTemplateFlowRequest struct {
	Flow json.RawMessage `json:"flow" validate:"required"`
}

// ===================== submissions =====================

type SubmissionResponse struct {
	ID               string             `json:"id"`
	ProjectID        string             `json:"project_id"`
	Name             string             `json:"name"`
	ShortDescription string             `json:"short_description"`
	Description      string             `json:"description"`
	Image            string             `json:"image,omitempty"`
	Video            string             `json:"video,omitempty"`
	DemoURL          string             `json:"demo_url,omitempty"`
	OriginalPrice    string             `json:"original_price"`
	Categories       []CategoryResponse `json:"categories"`
	Features         []FeatureResponse  `json:"features"`
	Status           string             `json:"status"`
	RejectionReason  string             `json:"rejection_reason,omitempty"`
	TemplateID       *string            `json:"template_id,omitempty"`
	TemplateSlug     string             `json:"-"`
	ReviewedBy       *string            `json:"reviewed_by,omitempty"`
	ReviewedAt       *time.Time         `json:"reviewed_at,omitempty"`
	RequiredEnvs     json.RawMessage    `json:"required_envs"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

func toSubmissionResponse(s *Submission) SubmissionResponse {
	return SubmissionResponse{
		ID:               s.ID.String(),
		ProjectID:        s.ProjectID.String(),
		Name:             s.Name,
		ShortDescription: s.ShortDescription,
		Description:      s.Description,
		Image:            media.URL(s.Image),
		Video:            media.URL(s.Video),
		DemoURL:          s.DemoURL,
		OriginalPrice:    s.OriginalPrice,
		Categories:       toCategoryResponses(s.Categories),
		Features:         toFeatureResponses(s.Features),
		Status:           s.Status,
		RejectionReason:  s.RejectionReason,
		TemplateID:       uuidPtrToStrPtr(s.TemplateID),
		ReviewedBy:       uuidPtrToStrPtr(s.ReviewedBy),
		ReviewedAt:       s.ReviewedAt,
		RequiredEnvs:     rawOrEmptyArray(s.RequiredEnvs),
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
	}
}

type SubmissionListResponse struct {
	Items  []SubmissionResponse `json:"items"`
	Total  int64                `json:"total"`
	Limit  int32                `json:"limit"`
	Offset int32                `json:"offset"`
}

func toSubmissionListResponse(items []Submission, total int64, limit, offset int32) SubmissionListResponse {
	out := make([]SubmissionResponse, len(items))
	for i := range items {
		out[i] = toSubmissionResponse(&items[i])
	}
	return SubmissionListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

type CreateSubmissionRequest struct {
	ProjectID        string          `json:"project_id" validate:"required,uuid"`
	Name             string          `json:"name" validate:"required,min=1,max=255"`
	ShortDescription string          `json:"short_description" validate:"required"`
	Description      string          `json:"description" validate:"required"`
	Image            string          `json:"image" validate:"omitempty"`
	Video            string          `json:"video" validate:"omitempty"`
	DemoURL          string          `json:"demo_url" validate:"omitempty"`
	OriginalPrice    string          `json:"original_price" validate:"required"`
	CategoryIDs      []string        `json:"category_ids" validate:"omitempty,dive,uuid"`
	FeatureIDs       []string        `json:"feature_ids" validate:"omitempty,dive,uuid"`
	RequiredEnvs     json.RawMessage `json:"required_envs" validate:"omitempty"`
}

type UpdateSubmissionRequest struct {
	Name             *string         `json:"name" validate:"omitempty,min=1,max=255"`
	ShortDescription *string         `json:"short_description" validate:"omitempty"`
	Description      *string         `json:"description" validate:"omitempty"`
	Image            *string         `json:"image" validate:"omitempty"`
	Video            *string         `json:"video" validate:"omitempty"`
	DemoURL          *string         `json:"demo_url" validate:"omitempty"`
	OriginalPrice    *string         `json:"original_price" validate:"omitempty"`
	CategoryIDs      []string        `json:"category_ids" validate:"omitempty,dive,uuid"`
	FeatureIDs       []string        `json:"feature_ids" validate:"omitempty,dive,uuid"`
	RequiredEnvs     json.RawMessage `json:"required_envs" validate:"omitempty"`
}

type ApproveSubmissionRequest struct {
	Discount     string `json:"discount" validate:"omitempty"`
	DiscountType string `json:"discount_type" validate:"omitempty,oneof=percent amount"`
}

type RejectRequest struct {
	RejectionReason string `json:"rejection_reason" validate:"required"`
}

// ===================== orders =====================

type OrderResponse struct {
	ID             string    `json:"id"`
	TemplateID     *string   `json:"template_id,omitempty"`
	Amount         string    `json:"amount"`
	Status         string    `json:"status"`
	PaymentStatus  bool      `json:"payment_status"`
	CommissionPaid bool      `json:"commission_paid"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toOrderResponse(o *Order) OrderResponse {
	return OrderResponse{
		ID:             o.ID.String(),
		TemplateID:     uuidPtrToStrPtr(o.TemplateID),
		Amount:         o.Amount,
		Status:         o.Status,
		PaymentStatus:  o.PaymentStatus,
		CommissionPaid: o.CommissionPaid,
		CreatedAt:      o.CreatedAt,
		UpdatedAt:      o.UpdatedAt,
	}
}

type OrderListResponse struct {
	Items  []OrderResponse `json:"items"`
	Total  int64           `json:"total"`
	Limit  int32           `json:"limit"`
	Offset int32           `json:"offset"`
}

func toOrderListResponse(items []Order, total int64, limit, offset int32) OrderListResponse {
	out := make([]OrderResponse, len(items))
	for i := range items {
		out[i] = toOrderResponse(&items[i])
	}
	return OrderListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

type CreateOrderRequest struct {
	TemplateID string `json:"template_id" validate:"required,uuid"`
}

// ===================== withdrawals =====================

type WithdrawalResponse struct {
	ID              string     `json:"id"`
	User            string     `json:"user,omitempty"`
	UserPhone       string     `json:"user_phone,omitempty"`
	Amount          string     `json:"amount"`
	CardNumber      string     `json:"card_number"`
	CardOwner       string     `json:"card_owner"`
	Status          string     `json:"status"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
	ReviewedBy      *string    `json:"reviewed_by,omitempty"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toWithdrawalResponse(w *Withdrawal) WithdrawalResponse {
	return WithdrawalResponse{
		ID:              w.ID.String(),
		User:            w.UserID.String(),
		UserPhone:       w.UserPhone,
		Amount:          w.Amount,
		CardNumber:      w.CardNumber,
		CardOwner:       w.CardOwner,
		Status:          w.Status,
		RejectionReason: w.RejectionReason,
		ReviewedBy:      uuidPtrToStrPtr(w.ReviewedBy),
		ReviewedAt:      w.ReviewedAt,
		CreatedAt:       w.CreatedAt,
		UpdatedAt:       w.UpdatedAt,
	}
}

type WithdrawalListResponse struct {
	Items  []WithdrawalResponse `json:"items"`
	Total  int64                `json:"total"`
	Limit  int32                `json:"limit"`
	Offset int32                `json:"offset"`
}

func toWithdrawalListResponse(items []Withdrawal, total int64, limit, offset int32) WithdrawalListResponse {
	out := make([]WithdrawalResponse, len(items))
	for i := range items {
		out[i] = toWithdrawalResponse(&items[i])
	}
	return WithdrawalListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

type CreateWithdrawalRequest struct {
	Amount     string `json:"amount" validate:"required"`
	CardNumber string `json:"card_number" validate:"required"`
	CardOwner  string `json:"card_owner" validate:"required"`
}

// ===================== shared helpers =====================

func uuidPtrToStrPtr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

// parseUUIDs converts request-supplied id strings to uuid.UUID, failing fast
// on the first invalid one (used for category_ids/feature_ids).
func parseUUIDs(ss []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, len(ss))
	for i, s := range ss {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid id %q", s)
		}
		out[i] = id
	}
	return out, nil
}
