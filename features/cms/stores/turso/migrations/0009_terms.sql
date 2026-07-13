CREATE TABLE IF NOT EXISTS terms (
    id         TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    kind       TEXT NOT NULL,
    slug       TEXT NOT NULL,
    name       TEXT NOT NULL,
    parent_id  TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
