DROP TRIGGER IF EXISTS main.iam_audit_immutable_update;
CREATE TRIGGER main.iam_audit_immutable_update BEFORE UPDATE ON iam_audit
BEGIN
    SELECT RAISE(ABORT, 'iam_audit is append-only');
END;
DROP TRIGGER IF EXISTS main.iam_audit_immutable_delete;
CREATE TRIGGER main.iam_audit_immutable_delete BEFORE DELETE ON iam_audit
BEGIN
    SELECT RAISE(ABORT, 'iam_audit is append-only');
END;
DROP TRIGGER IF EXISTS main.iam_operations_immutable_update;
CREATE TRIGGER main.iam_operations_immutable_update BEFORE UPDATE ON iam_operations
BEGIN
    SELECT RAISE(ABORT, 'iam_operations is append-only');
END;
DROP TRIGGER IF EXISTS main.iam_operations_immutable_delete;
CREATE TRIGGER main.iam_operations_immutable_delete BEFORE DELETE ON iam_operations
BEGIN
    SELECT CASE WHEN OLD.outcome = 'fenced' THEN RAISE(ABORT, 'iam_operations is append-only') END;
END;
