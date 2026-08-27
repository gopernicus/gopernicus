-- The identity aggregate (auth-v3 §2.1): the stable human subject and profile
-- only. The addresses by which the subject is found or contacted live in
-- user_identifiers (0010); the users table carries no email/verification column.
-- created_at/updated_at are fixed-width TEXT timestamps.
-- id defaults DB-side (a cryptids.Database host sends an empty id; the store
-- omits the column and RETURNING reads the generated key back).
-- auth_revision (auth-v3 §2.1/§5.6) is the optimistic-serialization anchor for
-- cross-table credential-policy mutations: ApplyVerifiedChange and the credential
-- mutation Apply CAS on this single counter (no separate revisions table).
-- INTEGER is SQLite's native 64-bit integer, matching the int64 domain field.
CREATE TABLE IF NOT EXISTS users (
    id             TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    display_name   TEXT NOT NULL DEFAULT '',
    auth_revision  INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
