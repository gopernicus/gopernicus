-- Audit-rail evidence (SOC 2 S2). request_id and trace_id correlate a row with
-- the request and trace that wrote it; the store reads and writes them.
-- recorded_at is the database clock, an evidence column no store names. SQLite
-- refuses ADD COLUMN with an expression default, so the table is rebuilt; the
-- copy sets recorded_at NULL, marking rows written before this migration rather
-- than stamping them with its time. There is no sequence: VACUUM renumbers
-- rowid, so it is not evidence. A later rebuild of this table drops the 0022
-- triggers with it and must ship a paired file that re-creates them.
CREATE TABLE main.security_events_next (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    user_id      TEXT NOT NULL DEFAULT '',
    actor_type   TEXT NOT NULL DEFAULT '',
    actor_id     TEXT NOT NULL DEFAULT '',
    event_type   TEXT NOT NULL,
    event_status TEXT NOT NULL,
    details      TEXT NOT NULL DEFAULT '{}',
    ip_address   TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    request_id   TEXT NOT NULL DEFAULT '',
    trace_id     TEXT NOT NULL DEFAULT '',
    recorded_at  TEXT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO main.security_events_next (id, user_id, actor_type, actor_id, event_type, event_status, details, ip_address, user_agent, created_at, recorded_at)
SELECT id, user_id, actor_type, actor_id, event_type, event_status, details, ip_address, user_agent, created_at, NULL AS recorded_at FROM main.security_events ORDER BY created_at, id;
DROP TABLE main.security_events;
ALTER TABLE main.security_events_next RENAME TO security_events;
CREATE INDEX main.idx_security_events_created_at_id ON security_events (created_at, id);
CREATE INDEX main.idx_security_events_user_id ON security_events (user_id);
CREATE INDEX main.idx_security_events_event_type ON security_events (event_type);
CREATE INDEX main.idx_security_events_event_status ON security_events (event_status);
