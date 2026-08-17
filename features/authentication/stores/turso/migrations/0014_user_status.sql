-- Account lifecycle status (coordination-hub-auth-upstream CHAU-1.2). APPEND-ONLY:
-- 0001_users.sql is already tagged and immutable, so the lifecycle columns arrive
-- as an ALTER here. A host that copied 0001-0013 must copy and apply THIS file
-- before deploying a binary built against the new store tag.
--
-- This is the dialect sibling of the pgx 0014: same filename, same columns, same
-- closed vocabulary, same directory index.
--
-- status is the CLOSED two-value vocabulary (domain/user.Status): 'active' is the
-- default and the backfill for every pre-existing row — SQLite's ADD COLUMN fills
-- existing rows from the DEFAULT, so no separate UPDATE is needed — and
-- 'deactivated' denies every new session and act-as-user authentication. The CHECK
-- is the schema half of the closed vocabulary: the store reader normalizes a legacy
-- EMPTY value to active, but with this constraint applied no empty or arbitrary
-- value can be persisted. Suspension, deletion, anonymization, and lock-until are
-- deliberately NOT values here.
--
-- SQLite has no ADD COLUMN IF NOT EXISTS; the migration ledger's applied-once
-- guarantee is what makes this safe to ship, exactly as it is for every CREATE
-- TABLE in this tree.
ALTER TABLE users
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'deactivated'));

-- status_changed_at is NULL until the first transition (the zero-value domain
-- sentinel), stored as a fixed-width TEXT timestamp like every other time column
-- in this tree. "Never transitioned" and "transitioned at account creation" are
-- different facts, so it is deliberately not defaulted.
ALTER TABLE users ADD COLUMN status_changed_at TEXT;

-- The directory's contractual order. users previously had no ordered access path
-- at all (PRIMARY KEY (id) only), so an operator listing would scan and sort the
-- whole table. It is a composite on exactly the contractual keyset (created_at,
-- id) and supports both the forward and backward cursor scans.
--
-- No collation pin is needed on this side: SQLite's default TEXT collation is
-- BINARY, which already IS the byte-wise order the pgx sibling pins with
-- COLLATE "C" and the storetest collision case asserts.
CREATE INDEX IF NOT EXISTS idx_users_created_at_id
    ON users (created_at DESC, id DESC);
