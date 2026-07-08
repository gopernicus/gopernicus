-- The identity aggregate. Email is stored normalized (trimmed + lowercased) and
-- is UNIQUE — a colliding Create surfaces as errs.ErrAlreadyExists via MapError.
-- email_verified is a native BOOLEAN; created_at/updated_at are TIMESTAMPTZ
-- (microsecond precision — the keyset tie-break on (created_at, id) is
-- load-bearing).
CREATE TABLE IF NOT EXISTS users (
    id             TEXT PRIMARY KEY,
    email          TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL DEFAULT '',
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL
);
