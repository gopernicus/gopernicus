-- Retention access path for the ordinary queue's bounded purge (Queue.PurgeTerminal,
-- driven by queue.Service.PurgeTerminalJobs): terminal rows in the purge's
-- (updated_at, job_id) order. Admitted, not required — StatusCheck WARNs
-- index_missing=idx_job_queue_terminal when it is absent, and the purge still runs
-- by sorting every terminal row per batch.
--
-- lock_timeout bounds only the wait to ACQUIRE the lock; once acquired, the build
-- holds a SHARE lock on job_queue that blocks enqueue, claim and complete until it
-- finishes. On a large job_queue, pre-build the same index with
-- CREATE INDEX CONCURRENTLY IF NOT EXISTS outside the migration runner (DROP an
-- INVALID leftover first) so this file applies as a no-op.
SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS idx_job_queue_terminal
    ON job_queue (updated_at, job_id) WHERE status IN ('completed','dead_letter');
