ALTER TABLE iam_operations DROP CONSTRAINT ck_iam_operations_outcome, ADD CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found', 'fenced'));
