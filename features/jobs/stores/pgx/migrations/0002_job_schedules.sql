-- Recurring schedules (postgres flavor of the turso 0002, identical filename).
-- Exactly one of cron_expr / every_secs is set (the schedule.Spec, validated at
-- Ensure). Timestamps are TIMESTAMPTZ; every_secs is a BIGINT count of seconds;
-- enabled is a native BOOLEAN. name is unique — the Ensure upsert key.
--
-- payload is JSON, not JSONB (same reasoning as 0001: opaque bytes, byte-exact
-- round-trip preserved).
CREATE TABLE IF NOT EXISTS job_schedules (
    schedule_id  TEXT        NOT NULL,
    name         TEXT        NOT NULL,
    kind         TEXT        NOT NULL,
    cron_expr    TEXT,
    every_secs   BIGINT,
    payload      JSON        NOT NULL DEFAULT '{}',
    enabled      BOOLEAN     NOT NULL DEFAULT TRUE,
    next_run_at  TIMESTAMPTZ NOT NULL,
    last_run_at  TIMESTAMPTZ,
    last_job_id  TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    CONSTRAINT job_schedules_pk PRIMARY KEY (schedule_id),
    CONSTRAINT job_schedules_name_unique UNIQUE (name)
);

-- Partial index over the due-scan hot path (ListDue): enabled schedules ordered
-- by next_run_at.
CREATE INDEX IF NOT EXISTS idx_job_schedules_due
    ON job_schedules (next_run_at) WHERE enabled = TRUE;
