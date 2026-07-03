package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// requiredEnvKeyRe/reservedEnvKeys mirror validators/template.py exactly:
// keys are SCREAMING_SNAKE_CASE and RELEASE_BOT_TOKEN is always injected by
// the release pipeline, never declared by the author.
var requiredEnvKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

var reservedEnvKeys = map[string]bool{"RELEASE_BOT_TOKEN": true}

type requiredEnvItem struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
}

// validateRequiredEnvs parses+validates the required_envs array, returning a
// normalized JSON array (never nil/null - "[]" for empty input).
func validateRequiredEnvs(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("[]"), nil
	}
	var items []requiredEnvItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("required_envs must be a JSON array of {key, description}")
	}
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		if item.Key == "" {
			return nil, fmt.Errorf("required_envs[%d]: key is required", i)
		}
		if !requiredEnvKeyRe.MatchString(item.Key) {
			return nil, fmt.Errorf("required_envs[%d]: key %q has invalid format (^[A-Z][A-Z0-9_]{0,63}$)", i, item.Key)
		}
		if reservedEnvKeys[item.Key] {
			return nil, fmt.Errorf("required_envs[%d]: key %q is reserved", i, item.Key)
		}
		if seen[item.Key] {
			return nil, fmt.Errorf("required_envs[%d]: key %q is duplicated", i, item.Key)
		}
		seen[item.Key] = true
	}
	out, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("marshal required_envs: %w", err)
	}
	return out, nil
}

// validatePositiveDecimal checks a money string is a valid, non-negative
// decimal (empty is rejected - callers that allow "unset" check that first).
func validatePositiveDecimal(s string) error {
	r, ok := parseRat(s)
	if !ok {
		return fmt.Errorf("must be a decimal number")
	}
	if r.Sign() < 0 {
		return fmt.Errorf("must not be negative")
	}
	return nil
}

// CreateSubmission validates and files a new template submission for review.
// Mirrors serializers/submission/submission.py: the source project must
// belong to the caller and required_envs must be well-formed.
func (u *usecase) CreateSubmission(ctx context.Context, in CreateSubmissionInput) (*Submission, error) {
	if in.Name == "" || in.ShortDescription == "" || in.Description == "" {
		return nil, httpx.BadRequest("template.invalid_submission", "name, short_description and description are required")
	}
	if err := validatePositiveDecimal(in.OriginalPrice); err != nil {
		return nil, httpx.BadRequest("template.invalid_price", err.Error())
	}
	envs, err := validateRequiredEnvs(in.RequiredEnvs)
	if err != nil {
		return nil, httpx.BadRequest("template.invalid_required_envs", err.Error())
	}
	in.RequiredEnvs = envs

	owned, err := u.repo.ProjectOwnedByUser(ctx, in.ProjectID, in.UserID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if !owned {
		return nil, httpx.NotFound("project.not_found", "project not found")
	}

	s, err := u.repo.CreateSubmission(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return s, nil
}

func (u *usecase) GetSubmission(ctx context.Context, id, ownerID uuid.UUID) (*Submission, error) {
	s, err := u.repo.GetSubmissionForOwner(ctx, id, ownerID)
	return mapSubmissionErr(s, err)
}

func (u *usecase) ListSubmissions(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]Submission, int64, error) {
	limit, offset = clampPage(limit, offset)
	items, err := u.repo.ListSubmissionsForOwner(ctx, ownerID, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountSubmissionsForOwner(ctx, ownerID)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// UpdateSubmission only succeeds while the submission is still pending
// (enforced by the SQL WHERE clause; a zero-row update maps to 404 here since
// the caller cannot distinguish "not found" from "not pending" otherwise -
// GetSubmission first gives them a clearer error in that case).
func (u *usecase) UpdateSubmission(ctx context.Context, id, ownerID uuid.UUID, in UpdateSubmissionInput) (*Submission, error) {
	if in.OriginalPrice != nil {
		if err := validatePositiveDecimal(*in.OriginalPrice); err != nil {
			return nil, httpx.BadRequest("template.invalid_price", err.Error())
		}
	}
	if in.RequiredEnvs != nil {
		envs, err := validateRequiredEnvs(in.RequiredEnvs)
		if err != nil {
			return nil, httpx.BadRequest("template.invalid_required_envs", err.Error())
		}
		in.RequiredEnvs = envs
	}
	existing, err := u.repo.GetSubmissionForOwner(ctx, id, ownerID)
	if err != nil {
		return mapSubmissionErr(nil, err)
	}
	if existing.Status != SubmissionStatusPending {
		return nil, httpx.Conflict("template.submission_not_pending", "cannot update a reviewed submission")
	}
	s, err := u.repo.UpdateSubmission(ctx, id, ownerID, in)
	return mapSubmissionErr(s, err)
}

func (u *usecase) DeleteSubmission(ctx context.Context, id, ownerID uuid.UUID) error {
	existing, err := u.repo.GetSubmissionForOwner(ctx, id, ownerID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("template.submission_not_found", "submission not found")
		}
		return httpx.Internal(err)
	}
	if existing.Status != SubmissionStatusPending {
		return httpx.Conflict("template.submission_not_pending", "cannot delete a reviewed submission")
	}
	if err := u.repo.DeleteSubmission(ctx, id, ownerID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("template.submission_not_found", "submission not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) ListSubmissionsAdmin(ctx context.Context, status string, limit, offset int32) ([]Submission, int64, error) {
	limit, offset = clampPage(limit, offset)
	items, err := u.repo.ListSubmissionsAdmin(ctx, status, limit, offset)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountSubmissionsAdmin(ctx, status)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

// ApproveSubmission publishes the submission as a Template (admin-only).
// Mirrors admin/submission.py::approve_submissions.
func (u *usecase) ApproveSubmission(
	ctx context.Context, id, reviewerID uuid.UUID, discount, discountType string,
) (*Submission, *Template, error) {
	if discount == "" {
		discount = "0"
	}
	if err := validatePositiveDecimal(discount); err != nil {
		return nil, nil, httpx.BadRequest("template.invalid_discount", err.Error())
	}
	s, t, err := u.repo.ApproveSubmission(ctx, id, reviewerID, discount, discountTypeOrDefault(discountType))
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, nil, httpx.NotFound("template.submission_not_found", "submission not found")
		}
		if errors.Is(err, ErrSubmissionNotPending) {
			return nil, nil, httpx.Conflict("template.submission_not_pending", "submission already reviewed")
		}
		return nil, nil, httpx.Internal(err)
	}
	return s, t, nil
}

// RejectSubmission marks a pending submission rejected (admin-only). Mirrors
// admin/submission.py::reject_submissions.
func (u *usecase) RejectSubmission(ctx context.Context, id, reviewerID uuid.UUID, reason string) (*Submission, error) {
	if reason == "" {
		return nil, httpx.BadRequest("template.rejection_reason_required", "rejection_reason is required")
	}
	s, err := u.repo.RejectSubmission(ctx, id, reviewerID, reason)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.submission_not_found", "submission not found or already reviewed")
		}
		return nil, httpx.Internal(err)
	}
	return s, nil
}

func mapSubmissionErr(s *Submission, err error) (*Submission, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.submission_not_found", "submission not found")
		}
		return nil, httpx.Internal(err)
	}
	return s, nil
}

// clampPage normalizes list pagination the same way across all sub-domains.
func clampPage(limit, offset int32) (int32, int32) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
