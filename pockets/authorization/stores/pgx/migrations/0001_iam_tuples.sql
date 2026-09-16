-- Canonical authorization authority and audit. Apply before constructing stores.
-- Independent labels coexist; only duplicate full facts conflict.
CREATE TABLE iam_tuples (
    scope_kind SMALLINT NOT NULL,
    resource_type TEXT COLLATE "C" NOT NULL,
    resource_id TEXT COLLATE "C" NOT NULL,
    relation TEXT COLLATE "C" NOT NULL,
    subject_type TEXT COLLATE "C" NOT NULL,
    subject_id TEXT COLLATE "C" NOT NULL,
    subject_relation TEXT COLLATE "C" NOT NULL,
    PRIMARY KEY (scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation),
    CONSTRAINT ck_iam_tuples_scope CHECK (
        (scope_kind = 1 AND resource_type = '' AND resource_id = '')
        OR (scope_kind = 2 AND resource_type <> '' AND resource_id <> '')
    ),
    CONSTRAINT ck_iam_tuples_refs CHECK (relation <> '' AND subject_type <> '' AND subject_id <> '')
);
CREATE INDEX idx_iam_tuples_subject ON iam_tuples
    (subject_type, subject_id, subject_relation, scope_kind, resource_type, resource_id, relation);
CREATE INDEX idx_iam_tuples_relation_resource ON iam_tuples
    (scope_kind, resource_type, relation, resource_id);

CREATE TABLE iam_audit (
    id TEXT COLLATE "C" NOT NULL PRIMARY KEY,
    event_id TEXT COLLATE "C" NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    actor_type TEXT COLLATE "C" NOT NULL,
    actor_id TEXT COLLATE "C" NOT NULL,
    system_source TEXT COLLATE "C" NOT NULL,
    reason TEXT COLLATE "C" NOT NULL,
    action TEXT COLLATE "C" NOT NULL,
    encoding TEXT COLLATE "C" NOT NULL,
    scope_kind SMALLINT NOT NULL,
    resource_type TEXT COLLATE "C" NOT NULL,
    resource_id TEXT COLLATE "C" NOT NULL,
    relation TEXT COLLATE "C" NOT NULL,
    subject_type TEXT COLLATE "C" NOT NULL,
    subject_id TEXT COLLATE "C" NOT NULL,
    subject_relation TEXT COLLATE "C" NOT NULL,
    CONSTRAINT ck_iam_audit_action CHECK (action IN ('added', 'removed')),
    CONSTRAINT ck_iam_audit_encoding CHECK (encoding = 'tuple/v2'),
    CONSTRAINT ck_iam_audit_scope CHECK (
        (scope_kind = 1 AND resource_type = '' AND resource_id = '')
        OR (scope_kind = 2 AND resource_type <> '' AND resource_id <> '')
    ),
    CONSTRAINT ck_iam_audit_tuple CHECK (relation <> '' AND subject_type <> '' AND subject_id <> ''),
    CONSTRAINT ck_iam_audit_source CHECK
        ((actor_type <> '' AND actor_id <> '' AND system_source = '') OR (actor_type = '' AND actor_id = '' AND system_source <> ''))
);
CREATE INDEX idx_iam_audit_time ON iam_audit (occurred_at DESC, id DESC);
CREATE INDEX idx_iam_audit_resource ON iam_audit (resource_type, resource_id, occurred_at DESC, id DESC);
CREATE INDEX idx_iam_audit_subject ON iam_audit (subject_type, subject_id, occurred_at DESC, id DESC);
CREATE INDEX idx_iam_audit_actor ON iam_audit (actor_type, actor_id, occurred_at DESC, id DESC);
