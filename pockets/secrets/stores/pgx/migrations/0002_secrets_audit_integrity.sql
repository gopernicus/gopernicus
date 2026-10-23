SET LOCAL lock_timeout = '5s';
-- Audit integrity for secrets_audit: each read's outcome, the request and
-- trace ids that caused a record, and two evidence columns the store never
-- reads or writes: seq (order of insertion; a rolled-back insert leaves a
-- gap) and recorded_at (the database clock). Rows from before this file keep
-- NULL in both. Every new column has a default, so an insert that lists only
-- 0001's columns keeps working during a rollout. The outcome CHECK is added
-- NOT VALID because validating it scans the table under ACCESS EXCLUSIVE; new
-- rows are checked either way, and VALIDATE CONSTRAINT is a later host step.
ALTER TABLE secrets_audit
    ADD COLUMN outcome     TEXT        NOT NULL DEFAULT 'ok',
    ADD COLUMN request_id  TEXT        NOT NULL DEFAULT '',
    ADD COLUMN trace_id    TEXT        NOT NULL DEFAULT '',
    ADD COLUMN seq         BIGINT      NULL,
    ADD COLUMN recorded_at TIMESTAMPTZ NULL;
ALTER TABLE secrets_audit
    ADD CONSTRAINT secrets_audit_outcome_check CHECK (outcome IN ('ok','denied')) NOT VALID;
CREATE SEQUENCE secrets_audit_seq;
ALTER SEQUENCE secrets_audit_seq OWNED BY secrets_audit.seq;
ALTER TABLE secrets_audit
    ALTER COLUMN seq SET DEFAULT nextval('secrets_audit_seq'),
    ALTER COLUMN recorded_at SET DEFAULT now();
-- nextval only: a runtime role without USAGE could not insert at all. A host
-- may move this grant to its runtime role and revoke it from PUBLIC.
GRANT USAGE ON SEQUENCE secrets_audit_seq TO PUBLIC;
