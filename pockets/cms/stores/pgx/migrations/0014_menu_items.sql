CREATE TABLE IF NOT EXISTS menu_items (
    id         TEXT COLLATE "C" PRIMARY KEY DEFAULT uuidv7()::text,
    menu_id    TEXT COLLATE "C" NOT NULL,
    label      TEXT NOT NULL,
    url        TEXT NOT NULL DEFAULT '',
    parent_id  TEXT COLLATE "C" NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
