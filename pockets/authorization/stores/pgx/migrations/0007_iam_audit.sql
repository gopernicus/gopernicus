-- Retire replay receipts and revision anchors. Old receipts cannot reconstruct
-- actual fact history; archive them before this upgrade if retention is needed.
DROP TABLE iam_mutations;
DROP TABLE iam_scopes;

CREATE TABLE IF NOT EXISTS iam_audit (
    id TEXT COLLATE "C" NOT NULL PRIMARY KEY,
    event_id TEXT COLLATE "C" NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    actor_type TEXT COLLATE "C" NOT NULL,
    actor_id TEXT COLLATE "C" NOT NULL,
    system_source TEXT COLLATE "C" NOT NULL,
    reason TEXT COLLATE "C" NOT NULL,
    action TEXT COLLATE "C" NOT NULL,
    fact_kind TEXT COLLATE "C" NOT NULL,
    resource_type TEXT COLLATE "C" NOT NULL,
    resource_id TEXT COLLATE "C" NOT NULL,
    subject_type TEXT COLLATE "C" NOT NULL,
    subject_id TEXT COLLATE "C" NOT NULL,
    relation TEXT COLLATE "C" NOT NULL,
    subject_relation TEXT COLLATE "C" NOT NULL,
    role TEXT COLLATE "C" NOT NULL,
    CONSTRAINT ck_iam_audit_action CHECK (action IN ('added', 'removed')),
    CONSTRAINT ck_iam_audit_fact CHECK ((fact_kind = 'relationship' AND relation <> '' AND role = '') OR (fact_kind = 'role' AND role <> '' AND relation = '' AND subject_relation = '')),
    CONSTRAINT ck_iam_audit_source CHECK ((actor_type <> '' AND actor_id <> '' AND system_source = '') OR (actor_type = '' AND actor_id = '' AND system_source <> ''))
);
CREATE INDEX idx_iam_audit_time ON iam_audit (occurred_at DESC, id DESC);
CREATE INDEX idx_iam_audit_resource ON iam_audit (resource_type, resource_id, occurred_at DESC, id DESC);
CREATE INDEX idx_iam_audit_subject ON iam_audit (subject_type, subject_id, occurred_at DESC, id DESC);
CREATE INDEX idx_iam_audit_actor ON iam_audit (actor_type, actor_id, occurred_at DESC, id DESC);
