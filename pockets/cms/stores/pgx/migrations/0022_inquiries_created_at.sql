SET LOCAL lock_timeout = '5s';
-- Retention access path: PurgeBefore deletes the oldest inquiries with
-- created_at <= a cutoff in bounded batches. Admitted, not required — without
-- it every batch sorts the whole table, and StatusCheck WARNs.
CREATE INDEX IF NOT EXISTS idx_inquiries_created_at ON inquiries (created_at);
