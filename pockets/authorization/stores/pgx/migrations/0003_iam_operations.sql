CREATE TABLE iam_operations (
    operation_id TEXT COLLATE "C" NOT NULL PRIMARY KEY,
    encoding TEXT COLLATE "C" NOT NULL,
    fingerprint TEXT COLLATE "C" NOT NULL,
    outcome TEXT COLLATE "C" NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
    CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
    CONSTRAINT ck_iam_operations_fingerprint CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found'))
);
