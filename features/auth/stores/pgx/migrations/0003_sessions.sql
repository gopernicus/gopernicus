-- Opaque server-side sessions keyed by token (the raw cookie value; no hashing).
-- Get filters on expires_at at the read clock (expired-at-read). user_id
-- references users.id by convention (no enforced FK). Timestamps are TIMESTAMPTZ.
CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
