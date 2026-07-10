-- Kind-aware invitations (identity-resolution P3): the invitee identifier gains
-- an address KIND (identity.KindEmail default, identity.KindPhone, or any open
-- string a wired notifier declares). Existing rows are all email — the literal
-- default 'email' is identity.KindEmail's value, so the backfill is exact and
-- safe. The pending-tuple uniqueness widens to include identifier_kind, so the
-- same value may have a pending invitation under two kinds at once (the
-- cross-kind-coexistence case). Dropping and recreating the partial unique index
-- is mandatory — the old key had no kind column.
ALTER TABLE invitations ADD COLUMN identifier_kind TEXT NOT NULL DEFAULT 'email';

DROP INDEX IF EXISTS idx_invitations_pending_tuple;
CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_pending_tuple
    ON invitations (resource_type, resource_id, identifier_kind, identifier, relation)
    WHERE status = 'pending';
