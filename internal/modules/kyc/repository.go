package kyc

import (
	"context"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. Approve needs a transaction (update the
// verification row + the owning user's kyc_verified_at atomically), so it
// keeps the pool alongside the generated Queries.
type pgRepo struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{pool: pool, queries: sqlcdb.New(pool)}
}

func (r *pgRepo) Create(ctx context.Context, in SubmitInput) (*Verification, error) {
	row, err := r.queries.CreateVerification(ctx, sqlcdb.CreateVerificationParams{
		UserID:             in.UserID,
		FullName:           in.FullName,
		PassportImage:      in.PassportImage,
		SelfieImage:        in.SelfieImage,
		ResidenceReference: in.ResidenceReference,
	})
	if err != nil {
		return nil, fmt.Errorf("create verification: %w", err)
	}
	v := fromSQLC(row)
	return &v, nil
}

func (r *pgRepo) GetLatestForUser(ctx context.Context, userID uuid.UUID) (*Verification, error) {
	row, err := r.queries.GetLatestVerificationForUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest verification: %w", err)
	}
	v := fromSQLC(row)
	return &v, nil
}

func (r *pgRepo) HasPending(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := r.queries.HasPendingVerificationForUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("check pending verification: %w", err)
	}
	return ok, nil
}

func (r *pgRepo) IsUserVerified(ctx context.Context, userID uuid.UUID) (bool, error) {
	ok, err := r.queries.IsUserKYCVerified(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, database.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("check user kyc status: %w", err)
	}
	return ok, nil
}

func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*Verification, error) {
	row, err := r.queries.GetVerificationWithReviewer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get verification: %w", err)
	}
	v := fromReviewerRow(row)
	return &v, nil
}

func (r *pgRepo) List(ctx context.Context, f ListFilter) ([]Verification, error) {
	rows, err := r.queries.ListVerificationsForAdmin(ctx, sqlcdb.ListVerificationsForAdminParams{
		Status: strPtr(f.Status),
		Limit:  f.Limit,
		Offset: f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list verifications: %w", err)
	}
	out := make([]Verification, len(rows))
	for i, row := range rows {
		out[i] = fromListRow(row)
	}
	return out, nil
}

func (r *pgRepo) Count(ctx context.Context, status string) (int64, error) {
	n, err := r.queries.CountVerificationsForAdmin(ctx, strPtr(status))
	if err != nil {
		return 0, fmt.Errorf("count verifications: %w", err)
	}
	return n, nil
}

// Approve transitions the request to approved and sets the submitter's
// kyc_verified_at in the same transaction. If the row is not currently
// pending, no rows match the guarded UPDATE and ErrNotPending is returned.
func (r *pgRepo) Approve(ctx context.Context, id, reviewerID uuid.UUID) (*Verification, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := r.queries.WithTx(tx)
	row, err := q.ApproveVerification(ctx, sqlcdb.ApproveVerificationParams{ID: id, ReviewedBy: &reviewerID})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := q.GetVerificationWithReviewer(ctx, id); errors.Is(getErr, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		return nil, ErrNotPending
	}
	if err != nil {
		return nil, fmt.Errorf("approve verification: %w", err)
	}
	if err := q.SetUserKYCVerifiedAt(ctx, row.UserID); err != nil {
		return nil, fmt.Errorf("set user kyc verified at: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit approve tx: %w", err)
	}
	v := fromSQLC(row)
	return &v, nil
}

func (r *pgRepo) Reject(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Verification, error) {
	row, err := r.queries.RejectVerification(ctx, sqlcdb.RejectVerificationParams{
		ID:              id,
		RejectionReason: strPtr(reason),
		ReviewedBy:      &reviewerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := r.queries.GetVerificationWithReviewer(ctx, id); errors.Is(getErr, pgx.ErrNoRows) {
			return nil, database.ErrNotFound
		}
		return nil, ErrNotPending
	}
	if err != nil {
		return nil, fmt.Errorf("reject verification: %w", err)
	}
	v := fromSQLC(row)
	return &v, nil
}

func fromSQLC(r sqlcdb.UserVerification) Verification {
	return Verification{
		ID:                 r.ID,
		UserID:             r.UserID,
		FullName:           r.FullName,
		PassportImage:      r.PassportImage,
		SelfieImage:        r.SelfieImage,
		ResidenceReference: r.ResidenceReference,
		Status:             r.Status,
		RejectionReason:    deref(r.RejectionReason),
		ReviewedBy:         r.ReviewedBy,
		ReviewedAt:         r.ReviewedAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

func fromReviewerRow(r sqlcdb.GetVerificationWithReviewerRow) Verification {
	return Verification{
		ID:                 r.ID,
		UserID:             r.UserID,
		FullName:           r.FullName,
		PassportImage:      r.PassportImage,
		SelfieImage:        r.SelfieImage,
		ResidenceReference: r.ResidenceReference,
		Status:             r.Status,
		RejectionReason:    deref(r.RejectionReason),
		ReviewedBy:         r.ReviewedBy,
		ReviewedAt:         r.ReviewedAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		UserPhone:          deref(r.UserPhone),
		ReviewedByPhone:    deref(r.ReviewedByPhone),
	}
}

func fromListRow(r sqlcdb.ListVerificationsForAdminRow) Verification {
	return Verification{
		ID:                 r.ID,
		UserID:             r.UserID,
		FullName:           r.FullName,
		PassportImage:      r.PassportImage,
		SelfieImage:        r.SelfieImage,
		ResidenceReference: r.ResidenceReference,
		Status:             r.Status,
		RejectionReason:    deref(r.RejectionReason),
		ReviewedBy:         r.ReviewedBy,
		ReviewedAt:         r.ReviewedAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		UserPhone:          deref(r.UserPhone),
		ReviewedByPhone:    deref(r.ReviewedByPhone),
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
