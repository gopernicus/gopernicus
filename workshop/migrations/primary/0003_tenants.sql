-- ============================================================================
-- TENANTS (Multi-Tenancy Core)
-- ============================================================================
-- Tenants are the top-level isolation boundary. In the UI, these might be called
-- "Organizations", "Workspaces", "Teams", or "Accounts" depending on the app.
--
-- Authorization: Permissions are managed via rebac_relationships table.
-- Role hierarchy: owner > manager > member
--
-- Tenancy is opt-in: only included when gopernicus.yml has tenants: gopernicus.
-- Apps without tenancy do not need this migration.

CREATE TABLE public.tenants (
    tenant_id VARCHAR NOT NULL,

    -- Tenant info
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL UNIQUE,      -- URL-friendly identifier
    description TEXT,

    -- Status and timestamps
    creator_principal_id VARCHAR NOT NULL REFERENCES principals(principal_id),
    record_state VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT tenants_pk PRIMARY KEY (tenant_id),
    CONSTRAINT tenants_record_state_check CHECK (record_state IN ('active', 'archived', 'deleted'))
);

CREATE INDEX idx_tenants_slug ON tenants(slug);
CREATE INDEX idx_tenants_creator ON tenants(creator_principal_id);
CREATE INDEX idx_tenants_record_state ON tenants(record_state, created_at DESC);
