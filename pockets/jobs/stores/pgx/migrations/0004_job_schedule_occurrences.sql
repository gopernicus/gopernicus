-- Pending dispatch survives schedule edits/deletion and queue failures. There is
-- deliberately no foreign key: a claimed occurrence is already admitted work.
CREATE TABLE IF NOT EXISTS job_schedule_occurrences (
 job_id TEXT PRIMARY KEY,
 schedule_id TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL,
 tenant_id TEXT,
 payload JSON NOT NULL,
 slot TIMESTAMPTZ NOT NULL,
 attempts BIGINT NOT NULL DEFAULT 0,
 claimed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_job_schedule_occurrences_pending
 ON job_schedule_occurrences (kind, attempts, slot, job_id);
