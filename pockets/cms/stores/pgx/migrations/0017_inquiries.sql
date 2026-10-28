CREATE TABLE IF NOT EXISTS inquiries (
    id         TEXT COLLATE "C" PRIMARY KEY DEFAULT uuidv7()::text,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    message    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
