-- User↔provider OAuth/OIDC links (design §3). Uniqueness is on
-- (provider, provider_user_id) — a provider identity belongs to at most one local
-- user; a colliding Create surfaces as errs.ErrAlreadyExists via MapError. The
-- token columns hold CIPHERTEXT when a Config.TokenEncrypter is wired and are
-- empty otherwise (login/linking still work, just no offline API access).
-- Booleans are 0/1; linked_at is a fixed-width TEXT timestamp, token_expires_at is
-- nullable. user_id references users.id by convention (no enforced FK).
CREATE TABLE IF NOT EXISTS oauth_accounts (
    provider                TEXT NOT NULL,
    provider_user_id        TEXT NOT NULL,
    user_id                 TEXT NOT NULL,
    provider_email          TEXT NOT NULL DEFAULT '',
    provider_email_verified INTEGER NOT NULL DEFAULT 0,
    account_verified        INTEGER NOT NULL DEFAULT 0,
    linked_at               TEXT NOT NULL,
    access_token            TEXT NOT NULL DEFAULT '',
    refresh_token           TEXT NOT NULL DEFAULT '',
    token_expires_at        TEXT,
    token_type              TEXT NOT NULL DEFAULT '',
    scope                   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (provider, provider_user_id)
);

-- ListByUser support (plan-cut named secondary index).
CREATE INDEX IF NOT EXISTS idx_oauth_accounts_user_id ON oauth_accounts (user_id);
