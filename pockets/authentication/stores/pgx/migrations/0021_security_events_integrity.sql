SET LOCAL lock_timeout = '5s';
-- Audit-rail evidence (SOC 2 S2). request_id and trace_id correlate a row with
-- the request and trace that wrote it; the store reads and writes them. seq and
-- recorded_at are evidence columns no store names: seq proves insertion order
-- (a rolled-back write leaves a gap, so it is not a deletion detector) and
-- recorded_at is the database clock. Both are added NULL and defaulted after
-- the add, so no row is rewritten and a NULL marks a row written before this
-- migration. lock_timeout aborts the stream instead of queueing logins behind
-- a long-running reader; the host retries.
ALTER TABLE security_events
    ADD COLUMN request_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN trace_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN seq BIGINT NULL,
    ADD COLUMN recorded_at TIMESTAMPTZ NULL;
CREATE SEQUENCE security_events_seq;
ALTER SEQUENCE security_events_seq OWNED BY security_events.seq;
ALTER TABLE security_events
    ALTER COLUMN seq SET DEFAULT nextval('security_events_seq'),
    ALTER COLUMN recorded_at SET DEFAULT now();
-- nextval needs USAGE on the sequence: without it every insert fails. PUBLIC
-- keeps an already split runtime role inserting; setval needs UPDATE, which
-- this does not grant.
GRANT USAGE ON SEQUENCE security_events_seq TO PUBLIC;
