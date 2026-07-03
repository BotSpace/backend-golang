package blog

import "time"

// PublicListItemResponse is the lightweight public listing projection
// (mirrors BlogPostListSerializer — no content, no author).
type PublicListItemResponse struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Excerpt     string     `json:"excerpt"`
	ReadingTime int        `json:"reading_time"`
	PublishedAt *time.Time `json:"published_at"`
}

func toPublicListItem(p *BlogPost) PublicListItemResponse {
	return PublicListItemResponse{
		ID:          p.ID.String(),
		Title:       p.Title,
		Slug:        p.Slug,
		Excerpt:     p.Excerpt,
		ReadingTime: p.ReadingTime(),
		PublishedAt: p.PublishedAt,
	}
}

// PublicListResponse is a paginated envelope for the public blog listing.
type PublicListResponse struct {
	Items  []PublicListItemResponse `json:"items"`
	Total  int64                    `json:"total"`
	Limit  int32                    `json:"limit"`
	Offset int32                    `json:"offset"`
}

func toPublicListResponse(items []BlogPost, total int64, limit, offset int32) PublicListResponse {
	out := make([]PublicListItemResponse, len(items))
	for i := range items {
		out[i] = toPublicListItem(&items[i])
	}
	return PublicListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// PublicDetailResponse is the full public post (mirrors BlogPostDetailSerializer).
type PublicDetailResponse struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content"`
	ReadingTime int        `json:"reading_time"`
	AuthorName  *string    `json:"author_name"`
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toPublicDetail(p *BlogPost) PublicDetailResponse {
	return PublicDetailResponse{
		ID:          p.ID.String(),
		Title:       p.Title,
		Slug:        p.Slug,
		Excerpt:     p.Excerpt,
		Content:     p.Content,
		ReadingTime: p.ReadingTime(),
		AuthorName:  p.AuthorName,
		PublishedAt: p.PublishedAt,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// AdminBlogPostResponse is the full admin projection (mirrors AdminBlogPostSerializer).
type AdminBlogPostResponse struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content"`
	IsPublished bool       `json:"is_published"`
	AuthorID    *string    `json:"author"`
	AuthorName  *string    `json:"author_name"`
	ReadingTime int        `json:"reading_time"`
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toAdminResponse(p *BlogPost) AdminBlogPostResponse {
	var authorID *string
	if p.AuthorID != nil {
		s := p.AuthorID.String()
		authorID = &s
	}
	return AdminBlogPostResponse{
		ID:          p.ID.String(),
		Title:       p.Title,
		Slug:        p.Slug,
		Excerpt:     p.Excerpt,
		Content:     p.Content,
		IsPublished: p.IsPublished,
		AuthorID:    authorID,
		AuthorName:  p.AuthorName,
		ReadingTime: p.ReadingTime(),
		PublishedAt: p.PublishedAt,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// AdminListResponse is a paginated envelope for the admin blog listing.
type AdminListResponse struct {
	Items  []AdminBlogPostResponse `json:"items"`
	Total  int64                   `json:"total"`
	Limit  int32                   `json:"limit"`
	Offset int32                   `json:"offset"`
}

func toAdminListResponse(items []BlogPost, total int64, limit, offset int32) AdminListResponse {
	out := make([]AdminBlogPostResponse, len(items))
	for i := range items {
		out[i] = toAdminResponse(&items[i])
	}
	return AdminListResponse{Items: out, Total: total, Limit: limit, Offset: offset}
}

// CreateRequest is the admin create payload. Slug/author are derived
// server-side (slug from title, author from the authenticated admin).
type CreateRequest struct {
	Title       string `json:"title" validate:"required,min=1,max=255"`
	Excerpt     string `json:"excerpt" validate:"omitempty,max=500"`
	Content     string `json:"content" validate:"required"`
	IsPublished bool   `json:"is_published"`
}

// UpdateRequest carries optional fields; nil pointers are left unchanged.
// Slug and author are immutable after creation (read-only in the Django
// serializer too), so they are not part of this payload.
type UpdateRequest struct {
	Title       *string `json:"title" validate:"omitempty,min=1,max=255"`
	Excerpt     *string `json:"excerpt" validate:"omitempty,max=500"`
	Content     *string `json:"content" validate:"omitempty"`
	IsPublished *bool   `json:"is_published"`
}
