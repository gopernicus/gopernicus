CREATE TABLE IF NOT EXISTS assets (
    id           TEXT PRIMARY KEY,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size         BIGINT NOT NULL DEFAULT 0,
    storage_key  TEXT NOT NULL,
    alt          TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL
);
