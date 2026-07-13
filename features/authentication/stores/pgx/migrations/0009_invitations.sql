-- Resource invitations (design §6), decoupled from ReBAC: the grant on acceptance
-- rides a host-supplied Granter and visibility rides these columns (identifier,
-- invited_by, resolved_subject_id), never authorization tuples. token_hash is the
-- SHA-256 of the mailed secret. identifier_kind is the invitee address KIND
-- (identity.KindEmail default, identity.KindPhone, or any open string a wired
-- notifier declares — identity-resolution P3). At most ONE pending invitation may
-- exist per (resource_type, resource_id, identifier_kind, identifier, relation) —
-- a PARTIAL unique index over pending rows only, so a new pending invite for the
-- same tuple succeeds once a prior one moves off pending, and the same value may
-- be pending under two kinds at once (cross-kind coexistence). auto_accept is a
-- native BOOLEAN; accepted_at is nullable; expires_at/created_at/updated_at are
-- TIMESTAMPTZ.
-- id defaults DB-side (a cryptids.Database host sends Create an empty id; the
-- store omits the column and RETURNING reads the generated key back).
CREATE TABLE IF NOT EXISTS invitations (
    id                  TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    resource_type       TEXT NOT NULL,
    resource_id         TEXT NOT NULL,
    relation            TEXT NOT NULL,
    identifier          TEXT NOT NULL,
    identifier_kind     TEXT NOT NULL DEFAULT 'email',
    resolved_subject_id TEXT NOT NULL DEFAULT '',
    invited_by          TEXT NOT NULL,
    token_hash          TEXT NOT NULL,
    auto_accept         BOOLEAN NOT NULL DEFAULT FALSE,
    status              TEXT NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    accepted_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL
);

-- token_hash uniqueness + the GetByTokenHash lookup index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_token_hash ON invitations (token_hash);

-- One pending invitation per (resource_type, resource_id, identifier_kind,
-- identifier, relation) — partial over pending rows only, so the tuple frees up
-- once a row moves off pending.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_pending_tuple
    ON invitations (resource_type, resource_id, identifier_kind, identifier, relation)
    WHERE status = 'pending';

-- ListByResource + resolve-on-registration lookup support (plan-cut named indexes).
CREATE INDEX IF NOT EXISTS idx_invitations_resource ON invitations (resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_invitations_resolved_subject_id ON invitations (resolved_subject_id);

-- ListBySubject ("mine" + resolve-on-registration) filters (identifier_kind,
-- identifier); the pending-tuple unique index leads with resource columns and
-- cannot serve this kind-first lookup (design §7 re-key).
CREATE INDEX IF NOT EXISTS idx_invitations_kind_identifier ON invitations (identifier_kind, identifier);
