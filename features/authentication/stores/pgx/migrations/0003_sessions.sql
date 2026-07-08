-- Opaque server-side sessions keyed by token. The token column holds an opaque
-- value the auth service supplies: the service SHA-256-hashes the cookie token
-- before every write/read (design §7.3), so this column stores the hash, not the
-- raw cookie value; the store does no hashing. Get filters on expires_at at the
-- read clock (expired-at-read). user_id references users.id by convention (no
-- enforced FK). Timestamps are TIMESTAMPTZ.
CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
