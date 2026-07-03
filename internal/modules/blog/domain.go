package blog

import (
	"context"
	"math"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// wordRe approximates Python's \w+ used by BlogPostModel.reading_time.
var wordRe = regexp.MustCompile(`\w+`)

// BlogPost is a Markdown blog article shown on the public site once
// published. AuthorName is a derived display value (first+last name, falling
// back to phone/username) resolved by the repository — it is nil unless the
// query that loaded the post joined the author.
type BlogPost struct {
	ID          uuid.UUID
	Title       string
	Slug        string
	Excerpt     string
	Content     string
	IsPublished bool
	AuthorID    *uuid.UUID
	AuthorName  *string
	PublishedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ReadingTime estimates minutes to read at ~200 words/minute, matching the
// Django model's `reading_time` property (min 1 minute).
func (p *BlogPost) ReadingTime() int {
	words := len(wordRe.FindAllString(p.Content, -1))
	minutes := int(math.Ceil(float64(words) / 200))
	if minutes < 1 {
		return 1
	}
	return minutes
}

// CreateInput is the admin create payload. Slug is computed by the usecase
// (unique-slug algorithm) before reaching the repository.
type CreateInput struct {
	Title       string
	Slug        string
	Excerpt     string
	Content     string
	IsPublished bool
	AuthorID    uuid.UUID
}

// UpdateInput carries optional fields; nil pointers are left unchanged. Slug
// and author are immutable after creation (mirrors the Django read-only
// serializer fields), so they are not editable here.
type UpdateInput struct {
	Title       *string
	Excerpt     *string
	Content     *string
	IsPublished *bool
}

// PublicListFilter narrows and paginates the public (published-only) listing.
type PublicListFilter struct {
	Search string
	Limit  int32
	Offset int32
}

// ListFilter narrows and paginates the admin listing (drafts included).
type ListFilter struct {
	Search      string
	IsPublished *bool
	Limit       int32
	Offset      int32
}

// Repository is the persistence port for blog posts.
type Repository interface {
	Create(ctx context.Context, in CreateInput) (*BlogPost, error)
	GetByID(ctx context.Context, id uuid.UUID) (*BlogPost, error)
	GetPublishedBySlug(ctx context.Context, slug string) (*BlogPost, error)
	SlugExists(ctx context.Context, slug string, excludeID uuid.UUID) (bool, error)
	Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*BlogPost, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListPublished(ctx context.Context, f PublicListFilter) ([]BlogPost, error)
	CountPublished(ctx context.Context, search string) (int64, error)
	ListAdmin(ctx context.Context, f ListFilter) ([]BlogPost, error)
	CountAdmin(ctx context.Context, f ListFilter) (int64, error)
}
