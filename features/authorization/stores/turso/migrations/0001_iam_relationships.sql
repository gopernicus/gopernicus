-- The relationship (ReBAC) kind's tuple store (migration source "authorization";
-- unique vs "cms"/"auth"/"jobs"/"events"). One row per Zanzibar-style tuple:
-- "subject has relation on resource", where an optional subject_relation names a
-- userset (group:eng#member) rather than a concrete principal. Turso dialect:
-- timestamps are fixed-width ISO-8601 TEXT (lexicographic order == chronological
-- order, which the keyset listings' created_at ordering relies on).
--
-- Rows are IMMUTABLE (no updated_at): a relationship is deleted and recreated,
-- never mutated — created_at is the keyset order column and relationship_id the
-- tiebreak. relationship_id carries an INLINE DEFAULT (Q6): under a
-- cryptids.Database wiring the engine mints no id, the store omits the
-- relationship_id column for the whole batch, and this DEFAULT fills the key
-- (there is no RETURNING — CreateRelationships is error-only).
CREATE TABLE IF NOT EXISTS iam_relationships (
    relationship_id  TEXT NOT NULL PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    resource_type    TEXT NOT NULL,
    resource_id      TEXT NOT NULL,
    relation         TEXT NOT NULL,
    subject_type     TEXT NOT NULL,
    subject_id       TEXT NOT NULL,
    subject_relation TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL
);

-- Unique-tuple index: no duplicate (resource, relation, subject) rows. Plain
-- columns (no COALESCE): subject_relation is NOT NULL DEFAULT '' here, so the
-- original's nullable-column + COALESCE(subject_relation, '') collapses to a
-- direct index and duplicate direct tuples cannot coexist under either dialect's
-- NULL semantics.
CREATE UNIQUE INDEX IF NOT EXISTS idx_iam_relationships_unique_tuple
    ON iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation);

-- Unique-subject index: one relation per subject per resource (owner OR member,
-- never both — the schema's AnyOf handles implication; a role change is
-- delete+create). A second, different relation for the same (resource, subject)
-- conflicts here and is a SILENT no-op under the store's bare ON CONFLICT DO
-- NOTHING (Q7). Plain columns for the same NOT-NULL reason as above.
CREATE UNIQUE INDEX IF NOT EXISTS idx_iam_relationships_unique_subject
    ON iam_relationships (resource_type, resource_id, subject_type, subject_id, subject_relation);

-- Secondary: "what subjects relate to this resource?" (GetRelationTargets,
-- DeleteResourceRelationships, ListRelationshipsByResource).
CREATE INDEX IF NOT EXISTS idx_iam_relationships_resource
    ON iam_relationships (resource_type, resource_id);

-- Secondary: "what resources does this subject relate to?" (permission checks,
-- ListRelationshipsBySubject, subject cleanup).
CREATE INDEX IF NOT EXISTS idx_iam_relationships_subject
    ON iam_relationships (subject_type, subject_id);

-- Secondary: "find all <relation> tuples of resource type <t>" — batch/direct
-- checks and the group-expansion recursive CTE.
CREATE INDEX IF NOT EXISTS idx_iam_relationships_type_relation
    ON iam_relationships (resource_type, relation);
