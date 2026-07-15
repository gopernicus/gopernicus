-- The roles kind's assignment store (migration source "authorization"). One row
-- per grant: a subject holds an opaque role, optionally scoped to a resource.
-- Plain lookups only — no schema, no graph walk, no recursion anywhere near this
-- table. PostgreSQL flavor of the turso 0002 — IDENTICAL filename so the version
-- set matches across dialects. created_at is TIMESTAMPTZ, STORE-STAMPED via the
-- connector helpers; a duplicate Assign retains the original timestamp (ON
-- CONFLICT DO NOTHING).
--
-- The (resource_type, resource_id) pair scopes an assignment; the empty pair
-- ('', '') is a GLOBAL grant. Both scope columns are NOT NULL DEFAULT '' — never
-- NULL — so a global assignment participates in the unique index (a nullable
-- scope would make two ('', '') rows DISTINCT under PostgreSQL NULL semantics,
-- silently duplicating global grants).
--
-- v3 canonical greenfield schema (authorizationv3, AZ3-2.1). Two constraints pin
-- the row shape: ck_iam_roles_nonempty keeps the structural subject/role columns
-- non-empty, and ck_iam_roles_scope_pair enforces a CONSISTENT global/scoped
-- pair — both scope columns empty (a global grant) or both non-empty (a scoped
-- grant), never a half-populated ('x', '') that would be neither honest scope.
-- Contractual COLLATE "C" (AAH-5 / plan D5): every structural text column
-- participates in a derived ordering key. ListBySubject/ListByResource page by
-- created_at DESC with a role_key tiebreak — subject_type||chr(1)||subject_id||
-- chr(1)||role||chr(1)||resource_type||chr(1)||resource_id — and
-- ListEffectiveByResource orders by grant_key (subject_type||chr(1)||subject_id||
-- chr(1)||role). The reference store orders those concatenated keys byte-wise (Go
-- string compare). A concatenation's collation is derived from its column
-- operands, so ALL five must be COLLATE "C" for the derived key to sort byte-wise
-- (collating only some would raise an indeterminate-collation error when the key
-- is compared). The chr(1) separators are coercible and combine without conflict.
CREATE TABLE IF NOT EXISTS iam_roles (
    subject_type  TEXT COLLATE "C" NOT NULL,
    subject_id    TEXT COLLATE "C" NOT NULL,
    role          TEXT COLLATE "C" NOT NULL,
    resource_type TEXT COLLATE "C" NOT NULL DEFAULT '',
    resource_id   TEXT COLLATE "C" NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_iam_roles_nonempty CHECK (
        subject_type <> '' AND subject_id <> '' AND role <> ''
    ),
    CONSTRAINT ck_iam_roles_scope_pair CHECK (
        (resource_type = '' AND resource_id = '')
        OR (resource_type <> '' AND resource_id <> '')
    )
);

-- Unique 5-tuple: the natural key and the ON CONFLICT target the idempotent
-- Assign uses (ON CONFLICT (subject_type, subject_id, role, resource_type,
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
