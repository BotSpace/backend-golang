-- name: CreateBlogPost :one
-- published_at is set on creation when the post is created already published
-- (mirrors BlogPostModel.save(): published_at is stamped the first time
-- is_published becomes true).
INSERT INTO blog_posts (id, title, slug, excerpt, content, is_published, author_id, published_at)
VALUES (
    sqlc.arg('id'), sqlc.arg('title'), sqlc.arg('slug'), sqlc.arg('excerpt'), sqlc.arg('content'),
    sqlc.arg('is_published'), sqlc.narg('author_id'),
    CASE WHEN sqlc.arg('is_published') THEN NOW() ELSE NULL END
)
RETURNING *;

-- name: GetBlogPostByID :one
SELECT * FROM blog_posts WHERE id = $1;

-- name: GetPublishedBlogPostBySlug :one
SELECT * FROM blog_posts WHERE slug = $1 AND is_published = TRUE;

-- name: ExistsBlogPostSlug :one
-- excludeID = uuid.Nil (never a real post id) when checking for a brand new post.
SELECT EXISTS(
    SELECT 1 FROM blog_posts WHERE slug = sqlc.arg('slug') AND id != sqlc.arg('exclude_id')
);

-- name: UpdateBlogPost :one
-- published_at is stamped the first time is_published flips to true; it is
-- never cleared afterwards, matching the Django model.
UPDATE blog_posts
SET title        = COALESCE(sqlc.narg('title'), title),
    excerpt      = COALESCE(sqlc.narg('excerpt'), excerpt),
    content      = COALESCE(sqlc.narg('content'), content),
    is_published = COALESCE(sqlc.narg('is_published'), is_published),
    published_at = CASE
        WHEN COALESCE(sqlc.narg('is_published'), is_published) = TRUE AND published_at IS NULL
            THEN NOW()
        ELSE published_at
    END,
    updated_at   = NOW()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: DeleteBlogPost :execrows
DELETE FROM blog_posts WHERE id = $1;

-- name: ListPublishedBlogPosts :many
-- Public list — lightweight, no author info (mirrors BlogPostListSerializer).
SELECT * FROM blog_posts
WHERE is_published = TRUE
  AND (sqlc.narg('search')::text IS NULL
       OR title ILIKE '%' || sqlc.narg('search') || '%'
       OR excerpt ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY published_at DESC, created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPublishedBlogPosts :one
SELECT COUNT(*) FROM blog_posts
WHERE is_published = TRUE
  AND (sqlc.narg('search')::text IS NULL
       OR title ILIKE '%' || sqlc.narg('search') || '%'
       OR excerpt ILIKE '%' || sqlc.narg('search') || '%');

-- name: ListBlogPostsAdmin :many
-- Admin list — joins users for author display name (mirrors Django's
-- select_related("author") on the admin queryset).
SELECT bp.*,
       u.first_name AS author_first_name,
       u.last_name  AS author_last_name,
       u.phone      AS author_phone,
       u.username   AS author_username
FROM blog_posts bp
LEFT JOIN users u ON u.id = bp.author_id
WHERE (sqlc.narg('is_published')::bool IS NULL OR bp.is_published = sqlc.narg('is_published'))
  AND (sqlc.narg('search')::text IS NULL
       OR bp.title ILIKE '%' || sqlc.narg('search') || '%'
       OR bp.slug ILIKE '%' || sqlc.narg('search') || '%'
       OR bp.excerpt ILIKE '%' || sqlc.narg('search') || '%')
ORDER BY bp.created_at DESC, bp.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountBlogPostsAdmin :one
SELECT COUNT(*) FROM blog_posts bp
WHERE (sqlc.narg('is_published')::bool IS NULL OR bp.is_published = sqlc.narg('is_published'))
  AND (sqlc.narg('search')::text IS NULL
       OR bp.title ILIKE '%' || sqlc.narg('search') || '%'
       OR bp.slug ILIKE '%' || sqlc.narg('search') || '%'
       OR bp.excerpt ILIKE '%' || sqlc.narg('search') || '%');

-- name: GetBlogAuthorDisplay :one
-- Small lookup used to build author_name for single-record blog responses
-- (create/update/get) without joining the main queries above.
SELECT first_name, last_name, phone, username FROM users WHERE id = $1;
