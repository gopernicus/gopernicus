-- Longer-lived password-reset tokens keyed by the opaque token value. Get
-- enforces expired-at-read. user_id references users.id by convention (no
-- enforced FK). Timestamps are fixed-width TEXT.
CREATE TABLE IF NOT EXISTS verification_tokens (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
