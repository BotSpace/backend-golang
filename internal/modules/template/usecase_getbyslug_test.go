package template

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// getBySlugStubRepo is a narrow Repository test double for GetBySlug. It
// embeds a nil Repository so it structurally satisfies the (large — see the
// comment atop usecase_test.go) Repository interface without hand-rolling
// every submission/order/withdrawal method; only GetBySlug/IncrementViews/
// AttachExtras are actually exercised by TestGetBySlug_AttachesOwnershipFlag,
// so only those three are overridden here. Any other method being called
// would panic on the nil embedded interface — acceptable since this stub is
// scoped to one test.
type getBySlugStubRepo struct {
	Repository

	tpl *Template
	// orderedFor is the set of viewer ids AttachExtras should mark this
	// template as ordered/owned for — mirrors the real
	// TemplateOrderedIDsForUser query's per-viewer result.
	orderedFor map[uuid.UUID]bool

	// lastViewerID records what AttachExtras was called with, so the test
	// can assert the handler's viewerID (from optionalAuth) actually reaches
	// the repository, not just that GetBySlug wires the call at all.
	lastViewerID *uuid.UUID
}

func (r *getBySlugStubRepo) GetBySlug(_ context.Context, slug string) (*Template, error) {
	cp := *r.tpl
	return &cp, nil
}

func (r *getBySlugStubRepo) IncrementViews(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (r *getBySlugStubRepo) AttachExtras(_ context.Context, items []Template, viewerID *uuid.UUID) error {
	r.lastViewerID = viewerID
	if viewerID == nil {
		return nil
	}
	owned := r.orderedFor[*viewerID]
	for i := range items {
		items[i].IsOrdered = owned
	}
	return nil
}

// TestGetBySlug_AttachesOwnershipFlag is the deliverable regression test for
// gap 2 (template DETAIL showing "Bepul olish" even when the user already
// owns it): GetBySlug — the usecase behind GET /template/list/:slug, the
// single-template detail endpoint — must run the SAME AttachExtras
// ownership resolution List already runs, and forward it the caller's
// viewerID, not silently drop it. TemplateResponse.IsOrdered (JSON
// "is_ordered") is exactly the field the dashboard's "Sizda bor" badge reads
// on the list; this locks in that the detail response carries the same
// signal for the same viewer.
func TestGetBySlug_AttachesOwnershipFlag(t *testing.T) {
	viewer := uuid.New()
	other := uuid.New()
	tpl := &Template{ID: uuid.New(), Slug: "crm-bot", IsVerify: true}

	t.Run("owner sees is_ordered=true on detail", func(t *testing.T) {
		repo := &getBySlugStubRepo{tpl: tpl, orderedFor: map[uuid.UUID]bool{viewer: true}}
		uc := NewUsecase(repo)

		got, err := uc.GetBySlug(context.Background(), "crm-bot", &viewer)
		if err != nil {
			t.Fatalf("GetBySlug: %v", err)
		}
		if !got.IsOrdered {
			t.Fatal("expected IsOrdered=true for the template's owner on the DETAIL fetch")
		}
		if repo.lastViewerID == nil || *repo.lastViewerID != viewer {
			t.Fatalf("expected AttachExtras to receive the caller's viewerID, got %v", repo.lastViewerID)
		}
	})

	t.Run("non-owner sees is_ordered=false", func(t *testing.T) {
		repo := &getBySlugStubRepo{tpl: tpl, orderedFor: map[uuid.UUID]bool{viewer: true}}
		uc := NewUsecase(repo)

		got, err := uc.GetBySlug(context.Background(), "crm-bot", &other)
		if err != nil {
			t.Fatalf("GetBySlug: %v", err)
		}
		if got.IsOrdered {
			t.Fatal("expected IsOrdered=false for a viewer who never ordered this template")
		}
	})

	t.Run("anonymous viewer (nil) sees is_ordered=false, no panic", func(t *testing.T) {
		repo := &getBySlugStubRepo{tpl: tpl, orderedFor: map[uuid.UUID]bool{viewer: true}}
		uc := NewUsecase(repo)

		got, err := uc.GetBySlug(context.Background(), "crm-bot", nil)
		if err != nil {
			t.Fatalf("GetBySlug: %v", err)
		}
		if got.IsOrdered {
			t.Fatal("expected IsOrdered=false for an anonymous viewer")
		}
	})
}
