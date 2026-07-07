-- The durable job queue (postgres flavor of the turso 0001, identical filename
-- so the version set matches across dialects). Timestamps are TIMESTAMPTZ
-- (postgres orders them natively; no lexicographic-TEXT convention needed);
-- ints are INT. The status CHECK mirrors the job.Status vocabulary. worker_name
-- and claimed_at are KEPT (design §6.1): they are the columns stale-claim
-- recovery stands on (the running-with-expired-lease claim arm reads
-- claimed_at). No tenant/aggregate/correlation columns (§1).
--
-- payload is JSON, not JSONB: the payload is opaque to this store (no jsonb
-- operators or indexes are used), and JSON preserves the caller's exact bytes
-- while JSONB re-canonicalizes whitespace/key order. The conformance suite
-- asserts a byte-exact payload round-trip, which only JSON satisfies.
CREATE TABLE IF NOT EXISTS job_queue (
    job_id         TEXT        NOT NULL,
    kind           TEXT        NOT NULL,
    payload        JSON        NOT NULL DEFAULT '{}',
    status         TEXT        NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','running','completed','failed','dead_letter')),
    priority       INT         NOT NULL DEFAULT 0,
    retry_count    INT         NOT NULL DEFAULT 0,
    max_attempts   INT         NOT NULL DEFAULT 3,
    worker_name    TEXT,
    failure_reason TEXT,
    scheduled_for  TIMESTAMPTZ NOT NULL,
    claimed_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT job_queue_pk PRIMARY KEY (job_id)
);

-- Partial index over the pending-claim hot path. priority DESC, then created_at
-- matches the claim subquery's ORDER BY.
CREATE INDEX IF NOT EXISTS idx_job_queue_claim
    ON job_queue (scheduled_for, priority DESC, created_at) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_job_queue_kind ON job_queue (kind, status);
