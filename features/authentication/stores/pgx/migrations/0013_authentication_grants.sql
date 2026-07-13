-- Recent-authentication / step-up grants (auth-v3 §5.0). A live session proves
-- revocation state; it does not prove the human recently presented an EXISTING
-- authenticator. Every sensitive credential/identifier mutation additionally
-- requires a Grant: a short-lived, single-use, server-side token bound to the live
-- session, user, intended operation (purpose), and the operation's context digest
-- (a provider name or an identifier-change ID). context_digest binds the grant to
-- one operation so a grant earned for one cannot authorize another.
--
-- methods/assurance record how the grant was earned (a JSON array of honest method
-- descriptors + the AssuranceLevel string) so a sufficiently recent primary login
-- can satisfy it without an extra prompt. authenticated_at is when the proving
-- authentication happened; consumed_at NULL is the unspent sentinel. Consume decides
-- expiry, single-use, session binding, and context match in one atomic operation.
-- id defaults DB-side; session_id/user_id reference by convention (no enforced FK).
CREATE TABLE IF NOT EXISTS authentication_grants (
    id               TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    session_id       TEXT NOT NULL,
    user_id          TEXT NOT NULL,
    purpose          TEXT NOT NULL,
    context_digest   TEXT NOT NULL,
    methods          TEXT NOT NULL DEFAULT '',
    assurance        TEXT NOT NULL DEFAULT '',
    authenticated_at TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ
);

-- Consume lookup by (session_id, purpose, context_digest); the leading session_id
-- also serves DeleteBySession (the revocation cascade).
CREATE INDEX IF NOT EXISTS idx_authentication_grants_session_purpose_context
    ON authentication_grants (session_id, purpose, context_digest);
