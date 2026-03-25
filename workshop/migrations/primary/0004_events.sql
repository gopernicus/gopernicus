-- Event outbox table for durable, at-least-once event delivery.
-- Events marked with .ToOutbox() are written here by the outbox.Bus decorator.
-- A worker process polls, stages, processes, and marks events completed.
--
-- Status lifecycle: PENDING → STAGED → COMPLETED
--                              └──────→ FAILED (retryable) → PENDING (rescheduled)
--                              └──────→ DEAD_LETTER (exhausted retries)

CREATE TABLE IF NOT EXISTS event_outbox (
    event_id       TEXT        NOT NULL,
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
    
    CONSTRAINT event_outbox_pk PRIMARY KEY (event_id)

);

-- Worker checkout: finds PENDING events ready to process, ordered by priority then age.
CREATE INDEX IF NOT EXISTS idx_event_outbox_pending
    ON event_outbox (scheduled_for, priority DESC, created_at)
    WHERE status = 'PENDING';

-- Monitoring: query by event type and status.
CREATE INDEX IF NOT EXISTS idx_event_outbox_type
    ON event_outbox (event_type, status);

-- Tracing: look up all events sharing a correlation ID.
CREATE INDEX IF NOT EXISTS idx_event_outbox_correlation
    ON event_outbox (correlation_id);
