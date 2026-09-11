CREATE TABLE documents (
    id TEXT COLLATE "C" PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    name_key TEXT COLLATE "C" NOT NULL
);

CREATE INDEX documents_tenant_name ON documents (tenant_id, name_key, id);
