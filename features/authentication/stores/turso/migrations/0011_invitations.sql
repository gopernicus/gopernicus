-- Resource invitations (design §6), decoupled from ReBAC: the grant on acceptance
-- rides a host-supplied Granter and visibility rides these columns (identifier,
-- invited_by, resolved_subject_id), never authorization tuples. token_hash is the
-- SHA-256 of the mailed secret. At most ONE pending invitation may exist per
-- (resource_type, resource_id, identifier, relation) — a PARTIAL unique index over
-- pending rows only, so a new pending invite for the same tuple succeeds once a
-- prior one moves off pending. auto_accept is 0/1; accepted_at is nullable;
-- expires_at/created_at/updated_at are fixed-width TEXT timestamps.
CREATE TABLE IF NOT EXISTS invitations (
    id                  TEXT PRIMARY KEY,
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

-- token_hash uniqueness + the GetByTokenHash lookup index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_token_hash ON invitations (token_hash);

-- One pending invitation per (resource_type, resource_id, identifier, relation) —
-- partial over pending rows only, so the tuple frees up once a row moves off
-- pending.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_pending_tuple
    ON invitations (resource_type, resource_id, identifier, relation)
    WHERE status = 'pending';

-- ListByResource + resolve-on-registration lookup support (plan-cut named indexes).
CREATE INDEX IF NOT EXISTS idx_invitations_resource ON invitations (resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_invitations_resolved_subject_id ON invitations (resolved_subject_id);
