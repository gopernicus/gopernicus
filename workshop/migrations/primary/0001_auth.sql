
-- ============================================================================
-- PRINCIPALS (Primary Identity Registry)
-- ============================================================================
-- ELI5: This is the registry of "principals" - entities that can authenticate
--       and have permissions. Both humans (users) and machines (service_accounts)
--       are principals.
--
-- Problem: We have users and service accounts that can create resources.
--          If we want "creator_principal_id" to reference either, we'd need
--          separate columns (creator_type + creator_id) with no FK integrity.
--
-- Solution: Primary identities (user, service_account) register here FIRST.
--           Then any "creator_principal_id" column can simply FK to this table.
--           One column, one FK, full referential integrity.
--
-- What's a principal vs a credential:
--   - Principals: user, service_account - these have permissions
--   - Credentials: password, OAuth token, API key - these prove who you are
--
-- API keys are NOT principals:
--   - API keys are credentials that belong to service_accounts
--   - When you authenticate with an API key, you authenticate AS the service_account
--   - The service_account's permissions apply, not the API key's
--
-- How it works:
--   1. Create a user? First INSERT into principals, then INSERT into users
--   2. Create a service account? First INSERT into principals, then INSERT into service_accounts
--   3. Track "who created this org"? Just: creator_principal_id → principals.principal_id
--
-- The principal_type tells you which detail table has the full record.
-- ============================================================================

-- Platform admin only - internal registry table, not directly accessed via API
CREATE TABLE public.principals (
    principal_id VARCHAR NOT NULL,
    principal_type VARCHAR(64) NOT NULL,  -- 'user', 'service_account'

    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT principals_pk PRIMARY KEY (principal_id),
    CONSTRAINT principals_type_check CHECK (principal_type IN ('user', 'service_account'))
);

-- ============================================================================
-- SERVICE ACCOUNTS (non-human principals)
-- ============================================================================
-- Service accounts are "robot users" - they can own API keys and create resources.
-- Like users, they're registered in principals for FK integrity.
-- Unlike users, they don't have email/password - they authenticate via API keys.

-- Service accounts: self-access handled by authorizer's checkSelf for read/update/delete
-- Platform admin can manage all service accounts
-- Secure by default: authorization middleware generated, checkSelf handles self-access
CREATE TABLE public.service_accounts (
    service_account_id VARCHAR PRIMARY KEY REFERENCES principals(principal_id) ON DELETE CASCADE,

    -- Service account info
    name VARCHAR(255) NOT NULL,             -- Human-readable: "CI/CD Pipeline"
    description TEXT,

    -- Status and timestamps
    creator_principal_id VARCHAR NOT NULL REFERENCES principals(principal_id),
    record_state VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT service_accounts_record_state_check CHECK (record_state IN ('active', 'archived', 'deleted'))
);

CREATE INDEX idx_service_accounts_creator ON service_accounts(creator_principal_id);
CREATE INDEX idx_service_accounts_record_state ON service_accounts(record_state, created_at DESC);


-- ============================================================================
-- USERS TABLE (human principals - OAuth ready)
-- ============================================================================
-- Users: self-access handled by authorizer's checkSelf for read/update/delete
-- Platform admin can manage all users
-- Secure by default: authorization middleware generated, checkSelf handles self-access
CREATE TABLE public.users (
    -- User identification
    user_id VARCHAR PRIMARY KEY REFERENCES principals(principal_id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL UNIQUE,
    display_name VARCHAR(255),

    -- Email verification
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,

    -- Security
    last_login_at TIMESTAMP WITH TIME ZONE,

    -- Status and timestamps
    record_state VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT users_record_state_check CHECK (record_state IN ('active', 'archived', 'deleted'))
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_record_state ON users(record_state, created_at DESC);

-- ============================================================================
-- API KEYS (credentials for service accounts)
-- ============================================================================
-- API keys are credentials that belong EXCLUSIVELY to service accounts.
-- They are NOT principals - they're how service accounts authenticate.
--
-- The GCP/AWS model:
--   - Users authenticate via JWT/OAuth (interactive sessions)
--   - Service accounts authenticate via API keys (programmatic access)
--   - If you need programmatic access, create a service account
--
-- When you authenticate with an API key:
--   1. Look up the API key by hash
--   2. Get the service_account_id from the key
--   3. Authenticate AS that service_account
--   4. All permission checks use service_account:{service_account_id}
--
-- Key rotation: Multiple API keys can exist per service account.
-- Revoke one key, create another - permissions stay with the service account.

-- API keys: owner (service_account) can manage their own keys
CREATE TABLE public.api_keys (
    api_key_id VARCHAR PRIMARY KEY,

    -- Owner: the service_account this key authenticates as
    parent_service_account_id VARCHAR NOT NULL REFERENCES service_accounts(service_account_id) ON DELETE CASCADE,

    -- Key identification
    name VARCHAR(255) NOT NULL,             -- Human-readable: "Production Server"
    key_prefix VARCHAR(12) NOT NULL,        -- For identification: "sk_live_abc1"
    key_hash VARCHAR(255) NOT NULL,         -- SHA256 hash for validation

    -- Expiration and usage tracking
    expires_at TIMESTAMP WITH TIME ZONE,    -- NULL = never expires
    last_used_at TIMESTAMP WITH TIME ZONE,
    last_used_ip VARCHAR(45),

    -- Rate limiting
    rate_limit_per_minute INT,              -- NULL = use default

    -- Status and timestamps
    record_state VARCHAR(50) NOT NULL DEFAULT 'active',
    revoked_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT api_keys_prefix_unique UNIQUE (key_prefix),
    CONSTRAINT api_keys_record_state_check CHECK (record_state IN ('active', 'archived', 'deleted'))
);

CREATE INDEX idx_api_keys_service_account ON api_keys(parent_service_account_id);
CREATE INDEX idx_api_keys_prefix ON api_keys(key_prefix) WHERE record_state = 'active';
CREATE INDEX idx_api_keys_expires ON api_keys(expires_at) WHERE expires_at IS NOT NULL;


-- ============================================================================
-- USER PASSWORDS (separate from users - not all users will have passwords)
-- ============================================================================
-- User passwords: owner (user) can manage their own password
CREATE TABLE public.user_passwords (
    user_id VARCHAR NOT NULL,
    password_hash VARCHAR(255) NOT NULL,    -- bcrypt hash (includes salt automatically)
    password_changed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    -- Verification (credential-level security)
    password_verified BOOLEAN NOT NULL DEFAULT FALSE, -- This specific password has been verified via email code

    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT user_passwords_pk PRIMARY KEY (user_id),
    CONSTRAINT user_passwords_user_fk FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

-- ============================================================================
-- OAUTH ACCOUNTS (supports multiple OAuth providers per user)
-- ============================================================================
-- OAuth accounts: owner (user) can manage their own linked accounts
-- Credentials — hard deleted on unlink. Audit trail is in security_events.
CREATE TABLE public.oauth_accounts (
    oauth_account_id VARCHAR NOT NULL,
    parent_user_id VARCHAR NOT NULL,

    -- Provider identification
    provider VARCHAR(50) NOT NULL,          -- 'google', 'github', 'microsoft', 'apple'
    provider_user_id VARCHAR(255) NOT NULL, -- User ID from the provider
    provider_email VARCHAR(255),            -- Email from provider (may differ from users.email)
    provider_email_verified BOOLEAN DEFAULT FALSE,

    -- Verification (credential-level security)
    account_verified BOOLEAN NOT NULL DEFAULT FALSE, -- This specific OAuth account has been verified via email code

    -- OAuth tokens (only if you need to make API calls on behalf of user)
    access_token TEXT,                      -- Optional: only store if calling provider APIs
    refresh_token TEXT,                     -- Optional: for refreshing access token
    token_expires_at TIMESTAMP WITH TIME ZONE,
    token_type VARCHAR(50),                 -- Usually "Bearer"
    scope TEXT,                             -- Space-separated OAuth scopes
    id_token TEXT,                          -- OpenID Connect ID token

    -- Additional data
    profile_data JSONB,                     -- Name, avatar, etc. from provider

    -- Timestamps
    linked_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT oauth_accounts_pk PRIMARY KEY (oauth_account_id),
    CONSTRAINT oauth_accounts_user_fk FOREIGN KEY (parent_user_id) REFERENCES users(user_id) ON DELETE CASCADE,
    CONSTRAINT oauth_accounts_provider_unique UNIQUE (provider, provider_user_id)
);

CREATE INDEX idx_oauth_accounts_user_id ON oauth_accounts(parent_user_id);
CREATE INDEX idx_oauth_accounts_provider ON oauth_accounts(provider);
CREATE INDEX idx_oauth_accounts_provider_email ON oauth_accounts(provider_email);

-- ============================================================================
-- SESSIONS (JWT session tracking with refresh token rotation)
-- ============================================================================
-- Sessions: owner (user) can view and revoke their own sessions
-- Transient — hard deleted on revocation/expiry. Audit trail is in security_events.
CREATE TABLE public.sessions (
    session_id VARCHAR NOT NULL, -- will also be the jti
    parent_user_id VARCHAR NOT NULL,

    -- Token tracking
    session_token_hash VARCHAR(255) NOT NULL,   -- Hash of JWT for validation
    refresh_token_hash VARCHAR(255),            -- Hash of refresh token

    -- Token rotation (for detecting reuse attacks)
    rotation_count INT DEFAULT 0,               -- How many times token has been rotated
    last_rotation_at TIMESTAMP WITH TIME ZONE,  -- When last rotation occurred
    previous_refresh_token_hash VARCHAR(255),   -- Previous token hash for reuse detection

    -- Session metadata
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_used_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    user_agent VARCHAR(500),
    ip_address VARCHAR(45),

    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT sessions_pk PRIMARY KEY (session_id),
    CONSTRAINT sessions_user_fk FOREIGN KEY (parent_user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

CREATE INDEX idx_sessions_user ON sessions(parent_user_id);
CREATE INDEX idx_sessions_token ON sessions(session_token_hash);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- ============================================================================
-- SECURITY EVENTS (audit log for authentication events + rate limiting)
-- ============================================================================
-- Security events: owner (user) can view their own security events (audit trail)
-- Note: user_id is nullable (system events), so auth relationships are created
-- manually in the auth case layer when user_id is present, not via @auth:create.
CREATE TABLE public.security_events (
    event_id VARCHAR NOT NULL,
    user_id VARCHAR,
    event_type VARCHAR(50) NOT NULL,        -- 'login', 'logout', 'password_change', 'oauth_link', 'mfa_enable'
    event_status VARCHAR(20) NOT NULL,      -- 'success', 'failure', 'suspicious'
    event_details JSONB,                    -- Additional context
    ip_address VARCHAR(45),
    user_agent VARCHAR(500),

    -- Append-only: no record_state, no updated_at
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT security_events_pk PRIMARY KEY (event_id),
    CONSTRAINT security_events_user_fk FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE SET NULL
);

CREATE INDEX idx_security_events_user_id ON security_events(user_id);
CREATE INDEX idx_security_events_created_at ON security_events(created_at);
CREATE INDEX idx_security_events_type ON security_events(event_type);
CREATE INDEX idx_security_events_status ON security_events(event_status);

-- Rate limiting indexes (for counting recent failed login attempts)
CREATE INDEX idx_security_events_email_failures ON security_events
    ((event_details->>'email'), created_at)
    WHERE event_type = 'login' AND event_status = 'failure';

CREATE INDEX idx_security_events_ip_failures ON security_events
    (ip_address, created_at)
    WHERE event_type = 'login' AND event_status = 'failure';

-- ============================================================================
-- ============================================================================
-- VERIFICATION TOKENS (URL-based verification for password resets & OAuth state)
-- ============================================================================
-- Usage: Password reset links (click-to-verify), OAuth state storage
-- Pattern: User clicks email link → token validated → token DELETED
-- Transactional: Tokens are deleted after use (not marked as used)
-- Platform admin only - internal/transient data
-- Secure by default: no @auth:perm = platform admin only
CREATE TABLE public.verification_tokens (
    token_id VARCHAR NOT NULL,
    token_hash VARCHAR(255) NOT NULL UNIQUE,
    purpose VARCHAR(50) NOT NULL,           -- 'password_reset'
    identifier VARCHAR(255) NOT NULL,       -- Email address
    user_id VARCHAR,                        -- FK to users
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT verification_tokens_pk PRIMARY KEY (token_id),
    CONSTRAINT verification_tokens_user_fk FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

CREATE INDEX idx_verification_tokens_hash ON verification_tokens(token_hash);
CREATE INDEX idx_verification_tokens_purpose ON verification_tokens(purpose);
CREATE INDEX idx_verification_tokens_identifier ON verification_tokens(identifier);
CREATE INDEX idx_verification_tokens_expires ON verification_tokens(expires_at);

-- ============================================================================
-- VERIFICATION CODES (6-digit codes for manual entry)
-- ============================================================================
-- Usage: Email verification, critical actions (user types code)
-- Pattern: User receives code via email → types into form → code validated → code DELETED
-- Transactional: Codes are deleted after use (not marked as used)
-- Rate limiting: Tracked in security_events (max 5 failed attempts per 15 min)
-- Platform admin only - internal/transient data
-- Secure by default: no @auth:perm = platform admin only
CREATE TABLE public.verification_codes (
    code_id VARCHAR NOT NULL,
    identifier VARCHAR(255) NOT NULL,       -- Email, phone, or username
    code_hash VARCHAR(255) NOT NULL,        -- SHA256 hash of 6-digit code
    purpose VARCHAR(50) NOT NULL,           -- 'email_verify', 'pending_oauth_link', 'oauth_state'
    user_id VARCHAR,                        -- FK to users
    data JSONB,                             -- Optional payload (e.g., PendingOAuthLink, OAuthState)
    attempt_count INT NOT NULL DEFAULT 0,   -- Failed verification attempts (rate limiting)
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT verification_codes_pk PRIMARY KEY (code_id),
    CONSTRAINT verification_codes_user_fk FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
    CONSTRAINT unique_active_code UNIQUE (identifier, purpose)
);

CREATE INDEX idx_verification_codes_lookup ON verification_codes(identifier, purpose);
CREATE INDEX idx_verification_codes_expires ON verification_codes(expires_at);
