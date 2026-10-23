CREATE TABLE main.iam_audit_next (
    id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    event_id TEXT COLLATE BINARY NOT NULL,
    occurred_at TEXT NOT NULL,
    actor_type TEXT COLLATE BINARY NOT NULL,
    actor_id TEXT COLLATE BINARY NOT NULL,
    system_source TEXT COLLATE BINARY NOT NULL,
    reason TEXT NOT NULL,
    action TEXT NOT NULL CONSTRAINT ck_iam_audit_action CHECK (action IN ('added','removed')),
    encoding TEXT NOT NULL CONSTRAINT ck_iam_audit_encoding CHECK (encoding = 'tuple/v2'),
    scope_kind INTEGER NOT NULL,
    resource_type TEXT COLLATE BINARY NOT NULL,
    resource_id TEXT COLLATE BINARY NOT NULL,
    relation TEXT COLLATE BINARY NOT NULL,
    subject_type TEXT COLLATE BINARY NOT NULL,
    subject_id TEXT COLLATE BINARY NOT NULL,
    subject_relation TEXT COLLATE BINARY NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    recorded_at TEXT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CONSTRAINT ck_iam_audit_scope CHECK (
        (scope_kind=1 AND resource_type='' AND resource_id='')
        OR (scope_kind=2 AND resource_type<>'' AND resource_id<>'')
    ),
    CONSTRAINT ck_iam_audit_tuple CHECK (relation<>'' AND subject_type<>'' AND subject_id<>''),
    CONSTRAINT ck_iam_audit_source CHECK
        ((actor_type<>'' AND actor_id<>'' AND system_source='') OR (actor_type='' AND actor_id='' AND system_source<>''))
);
INSERT INTO main.iam_audit_next (id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation, recorded_at)
SELECT id, event_id, occurred_at, actor_type, actor_id, system_source, reason, action, encoding, scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation, NULL AS recorded_at FROM main.iam_audit ORDER BY occurred_at, id;
DROP TABLE main.iam_audit;
ALTER TABLE main.iam_audit_next RENAME TO iam_audit;
CREATE INDEX main.idx_iam_audit_time ON iam_audit (occurred_at DESC, id DESC);
CREATE INDEX main.idx_iam_audit_resource ON iam_audit (resource_type, resource_id, occurred_at DESC, id DESC);
CREATE INDEX main.idx_iam_audit_subject ON iam_audit (subject_type, subject_id, occurred_at DESC, id DESC);
CREATE INDEX main.idx_iam_audit_actor ON iam_audit (actor_type, actor_id, occurred_at DESC, id DESC);
CREATE TABLE main.iam_operations_next (
    operation_id TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    encoding TEXT COLLATE BINARY NOT NULL,
    fingerprint TEXT COLLATE BINARY NOT NULL,
    outcome TEXT COLLATE BINARY NOT NULL,
    committed_at TEXT NOT NULL,
    recorded_at TEXT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CONSTRAINT ck_iam_operations_id CHECK (operation_id <> ''),
    CONSTRAINT ck_iam_operations_encoding CHECK (encoding = 'operation/v1'),
    CONSTRAINT ck_iam_operations_fingerprint CHECK (length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
    CONSTRAINT ck_iam_operations_outcome CHECK (outcome IN ('applied', 'no_change', 'not_found', 'fenced'))
);
INSERT INTO main.iam_operations_next (operation_id, encoding, fingerprint, outcome, committed_at, recorded_at)
SELECT operation_id, encoding, fingerprint, outcome, committed_at, NULL AS recorded_at FROM main.iam_operations ORDER BY committed_at, operation_id;
DROP TABLE main.iam_operations;
ALTER TABLE main.iam_operations_next RENAME TO iam_operations;
