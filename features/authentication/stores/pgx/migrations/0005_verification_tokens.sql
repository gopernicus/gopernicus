-- Longer-lived password-reset tokens keyed by the opaque token value. Get
-- enforces expired-at-read. user_id references users.id by convention (no
-- enforced FK). Timestamps are TIMESTAMPTZ.
CREATE TABLE IF NOT EXISTS verification_tokens (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
