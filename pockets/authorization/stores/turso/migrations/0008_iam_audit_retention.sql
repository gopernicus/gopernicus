-- Audit retention. Row 1 of iam_audit_retention is the floor: the store refuses
-- a purge cutoff younger than now - floor_days. It is a footgun guard, not a
-- control — SQLite has no roles, and the connection that can purge can also
-- drop the triggers — and the floor is a framework invariant: no procedure
-- lowers it. A missing row is a refusal, never a default of zero.
-- iam_audit_purges takes one row per purge call, a zero count included, in the
-- purge's own transaction; iam_audit itself takes no summary row. Re-running
-- this file is safe and keeps an existing floor row.
CREATE TABLE IF NOT EXISTS main.iam_audit_retention (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    floor_days INTEGER NOT NULL CHECK (floor_days >= 1)
);
INSERT OR IGNORE INTO main.iam_audit_retention (id, floor_days) VALUES (1, 365);
CREATE TABLE IF NOT EXISTS main.iam_audit_purges (
    id          INTEGER PRIMARY KEY,
    cutoff      TEXT NOT NULL,
    purged      INTEGER NOT NULL CONSTRAINT ck_iam_audit_purges_purged CHECK (purged >= 0),
    floor_days  INTEGER NOT NULL,
    max_rows    INTEGER NOT NULL,
    recorded_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
DROP TRIGGER IF EXISTS main.iam_audit_purges_immutable_update;
CREATE TRIGGER main.iam_audit_purges_immutable_update BEFORE UPDATE ON iam_audit_purges
BEGIN
    SELECT RAISE(ABORT, 'iam_audit_purges is append-only');
END;
DROP TRIGGER IF EXISTS main.iam_audit_purges_immutable_delete;
CREATE TRIGGER main.iam_audit_purges_immutable_delete BEFORE DELETE ON iam_audit_purges
BEGIN
    SELECT RAISE(ABORT, 'iam_audit_purges is append-only');
END;
