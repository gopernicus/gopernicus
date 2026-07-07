-- Recurring schedules. Exactly one of cron_expr / every_secs is set (the
-- schedule.Spec, validated at Ensure). Timestamps are fixed-width ISO-8601 TEXT;
-- payload is TEXT JSON; every_secs is an INTEGER count of seconds; enabled is a
-- 0/1 INTEGER. name is unique — the Ensure upsert key.
CREATE TABLE IF NOT EXISTS job_schedules (
    schedule_id  TEXT    NOT NULL,
    name         TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    cron_expr    TEXT,
    every_secs   INTEGER,
    payload      TEXT    NOT NULL DEFAULT '{}',
    enabled      INTEGER NOT NULL DEFAULT 1,
    next_run_at  TEXT    NOT NULL,
    last_run_at  TEXT,
    last_job_id  TEXT,
    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL,
    CONSTRAINT job_schedules_pk PRIMARY KEY (schedule_id),
    CONSTRAINT job_schedules_name_unique UNIQUE (name)
);

-- Partial index over the due-scan hot path (ListDue): enabled schedules ordered
-- by next_run_at.
CREATE INDEX IF NOT EXISTS idx_job_schedules_due
    ON job_schedules (next_run_at) WHERE enabled = 1;
