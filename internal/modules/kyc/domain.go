// Package kyc ports the Django accounts KYC flow (UserVerificationModel): a
// user submits passport/selfie/residence documents, an admin reviews them,
// and approval unlocks withdrawals by setting users.kyc_verified_at.
package kyc

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Verification statuses. Mirrors accounts.choices.VerificationStatusChoice.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// Verification is a single KYC submission. File fields hold paths relative to
// APP_MEDIA_DIR, saved by the handler before the usecase is invoked.
// UserPhone/ReviewedByPhone are populated only by admin-facing queries that
// join the users table; they are empty on the self-service paths.
type Verification struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	FullName           string
	PassportImage      string
	SelfieImage        string
	ResidenceReference string
	Status             string
	RejectionReason    string
	ReviewedBy         *uuid.UUID
	ReviewedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time

	UserPhone       string
	ReviewedByPhone string
}

// SubmitInput creates a new verification request for a user. Files are
// already saved to disk; only their relative paths travel through this type.
type SubmitInput struct {
	UserID             uuid.UUID
	FullName           string
	PassportImage      string
	SelfieImage        string
	ResidenceReference string
}

// ListFilter narrows and paginates the admin listing.
type ListFilter struct {
	Status string
	Limit  int32
	Offset int32
}

// Repository is the persistence port.
type Repository interface {
	Create(ctx context.Context, in SubmitInput) (*Verification, error)
	GetLatestForUser(ctx context.Context, userID uuid.UUID) (*Verification, error)
	HasPending(ctx context.Context, userID uuid.UUID) (bool, error)
	IsUserVerified(ctx context.Context, userID uuid.UUID) (bool, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Verification, error)
	List(ctx context.Context, f ListFilter) ([]Verification, error)
	Count(ctx context.Context, status string) (int64, error)
	// Approve transitions a pending request to approved and, in the same
	// transaction, sets the owning user's kyc_verified_at.
	Approve(ctx context.Context, id, reviewerID uuid.UUID) (*Verification, error)
	Reject(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Verification, error)
}
