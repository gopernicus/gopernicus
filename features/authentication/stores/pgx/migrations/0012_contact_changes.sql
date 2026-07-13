-- The pending-value flow state for an email/phone add-or-change (auth-v3 §2.4). It
-- holds the PENDING new address between a change flow's start and confirm — a home
-- that is deliberately NOT challenge.context (the freeze-transfer clause: context is
-- a binding validator, never a payload channel) and NOT accreted pending_* columns
-- on users (the column-sprawl §2 refuses). A change flow is one challenge (purpose
-- change_email/change_phone, carrying the secret + lockout) plus one contact_changes
-- row (this table, carrying the pending value + requested uses). This row carries NO
-- secret.
--
-- new_value is already normalized. The use flags, make_primary, and
-- replaces_identifier_id line up one-to-one with identifier.ApplyVerifiedChangeInput
-- so the confirm step is a plain field copy. kind is closed to {email, phone}.
-- One pending change per (user_id, kind): Create is delete-before-create and this
-- unique index is the backstop. Consume is a single-use get-and-delete; an expired
-- row is deleted and returns sdk.ErrExpired.
-- id defaults DB-side; user_id references users.id by convention (no enforced FK).
CREATE TABLE IF NOT EXISTS contact_changes (
    id                     TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id                TEXT NOT NULL,
    kind                   TEXT NOT NULL CHECK (kind IN ('email', 'phone')),
    new_value              TEXT NOT NULL,
    login_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    notification_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    make_primary           BOOLEAN NOT NULL DEFAULT FALSE,
    replaces_identifier_id TEXT NOT NULL DEFAULT '',
    expires_at             TIMESTAMPTZ NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL
);

-- One pending change per (user, kind); the same value may be pending under two
-- kinds at once (cross-kind coexistence).
CREATE UNIQUE INDEX IF NOT EXISTS idx_contact_changes_user_kind
    ON contact_changes (user_id, kind);
