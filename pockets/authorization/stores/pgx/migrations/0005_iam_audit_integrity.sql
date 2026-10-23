SET LOCAL lock_timeout = '5s';
ALTER TABLE iam_audit
    ADD COLUMN request_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    ADD COLUMN trace_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    ADD COLUMN seq BIGINT NULL,
    ADD COLUMN recorded_at TIMESTAMPTZ NULL;
CREATE SEQUENCE iam_audit_seq;
ALTER SEQUENCE iam_audit_seq OWNED BY iam_audit.seq;
ALTER TABLE iam_audit
    ALTER COLUMN seq SET DEFAULT nextval('iam_audit_seq'),
    ALTER COLUMN recorded_at SET DEFAULT now();
ALTER TABLE iam_operations ADD COLUMN recorded_at TIMESTAMPTZ NULL;
ALTER TABLE iam_operations ALTER COLUMN recorded_at SET DEFAULT now();
GRANT USAGE ON SEQUENCE iam_audit_seq TO PUBLIC;
