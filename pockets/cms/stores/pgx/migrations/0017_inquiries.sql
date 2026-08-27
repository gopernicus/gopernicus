CREATE TABLE IF NOT EXISTS inquiries (
    id         TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    message    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
