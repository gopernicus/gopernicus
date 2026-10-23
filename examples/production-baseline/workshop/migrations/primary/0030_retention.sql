SET LOCAL lock_timeout = '5s';
-- Retention (SOC 2 S3). The maintenance purges select expired rows by
-- expires_at, which no index served, so each gets one here, every one
-- IF NOT EXISTS so an index pre-built CONCURRENTLY under the same name makes
-- this build a no-op. Invitations get none: one row per invite, human-scale.
-- The indexes come FIRST and the users column LAST: ADD COLUMN is metadata-only
-- but takes ACCESS EXCLUSIVE on users, so it must not wait behind the index
-- builds while logins queue behind it. lock_timeout aborts the stream instead
-- of queueing; the host retries. Apply this file in its own runner invocation.
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_authentication_grants_expires_at ON authentication_grants (expires_at);
CREATE INDEX IF NOT EXISTS idx_contact_changes_expires_at ON contact_changes (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_states_expires_at ON oauth_states (expires_at);
CREATE INDEX IF NOT EXISTS idx_challenges_expires_at ON challenges (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_authorization_codes_expires_at ON oauth_authorization_codes (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_clients_expires_at ON oauth_clients (expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_history_expires_at ON oauth_refresh_history (expires_at);
-- The erasure marker: NULL for every existing row. An erased user keeps
-- status 'deactivated' (every credential path already refuses it, so a binary
-- built before this file reads a correct denial); the store projects the
-- erased status from erased_at IS NOT NULL.
ALTER TABLE users ADD COLUMN IF NOT EXISTS erased_at TIMESTAMPTZ NULL;
