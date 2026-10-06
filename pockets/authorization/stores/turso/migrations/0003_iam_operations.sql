CREATE TABLE main.iam_operations (
    operation_id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    encoding TEXT COLLATE BINARY NOT NULL,
    fingerprint TEXT COLLATE BINARY NOT NULL,
    outcome TEXT COLLATE BINARY NOT NULL,
    committed_at TEXT NOT NULL,
    CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
    CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
    CONSTRAINT ck_iam_operations_fingerprint CHECK (length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
    CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found'))
);
