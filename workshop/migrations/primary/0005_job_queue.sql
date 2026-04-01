-- Job queue for durable, at-least-once processing with retry and dead-lettering.
-- A worker process polls, stages, processes, and marks jobs completed.
--
-- Status lifecycle: PENDING → STAGED → COMPLETED
--                              └──────→ FAILED (retryable) → PENDING (rescheduled)
--                              └──────→ DEAD_LETTER (exhausted retries)

CREATE TABLE IF NOT EXISTS job_queue (
    job_id         TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    correlation_id TEXT        NOT NULL,
    tenant_id      TEXT,
    aggregate_type TEXT,
    aggregate_id   TEXT,
    payload        JSONB       NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'PENDING'
                               CHECK (status IN ('PENDING', 'STAGED', 'COMPLETED', 'FAILED', 'DEAD_LETTER')),
    priority       INT         NOT NULL DEFAULT 0,
    retry_count    INT         NOT NULL DEFAULT 0,
    max_retries    INT         NOT NULL DEFAULT 3,
    worker_name    TEXT,
    failure_reason TEXT,
    scheduled_for  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    staged_at      TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,

    CONSTRAINT job_queue_pk PRIMARY KEY (job_id)
);

-- Worker checkout: finds PENDING jobs ready to process, ordered by priority then age.
CREATE INDEX IF NOT EXISTS idx_job_queue_pending
    ON job_queue (scheduled_for, priority DESC, created_at)
    WHERE status = 'PENDING';

-- Monitoring: query by event type and status.
CREATE INDEX IF NOT EXISTS idx_job_queue_type
    ON job_queue (event_type, status);

-- Tracing: look up all jobs sharing a correlation ID.
CREATE INDEX IF NOT EXISTS idx_job_queue_correlation
    ON job_queue (correlation_id);
