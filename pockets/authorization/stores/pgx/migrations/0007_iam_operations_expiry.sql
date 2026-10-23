SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS idx_iam_operations_committed ON iam_operations (committed_at, operation_id);
