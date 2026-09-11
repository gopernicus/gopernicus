-- Natural-key cursors reserve U+0001 as a separator. Reject legacy violations
-- before dropping metadata so the host can repair the unchanged source rows.
CREATE TEMP TABLE iam_tuple_key_preflight (
    valid INTEGER CONSTRAINT ck_iam_tuple_keys_no_separator CHECK (valid = 1)
);
INSERT INTO iam_tuple_key_preflight (valid)
SELECT 0 WHERE EXISTS (SELECT 1 FROM iam_relationships WHERE
    strpos(resource_type, chr(1)) > 0
    OR strpos(resource_id, chr(1)) > 0
    OR strpos(relation, chr(1)) > 0
    OR strpos(subject_type, chr(1)) > 0
    OR strpos(subject_id, chr(1)) > 0
    OR strpos(subject_relation, chr(1)) > 0)
OR EXISTS (SELECT 1 FROM iam_roles WHERE
    strpos(subject_type, chr(1)) > 0
    OR strpos(subject_id, chr(1)) > 0
    OR strpos(role, chr(1)) > 0
    OR strpos(resource_type, chr(1)) > 0
    OR strpos(resource_id, chr(1)) > 0);
DROP TABLE iam_tuple_key_preflight;

-- Relationships are identified by the complete tuple. Existing exact-tuple
-- uniqueness makes this primary-key change lossless; the independent exclusive
-- subject constraint remains in force. Apply while authorization writers are
-- stopped: older binaries still require the removed metadata columns.
ALTER TABLE iam_relationships
    DROP COLUMN relationship_id,
    DROP COLUMN created_at,
    ADD PRIMARY KEY (resource_type, resource_id, relation, subject_type, subject_id, subject_relation);

-- The primary key now supplies exact-tuple uniqueness.
DROP INDEX idx_iam_relationships_unique_tuple;

DROP INDEX idx_iam_roles_resource;
ALTER TABLE iam_roles DROP COLUMN created_at;
CREATE INDEX idx_iam_roles_resource ON iam_roles (resource_type, resource_id);
