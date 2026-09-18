-- OAuth grants use their own session row. Existing rows remain first-party.
ALTER TABLE sessions ADD COLUMN session_profile TEXT NOT NULL DEFAULT 'first_party';
ALTER TABLE sessions ADD COLUMN delegation TEXT NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_sessions_user_created_id ON sessions (user_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS oauth_clients (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    code_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    auth_revision INTEGER NOT NULL,
    delegation TEXT NOT NULL,
    redirect_uri TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_oauth_authorization_codes_expiry ON oauth_authorization_codes (expires_at);

-- Retain every spent hash until the original session horizon, including after
-- revocation. Refresh hashes and authorization codes are never stored raw.
CREATE TABLE IF NOT EXISTS oauth_refresh_history (
    refresh_token_hash TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_history_session ON oauth_refresh_history (session_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_history_expiry ON oauth_refresh_history (expires_at);
