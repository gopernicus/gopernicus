CREATE TABLE IF NOT EXISTS terms (
    id         TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    kind       TEXT NOT NULL,
    slug       TEXT NOT NULL,
    name       TEXT NOT NULL,
    parent_id  TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
