-- The spine: the columns ALL content shares (the WP wp_posts core-columns
-- analog). This table never changes shape when an author adds a type or a field
-- (plan §2); it changes only on a framework upgrade.
CREATE TABLE IF NOT EXISTS entries (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    type         TEXT NOT NULL,            -- content type slug: 'article','page','product'
    slug         TEXT NOT NULL,
    title        TEXT NOT NULL,
    status       TEXT NOT NULL,            -- draft | published
    body         TEXT NOT NULL DEFAULT '', -- raw markdown (universal long-form field)
    excerpt      TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    template     TEXT NOT NULL DEFAULT 'default',
    parent_id    TEXT NOT NULL DEFAULT '', -- hierarchy (pages); '' otherwise
    menu_order   INTEGER NOT NULL DEFAULT 0,
    published_at TEXT,                      -- set the first time published
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (type, slug)
);
