-- Identity-discovery addresses (auth-v3 §2.1). A user is the stable subject;
-- user_identifiers holds the email/phone addresses that subject is found or
-- contacted by, with explicit login/recovery/notification uses. Passwords, OAuth
-- accounts, and future MFA authenticators stay in their own typed tables — this is
-- never a uniform accounts table.
--
-- kind is closed to {email, phone} (a DB CHECK backs the domain's closed
-- vocabulary). verified_at NULL is the unverified sentinel (a proof TIME, not a
-- boolean, is kept for lifecycle/risk policy). replaced_at NULL is the active
-- sentinel; retirement is history-preserving, not a hard delete, so active reads
-- filter replaced_at IS NULL. Booleans are 0/1; timestamps are fixed-width TEXT.
-- id defaults DB-side (empty id on Create → the schema default generates it;
-- RETURNING reads it back). user_id references users.id by convention (no enforced
-- FK — matching every other auth table's logged decision; the aggregate atomicity
-- lives in CreateWithPrimaryIdentifier/ApplyVerifiedChange transactions, not a FK).
CREATE TABLE IF NOT EXISTS user_identifiers (
    id                   TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id              TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    normalized_value     TEXT NOT NULL,
    verified_at          TEXT,
    login_enabled        INTEGER NOT NULL DEFAULT 0,
    recovery_enabled     INTEGER NOT NULL DEFAULT 0,
    notification_enabled INTEGER NOT NULL DEFAULT 1,
    is_primary           INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    replaced_at          TEXT
);

-- The authentication claim: an active login- or recovery-enabled (kind, value)
-- resolves to exactly one subject. PARTIAL over active claiming rows only, so a
-- shared household phone may be notification-only on many accounts but cannot
-- identify two login/recovery subjects. A lost race surfaces as sdk.ErrAlreadyExists.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_auth_claim
    ON user_identifiers (kind, normalized_value)
    WHERE replaced_at IS NULL AND (login_enabled = 1 OR recovery_enabled = 1);

-- At most one active primary per (user, kind). PARTIAL over active primaries.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_identifiers_primary
    ON user_identifiers (user_id, kind)
    WHERE replaced_at IS NULL AND is_primary = 1;

-- ListByUser / active-read support, ordered by (created_at) then id.
CREATE INDEX IF NOT EXISTS idx_user_identifiers_user_active
    ON user_identifiers (user_id, kind, created_at)
    WHERE replaced_at IS NULL;
