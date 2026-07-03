package blog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// slugNonAlnum collapses any run of non a-z0-9 characters into a single
// hyphen. This approximates django.utils.text.slugify(allow_unicode=False):
// ASCII letters/digits survive, everything else (including Cyrillic, which
// Django's slugify also strips since it doesn't transliterate) is dropped.
var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a title into a URL-safe base slug, falling back to "post"
// when nothing alphanumeric survives (matches BlogPostModel._unique_slug).
func slugify(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "post"
	}
	if len(s) > 200 {
		s = strings.Trim(s[:200], "-")
	}
	return s
}

// Usecase exposes blog operations for both the public site and the admin panel.
type Usecase interface {
	Create(ctx context.Context, in CreateInput) (*BlogPost, error)
	Get(ctx context.Context, id uuid.UUID) (*BlogPost, error)
	GetPublishedBySlug(ctx context.Context, slug string) (*BlogPost, error)
	Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*BlogPost, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListPublished(ctx context.Context, f PublicListFilter) ([]BlogPost, int64, error)
	ListAdmin(ctx context.Context, f ListFilter) ([]BlogPost, int64, error)
}

type usecase struct {
	repo Repository
}

func NewUsecase(repo Repository) Usecase {
	return &usecase{repo: repo}
}

func (u *usecase) Create(ctx context.Context, in CreateInput) (*BlogPost, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, httpx.BadRequest("blog.title_required", "title must not be empty")
	}
	in.Title = title
	slug, err := u.uniqueSlug(ctx, title, uuid.Nil)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	in.Slug = slug
	p, err := u.repo.Create(ctx, in)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return p, nil
}

func (u *usecase) Get(ctx context.Context, id uuid.UUID) (*BlogPost, error) {
	p, err := u.repo.GetByID(ctx, id)
	return mapErr(p, err)
}

func (u *usecase) GetPublishedBySlug(ctx context.Context, slug string) (*BlogPost, error) {
	p, err := u.repo.GetPublishedBySlug(ctx, slug)
	return mapErr(p, err)
}

func (u *usecase) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*BlogPost, error) {
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			return nil, httpx.BadRequest("blog.title_required", "title must not be empty")
		}
		in.Title = &t
	}
	p, err := u.repo.Update(ctx, id, in)
	return mapErr(p, err)
}

func (u *usecase) Delete(ctx context.Context, id uuid.UUID) error {
	if err := u.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return httpx.NotFound("blog.not_found", "blog post not found")
		}
		return httpx.Internal(err)
	}
	return nil
}

func (u *usecase) ListPublished(ctx context.Context, f PublicListFilter) ([]BlogPost, int64, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	items, err := u.repo.ListPublished(ctx, f)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	total, err := u.repo.CountPublished(ctx, f.Search)
	if err != nil {
		return nil, 0, httpx.Internal(err)
	}
	return items, total, nil
}

func (u *usecase) ListAdmin(ctx context.Context, f ListFilter) ([]BlogPost, int64, error) {
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

// uniqueSlug appends "-2", "-3", ... to the slugified title until it no
// longer collides with an existing post (excludeID skips the post being
// updated; pass uuid.Nil for a brand-new post). Mirrors _unique_slug().
func (u *usecase) uniqueSlug(ctx context.Context, title string, excludeID uuid.UUID) (string, error) {
	base := slugify(title)
	slug := base
	for counter := 2; ; counter++ {
		exists, err := u.repo.SlugExists(ctx, slug, excludeID)
		if err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, counter)
	}
}

// mapErr translates a not-found into a 404 and any other error into a 500.
func mapErr(p *BlogPost, err error) (*BlogPost, error) {
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, httpx.NotFound("blog.not_found", "blog post not found")
		}
		return nil, httpx.Internal(err)
	}
	return p, nil
}
