-- Retention (SOC 2 S3). The maintenance purges select expired rows by
-- expires_at, which no index served, so each gets one here. The OAuth2
-- authorization codes and refresh history already carry theirs from 0019
-- (idx_oauth_authorization_codes_expiry, idx_oauth_refresh_history_expiry).
-- Invitations get none: one row per invite, human-scale. The indexes come
-- first and the users column last, as in the pgx sibling.
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_authentication_grants_expires_at ON authentication_grants (expires_at);
CREATE INDEX IF NOT EXISTS idx_contact_changes_expires_at ON contact_changes (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_states_expires_at ON oauth_states (expires_at);
CREATE INDEX IF NOT EXISTS idx_challenges_expires_at ON challenges (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_clients_expires_at ON oauth_clients (expires_at);
-- The erasure marker, a fixed-width TEXT timestamp: NULL for every existing
-- row. An erased user keeps status 'deactivated' (every credential path already
-- refuses it); the store projects the erased status from erased_at IS NOT NULL.
ALTER TABLE users ADD COLUMN erased_at TEXT NULL;
