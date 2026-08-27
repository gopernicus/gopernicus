CREATE TABLE IF NOT EXISTS inquiries (
    id         TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    name       TEXT NOT NULL,
    email      TEXT NOT NULL,
    message    TEXT NOT NULL,
    created_at TEXT NOT NULL
);
