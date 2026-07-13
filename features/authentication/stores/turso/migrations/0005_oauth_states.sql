-- One-time, expiring OAuth flow secrets keyed by token (design §3). Consume is a
-- DELETE … RETURNING: the row is removed regardless of expiry and the expiry
-- decision is computed in Go from the returned row (the jobs queue.go precedent).
-- payload is an opaque blob the service reads back verbatim (PKCE verifier + OIDC
-- nonce, or the pending-link account). expires_at is a fixed-width TEXT timestamp.
CREATE TABLE IF NOT EXISTS oauth_states (
    token      TEXT PRIMARY KEY,
    provider   TEXT NOT NULL,
    purpose    TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL
);
