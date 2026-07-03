package blog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// fakeRepo is an in-memory Repository for usecase tests — no DB required.
type fakeRepo struct {
	posts map[uuid.UUID]*BlogPost

	lastPublicFilter PublicListFilter
	lastAdminFilter  ListFilter
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{posts: map[uuid.UUID]*BlogPost{}}
}

func (r *fakeRepo) Create(_ context.Context, in CreateInput) (*BlogPost, error) {
	now := time.Now()
	p := &BlogPost{
		ID:          uuid.New(),
		Title:       in.Title,
		Slug:        in.Slug,
		Excerpt:     in.Excerpt,
		Content:     in.Content,
		IsPublished: in.IsPublished,
		AuthorID:    &in.AuthorID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if p.IsPublished {
		p.PublishedAt = &now
	}
	r.posts[p.ID] = p
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*BlogPost, error) {
	if p, ok := r.posts[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) GetPublishedBySlug(_ context.Context, slug string) (*BlogPost, error) {
	for _, p := range r.posts {
		if p.Slug == slug && p.IsPublished {
			cp := *p
			return &cp, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) SlugExists(_ context.Context, slug string, excludeID uuid.UUID) (bool, error) {
	for _, p := range r.posts {
		if p.Slug == slug && p.ID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) Update(_ context.Context, id uuid.UUID, in UpdateInput) (*BlogPost, error) {
	p, ok := r.posts[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	if in.Title != nil {
		p.Title = *in.Title
	}
	if in.Excerpt != nil {
		p.Excerpt = *in.Excerpt
	}
	if in.Content != nil {
		p.Content = *in.Content
	}
	if in.IsPublished != nil {
		p.IsPublished = *in.IsPublished
		if p.IsPublished && p.PublishedAt == nil {
			now := time.Now()
			p.PublishedAt = &now
		}
	}
	p.UpdatedAt = time.Now()
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.posts[id]; !ok {
		return database.ErrNotFound
	}
	delete(r.posts, id)
	return nil
}

func (r *fakeRepo) filterPublished(f PublicListFilter) []BlogPost {
	var out []BlogPost
	for _, p := range r.posts {
		if !p.IsPublished {
			continue
		}
		if f.Search != "" && !strings.Contains(p.Title, f.Search) && !strings.Contains(p.Excerpt, f.Search) {
			continue
		}
		out = append(out, *p)
	}
	return out
}

func (r *fakeRepo) ListPublished(_ context.Context, f PublicListFilter) ([]BlogPost, error) {
	r.lastPublicFilter = f
	return r.filterPublished(f), nil
}

func (r *fakeRepo) CountPublished(_ context.Context, search string) (int64, error) {
	return int64(len(r.filterPublished(PublicListFilter{Search: search}))), nil
}

func (r *fakeRepo) filterAdmin(f ListFilter) []BlogPost {
	var out []BlogPost
	for _, p := range r.posts {
		if f.IsPublished != nil && p.IsPublished != *f.IsPublished {
			continue
		}
		if f.Search != "" && !strings.Contains(p.Title, f.Search) && !strings.Contains(p.Slug, f.Search) &&
			!strings.Contains(p.Excerpt, f.Search) {
			continue
		}
		out = append(out, *p)
	}
	return out
}

func (r *fakeRepo) ListAdmin(_ context.Context, f ListFilter) ([]BlogPost, error) {
	r.lastAdminFilter = f
	return r.filterAdmin(f), nil
}

func (r *fakeRepo) CountAdmin(_ context.Context, f ListFilter) (int64, error) {
	return int64(len(r.filterAdmin(f))), nil
}

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

// TestCreateGeneratesUniqueSlug mirrors BlogPostModel._unique_slug: a second
// post with the same title must get a "-2" suffixed slug instead of
// colliding with the first.
func TestCreateGeneratesUniqueSlug(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	ctx := context.Background()
	authorID := uuid.New()

	p1, err := uc.Create(ctx, CreateInput{Title: "Hello World", Content: "first post", AuthorID: authorID})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if p1.Slug != "hello-world" {
		t.Fatalf("expected slug 'hello-world', got %q", p1.Slug)
	}

	p2, err := uc.Create(ctx, CreateInput{Title: "Hello World", Content: "second post", AuthorID: authorID})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if p2.Slug != "hello-world-2" {
		t.Fatalf("expected slug 'hello-world-2', got %q", p2.Slug)
	}
}

// TestGetPublishedBySlugHidesDrafts ensures a draft (is_published=false) is
// invisible via the public slug lookup, mirroring get_queryset() filtering
// on BlogPostPublicView.
func TestGetPublishedBySlugHidesDrafts(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()
	authorID := uuid.New()

	draft, err := uc.Create(ctx, CreateInput{Title: "Draft Post", Content: "not yet", AuthorID: authorID, IsPublished: false})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	if _, err := uc.GetPublishedBySlug(ctx, draft.Slug); status(err) != 404 {
		t.Fatalf("expected 404 for draft slug, got %v", err)
	}

	// Publishing it makes it visible.
	published := true
	if _, err := uc.Update(ctx, draft.ID, UpdateInput{IsPublished: &published}); err != nil {
		t.Fatalf("update to published: %v", err)
	}
	got, err := uc.GetPublishedBySlug(ctx, draft.Slug)
	if err != nil {
		t.Fatalf("expected published post to be visible, got %v", err)
	}
	if got.ID != draft.ID {
		t.Fatalf("expected id %v, got %v", draft.ID, got.ID)
	}
}

// TestListPublishedClampsDefaultLimit checks that an unset/oversized limit is
// normalized before reaching the repository (mirrors DRF pagination defaults).
func TestListPublishedClampsDefaultLimit(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo)
	ctx := context.Background()

	if _, _, err := uc.ListPublished(ctx, PublicListFilter{Limit: 0, Offset: -5}); err != nil {
		t.Fatalf("list published: %v", err)
	}
	if repo.lastPublicFilter.Limit != 20 {
		t.Fatalf("expected default limit 20, got %d", repo.lastPublicFilter.Limit)
	}
	if repo.lastPublicFilter.Offset != 0 {
		t.Fatalf("expected offset clamped to 0, got %d", repo.lastPublicFilter.Offset)
	}

	if _, _, err := uc.ListPublished(ctx, PublicListFilter{Limit: 1000}); err != nil {
		t.Fatalf("list published (oversized limit): %v", err)
	}
	if repo.lastPublicFilter.Limit != 20 {
		t.Fatalf("expected oversized limit clamped to 20, got %d", repo.lastPublicFilter.Limit)
	}
}

// TestDeleteNotFound ensures deleting a missing post surfaces as 404.
func TestDeleteNotFound(t *testing.T) {
	uc := NewUsecase(newFakeRepo())
	if err := uc.Delete(context.Background(), uuid.New()); status(err) != 404 {
		t.Fatalf("expected 404 for missing post, got %v", err)
	}
}
