-- Short-lived email-verification codes keyed by the opaque code value. Get
-- enforces expired-at-read. user_id references users.id by convention (no
-- enforced FK). Timestamps are TIMESTAMPTZ.
CREATE TABLE IF NOT EXISTS verification_codes (
    code       TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
