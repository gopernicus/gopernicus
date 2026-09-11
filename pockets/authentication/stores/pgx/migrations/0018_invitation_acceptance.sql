-- An acceptance claim reserves the tuple until its idempotent host grant is finalized.
ALTER TABLE invitations ADD COLUMN resolved_subject_type TEXT NOT NULL DEFAULT '';
DROP INDEX idx_invitations_pending_tuple;
CREATE UNIQUE INDEX idx_invitations_pending_tuple
    ON invitations (resource_type, resource_id, identifier_kind, identifier, relation)
    WHERE status IN ('pending', 'accepting');
