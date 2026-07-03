package blog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

// NewPostgresRepository is an alias kept for wiring symmetry with other modules.
func NewPostgresRepository(pool *pgxpool.Pool) Repository { return NewRepository(pool) }

func (r *pgRepo) Create(ctx context.Context, in CreateInput) (*BlogPost, error) {
	row, err := r.queries.CreateBlogPost(ctx, sqlcdb.CreateBlogPostParams{
		ID:          uuid.New(),
		Title:       in.Title,
		Slug:        in.Slug,
		Excerpt:     in.Excerpt,
		Content:     in.Content,
		IsPublished: in.IsPublished,
		AuthorID:    ptrIfNotNil(in.AuthorID),
	})
	if err != nil {
		return nil, fmt.Errorf("create blog post: %w", err)
	}
	p := fromSQLC(row)
	r.attachAuthor(ctx, &p)
	return &p, nil
}

func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*BlogPost, error) {
	row, err := r.queries.GetBlogPostByID(ctx, id)
	p, err := mapOne(row, err)
	if err != nil {
		return nil, err
	}
	r.attachAuthor(ctx, p)
	return p, nil
}

func (r *pgRepo) GetPublishedBySlug(ctx context.Context, slug string) (*BlogPost, error) {
	row, err := r.queries.GetPublishedBlogPostBySlug(ctx, slug)
	return mapOne(row, err)
}

func (r *pgRepo) SlugExists(ctx context.Context, slug string, excludeID uuid.UUID) (bool, error) {
	ok, err := r.queries.ExistsBlogPostSlug(ctx, sqlcdb.ExistsBlogPostSlugParams{Slug: slug, ExcludeID: excludeID})
	if err != nil {
		return false, fmt.Errorf("slug exists: %w", err)
	}
	return ok, nil
}

func (r *pgRepo) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*BlogPost, error) {
	row, err := r.queries.UpdateBlogPost(ctx, sqlcdb.UpdateBlogPostParams{
		ID:          id,
		Title:       in.Title,
		Excerpt:     in.Excerpt,
		Content:     in.Content,
		IsPublished: in.IsPublished,
	})
	p, err := mapOne(row, err)
	if err != nil {
		return nil, err
	}
	r.attachAuthor(ctx, p)
	return p, nil
}

func (r *pgRepo) Delete(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries.DeleteBlogPost(ctx, id)
	if err != nil {
		return fmt.Errorf("delete blog post: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) ListPublished(ctx context.Context, f PublicListFilter) ([]BlogPost, error) {
	rows, err := r.queries.ListPublishedBlogPosts(ctx, sqlcdb.ListPublishedBlogPostsParams{
		Search: strPtr(f.Search),
		Limit:  f.Limit,
		Offset: f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list published: %w", err)
	}
	out := make([]BlogPost, len(rows))
	for i, row := range rows {
		out[i] = fromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountPublished(ctx context.Context, search string) (int64, error) {
	n, err := r.queries.CountPublishedBlogPosts(ctx, strPtr(search))
	if err != nil {
		return 0, fmt.Errorf("count published: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListAdmin(ctx context.Context, f ListFilter) ([]BlogPost, error) {
	rows, err := r.queries.ListBlogPostsAdmin(ctx, sqlcdb.ListBlogPostsAdminParams{
		IsPublished: f.IsPublished,
		Search:      strPtr(f.Search),
		Limit:       f.Limit,
		Offset:      f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list admin: %w", err)
	}
	out := make([]BlogPost, len(rows))
	for i, row := range rows {
		p := BlogPost{
			ID:          row.ID,
			Title:       row.Title,
			Slug:        row.Slug,
			Excerpt:     row.Excerpt,
			Content:     row.Content,
			IsPublished: row.IsPublished,
			AuthorID:    row.AuthorID,
			PublishedAt: row.PublishedAt,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		}
		if name := displayName(row.AuthorFirstName, row.AuthorLastName, row.AuthorPhone, row.AuthorUsername); name != "" {
			p.AuthorName = &name
		}
		out[i] = p
	}
	return out, nil
}

func (r *pgRepo) CountAdmin(ctx context.Context, f ListFilter) (int64, error) {
	n, err := r.queries.CountBlogPostsAdmin(ctx, sqlcdb.CountBlogPostsAdminParams{
		IsPublished: f.IsPublished,
		Search:      strPtr(f.Search),
	})
	if err != nil {
		return 0, fmt.Errorf("count admin: %w", err)
	}
	return n, nil
}

// attachAuthor fills AuthorName for single-record responses via a tiny lookup.
func (r *pgRepo) attachAuthor(ctx context.Context, p *BlogPost) {
	if p == nil || p.AuthorID == nil {
		return
	}
	a, err := r.queries.GetBlogAuthorDisplay(ctx, *p.AuthorID)
	if err != nil {
		return
	}
	if name := displayName(&a.FirstName, &a.LastName, a.Phone, a.Username); name != "" {
		p.AuthorName = &name
	}
}

func mapOne(row sqlcdb.BlogPost, err error) (*BlogPost, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query blog post: %w", err)
	}
	p := fromSQLC(row)
	return &p, nil
}

func fromSQLC(r sqlcdb.BlogPost) BlogPost {
	return BlogPost{
		ID:          r.ID,
		Title:       r.Title,
		Slug:        r.Slug,
		Excerpt:     r.Excerpt,
		Content:     r.Content,
		IsPublished: r.IsPublished,
		AuthorID:    r.AuthorID,
		PublishedAt: r.PublishedAt,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// displayName mirrors User.__str__ fallback: "First Last" -> phone -> username.
func displayName(first, last, phone, username *string) string {
	f, l := deref(first), deref(last)
	full := strings.TrimSpace(f + " " + l)
	if full != "" {
		return full
	}
	if p := deref(phone); p != "" {
		return p
	}
	return deref(username)
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

// ptrIfNotNil returns a pointer to id, or nil when id is the zero UUID —
// mirrors the nullable-FK semantics the pgtype helpers used to provide.
func ptrIfNotNil(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
