-- The external identity mirror: which local user an external identity provider's
-- subject maps to. provider is the HOST-CHOSEN provider name, not the token iss —
-- one provider issues one subject under several issuers (an ID token and a session
-- cookie), and each must reach the same user. A provider subject belongs to at
-- most one local user; a colliding insert surfaces as sdk.ErrAlreadyExists via
-- MapError, which rolls a Users.Provision back whole.
--
-- It is deliberately NOT oauth_accounts: mirror rows are never login methods,
-- never user-unlinkable, and hold no provider tokens. issuer and email are
-- informational. first_seen_at is TIMESTAMPTZ. user_id references users.id by
-- convention (no enforced FK). APPEND-ONLY: it adds a table and touches no
-- existing one.
CREATE TABLE IF NOT EXISTS external_subjects (
    provider      TEXT NOT NULL,
    subject       TEXT NOT NULL,
    user_id       TEXT COLLATE "C" NOT NULL,
    issuer        TEXT NOT NULL DEFAULT '',
    email         TEXT NOT NULL DEFAULT '',
    first_seen_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (provider, subject)
);

-- ListByUser support.
CREATE INDEX IF NOT EXISTS idx_external_subjects_user_id ON external_subjects (user_id);
