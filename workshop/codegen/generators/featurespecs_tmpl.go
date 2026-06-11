package generators

// featureSpecSources mirrors the framework's feature entity specs — the
// queries.sql files under core/repositories/{domain}/{entity} — keyed by
// "<domain>/<package>" (the same key shape nestedBindings uses:
// domain + "/" + ToPackageName(table)). These specs are version-locked with
// the framework: generation falls back to them when a project has no local
// queries.sql for the entity, and a project-local queries.sql always wins
// (creating the file ejects that entity's spec). Drift between this snapshot
// and the framework sources is pinned by TestFeatureSpecSourcesMatchFramework.
var featureSpecSources = map[string]string{
	"auth/apikeys": `-- @func: List
-- @filter:conditions *
-- @search: ilike(name, last_used_ip)
-- @order: *
-- @max: 100
SELECT *
FROM api_keys
WHERE parent_service_account_id = @parent_service_account_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM api_keys
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO api_keys
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-api_key_id,-parent_service_account_id,-record_state,-created_at
UPDATE api_keys
SET $fields
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
RETURNING *;

-- @func: SoftDelete
UPDATE api_keys
SET record_state = 'deleted'
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Archive
UPDATE api_keys
SET record_state = 'archived'
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Restore
UPDATE api_keys
SET record_state = 'active'
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Delete
DELETE FROM api_keys
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

`,
	"auth/oauthaccounts": `-- @func: List
-- @filter:conditions *
-- @search: ilike(provider, provider_user_id, provider_email, scope)
-- @order: *
-- @max: 100
SELECT *
FROM oauth_accounts
WHERE parent_user_id = @parent_user_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM oauth_accounts
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO oauth_accounts
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-oauth_account_id,-parent_user_id,-created_at
UPDATE oauth_accounts
SET $fields
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
RETURNING *;

-- @func: Delete
DELETE FROM oauth_accounts
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
;

-- GetByProvider, ListByUser, and DeleteByUserAndProvider back the emitted
-- authentication satisfier (satisfiers/oauth_accounts.go) — its repo
-- interface requires all three.

-- @func: GetByProvider
SELECT *
FROM oauth_accounts
WHERE provider = @provider AND provider_user_id = @provider_user_id
;

-- @func: ListByUser
-- @scan: many
-- @type:limit int
SELECT *
FROM oauth_accounts
WHERE parent_user_id = @parent_user_id
ORDER BY linked_at DESC
LIMIT @limit
;

-- @func: DeleteByUserAndProvider
DELETE FROM oauth_accounts
WHERE parent_user_id = @parent_user_id AND provider = @provider
;

`,
	"auth/principals": `-- @func: List
-- @filter:conditions *
-- @search: ilike(principal_type)
-- @order: *
-- @max: 100
SELECT *
FROM principals
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM principals
WHERE principal_id = @principal_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO principals
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-principal_id,-created_at
UPDATE principals
SET $fields
WHERE principal_id = @principal_id
RETURNING *;

-- @func: Delete
DELETE FROM principals
WHERE principal_id = @principal_id
;

`,
	"auth/securityevents": `-- @func: List
-- @filter:conditions *
-- @search: ilike(event_type, event_status, ip_address, user_agent)
-- @order: *
-- @max: 100
SELECT *
FROM security_events
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM security_events
WHERE event_id = @event_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO security_events
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-event_id,-created_at
UPDATE security_events
SET $fields
WHERE event_id = @event_id
RETURNING *;

-- @func: Delete
DELETE FROM security_events
WHERE event_id = @event_id
;

`,
	"auth/serviceaccounts": `-- @func: List
-- @filter:conditions *
-- @search: ilike(name, description)
-- @order: *
-- @max: 100
SELECT *
FROM service_accounts
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM service_accounts
WHERE service_account_id = @service_account_id
;

-- Create is handled by a custom store method (transactional principal + service account insert).
-- See serviceaccountspgx/store.go.

-- @func: Update
-- @fields: *,-service_account_id,-record_state,-created_at,-act_as_user,-owner_user_id
UPDATE service_accounts
SET $fields
WHERE service_account_id = @service_account_id
RETURNING *;

-- @func: GetPrincipalInfo
-- @returns: act_as_user, owner_user_id
-- owner_user_id is nullable but the result struct (and the authentication
-- engine's ServiceAccountPrincipal) carry a plain string where "" means no
-- owner — COALESCE keeps the NULL row scannable.
SELECT act_as_user, COALESCE(owner_user_id, '') AS owner_user_id
FROM service_accounts
WHERE service_account_id = @service_account_id
;

-- @func: SoftDelete
UPDATE service_accounts
SET record_state = 'deleted'
WHERE service_account_id = @service_account_id
;

-- @func: Archive
UPDATE service_accounts
SET record_state = 'archived'
WHERE service_account_id = @service_account_id
;

-- @func: Restore
UPDATE service_accounts
SET record_state = 'active'
WHERE service_account_id = @service_account_id
;

-- @func: Delete
DELETE FROM service_accounts
WHERE service_account_id = @service_account_id
;

`,
	"auth/sessions": `-- @func: List
-- @filter:conditions *
-- @search: ilike(user_agent, ip_address)
-- @order: *
-- @max: 100
SELECT *
FROM sessions
WHERE parent_user_id = @parent_user_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM sessions
WHERE session_id = @session_id AND parent_user_id = @parent_user_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO sessions
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-session_id,-parent_user_id,-created_at
UPDATE sessions
SET $fields
WHERE session_id = @session_id AND parent_user_id = @parent_user_id
RETURNING *;

-- @func: Delete
DELETE FROM sessions
WHERE session_id = @session_id AND parent_user_id = @parent_user_id
;

-- @func: GetByTokenHash
SELECT *
FROM sessions
WHERE session_token_hash = @session_token_hash
;

-- @func: GetByRefreshHash
SELECT *
FROM sessions
WHERE refresh_token_hash = @refresh_token_hash
;

-- @func: GetByPreviousRefreshHash
SELECT *
FROM sessions
WHERE previous_refresh_token_hash = @previous_refresh_token_hash
;

-- @func: DeleteAllForUser
DELETE FROM sessions
WHERE parent_user_id = @parent_user_id
;

-- @func: DeleteAllForUserExcept
DELETE FROM sessions
WHERE parent_user_id = @parent_user_id
  AND session_id != @session_id
;

-- @func: UpdateByID
-- @fields: *,-session_id,-parent_user_id,-created_at
UPDATE sessions
SET $fields
WHERE session_id = @session_id
RETURNING *;

-- @func: DeleteByID
DELETE FROM sessions
WHERE session_id = @session_id
;

`,
	"auth/userpasswords": `-- @func: List
-- @filter:conditions *
-- @order: *
-- @max: 100
SELECT *
FROM user_passwords
WHERE $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM user_passwords
WHERE user_id = @user_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO user_passwords
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-user_id,-created_at
UPDATE user_passwords
SET $fields
WHERE user_id = @user_id
RETURNING *;

-- @func: Delete
DELETE FROM user_passwords
WHERE user_id = @user_id
;

-- @func: SetVerified
UPDATE user_passwords
SET password_verified = true, updated_at = @updated_at
WHERE user_id = @user_id
;

`,
	"auth/users": `-- @func: List
-- @filter:conditions *
-- @search: ilike(email, display_name)
-- @order: *
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM users
WHERE user_id = @user_id
;

-- Create is handled by a custom store method (transactional principal + user insert).
-- See userspgx/store.go.

-- @func: Update
-- @fields: *,-user_id,-record_state,-created_at
UPDATE users
SET $fields
WHERE user_id = @user_id
RETURNING *;

-- @func: SoftDelete
UPDATE users
SET record_state = 'deleted'
WHERE user_id = @user_id
;

-- @func: Archive
UPDATE users
SET record_state = 'archived'
WHERE user_id = @user_id
;

-- @func: Restore
UPDATE users
SET record_state = 'active'
WHERE user_id = @user_id
;

-- @func: Delete
DELETE FROM users
WHERE user_id = @user_id
;

-- @func: GetByEmail
SELECT *
FROM users
WHERE email = @email
;

-- @func: SetEmailVerified
-- @event: user.email_verified
UPDATE users
SET email_verified = true, updated_at = @updated_at
WHERE user_id = @user_id
;

-- @func: SetLastLogin
UPDATE users
SET last_login_at = @last_login_at, updated_at = @updated_at
WHERE user_id = @user_id
;

`,
	"auth/verificationcodes": `-- @func: List
-- @filter:conditions *
-- @search: ilike(identifier, purpose)
-- @order: *
-- @max: 100
SELECT *
FROM verification_codes
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM verification_codes
WHERE code_id = @code_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO verification_codes
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-code_id,-created_at
UPDATE verification_codes
SET $fields
WHERE code_id = @code_id
RETURNING *;

-- @func: Delete
DELETE FROM verification_codes
WHERE code_id = @code_id
;

-- @func: GetByIdentifierAndPurpose
-- Looks up an active, non-expired verification code by identifier and purpose.
SELECT *
FROM verification_codes
WHERE identifier = @identifier
  AND purpose = @purpose
  AND expires_at > @now
;

-- @func: IncrementAttempts
-- Increments the failed attempt count. Returns the updated row.
-- @returns: attempt_count
UPDATE verification_codes
SET attempt_count = attempt_count + 1
WHERE identifier = @identifier
  AND purpose = @purpose
RETURNING attempt_count
;

-- @func: DeleteByIdentifierAndPurpose
-- Deletes a verification code after successful use or expiry.
DELETE FROM verification_codes
WHERE identifier = @identifier
  AND purpose = @purpose
;

`,
	"auth/verificationtokens": `-- @func: List
-- @filter:conditions *
-- @search: ilike(purpose, identifier)
-- @order: *
-- @max: 100
SELECT *
FROM verification_tokens
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM verification_tokens
WHERE token_id = @token_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO verification_tokens
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-token_id,-created_at
UPDATE verification_tokens
SET $fields
WHERE token_id = @token_id
RETURNING *;

-- @func: Delete
DELETE FROM verification_tokens
WHERE token_id = @token_id
;

-- @func: GetByIdentifierAndPurpose
-- Looks up a non-expired verification token by identifier and purpose.
SELECT *
FROM verification_tokens
WHERE identifier = @identifier
  AND purpose = @purpose
  AND expires_at > @now
;

-- @func: DeleteByIdentifierAndPurpose
-- Deletes all tokens for an identifier+purpose pair (e.g. invalidate on re-send).
DELETE FROM verification_tokens
WHERE identifier = @identifier
  AND purpose = @purpose
;

-- @func: DeleteByUserIDAndPurpose
-- Deletes all tokens for a user+purpose pair (e.g. cleanup on password reset completion).
DELETE FROM verification_tokens
WHERE user_id = @user_id
  AND purpose = @purpose
;

`,
	"events/eventoutbox": `-- @func: List
-- @filter:conditions event_type, published
-- @order: created_at
-- @max: 100
SELECT *
FROM event_outbox
WHERE $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM event_outbox
WHERE event_id = @event_id
;

-- @func: Create
-- @fields: event_id, event_type, payload
INSERT INTO event_outbox
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: published
UPDATE event_outbox
SET $fields
WHERE event_id = @event_id
RETURNING *;

-- @func: Delete
DELETE FROM event_outbox
WHERE event_id = @event_id
;
`,
	"jobs/jobqueue": `-- @func: List
-- @filter:conditions *
-- @search: ilike(event_type, correlation_id, tenant_id, aggregate_type, aggregate_id, status, worker_name, failure_reason)
-- @order: *
-- @max: 100
SELECT *
FROM job_queue
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM job_queue
WHERE job_id = @job_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO job_queue
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-job_id,-created_at
UPDATE job_queue
SET $fields
WHERE job_id = @job_id
RETURNING *;

-- @func: Delete
DELETE FROM job_queue
WHERE job_id = @job_id
;
`,
	"rebac/groups": `-- @func: List
-- @filter:conditions *
-- @search: ilike(name, slug, description)
-- @order: *
-- @max: 100
SELECT *
FROM groups
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM groups
WHERE group_id = @group_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO groups
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-group_id,-record_state,-created_at
UPDATE groups
SET $fields
WHERE group_id = @group_id
RETURNING *;

-- @func: SoftDelete
UPDATE groups
SET record_state = 'deleted'
WHERE group_id = @group_id
;

-- @func: Archive
UPDATE groups
SET record_state = 'archived'
WHERE group_id = @group_id
;

-- @func: Restore
UPDATE groups
SET record_state = 'active'
WHERE group_id = @group_id
;

-- @func: Delete
DELETE FROM groups
WHERE group_id = @group_id
;

`,
	"rebac/invitations": `-- @func: List
-- @filter:conditions *
-- @search: ilike(resource_type, resource_id, relation, identifier, identifier_type, resolved_subject_id, invited_by, invitation_status)
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM invitations
WHERE invitation_id = @invitation_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO invitations
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-invitation_id,-record_state,-created_at
UPDATE invitations
SET $fields
WHERE invitation_id = @invitation_id
RETURNING *;

-- @func: SoftDelete
UPDATE invitations
SET record_state = 'deleted'
WHERE invitation_id = @invitation_id
;

-- @func: Archive
UPDATE invitations
SET record_state = 'archived'
WHERE invitation_id = @invitation_id
;

-- @func: Restore
UPDATE invitations
SET record_state = 'active'
WHERE invitation_id = @invitation_id
;

-- @func: Delete
DELETE FROM invitations
WHERE invitation_id = @invitation_id
;

-- @func: GetByToken
-- Used by the accept flow to look up a pending invitation by its token hash.
SELECT *
FROM invitations
WHERE token_hash = @token_hash
  AND invitation_status = 'pending'
  AND expires_at > @now
;

-- @func: ListByResource
-- Lists all invitations for a resource (e.g. show pending invitations for a tenant).
-- Used by resource owners to manage outstanding invitations.
-- @filter:conditions invitation_status,relation,auto_accept
-- @search: ilike(identifier)
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: ListBySubject
-- Lists all invitations for an authenticated subject (by resolved_subject_id).
-- @filter:conditions resource_type,invitation_status,relation,auto_accept
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE resolved_subject_id = @resolved_subject_id
  AND $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: ListByIdentifier
-- Lists invitations for an identifier (email). Used during registration to
-- auto-accept invitations when a user verifies their email. Non-expired only.
-- @filter:conditions invitation_status,auto_accept
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE identifier = @identifier
  AND identifier_type = @identifier_type
  AND expires_at > @now
  AND $conditions
ORDER BY $order
LIMIT $limit
;

`,
	"rebac/rebacrelationshipmetadata": `-- @func: List
-- @filter:conditions *
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationship_metadata
WHERE $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM rebac_relationship_metadata
WHERE relationship_id = @relationship_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO rebac_relationship_metadata
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-relationship_id,-created_at
UPDATE rebac_relationship_metadata
SET $fields
WHERE relationship_id = @relationship_id
RETURNING *;

-- @func: Delete
DELETE FROM rebac_relationship_metadata
WHERE relationship_id = @relationship_id
;

`,
	"rebac/rebacrelationships": `-- @func: List
-- @filter:conditions *
-- @search: ilike(resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationships
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM rebac_relationships
WHERE relationship_id = @relationship_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO rebac_relationships
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-relationship_id,-created_at
UPDATE rebac_relationships
SET $fields
WHERE relationship_id = @relationship_id
RETURNING *;

-- @func: Delete
DELETE FROM rebac_relationships
WHERE relationship_id = @relationship_id
;

-- @func: ListBySubject
-- Returns all relationships where the subject matches, with optional filtering by resource_type or relation.
-- Used to enumerate what resources a subject has access to (e.g. "what tenants is this user a member of?").
-- @filter:conditions resource_type,relation
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationships
WHERE subject_type = @subject_type
  AND subject_id = @subject_id
  AND $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: ListByResource
-- Returns all relationships for a given resource, with optional filtering by subject_type or relation.
-- Used to enumerate who has access to a resource (e.g. "who are the members of this tenant?").
-- @filter:conditions subject_type,relation
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: DeleteAllForResource
-- Hard-deletes all relationships for a resource (called on resource deletion).
DELETE FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
;

-- @func: DeleteByTuple
-- Hard-deletes the specific relationship tuple.
DELETE FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND relation = @relation
  AND subject_type = @subject_type
  AND subject_id = @subject_id
;

-- @func: DeleteByResourceAndSubject
-- Removes all relations a subject holds on a specific resource (e.g., unassign all roles from a user on a resource).
DELETE FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND subject_type = @subject_type
  AND subject_id = @subject_id
;

-- @func: DeleteBySubject
-- Removes all relationships for a subject across all resources (e.g., when a user is deleted from the system).
DELETE FROM rebac_relationships
WHERE subject_type = @subject_type
  AND subject_id = @subject_id
;

`,
	"tenancy/tenants": `-- @func: List
-- @filter:conditions *
-- @search: ilike(name, slug, description)
-- @order: *
-- @max: 100
SELECT *
FROM tenants
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM tenants
WHERE tenant_id = @tenant_id
;

-- @func: GetBySlug
SELECT *
FROM tenants
WHERE slug = @slug
AND record_state = 'active'
;

-- @func: GetIDBySlug
-- @returns: tenant_id
SELECT tenant_id
FROM tenants
WHERE slug = @slug
AND record_state = 'active'
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO tenants
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-tenant_id,-record_state,-created_at
UPDATE tenants
SET $fields
WHERE tenant_id = @tenant_id
RETURNING *;

-- @func: SoftDelete
UPDATE tenants
SET record_state = 'deleted'
WHERE tenant_id = @tenant_id
;

-- @func: Archive
UPDATE tenants
SET record_state = 'archived'
WHERE tenant_id = @tenant_id
;

-- @func: Restore
UPDATE tenants
SET record_state = 'active'
WHERE tenant_id = @tenant_id
;

-- @func: Delete
DELETE FROM tenants
WHERE tenant_id = @tenant_id
;

`,
}
