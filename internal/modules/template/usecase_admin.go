package template

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

func (u *usecase) AdminUpdateFlow(ctx context.Context, id uuid.UUID, flow json.RawMessage, actorID uuid.UUID, reason string) error {
	if err := u.repo.UpdateLinkedProjectFlow(ctx, id, flow, actorID, strings.TrimSpace(reason)); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("template.project_not_found", "linked template project not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

// AdminList returns a page of the admin-wide catalog (any verification
// status) plus the total match count.
func (u *usecase) AdminList(ctx context.Context, f AdminTemplateListFilter) ([]AdminTemplateSummary, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.ListAdmin(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountAdmin(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) AdminGet(ctx context.Context, id uuid.UUID) (*AdminTemplateSummary, error) {
	s, err := u.repo.GetAdminByID(ctx, id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.not_found", "template not found")
		}
		return nil, httpx.Internal(err)
	}
	return s, nil
}

func (u *usecase) AdminCreate(ctx context.Context, in CreateAdminTemplateInput) (*Template, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, httpx.BadRequest("template.name_required", "name is required")
	}
	if in.OriginalPrice == "" {
		in.OriginalPrice = "0"
	}
	if in.Discount == "" {
		in.Discount = "0"
	}
	t, err := u.repo.CreateAdmin(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return t, nil
}

func (u *usecase) AdminUpdate(ctx context.Context, id uuid.UUID, in UpdateAdminTemplateInput) (*Template, error) {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, httpx.BadRequest("template.name_required", "name must not be empty")
		}
		in.Name = &name
	}
	t, err := u.repo.UpdateAdmin(ctx, id, in)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.not_found", "template not found")
		}
		return nil, httpx.Internal(err)
	}
	return t, nil
}

func (u *usecase) AdminDelete(ctx context.Context, id uuid.UUID) error {
	if err := u.repo.DeleteAdmin(ctx, id); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("template.not_found", "template not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

// AdminSetVerify flips is_verify (approve when verify=true, unverify/reject
// when verify=false) — a direct status toggle independent of the submission
// review workflow (ApproveSubmission/RejectSubmission), for templates the
// admin created or edited directly.
func (u *usecase) AdminSetVerify(ctx context.Context, id uuid.UUID, verify bool) (*Template, error) {
	t, err := u.repo.SetVerify(ctx, id, verify)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("template.not_found", "template not found")
		}
		return nil, httpx.Internal(err)
	}
	return t, nil
}
