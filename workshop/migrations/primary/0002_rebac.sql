
-- ============================================================================
-- REBAC RELATIONSHIPS (Pluggable Authorization Data Store)
-- ============================================================================
-- This table stores Zanzibar-style relationship tuples for ReBAC authorization.
--
-- WHY SEPARATE FROM AUTH?
-- This is intentionally decoupled from the auth module because:
--   1. ReBAC is a pluggable subsystem - you could swap this for SpiceDB
--   2. No FK dependencies on auth tables (uses string-based references)
--   3. Can reference ANY resource type, not just auth entities
--
-- The authorization logic lives in core/auth/authorization/. This is just
-- the data store. To use SpiceDB instead, implement authorization.Storer
-- with a SpiceDB client and wire it in - this table becomes unused.
--
-- Tuple format: resource_type:resource_id#relation@subject_type:subject_id[#subject_relation]
-- Example: org:acme#admin@user:bob
-- Example with subject_relation: org:acme#member@group:engineers#member
--
-- Platform admin only - direct tuple management is not exposed via API

-- REBAC relationships are cached via the authorization cacher that wraps the authorization store.  
CREATE TABLE public.rebac_relationships (
    relationship_id VARCHAR NOT NULL,

    -- The resource being accessed
    resource_type VARCHAR(64) NOT NULL,
    resource_id VARCHAR(255) NOT NULL,

    -- The relation name (e.g., "owner", "admin", "member", "viewer")
    relation VARCHAR(64) NOT NULL,

    -- The subject who has this relation
    subject_type VARCHAR(64) NOT NULL,
    subject_id VARCHAR(255) NOT NULL,
    subject_relation VARCHAR(64),  -- For group membership: group:engineers#member

    -- Immutable: no updated_at. Relationships are deleted and recreated, never mutated.
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT rebac_relationships_pk PRIMARY KEY (relationship_id)
);

-- Unique constraint on tuples (prevents duplicate relationships)
-- Uses COALESCE because subject_relation is nullable
CREATE UNIQUE INDEX idx_rebac_relationships_unique_tuple ON rebac_relationships (
    resource_type, resource_id, relation,
    subject_type, subject_id, COALESCE(subject_relation, '')
);

-- Index: "what relations exist on this resource?"
-- Used by: GetRelationTargets, DeleteAllForResource
CREATE INDEX idx_rebac_rel_resource ON rebac_relationships(resource_type, resource_id);

-- Index: "what resources does this subject have access to?"
-- Used by: permission checks, subject cleanup
CREATE INDEX idx_rebac_rel_subject ON rebac_relationships(subject_type, subject_id);

-- Index: "find all X relations on resource type Y"
-- Used by: batch permission checks
CREATE INDEX idx_rebac_rel_type_relation ON rebac_relationships(resource_type, relation);

-- Index: for group membership expansion (recursive CTE)
-- Used by: CheckRelationWithGroupExpansion
CREATE INDEX idx_rebac_rel_group_member ON rebac_relationships(resource_type, relation, subject_type)
    WHERE relation = 'member';

-- Unique constraint: one relationship per subject per resource
-- This enforces that a subject can only have ONE relation to a resource.
-- Example: user:456 can be owner OR member of tenant:123, not both.
-- The schema's AnyOf rules handle permission inheritance (owner implies member access).
-- Note: Different from tuple uniqueness above - this is about subject uniqueness.
-- The COALESCE handles subject_relation for group membership patterns:
--   user:456 (null subject_relation) vs group:789#member (subject_relation='member')
CREATE UNIQUE INDEX idx_rebac_relationships_unique_subject ON rebac_relationships (
    resource_type, resource_id,
    subject_type, subject_id, COALESCE(subject_relation, '')
);


-- ============================================================================
-- REBAC RELATIONSHIP METADATA (Optional Display Data for Relationships)
-- ============================================================================
-- Stores optional metadata for relationships (job_title, department, joined_at, etc.)
-- This is NOT used for authorization - purely for display/business purposes.
-- One metadata row per relationship (1:1 extension table).
--
-- Example metadata for a tenant membership:
--   {"job_title": "Senior Engineer", "department": "Engineering", "joined_at": "2024-01-15T00:00:00Z"}

CREATE TABLE public.rebac_relationship_metadata (
    relationship_id VARCHAR NOT NULL REFERENCES rebac_relationships(relationship_id) ON DELETE CASCADE,
    metadata JSONB NOT NULL DEFAULT '{}',

    -- No record_state: lifecycle is fully owned by the parent rebac_relationships row (ON DELETE CASCADE)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT rebac_relationship_metadata_pk PRIMARY KEY (relationship_id)
);

-- GIN index for JSONB queries
CREATE INDEX idx_rebac_relationship_metadata_gin ON rebac_relationship_metadata USING GIN (metadata);


-- ============================================================================
-- INVITATIONS (Generic Resource Invitations)
-- ============================================================================
-- Invitations allow users to invite others to resources via email.
-- Resource-agnostic: works with any resource type (tenant, project, team, etc.)
--
-- Flow:
--   1. User creates invitation → token generated → email sent
--   2. Invitee clicks link → token verified → ReBAC relationship created
--   3. Invitation marked as accepted
--
-- The invitation itself is a ReBAC resource:
--   invitation:INV_ID#owner@user:INVITER_ID     (who created it)
--   invitation:INV_ID#resource@RESOURCE_TYPE:ID  (what resource it's for)

CREATE TABLE public.invitations (
    invitation_id       VARCHAR NOT NULL,

    -- Target resource (what are they being invited to?)
    resource_type       VARCHAR(64)  NOT NULL,
    resource_id         VARCHAR(255) NOT NULL,
    relation            VARCHAR(64)  NOT NULL,  -- role to grant on acceptance (e.g., "member", "admin")

    -- Invitee identifier
    identifier          VARCHAR(255) NOT NULL,                  -- email or other contact
    identifier_type     VARCHAR(50)  NOT NULL DEFAULT 'email',  -- type of identifier

    -- Resolution (filled on acceptance)
    resolved_subject_id VARCHAR(255),           -- principal ID of who accepted

    -- Inviter
    invited_by          VARCHAR(255) NOT NULL,  -- principal ID of who invited

    -- Token (hashed, for secure acceptance)
    token_hash          VARCHAR(255) NOT NULL UNIQUE,

    -- Auto-accept: when true, the invitation is automatically accepted when the
    -- invitee registers and verifies their email. When false, the invitee must
    -- explicitly accept or decline.
    auto_accept         BOOLEAN      NOT NULL DEFAULT false,

    -- Status lifecycle: pending → accepted | declined | cancelled | expired
    invitation_status   VARCHAR(50)  NOT NULL DEFAULT 'pending',

    -- Expiry
    expires_at          TIMESTAMPTZ  NOT NULL,
    accepted_at         TIMESTAMPTZ,

    -- Standard timestamps
    record_state        VARCHAR(50)  NOT NULL DEFAULT 'active',
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT invitations_pk PRIMARY KEY (invitation_id),
    CONSTRAINT invitations_status_check CHECK (invitation_status IN ('pending', 'accepted', 'declined', 'cancelled', 'expired')),
    CONSTRAINT invitations_record_state_check CHECK (record_state IN ('active', 'archived', 'deleted')),
    CONSTRAINT invitations_identifier_type_check CHECK (identifier_type IN ('email'))
);

-- Only one pending invitation per identifier per resource
CREATE UNIQUE INDEX idx_invitations_unique_pending
    ON invitations (resource_type, resource_id, identifier, relation)
    WHERE invitation_status = 'pending' AND record_state = 'active';

-- Lookup by identifier (e.g., "show me my pending invitations")
CREATE INDEX idx_invitations_identifier ON invitations (identifier, invitation_status);

-- Lookup by resource (e.g., "show all invitations for this tenant")
CREATE INDEX idx_invitations_resource ON invitations (resource_type, resource_id, invitation_status);

-- Token lookup (for acceptance flow)
CREATE INDEX idx_invitations_token ON invitations (token_hash);

-- Inviter lookup
CREATE INDEX idx_invitations_invited_by ON invitations (invited_by);

-- Expiry cleanup
CREATE INDEX idx_invitations_expires ON invitations (expires_at)
    WHERE invitation_status = 'pending';

-- Record state + chronological ordering
CREATE INDEX idx_invitations_record_state ON invitations (record_state, created_at DESC);



-- ============================================================================
-- GROUPS (ReBAC Permission Containers)
-- ============================================================================
-- Groups are named collections of principals used as subjects in ReBAC
-- relationships. Ownership and scoping are expressed as relationships, not
-- foreign keys:
--
--   Tenant-scoped:  group:{id}#tenant@tenant:{tenant_id}
--   User-owned:     group:{id}#owner@user:{user_id}
--   Nested:         group:{id}#member@group:{other_id}#member
--
-- Groups are always available regardless of whether tenancy is enabled.
-- Slug is not unique — it is meaningful only within the context of a parent
-- (tenant, user, etc.) resolved via ReBAC relationships.

CREATE TABLE public.groups (
    group_id VARCHAR NOT NULL,

    -- Group info
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    description TEXT,

    -- Status and timestamps
    creator_principal_id VARCHAR NOT NULL REFERENCES principals(principal_id),
    record_state VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT groups_pk PRIMARY KEY (group_id),
    CONSTRAINT groups_record_state_check CHECK (record_state IN ('active', 'archived', 'deleted'))
);

CREATE INDEX idx_groups_slug ON groups(slug);
CREATE INDEX idx_groups_creator ON groups(creator_principal_id);
CREATE INDEX idx_groups_record_state ON groups(record_state, created_at DESC);
