-- One-time, expiring OAuth flow secrets keyed by token (design §3). Consume is a
-- DELETE … RETURNING: the row is removed regardless of expiry and the expiry
-- decision is computed in Go from the returned row (the jobs queue.go precedent).
-- payload is an opaque blob the service reads back VERBATIM (PKCE verifier + OIDC
-- nonce, or the pending-link account) — stored as BYTEA, not JSONB: the conformance
-- suite asserts a byte-exact payload round-trip (a non-JSON value included), which
-- JSONB (re-canonicalizes / rejects non-JSON) cannot satisfy. expires_at is
-- TIMESTAMPTZ.
CREATE TABLE IF NOT EXISTS oauth_states (
    token      TEXT PRIMARY KEY,
    provider   TEXT NOT NULL,
    purpose    TEXT NOT NULL,
    payload    BYTEA NOT NULL DEFAULT ''::bytea,
    expires_at TIMESTAMPTZ NOT NULL
);
