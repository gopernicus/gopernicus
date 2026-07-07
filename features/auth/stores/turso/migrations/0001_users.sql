-- The identity aggregate. Email is stored normalized (trimmed + lowercased) and
-- is UNIQUE — a colliding Create surfaces as errs.ErrAlreadyExists via MapError.
-- email_verified is 0/1; created_at/updated_at are fixed-width TEXT timestamps.
CREATE TABLE IF NOT EXISTS users (
    id             TEXT PRIMARY KEY,
    email          TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL DEFAULT '',
    email_verified INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
