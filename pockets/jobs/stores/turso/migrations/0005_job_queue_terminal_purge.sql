-- Retention access path for the ordinary queue's bounded purge (Queue.PurgeTerminal,
-- driven by queue.Service.PurgeTerminalJobs): terminal rows in the purge's
-- (updated_at, job_id) order — the twin of the pgx 0005 (SQLite supports partial
-- indexes). Admitted, not required: without it the purge sorts every terminal row.
CREATE INDEX IF NOT EXISTS idx_job_queue_terminal
    ON job_queue (updated_at, job_id) WHERE status IN ('completed','dead_letter');
