CREATE TABLE IF NOT EXISTS menus (
    id         TEXT COLLATE "C" PRIMARY KEY DEFAULT uuidv7()::text,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
