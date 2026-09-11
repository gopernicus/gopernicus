-- Natural-key cursors reserve U+0001 as a separator. Reject legacy violations
-- before dropping metadata so the host can repair the unchanged source rows.
-- libSQL's remote parser does not support TEMP; this table exists only within
-- the migration transaction and is dropped before it commits.
CREATE TABLE iam_tuple_key_preflight (
    valid INTEGER CONSTRAINT ck_iam_tuple_keys_no_separator CHECK (valid = 1)
);
INSERT INTO iam_tuple_key_preflight (valid)
SELECT 0 WHERE EXISTS (SELECT 1 FROM iam_relationships WHERE
    instr(resource_type, char(1)) > 0
    OR instr(resource_id, char(1)) > 0
    OR instr(relation, char(1)) > 0
    OR instr(subject_type, char(1)) > 0
    OR instr(subject_id, char(1)) > 0
    OR instr(subject_relation, char(1)) > 0)
OR EXISTS (SELECT 1 FROM iam_roles WHERE
    instr(subject_type, char(1)) > 0
    OR instr(subject_id, char(1)) > 0
    OR instr(role, char(1)) > 0
    OR instr(resource_type, char(1)) > 0
    OR instr(resource_id, char(1)) > 0);
DROP TABLE iam_tuple_key_preflight;

-- SQLite requires a table rebuild to replace a primary key. The migration
-- runner holds one transaction across the copy, replacement, and index rebuild.
-- Stop authorization writers first: older binaries require the removed metadata.
CREATE TABLE iam_relationships_tuple (
    resource_type    TEXT NOT NULL,
    resource_id      TEXT NOT NULL,
    relation         TEXT NOT NULL,
    subject_type     TEXT NOT NULL,
    subject_id       TEXT NOT NULL,
    subject_relation TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (resource_type, resource_id, relation, subject_type, subject_id, subject_relation),
    CONSTRAINT ck_iam_relationships_nonempty CHECK (
        resource_type <> '' AND resource_id <> '' AND relation <> ''
        AND subject_type <> '' AND subject_id <> ''
    )
);

INSERT INTO iam_relationships_tuple (resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
SELECT resource_type, resource_id, relation, subject_type, subject_id, subject_relation FROM iam_relationships;

DROP TABLE iam_relationships;
ALTER TABLE iam_relationships_tuple RENAME TO iam_relationships;

CREATE UNIQUE INDEX idx_iam_relationships_unique_subject
    ON iam_relationships (resource_type, resource_id, subject_type, subject_id, subject_relation);
CREATE INDEX idx_iam_relationships_resource
    ON iam_relationships (resource_type, resource_id);
CREATE INDEX idx_iam_relationships_subject
    ON iam_relationships (subject_type, subject_id);
CREATE INDEX idx_iam_relationships_type_relation
    ON iam_relationships (resource_type, relation);
CREATE INDEX idx_iam_relationships_type_relation_resource
    ON iam_relationships (resource_type, relation, resource_id);

DROP INDEX idx_iam_roles_resource;
ALTER TABLE iam_roles DROP COLUMN created_at;
CREATE INDEX idx_iam_roles_resource ON iam_roles (resource_type, resource_id);
