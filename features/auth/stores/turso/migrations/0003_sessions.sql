-- Opaque server-side sessions keyed by token (the raw cookie value; no hashing).
-- Get filters on expires_at at the read clock (expired-at-read). user_id
-- references users.id by convention (no enforced FK). Timestamps are fixed-width
-- TEXT.
CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
