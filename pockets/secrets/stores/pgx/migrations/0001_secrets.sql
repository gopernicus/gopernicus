-- The secrets pocket: one wrapped data key per owner, values sealed under it,
-- and an append-only audit log. Ciphertext columns hold the vault's base64url
-- text. The host's role setup should REVOKE UPDATE, DELETE on secrets_audit
-- from the application role; the pocket never issues either.
--
-- Ordering columns compare under COLLATE "C" so pages match the memory store's
-- byte order regardless of the database's default collation.
CREATE TABLE IF NOT EXISTS secrets_owner_keys (
    owner_type     TEXT        NOT NULL,
    owner_id       TEXT        NOT NULL,
    key_id         TEXT        NOT NULL,
    wrapped_key    TEXT        NOT NULL,
    wrapper_key_id TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    rotated_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT secrets_owner_keys_pk PRIMARY KEY (owner_type, owner_id)
);

-- No ON DELETE CASCADE: an owner's secrets go only through DeleteOwner, which
-- records each one.
CREATE TABLE IF NOT EXISTS secrets_values (
    owner_type TEXT        NOT NULL,
    owner_id   TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    version    INT         NOT NULL,
    key_id     TEXT        NOT NULL,
    ciphertext TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT secrets_values_pk PRIMARY KEY (owner_type, owner_id, name),
    CONSTRAINT secrets_values_owner_fk FOREIGN KEY (owner_type, owner_id)
        REFERENCES secrets_owner_keys (owner_type, owner_id)
);

CREATE TABLE IF NOT EXISTS secrets_audit (
    id          TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    action      TEXT        NOT NULL
                CHECK (action IN ('owner_key_created','owner_key_rewrapped','owner_key_rotated','owner_key_destroyed','secret_put','secret_read','secret_deleted')),
    owner_type  TEXT        NOT NULL,
    owner_id    TEXT        NOT NULL,
    name        TEXT        NOT NULL DEFAULT '',
    version     INT         NOT NULL DEFAULT 0,
    key_id      TEXT        NOT NULL DEFAULT '',
    actor_type  TEXT        NOT NULL DEFAULT '',
    actor_id    TEXT        NOT NULL DEFAULT '',
    system      TEXT        NOT NULL DEFAULT '',
    reason      TEXT        NOT NULL DEFAULT '',
    CONSTRAINT secrets_audit_pk PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS idx_secrets_audit_owner ON secrets_audit (owner_type, owner_id, occurred_at, id);
CREATE INDEX IF NOT EXISTS idx_secrets_audit_time ON secrets_audit (occurred_at, id);
