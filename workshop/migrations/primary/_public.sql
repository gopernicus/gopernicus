-- Schema: gopernicus_db.public  reflected at: 2026-04-24 14:18:06

CREATE TABLE public.api_keys (
    api_key_id varchar NOT NULL,
    parent_service_account_id varchar NOT NULL,
    name varchar(255) NOT NULL,
    key_prefix varchar(12) NOT NULL,
    key_hash varchar(255) NOT NULL,
    expires_at timestamptz,
    last_used_at timestamptz,
    last_used_ip varchar(45),
    rate_limit_per_minute int4,
    record_state varchar(50) NOT NULL DEFAULT active,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (api_key_id),
    CONSTRAINT api_keys_prefix_unique UNIQUE (key_prefix),
    CONSTRAINT api_keys_record_state_check CHECK (((record_state)::text = ANY ((ARRAY['active'::character varying, 'archived'::character varying, 'deleted'::character varying])::text[])))
);
ALTER TABLE public.api_keys ADD CONSTRAINT api_keys_parent_service_account_id_fkey FOREIGN KEY (parent_service_account_id) REFERENCES public.service_accounts(service_account_id) ON DELETE CASCADE;
CREATE UNIQUE INDEX api_keys_prefix_unique ON public.api_keys USING btree (key_prefix);
CREATE INDEX idx_api_keys_expires ON public.api_keys USING btree (expires_at) WHERE (expires_at IS NOT NULL);
CREATE INDEX idx_api_keys_prefix ON public.api_keys USING btree (key_prefix) WHERE ((record_state)::text = 'active'::text);
CREATE INDEX idx_api_keys_service_account ON public.api_keys USING btree (parent_service_account_id);

CREATE TABLE public.event_outbox (
    event_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published bool NOT NULL DEFAULT false,
    PRIMARY KEY (event_id)
);
CREATE INDEX idx_event_outbox_unpublished ON public.event_outbox USING btree (created_at) WHERE (published = false);

CREATE TABLE public.groups (
    group_id varchar NOT NULL,
    name varchar(255) NOT NULL,
    slug varchar(255) NOT NULL,
    description text,
    creator_principal_id varchar NOT NULL,
    record_state varchar(50) NOT NULL DEFAULT active,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id),
    CONSTRAINT groups_record_state_check CHECK (((record_state)::text = ANY ((ARRAY['active'::character varying, 'archived'::character varying, 'deleted'::character varying])::text[])))
);
ALTER TABLE public.groups ADD CONSTRAINT groups_creator_principal_id_fkey FOREIGN KEY (creator_principal_id) REFERENCES public.principals(principal_id);
CREATE INDEX idx_groups_creator ON public.groups USING btree (creator_principal_id);
CREATE INDEX idx_groups_record_state ON public.groups USING btree (record_state, created_at DESC);
CREATE INDEX idx_groups_slug ON public.groups USING btree (slug);

CREATE TABLE public.invitations (
    invitation_id varchar NOT NULL,
    resource_type varchar(64) NOT NULL,
    resource_id varchar(255) NOT NULL,
    relation varchar(64) NOT NULL,
    identifier varchar(255) NOT NULL,
    identifier_type varchar(50) NOT NULL DEFAULT email,
    resolved_subject_id varchar(255),
    invited_by varchar(255) NOT NULL,
    token_hash varchar(255) NOT NULL,
    auto_accept bool NOT NULL DEFAULT false,
    invitation_status varchar(50) NOT NULL DEFAULT pending,
    expires_at timestamptz NOT NULL,
    accepted_at timestamptz,
    record_state varchar(50) NOT NULL DEFAULT active,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    redirect_url text,
    PRIMARY KEY (invitation_id),
    CONSTRAINT invitations_identifier_type_check CHECK (((identifier_type)::text = 'email'::text)),
    CONSTRAINT invitations_record_state_check CHECK (((record_state)::text = ANY ((ARRAY['active'::character varying, 'archived'::character varying, 'deleted'::character varying])::text[]))),
    CONSTRAINT invitations_status_check CHECK (((invitation_status)::text = ANY ((ARRAY['pending'::character varying, 'accepted'::character varying, 'declined'::character varying, 'cancelled'::character varying, 'expired'::character varying])::text[]))),
    CONSTRAINT invitations_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX idx_invitations_expires ON public.invitations USING btree (expires_at) WHERE ((invitation_status)::text = 'pending'::text);
CREATE INDEX idx_invitations_identifier ON public.invitations USING btree (identifier, invitation_status);
CREATE INDEX idx_invitations_invited_by ON public.invitations USING btree (invited_by);
CREATE INDEX idx_invitations_record_state ON public.invitations USING btree (record_state, created_at DESC);
CREATE INDEX idx_invitations_resource ON public.invitations USING btree (resource_type, resource_id, invitation_status);
CREATE INDEX idx_invitations_token ON public.invitations USING btree (token_hash);
CREATE UNIQUE INDEX idx_invitations_unique_pending ON public.invitations USING btree (resource_type, resource_id, identifier, relation) WHERE (((invitation_status)::text = 'pending'::text) AND ((record_state)::text = 'active'::text));
CREATE UNIQUE INDEX invitations_token_hash_key ON public.invitations USING btree (token_hash);

CREATE TABLE public.job_queue (
    job_id text NOT NULL,
    event_type text NOT NULL,
    correlation_id text NOT NULL,
    tenant_id text,
    aggregate_type text,
    aggregate_id text,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT PENDING,
    priority int4 NOT NULL DEFAULT 0,
    retry_count int4 NOT NULL DEFAULT 0,
    max_retries int4 NOT NULL DEFAULT 3,
    worker_name text,
    failure_reason text,
    scheduled_for timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    staged_at timestamptz,
    completed_at timestamptz,
    PRIMARY KEY (job_id),
    CONSTRAINT job_queue_status_check CHECK ((status = ANY (ARRAY['PENDING'::text, 'STAGED'::text, 'COMPLETED'::text, 'FAILED'::text, 'DEAD_LETTER'::text])))
);
CREATE INDEX idx_job_queue_correlation ON public.job_queue USING btree (correlation_id);
CREATE INDEX idx_job_queue_pending ON public.job_queue USING btree (scheduled_for, priority DESC, created_at) WHERE (status = 'PENDING'::text);
CREATE INDEX idx_job_queue_type ON public.job_queue USING btree (event_type, status);

CREATE TABLE public.oauth_accounts (
    oauth_account_id varchar NOT NULL,
    parent_user_id varchar NOT NULL,
    provider varchar(50) NOT NULL,
    provider_user_id varchar(255) NOT NULL,
    provider_email varchar(255),
    provider_email_verified bool DEFAULT false,
    account_verified bool NOT NULL DEFAULT false,
    access_token text,
    refresh_token text,
    token_expires_at timestamptz,
    token_type varchar(50),
    scope text,
    id_token text,
    profile_data jsonb,
    linked_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (oauth_account_id),
    CONSTRAINT oauth_accounts_provider_unique UNIQUE (provider, provider_user_id)
);
ALTER TABLE public.oauth_accounts ADD CONSTRAINT oauth_accounts_user_fk FOREIGN KEY (parent_user_id) REFERENCES public.users(user_id) ON DELETE CASCADE;
CREATE INDEX idx_oauth_accounts_provider ON public.oauth_accounts USING btree (provider);
CREATE INDEX idx_oauth_accounts_provider_email ON public.oauth_accounts USING btree (provider_email);
CREATE INDEX idx_oauth_accounts_user_id ON public.oauth_accounts USING btree (parent_user_id);
CREATE UNIQUE INDEX oauth_accounts_provider_unique ON public.oauth_accounts USING btree (provider, provider_user_id);

CREATE TABLE public.principals (
    principal_id varchar NOT NULL,
    principal_type varchar(64) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (principal_id),
    CONSTRAINT principals_type_check CHECK (((principal_type)::text = ANY ((ARRAY['user'::character varying, 'service_account'::character varying])::text[])))
);

CREATE TABLE public.rebac_relationship_metadata (
    relationship_id varchar NOT NULL,
    metadata jsonb NOT NULL DEFAULT {},
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (relationship_id)
);
ALTER TABLE public.rebac_relationship_metadata ADD CONSTRAINT rebac_relationship_metadata_relationship_id_fkey FOREIGN KEY (relationship_id) REFERENCES public.rebac_relationships(relationship_id) ON DELETE CASCADE;
CREATE INDEX idx_rebac_relationship_metadata_gin ON public.rebac_relationship_metadata USING gin (metadata);

CREATE TABLE public.rebac_relationships (
    relationship_id varchar NOT NULL,
    resource_type varchar(64) NOT NULL,
    resource_id varchar(255) NOT NULL,
    relation varchar(64) NOT NULL,
    subject_type varchar(64) NOT NULL,
    subject_id varchar(255) NOT NULL,
    subject_relation varchar(64),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (relationship_id)
);
CREATE INDEX idx_rebac_rel_group_member ON public.rebac_relationships USING btree (resource_type, relation, subject_type) WHERE ((relation)::text = 'member'::text);
CREATE INDEX idx_rebac_rel_resource ON public.rebac_relationships USING btree (resource_type, resource_id);
CREATE INDEX idx_rebac_rel_subject ON public.rebac_relationships USING btree (subject_type, subject_id);
CREATE INDEX idx_rebac_rel_type_relation ON public.rebac_relationships USING btree (resource_type, relation);
CREATE UNIQUE INDEX idx_rebac_relationships_unique_subject ON public.rebac_relationships USING btree (resource_type, resource_id, subject_type, subject_id, COALESCE(subject_relation, ''::character varying));
CREATE UNIQUE INDEX idx_rebac_relationships_unique_tuple ON public.rebac_relationships USING btree (resource_type, resource_id, relation, subject_type, subject_id, COALESCE(subject_relation, ''::character varying));

CREATE TABLE public.schema_migrations (
    version varchar(255) NOT NULL,
    checksum varchar(64) NOT NULL,
    raw_sql text,
    applied_at timestamp NOT NULL DEFAULT now(),
    PRIMARY KEY (version)
);

CREATE TABLE public.security_events (
    event_id varchar NOT NULL,
    user_id varchar,
    event_type varchar(50) NOT NULL,
    event_status varchar(20) NOT NULL,
    event_details jsonb,
    ip_address varchar(45),
    user_agent varchar(500),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id)
);
ALTER TABLE public.security_events ADD CONSTRAINT security_events_user_fk FOREIGN KEY (user_id) REFERENCES public.users(user_id) ON DELETE SET NULL;
CREATE INDEX idx_security_events_created_at ON public.security_events USING btree (created_at);
CREATE INDEX idx_security_events_email_failures ON public.security_events USING btree (((event_details ->> 'email'::text)), created_at) WHERE (((event_type)::text = 'login'::text) AND ((event_status)::text = 'failure'::text));
CREATE INDEX idx_security_events_ip_failures ON public.security_events USING btree (ip_address, created_at) WHERE (((event_type)::text = 'login'::text) AND ((event_status)::text = 'failure'::text));
CREATE INDEX idx_security_events_status ON public.security_events USING btree (event_status);
CREATE INDEX idx_security_events_type ON public.security_events USING btree (event_type);
CREATE INDEX idx_security_events_user_id ON public.security_events USING btree (user_id);

CREATE TABLE public.service_accounts (
    service_account_id varchar NOT NULL,
    name varchar(255) NOT NULL,
    description text,
    creator_principal_id varchar NOT NULL,
    record_state varchar(50) NOT NULL DEFAULT active,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (service_account_id),
    CONSTRAINT service_accounts_record_state_check CHECK (((record_state)::text = ANY ((ARRAY['active'::character varying, 'archived'::character varying, 'deleted'::character varying])::text[])))
);
ALTER TABLE public.service_accounts ADD CONSTRAINT service_accounts_creator_principal_id_fkey FOREIGN KEY (creator_principal_id) REFERENCES public.principals(principal_id);
ALTER TABLE public.service_accounts ADD CONSTRAINT service_accounts_service_account_id_fkey FOREIGN KEY (service_account_id) REFERENCES public.principals(principal_id) ON DELETE CASCADE;
CREATE INDEX idx_service_accounts_creator ON public.service_accounts USING btree (creator_principal_id);
CREATE INDEX idx_service_accounts_record_state ON public.service_accounts USING btree (record_state, created_at DESC);

CREATE TABLE public.sessions (
    session_id varchar NOT NULL,
    parent_user_id varchar NOT NULL,
    session_token_hash varchar(255) NOT NULL,
    refresh_token_hash varchar(255),
    rotation_count int4 DEFAULT 0,
    last_rotation_at timestamptz,
    previous_refresh_token_hash varchar(255),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz NOT NULL DEFAULT now(),
    user_agent varchar(500),
    ip_address varchar(45),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id)
);
ALTER TABLE public.sessions ADD CONSTRAINT sessions_user_fk FOREIGN KEY (parent_user_id) REFERENCES public.users(user_id) ON DELETE CASCADE;
CREATE INDEX idx_sessions_expires ON public.sessions USING btree (expires_at);
CREATE INDEX idx_sessions_token ON public.sessions USING btree (session_token_hash);
CREATE INDEX idx_sessions_user ON public.sessions USING btree (parent_user_id);

CREATE TABLE public.tenants (
    tenant_id varchar NOT NULL,
    name varchar(255) NOT NULL,
    slug varchar(255) NOT NULL,
    description text,
    creator_principal_id varchar NOT NULL,
    record_state varchar(50) NOT NULL DEFAULT active,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id),
    CONSTRAINT tenants_record_state_check CHECK (((record_state)::text = ANY ((ARRAY['active'::character varying, 'archived'::character varying, 'deleted'::character varying])::text[]))),
    CONSTRAINT tenants_slug_key UNIQUE (slug)
);
ALTER TABLE public.tenants ADD CONSTRAINT tenants_creator_principal_id_fkey FOREIGN KEY (creator_principal_id) REFERENCES public.principals(principal_id);
CREATE INDEX idx_tenants_creator ON public.tenants USING btree (creator_principal_id);
CREATE INDEX idx_tenants_record_state ON public.tenants USING btree (record_state, created_at DESC);
CREATE INDEX idx_tenants_slug ON public.tenants USING btree (slug);
CREATE UNIQUE INDEX tenants_slug_key ON public.tenants USING btree (slug);

CREATE TABLE public.user_passwords (
    user_id varchar NOT NULL,
    password_hash varchar(255) NOT NULL,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    password_verified bool NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id)
);
ALTER TABLE public.user_passwords ADD CONSTRAINT user_passwords_user_fk FOREIGN KEY (user_id) REFERENCES public.users(user_id) ON DELETE CASCADE;

CREATE TABLE public.users (
    user_id varchar NOT NULL,
    email varchar(255) NOT NULL,
    display_name varchar(255),
    email_verified bool NOT NULL DEFAULT false,
    last_login_at timestamptz,
    record_state varchar(50) NOT NULL DEFAULT active,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_record_state_check CHECK (((record_state)::text = ANY ((ARRAY['active'::character varying, 'archived'::character varying, 'deleted'::character varying])::text[])))
);
ALTER TABLE public.users ADD CONSTRAINT users_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.principals(principal_id) ON DELETE CASCADE;
CREATE INDEX idx_users_email ON public.users USING btree (email);
CREATE INDEX idx_users_record_state ON public.users USING btree (record_state, created_at DESC);
CREATE UNIQUE INDEX users_email_key ON public.users USING btree (email);

CREATE TABLE public.verification_codes (
    code_id varchar NOT NULL,
    identifier varchar(255) NOT NULL,
    code_hash varchar(255) NOT NULL,
    purpose varchar(50) NOT NULL,
    user_id varchar,
    data jsonb,
    attempt_count int4 NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (code_id),
    CONSTRAINT unique_active_code UNIQUE (identifier, purpose)
);
ALTER TABLE public.verification_codes ADD CONSTRAINT verification_codes_user_fk FOREIGN KEY (user_id) REFERENCES public.users(user_id) ON DELETE CASCADE;
CREATE INDEX idx_verification_codes_expires ON public.verification_codes USING btree (expires_at);
CREATE INDEX idx_verification_codes_lookup ON public.verification_codes USING btree (identifier, purpose);
CREATE UNIQUE INDEX unique_active_code ON public.verification_codes USING btree (identifier, purpose);

CREATE TABLE public.verification_tokens (
    token_id varchar NOT NULL,
    token_hash varchar(255) NOT NULL,
    purpose varchar(50) NOT NULL,
    identifier varchar(255) NOT NULL,
    user_id varchar,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (token_id),
    CONSTRAINT verification_tokens_token_hash_key UNIQUE (token_hash)
);
ALTER TABLE public.verification_tokens ADD CONSTRAINT verification_tokens_user_fk FOREIGN KEY (user_id) REFERENCES public.users(user_id) ON DELETE CASCADE;
CREATE INDEX idx_verification_tokens_expires ON public.verification_tokens USING btree (expires_at);
CREATE INDEX idx_verification_tokens_hash ON public.verification_tokens USING btree (token_hash);
CREATE INDEX idx_verification_tokens_identifier ON public.verification_tokens USING btree (identifier);
CREATE INDEX idx_verification_tokens_purpose ON public.verification_tokens USING btree (purpose);
CREATE UNIQUE INDEX verification_tokens_token_hash_key ON public.verification_tokens USING btree (token_hash);

