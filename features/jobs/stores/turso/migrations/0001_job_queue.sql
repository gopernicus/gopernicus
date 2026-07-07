-- The durable job queue. Timestamps are fixed-width ISO-8601 TEXT (lexicographic
-- order == chronological order, which the claim ORDER BY and keyset pagination
-- rely on); payload is TEXT JSON; ints are INTEGER. The status CHECK mirrors the
-- job.Status vocabulary. worker_name and claimed_at are KEPT (design §6.1): they
-- are the columns stale-claim recovery stands on (the running-with-expired-lease
-- claim arm reads claimed_at). No tenant/aggregate/correlation columns (§1).
CREATE TABLE IF NOT EXISTS job_queue (
    job_id         TEXT    NOT NULL,
    kind           TEXT    NOT NULL,
    payload        TEXT    NOT NULL DEFAULT '{}',
    status         TEXT    NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','running','completed','failed','dead_letter')),
    priority       INTEGER NOT NULL DEFAULT 0,
    retry_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts   INTEGER NOT NULL DEFAULT 3,
    worker_name    TEXT,
    failure_reason TEXT,
    scheduled_for  TEXT    NOT NULL,
    claimed_at     TEXT,
    completed_at   TEXT,
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL,
    CONSTRAINT job_queue_pk PRIMARY KEY (job_id)
);

-- Partial index over the pending-claim hot path (SQLite supports partial
-- indexes). priority DESC, then created_at matches the claim subquery's ORDER BY.
CREATE INDEX IF NOT EXISTS idx_job_queue_claim
    ON job_queue (scheduled_for, priority DESC, created_at) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_job_queue_kind ON job_queue (kind, status);
