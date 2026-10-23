-- Retention access path: PurgeBefore deletes the oldest inquiries with
-- created_at <= a cutoff in bounded batches. Admitted, not required — without
-- it every batch scans and sorts the whole table.
CREATE INDEX IF NOT EXISTS idx_inquiries_created_at ON inquiries (created_at);
