-- The identity aggregate (auth-v3 §2.1): the stable human subject and profile
-- only. The addresses by which the subject is found or contacted live in
-- user_identifiers (0010); the users table carries no email/verification column.
-- created_at/updated_at are TIMESTAMPTZ (microsecond precision — the keyset
-- tie-break on (created_at, id) is load-bearing).
-- id defaults DB-side (a cryptids.Database host sends an empty id; the store
-- omits the column and RETURNING reads the generated key back).
-- auth_revision (auth-v3 §2.1/§5.6) is the optimistic-serialization anchor for
-- cross-table credential-policy mutations: ApplyVerifiedChange and the credential
-- mutation Apply CAS on this single counter (no separate revisions table). BIGINT
-- matches the int64 domain field the CAS scans and increments.
CREATE TABLE IF NOT EXISTS users (
    id             TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    display_name   TEXT NOT NULL DEFAULT '',
    auth_revision  BIGINT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL
);
