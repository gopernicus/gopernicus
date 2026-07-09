-- Database-generated entity keys (segovia-lessons phase 04, amended D10): a
-- host that wires cryptids.Database sends Create an empty id; the store omits
-- the id column and these defaults generate the key (32 hex chars from 16
-- random bytes), read back with RETURNING. SQLite cannot ALTER a column
-- default, so each entity table is rebuilt: create-with-default, copy, drop,
-- rename, recreate indexes. Column order matches the original CREATEs exactly
-- (the INSERT ... SELECT * copy depends on it). Secret-keyed tables (sessions,
-- verification codes/tokens, oauth states) get NO default: an empty secret is
-- a bug, never a strategy.

-- users (0001)
CREATE TABLE users_new (
    id             TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    email          TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL DEFAULT '',
    email_verified INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
INSERT INTO users_new SELECT * FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

-- service_accounts (0008)
CREATE TABLE service_accounts_new (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL,
    act_as_user   INTEGER NOT NULL DEFAULT 0,
    owner_user_id TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
INSERT INTO service_accounts_new SELECT * FROM service_accounts;
DROP TABLE service_accounts;
ALTER TABLE service_accounts_new RENAME TO service_accounts;

-- api_keys (0009)
CREATE TABLE api_keys_new (
    id                 TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    service_account_id TEXT NOT NULL,
    name               TEXT NOT NULL DEFAULT '',
    key_prefix         TEXT NOT NULL DEFAULT '',
    key_hash           TEXT NOT NULL,
    expires_at         TEXT,
    revoked_at         TEXT,
    last_used_at       TEXT,
    created_at         TEXT NOT NULL
);
INSERT INTO api_keys_new SELECT * FROM api_keys;
DROP TABLE api_keys;
ALTER TABLE api_keys_new RENAME TO api_keys;
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys (key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_service_account_id ON api_keys (service_account_id);

-- security_events (0010)
CREATE TABLE security_events_new (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id      TEXT NOT NULL DEFAULT '',
    actor_type   TEXT NOT NULL DEFAULT '',
    actor_id     TEXT NOT NULL DEFAULT '',
    event_type   TEXT NOT NULL,
    event_status TEXT NOT NULL,
    details      TEXT NOT NULL DEFAULT '{}',
    ip_address   TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);
INSERT INTO security_events_new SELECT * FROM security_events;
DROP TABLE security_events;
ALTER TABLE security_events_new RENAME TO security_events;
CREATE INDEX IF NOT EXISTS idx_security_events_created_at_id ON security_events (created_at, id);
CREATE INDEX IF NOT EXISTS idx_security_events_user_id ON security_events (user_id);
CREATE INDEX IF NOT EXISTS idx_security_events_event_type ON security_events (event_type);
CREATE INDEX IF NOT EXISTS idx_security_events_event_status ON security_events (event_status);

-- invitations (0011)
CREATE TABLE invitations_new (
    id                  TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    resource_type       TEXT NOT NULL,
    resource_id         TEXT NOT NULL,
    relation            TEXT NOT NULL,
    identifier          TEXT NOT NULL,
    resolved_subject_id TEXT NOT NULL DEFAULT '',
    invited_by          TEXT NOT NULL,
    token_hash          TEXT NOT NULL,
    auto_accept         INTEGER NOT NULL DEFAULT 0,
    status              TEXT NOT NULL,
    expires_at          TEXT NOT NULL,
    accepted_at         TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
INSERT INTO invitations_new SELECT * FROM invitations;
DROP TABLE invitations;
ALTER TABLE invitations_new RENAME TO invitations;
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_token_hash ON invitations (token_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_pending_tuple
    ON invitations (resource_type, resource_id, identifier, relation)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_invitations_resource ON invitations (resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_invitations_resolved_subject_id ON invitations (resolved_subject_id);
