package kyc

import (
	"context"
	"errors"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// Repository sentinels, translated to HTTP errors by the usecase.
var ErrNotPending = errors.New("verification is not pending")

// Usecase exposes KYC business operations: self-service submission/status for
// the user, and review (list/approve/reject) for admins.
type Usecase interface {
	Submit(ctx context.Context, in SubmitInput) (*Verification, error)
	Status(ctx context.Context, userID uuid.UUID) (bool, *Verification, error)
	Get(ctx context.Context, id uuid.UUID) (*Verification, error)
	List(ctx context.Context, f ListFilter) ([]Verification, int64, error)
	Approve(ctx context.Context, id, reviewerID uuid.UUID) (*Verification, error)
	Reject(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Verification, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

// Submit creates a new pending verification request. It rejects a user who
// is already verified, or who already has a request under review — mirrors
// the two guard checks in Django's VerificationView.create.
func (u *usecase) Submit(ctx context.Context, in SubmitInput) (*Verification, error) {
	verified, err := u.repo.IsUserVerified(ctx, in.UserID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if verified {
		return nil, httpx.Conflict("kyc.already_verified", "siz allaqachon tasdiqlangansiz")
	}

	pending, err := u.repo.HasPending(ctx, in.UserID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if pending {
		return nil, httpx.Conflict("kyc.pending_exists", "sizda tekshiruvdagi so'rov mavjud")
	}

	v, err := u.repo.Create(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return v, nil
}

// Status reports whether the user is verified plus their latest submission
// (nil if they have never submitted one).
func (u *usecase) Status(ctx context.Context, userID uuid.UUID) (bool, *Verification, error) {
	verified, err := u.repo.IsUserVerified(ctx, userID)
	if err != nil {
		return false, nil, httpx.Internal(err)
	}
	latest, err := u.repo.GetLatestForUser(ctx, userID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return verified, nil, nil
		}
		return false, nil, httpx.Internal(err)
	}
	return verified, latest, nil
}

func (u *usecase) Get(ctx context.Context, id uuid.UUID) (*Verification, error) {
	return u.get(u.repo.GetByID(ctx, id))
}

func (u *usecase) List(ctx context.Context, f ListFilter) ([]Verification, int64, error) {
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
	total, err := u.repo.Count(ctx, f.Status)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// Approve transitions a pending request to approved and unlocks withdrawals
// for the submitter (repo.Approve sets users.kyc_verified_at atomically).
func (u *usecase) Approve(ctx context.Context, id, reviewerID uuid.UUID) (*Verification, error) {
	v, err := u.repo.Approve(ctx, id, reviewerID)
	if err != nil {
		if errors.Is(err, ErrNotPending) {
			return nil, httpx.Conflict("kyc.not_pending", "so'rov allaqachon ko'rib chiqilgan")
		}
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("kyc.not_found", "verification not found")
		}
		return nil, httpx.Internal(err)
	}
	return v, nil
}

// Reject transitions a pending request to rejected with a reason.
func (u *usecase) Reject(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Verification, error) {
	v, err := u.repo.Reject(ctx, id, reviewerID, reason)
	if err != nil {
		if errors.Is(err, ErrNotPending) {
			return nil, httpx.Conflict("kyc.not_pending", "so'rov allaqachon ko'rib chiqilgan")
		}
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("kyc.not_found", "verification not found")
		}
		return nil, httpx.Internal(err)
	}
	return v, nil
}

// get maps a repository (verification, err) into HTTP-aware errors.
func (u *usecase) get(v *Verification, err error) (*Verification, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("kyc.not_found", "verification not found")
		}
		return nil, httpx.Internal(err)
	}
	return v, nil
}
