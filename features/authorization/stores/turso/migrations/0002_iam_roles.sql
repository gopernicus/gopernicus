-- The roles kind's assignment store (migration source "authorization"). One row
-- per grant: a subject holds an opaque role, optionally scoped to a resource.
-- Plain lookups only — no schema, no graph walk, no recursion anywhere near this
-- table. Turso dialect: created_at is a fixed-width ISO-8601 TEXT timestamp,
-- STORE-STAMPED via the connector helpers; a duplicate Assign retains the
-- original timestamp (ON CONFLICT DO NOTHING).
--
-- The (resource_type, resource_id) pair scopes an assignment; the empty pair
-- ("", "") is a GLOBAL grant. Both scope columns are NOT NULL DEFAULT '' — never
-- NULL — so a global assignment participates in the unique index under both
-- dialects (a nullable scope would make two ("", "") rows DISTINCT under SQL NULL
-- semantics, silently duplicating global grants).
CREATE TABLE IF NOT EXISTS iam_roles (
    subject_type  TEXT NOT NULL,
    subject_id    TEXT NOT NULL,
    role          TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id   TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

-- Unique 5-tuple: the natural key and the ON CONFLICT target the idempotent
-- Assign uses (ON CONFLICT(subject_type, subject_id, role, resource_type,
-- resource_id) DO NOTHING). Exact-match HasExactRole rides this index too.
CREATE UNIQUE INDEX IF NOT EXISTS idx_iam_roles_unique
    ON iam_roles (subject_type, subject_id, role, resource_type, resource_id);

-- Secondary: "what roles does this subject hold?" (ListBySubject).
CREATE INDEX IF NOT EXISTS idx_iam_roles_subject
    ON iam_roles (subject_type, subject_id);

-- Secondary: "what assignments are scoped to this resource?" (ListByResource) —
-- the (resource_type, resource_id) filter with the created_at keyset order.
CREATE INDEX IF NOT EXISTS idx_iam_roles_resource
    ON iam_roles (resource_type, resource_id, created_at);
