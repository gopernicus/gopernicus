-- The scope revision anchors (migration source "authorization"). One row per
-- authorization scope; its `revision` is the monotonically increasing anchor the
-- atomic mutation repositories bump (exactly once per applied change) and validate
-- under lock. It is the concurrency spine of authorization v3's write path
-- (mutation.ScopeKey / mutation.Revision, AZ3-0.4). Turso dialect of the pgx 0003
-- — IDENTICAL filename so the version set matches across dialects.
--
-- Scope kinds (mutation.ScopeKind), keyed (scope_kind, scope_type, scope_id):
--   * resource — serializes RELATIONSHIP mutations and SCOPED-role mutations on
--     one resource (Type, ID).
--   * subject  — serializes GLOBAL-role mutations for one subject (Type, ID);
--     global roles have no resource, so their revision anchor is the subject.
--
-- An ABSENT anchor reads as revision 0 (the mutation contract): a concurrent
-- first writer is therefore a detectable 0→1 change, never a phantom. The
-- DEFAULT 0 lets a repository seed a bare revision-0 anchor by key alone before
-- locking. The (scope_kind, scope_type, scope_id) PRIMARY KEY is the lock/read
-- key the repositories order canonically; no separate index is needed.
--
-- Constraints: ck_iam_scopes_kind pins a valid scope kind, ck_iam_scopes_nonempty
-- keeps the structural key columns non-empty, and ck_iam_scopes_revision forbids a
-- negative revision (revision is a mutation.Revision / uint64 counter; SQLite's
-- 64-bit INTEGER holds the full monotonic range in practice).
CREATE TABLE IF NOT EXISTS iam_scopes (
    scope_kind TEXT    NOT NULL,
    scope_type TEXT    NOT NULL,
    scope_id   TEXT    NOT NULL,
    revision   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scope_kind, scope_type, scope_id),
    CONSTRAINT ck_iam_scopes_kind     CHECK (scope_kind IN ('resource', 'subject')),
    CONSTRAINT ck_iam_scopes_nonempty CHECK (scope_type <> '' AND scope_id <> ''),
    CONSTRAINT ck_iam_scopes_revision CHECK (revision >= 0)
);
