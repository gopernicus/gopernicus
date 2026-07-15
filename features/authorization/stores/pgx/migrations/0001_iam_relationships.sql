-- The relationship (ReBAC) kind's tuple store (migration source "authorization";
-- unique vs "cms"/"auth"/"jobs"/"events"). One row per Zanzibar-style tuple:
-- "subject has relation on resource", where an optional subject_relation names a
-- userset (group:eng#member) rather than a concrete principal. PostgreSQL flavor
-- of the turso 0001 — IDENTICAL filename so the version set matches across
-- dialects; representation changes, structure and semantics do not. Timestamps
-- are TIMESTAMPTZ (postgres orders them natively; no lexicographic-TEXT
-- convention needed).
--
-- Rows are IMMUTABLE (no updated_at): a relationship is deleted and recreated,
-- never mutated — created_at is the keyset order column and relationship_id the
-- tiebreak. relationship_id carries an INLINE DEFAULT (Q6): under a
-- cryptids.Database wiring the engine mints no id, the store drops the
-- relationship_id column from the UNNEST insert, and this DEFAULT fills the key
-- (there is no RETURNING — CreateRelationships is error-only). gen_random_uuid()
-- is built into PostgreSQL 13+; the ::text cast keeps the key a TEXT column,
-- matching the rim and the turso dialect's lower(hex(randomblob(16))).
--
-- v3 canonical greenfield schema (authorizationv3, AZ3-2.1; folded clean because
-- no module tag exists). The ck_iam_relationships_nonempty constraint pins the
-- structural columns non-empty at the storage layer — a defense-in-depth mirror
-- of the domain's ValidateRefField. subject_relation is DELIBERATELY excluded: it
-- is the exact userset relation, NOT NULL but legitimately empty for a concrete
-- subject and non-empty (group:eng#member) for a userset — the non-empty check
-- must never conflate that relation state.
CREATE TABLE IF NOT EXISTS iam_relationships (
    relationship_id  TEXT        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid()::text,
    resource_type    TEXT        NOT NULL,
    resource_id      TEXT        NOT NULL,
    relation         TEXT        NOT NULL,
    subject_type     TEXT        NOT NULL,
    subject_id       TEXT        NOT NULL,
    subject_relation TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_iam_relationships_nonempty CHECK (
        resource_type <> '' AND resource_id <> '' AND relation <> ''
        AND subject_type <> '' AND subject_id <> ''
    )
);

-- Unique-tuple index: no duplicate (resource, relation, subject) rows. Plain
-- columns (no COALESCE): subject_relation is NOT NULL DEFAULT '' here, so the
-- original's nullable-column + COALESCE(subject_relation, '') collapses to a
-- direct index and duplicate direct tuples cannot coexist under PostgreSQL's NULL
-- semantics.
CREATE UNIQUE INDEX IF NOT EXISTS idx_iam_relationships_unique_tuple
    ON iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation);

-- Unique-subject index: ONE relation per exact SubjectRef per resource (the
-- ratified one-relation rule, default #1). The key is (resource, subject_type,
-- subject_id, subject_relation) WITHOUT `relation`, so a subject already related
-- to the resource cannot acquire a second, different relation. Because
-- subject_relation IS in the key, group:eng#member and group:eng#admin are
-- distinct exact SubjectRefs, each free to hold its own one relation — relation
-- state is preserved, never conflated. Under v3 a conflicting second relation is
-- surfaced as an explicit semantic_conflict outcome (not a silent ON CONFLICT DO
-- NOTHING) and resolved atomically by OpReplace; this index is the arbiter the
-- atomic mutation repositories (AZ3-2.3/2.4) lock and read against. Plain columns
-- for the same NOT-NULL reason as above.
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
