-- An OAuth grant has its own session and fixed refresh horizon. Existing rows
-- become first-party sessions; no browser session is reused for delegation.
ALTER TABLE sessions ADD COLUMN session_profile TEXT NOT NULL DEFAULT 'first_party';
ALTER TABLE sessions ADD COLUMN delegation TEXT NOT NULL DEFAULT '{}';
ALTER TABLE sessions ALTER COLUMN id TYPE TEXT COLLATE "C";

CREATE INDEX IF NOT EXISTS idx_sessions_user_created_at_id
    ON sessions (user_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS oauth_clients (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    code_hash      TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL,
    auth_revision  BIGINT NOT NULL,
    delegation     TEXT NOT NULL,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL
);

-- Retain spent hashes until the grant's fixed expiry, including after its
-- session is revoked. There is deliberately no cascading foreign key.
CREATE TABLE IF NOT EXISTS oauth_refresh_history (
    refresh_token_hash TEXT PRIMARY KEY,
    session_id        TEXT NOT NULL,
    expires_at        TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_history_session_id
    ON oauth_refresh_history (session_id);
