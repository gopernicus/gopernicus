-- Account lifecycle status (coordination-hub-auth-upstream CHAU-1.2). APPEND-ONLY:
-- 0001_users.sql is already tagged and immutable, so the lifecycle columns arrive
-- as an ALTER here. A host that copied 0001-0013 must copy and apply THIS file
-- before deploying a binary built against the new store tag.
--
-- status is the CLOSED two-value vocabulary (domain/user.Status): 'active' is the
-- default and the backfill for every pre-existing row — a user created before this
-- concept existed was, by definition, usable — and 'deactivated' denies every new
-- session and act-as-user authentication. The CHECK is the schema half of the
-- closed vocabulary: the store reader normalizes a legacy EMPTY value to active,
-- but with this constraint applied no empty or arbitrary value can be persisted.
-- Suspension, deletion, anonymization, and lock-until are deliberately NOT values
-- here; adding one is a design decision, not a constraint edit.
--
-- status_changed_at is NULL until the first transition (the zero-value domain
-- sentinel). It is not a NOT NULL column defaulted to now(): "never transitioned"
-- and "transitioned at account creation" are different facts.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'deactivated'));

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ;

-- users.id becomes a CONTRACTUAL keyset tiebreak with this release: the operator
-- directory pages users by (created_at DESC, id DESC), and the storetest collision
-- case pins that tiebreak to the reference store's byte-wise (Go string) order. It
-- therefore joins the COLLATE "C" inventory (AAH-5 / plan D5) that every other
-- paged auth table already carries in its CREATE TABLE.
--
-- It arrives as an ALTER because 0001 is immutable. This is a REWRITING DDL: it
-- takes an ACCESS EXCLUSIVE lock and rebuilds the primary-key index, so on a large
-- users table run it in a maintenance window. Skipping it does not corrupt
-- anything — it only means the directory's tiebreak follows the cluster's default
-- collation, which can disagree with the other dialect's byte order for ids that
-- mix cases. See the store README's upgrade note.
ALTER TABLE users ALTER COLUMN id TYPE TEXT COLLATE "C";

-- The directory's contractual order. users previously had no ordered access path
-- at all (PRIMARY KEY (id) only), so an operator listing would seq-scan and sort
-- the whole table; the existing access really is insufficient, which is the
-- condition for adding one. It is a composite on exactly the contractual keyset
-- (created_at, id) and supports both the forward and backward cursor scans.
CREATE INDEX IF NOT EXISTS idx_users_created_at_id
    ON users (created_at DESC, id DESC);
