CREATE TABLE IF NOT EXISTS menu_items (
    id         TEXT PRIMARY KEY,
    menu_id    TEXT NOT NULL,
    label      TEXT NOT NULL,
    url        TEXT NOT NULL DEFAULT '',
    parent_id  TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
