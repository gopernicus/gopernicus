SET LOCAL lock_timeout = '5s';
-- Passkeys (SOC 2 S4b): one registered WebAuthn credential per row, owned by
-- one user. credential_id, public_key (a COSE_Key), aaguid and user_handle are
-- opaque bytes; none is ever logged, audited or returned by a transport.
-- sign_count is the authenticator's unsigned 32-bit counter. transports is a
-- JSON array of strings. user_verified, backup_eligible and backup_state are the
-- registration ceremony's flags; sign_count and backup_state advance with each
-- verified use, and last_used_at is NULL until the first. id carries COLLATE "C"
-- like every other contractual id. user_id references users.id by convention (no
-- enforced FK). A new, empty table: it touches no existing one, so its indexes
-- build in place.
CREATE TABLE IF NOT EXISTS passkey_credentials (
    id              TEXT COLLATE "C" PRIMARY KEY DEFAULT uuidv7()::text,
    user_id         TEXT COLLATE "C" NOT NULL,
    name            TEXT NOT NULL,
    credential_id   BYTEA NOT NULL,
    public_key      BYTEA NOT NULL,
    aaguid          BYTEA NOT NULL DEFAULT '\x'::bytea,
    user_handle     BYTEA NOT NULL,
    sign_count      BIGINT NOT NULL DEFAULT 0 CHECK (sign_count BETWEEN 0 AND 4294967295),
    transports      TEXT NOT NULL DEFAULT '[]',
    user_verified   BOOLEAN NOT NULL DEFAULT FALSE,
    backup_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    backup_state    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ
);

-- A credential id is registered at most once, for anyone: the sign-in lookup.
CREATE UNIQUE INDEX IF NOT EXISTS idx_passkey_credentials_credential_id ON passkey_credentials (credential_id);
-- ListByUser oldest first, the cap and the handle checks, Snapshot and erasure.
CREATE INDEX IF NOT EXISTS idx_passkey_credentials_user_created_id ON passkey_credentials (user_id, created_at, id);
