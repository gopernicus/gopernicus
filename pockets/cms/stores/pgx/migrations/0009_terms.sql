CREATE TABLE IF NOT EXISTS terms (
    id         TEXT COLLATE "C" PRIMARY KEY DEFAULT uuidv7()::text,
    kind       TEXT NOT NULL,
    slug       TEXT NOT NULL,
    name       TEXT NOT NULL,
    parent_id  TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
