CREATE INDEX IF NOT EXISTS main.idx_iam_operations_committed ON iam_operations (committed_at, operation_id);
