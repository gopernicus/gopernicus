-- Passkeys (SOC 2 S4b): one registered WebAuthn credential per row, owned by
-- one user. credential_id, public_key (a COSE_Key), aaguid and user_handle are
-- opaque BLOBs; none is ever logged, audited or returned by a transport.
-- sign_count is the authenticator's unsigned 32-bit counter. transports is a
-- JSON array of strings. user_verified, backup_eligible and backup_state are the
-- registration ceremony's 0/1 flags; sign_count and backup_state advance with
-- each verified use, and last_used_at is NULL until the first. Timestamps are
-- fixed-width TEXT. user_id references users.id by convention (no enforced FK).
-- A new, empty table: it touches no existing one.
CREATE TABLE IF NOT EXISTS passkey_credentials (
    id              TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id         TEXT NOT NULL,
    name            TEXT NOT NULL,
    credential_id   BLOB NOT NULL,
    public_key      BLOB NOT NULL,
    aaguid          BLOB NOT NULL DEFAULT (x''),
    user_handle     BLOB NOT NULL,
    sign_count      INTEGER NOT NULL DEFAULT 0 CHECK (sign_count BETWEEN 0 AND 4294967295),
    transports      TEXT NOT NULL DEFAULT '[]',
    user_verified   INTEGER NOT NULL DEFAULT 0,
    backup_eligible INTEGER NOT NULL DEFAULT 0,
    backup_state    INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    last_used_at    TEXT
);

-- A credential id is registered at most once, for anyone: the sign-in lookup.
CREATE UNIQUE INDEX IF NOT EXISTS idx_passkey_credentials_credential_id ON passkey_credentials (credential_id);
-- ListByUser oldest first, the cap and the handle checks, Snapshot and erasure.
CREATE INDEX IF NOT EXISTS idx_passkey_credentials_user_created_id ON passkey_credentials (user_id, created_at, id);
