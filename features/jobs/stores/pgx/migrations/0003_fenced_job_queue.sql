-- The fenced/keyed durable queue (postgres flavor of the turso 0003, identical
-- filename so the version set matches across dialects). It is the hardened
-- sibling of job_queue (0001) that carries the AV3D fenced contract
-- (job.FencedQueueRepository): a per-claim lease fence (lease_id/leased_until),
-- an optional PII-free logical_key for atomic enqueue-once/replace supersession,
-- a terminal_at purge cursor, and an extended status vocabulary (canceled,
-- superseded). It is a SEPARATE table from job_queue: a single table cannot carry
-- both the unfenced worker_name/claimed_at claim model and the lease-fenced one,
-- so the two coexist until the phase-5 migration retires the bespoke path. This
-- is canonical final schema (greenfield rule), not an ALTER of job_queue.
--
-- payload is BYTEA, NOT json/text: the fenced checkpoint stores opaque encrypted
-- ciphertext that must round-trip BYTE-FOR-BYTE, including arbitrary non-UTF8
-- bytes a TEXT/JSON column would mangle, re-encode, or reject. The conformance
-- suite's CheckpointWhileClaimCurrent case asserts a byte-exact non-UTF8
-- round-trip, which only a binary column satisfies.
CREATE TABLE IF NOT EXISTS fenced_job_queue (
    job_id         TEXT        NOT NULL,
    kind           TEXT        NOT NULL,
    payload        BYTEA       NOT NULL DEFAULT '\x'::bytea,
    status         TEXT        NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','running','completed','failed','dead_letter','canceled','superseded')),
    priority       INT         NOT NULL DEFAULT 0,
    retry_count    INT         NOT NULL DEFAULT 0,
    max_attempts   INT         NOT NULL DEFAULT 3,
    logical_key    TEXT,
    lease_id       TEXT,
    leased_until   TIMESTAMPTZ,
    worker_name    TEXT,
    failure_reason TEXT,
    scheduled_for  TIMESTAMPTZ NOT NULL,
    claimed_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    terminal_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT fenced_job_queue_pk PRIMARY KEY (job_id)
);

-- At most one ACTIVE (pending|running) generation per logical_key — the hard
-- invariant behind atomic enqueue-once and replace-supersede. A superseded /
-- canceled / completed / dead-lettered tombstone leaves the active slot free for
-- the next generation. Keyless jobs (logical_key IS NULL) are excluded.
CREATE UNIQUE INDEX IF NOT EXISTS uq_fenced_job_queue_active_key
    ON fenced_job_queue (logical_key)
    WHERE logical_key IS NOT NULL AND status IN ('pending','running');

-- Partial index over the pending-claim hot path. priority DESC, then created_at
-- matches the claim subquery's ORDER BY.
CREATE INDEX IF NOT EXISTS idx_fenced_job_queue_claim
    ON fenced_job_queue (scheduled_for, priority DESC, created_at) WHERE status = 'pending';

-- The latest-by-key status projection reads newest generation first.
CREATE INDEX IF NOT EXISTS idx_fenced_job_queue_key
    ON fenced_job_queue (logical_key, created_at DESC, job_id DESC) WHERE logical_key IS NOT NULL;

-- The purge cursor batches terminal rows by terminal_at.
CREATE INDEX IF NOT EXISTS idx_fenced_job_queue_terminal
    ON fenced_job_queue (terminal_at) WHERE terminal_at IS NOT NULL;
