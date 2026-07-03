-- Blog posts — Markdown content shown on the public site.
-- Ported from core/apps/blog (BlogPostModel). Django uses modeltranslation on
-- some apps but NOT on this model (verified: no translation.py registers
-- BlogPostModel), so title/excerpt/content stay single-language columns here.
-- slug is generated app-side from title (unique, "-2"/"-3" suffix on clash)
-- and is immutable after creation, matching the Django save() logic.
-- author_id is nullable + ON DELETE SET NULL so deleting a user keeps the post.
CREATE TABLE IF NOT EXISTS blog_posts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title        TEXT NOT NULL,
    slug         TEXT NOT NULL,
    excerpt      TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    is_published BOOLEAN NOT NULL DEFAULT FALSE,
    author_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_blog_posts_slug ON blog_posts (slug);
CREATE INDEX IF NOT EXISTS idx_blog_posts_published ON blog_posts (is_published, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_blog_posts_author ON blog_posts (author_id);
