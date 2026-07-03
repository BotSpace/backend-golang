package kyc

import (
	"context"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
// verified tracks users.kyc_verified_at as a plain bool set (Approve is
// expected to flip it, mirroring the cross-table update in repository.go).
type fakeRepo struct {
	byID     map[uuid.UUID]*Verification
	verified map[uuid.UUID]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byID: map[uuid.UUID]*Verification{}, verified: map[uuid.UUID]bool{}}
}

func (r *fakeRepo) Create(_ context.Context, in SubmitInput) (*Verification, error) {
	v := &Verification{
		ID:                 uuid.New(),
		UserID:             in.UserID,
		FullName:           in.FullName,
		PassportImage:      in.PassportImage,
		SelfieImage:        in.SelfieImage,
		ResidenceReference: in.ResidenceReference,
		Status:             StatusPending,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	r.byID[v.ID] = v
	cp := *v
	return &cp, nil
}

func (r *fakeRepo) GetLatestForUser(_ context.Context, userID uuid.UUID) (*Verification, error) {
	var latest *Verification
	for _, v := range r.byID {
		if v.UserID == userID && (latest == nil || v.CreatedAt.After(latest.CreatedAt)) {
			latest = v
		}
	}
	if latest == nil {
		return nil, database.ErrNotFound
	}
	cp := *latest
	return &cp, nil
}

func (r *fakeRepo) HasPending(_ context.Context, userID uuid.UUID) (bool, error) {
	for _, v := range r.byID {
		if v.UserID == userID && v.Status == StatusPending {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) IsUserVerified(_ context.Context, userID uuid.UUID) (bool, error) {
	return r.verified[userID], nil
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Verification, error) {
	v, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (r *fakeRepo) List(_ context.Context, _ ListFilter) ([]Verification, error) { return nil, nil }
func (r *fakeRepo) Count(_ context.Context, _ string) (int64, error)             { return 0, nil }

func (r *fakeRepo) Approve(_ context.Context, id, reviewerID uuid.UUID) (*Verification, error) {
	v, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if v.Status != StatusPending {
		return nil, ErrNotPending
	}
	now := time.Now()
	v.Status = StatusApproved
	v.ReviewedBy = &reviewerID
	v.ReviewedAt = &now
	r.verified[v.UserID] = true
	cp := *v
	return &cp, nil
}

func (r *fakeRepo) Reject(_ context.Context, id, reviewerID uuid.UUID, reason string) (*Verification, error) {
	v, ok := r.byID[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if v.Status != StatusPending {
		return nil, ErrNotPending
	}
	now := time.Now()
	v.Status = StatusRejected
	v.RejectionReason = reason
	v.ReviewedBy = &reviewerID
	v.ReviewedAt = &now
	cp := *v
	return &cp, nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

func TestSubmitThenPendingRequestBlocksResubmit(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	ctx := context.Background()
	uid := uuid.New()
	in := SubmitInput{
		UserID:             uid,
		FullName:           "Test User",
		PassportImage:      "kyc/passport/a.jpg",
		SelfieImage:        "kyc/selfie/b.jpg",
		ResidenceReference: "kyc/residence/c.pdf",
	}

	v, err := uc.Submit(ctx, in)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if v.Status != StatusPending {
		t.Fatalf("expected pending status, got %q", v.Status)
	}

	// A second submission while the first is still pending -> 409.
	if _, err := uc.Submit(ctx, in); status(err) != 409 {
		t.Fatalf("expected 409 for pending resubmit, got %v", err)
	}

	verified, latest, err := uc.Status(ctx, uid)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if verified {
		t.Fatal("user must not be verified before approval")
	}
	if latest == nil || latest.ID != v.ID {
		t.Fatal("status must report the submitted request as latest")
	}
}

func TestApproveUnlocksUserAndBlocksFurtherSubmit(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	uid := uuid.New()
	admin := uuid.New()

	v, err := uc.Submit(ctx, SubmitInput{
		UserID:             uid,
		FullName:           "Test User",
		PassportImage:      "kyc/passport/a.jpg",
		SelfieImage:        "kyc/selfie/b.jpg",
		ResidenceReference: "kyc/residence/c.pdf",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	approved, err := uc.Approve(ctx, v.ID, admin)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != StatusApproved {
		t.Fatalf("expected approved status, got %q", approved.Status)
	}

	verified, _, err := uc.Status(ctx, uid)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !verified {
		t.Fatal("approve must set the user as kyc verified")
	}

	// Already verified -> further submissions rejected with 409.
	if _, err := uc.Submit(ctx, SubmitInput{UserID: uid, FullName: "x"}); status(err) != 409 {
		t.Fatalf("expected 409 for already-verified resubmit, got %v", err)
	}

	// Re-approving the same (now approved) request -> 409, not a silent no-op.
	if _, err := uc.Approve(ctx, v.ID, admin); status(err) != 409 {
		t.Fatalf("expected 409 for re-approve, got %v", err)
	}
}

func TestRejectSetsReasonAndAllowsResubmit(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	uid := uuid.New()
	admin := uuid.New()

	v, err := uc.Submit(ctx, SubmitInput{
		UserID:             uid,
		FullName:           "Test User",
		PassportImage:      "kyc/passport/a.jpg",
		SelfieImage:        "kyc/selfie/b.jpg",
		ResidenceReference: "kyc/residence/c.pdf",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	rejected, err := uc.Reject(ctx, v.ID, admin, "hujjat sifati past")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != StatusRejected || rejected.RejectionReason != "hujjat sifati past" {
		t.Fatalf("unexpected rejected verification: %+v", rejected)
	}

	// No pending request remains -> resubmission is allowed.
	if _, err := uc.Submit(ctx, SubmitInput{
		UserID:             uid,
		FullName:           "Test User",
		PassportImage:      "kyc/passport/a2.jpg",
		SelfieImage:        "kyc/selfie/b2.jpg",
		ResidenceReference: "kyc/residence/c2.pdf",
	}); err != nil {
		t.Fatalf("resubmit after rejection: %v", err)
	}
}
