CREATE TABLE IF NOT EXISTS assets (
    id           TEXT COLLATE "C" PRIMARY KEY DEFAULT uuidv7()::text,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size         BIGINT NOT NULL DEFAULT 0,
    storage_key  TEXT NOT NULL,
    alt          TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL
);
