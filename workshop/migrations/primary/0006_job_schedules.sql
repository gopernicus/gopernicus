-- Recurring job schedules: cron-defined templates that the scheduler engine
-- fires into job_queue. Claiming uses FOR UPDATE SKIP LOCKED (CheckoutDue),
-- so N instances never double-fire and no leader election is needed.

CREATE TABLE IF NOT EXISTS job_schedules (
    schedule_id  TEXT         NOT NULL,
    name         VARCHAR(255) NOT NULL,
    event_type   VARCHAR(255) NOT NULL,
    cron_expr    VARCHAR(255) NOT NULL,
    payload      JSONB        NOT NULL DEFAULT '{}',
    enabled      BOOLEAN      NOT NULL DEFAULT TRUE,
    next_run_at  TIMESTAMPTZ  NOT NULL,
    last_run_at  TIMESTAMPTZ,
    last_job_id  TEXT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT job_schedules_pk PRIMARY KEY (schedule_id),
    CONSTRAINT job_schedules_name_unique UNIQUE (name)
);

CREATE INDEX IF NOT EXISTS idx_job_schedules_due
    ON job_schedules (next_run_at)
    WHERE enabled = TRUE;
