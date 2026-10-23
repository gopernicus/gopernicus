-- Rail retention (SOC 2 S3). Row 1 of security_events_retention is the floor:
-- the store refuses a purge cutoff younger than now - floor_days. It is a
-- footgun guard, not a control — SQLite has no roles, and the connection that
-- can purge can also drop the triggers — and the floor is a framework
-- invariant: no procedure lowers it. A missing row is a boot refusal, never a
-- default of zero. Re-running this file is safe and keeps an existing row.
CREATE TABLE IF NOT EXISTS security_events_retention (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    floor_days INTEGER NOT NULL CHECK (floor_days >= 1)
);
INSERT OR IGNORE INTO security_events_retention (id, floor_days) VALUES (1, 365);
