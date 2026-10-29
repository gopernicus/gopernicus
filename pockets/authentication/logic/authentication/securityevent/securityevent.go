// Package securityevent is the append-only audit-rail domain (design §5.1): a
// synchronous record of sensitive authentication events — registrations, logins,
// logouts, verifications, password changes, OAuth logins, API-key
// authentications and token issuance, each with its denials (StatusFailure or
// StatusBlocked with a bounded Reason* under the Details key "reason"). It is
// append-only AT THE PORT: there
// are no Update or Delete methods (structural, not enforced by a flag). The SQL
// stores' immutability migration (0022) refuses UPDATE and DELETE on the table —
// on PostgreSQL from every role but the table owner, TRUNCATE included — and
// the pgx store refuses to boot in production on a runtime role that could
// rewrite it; see the docs site's "Production database posture" guide.
//
// Recorder is the one recording site: it stamps ids, time, correlation and
// client attribution, and owns the never-fail write.
//
// Content hygiene (plan-cut amendment, SRE): Details/IPAddress/UserAgent carry
// identifiers and key PREFIXES only — raw API keys, JWTs, session tokens,
// passwords, and OAuth tokens NEVER land in audit content. The service-layer
// writer is the single enforcement point.
//
// The rail is optional (ratified AV9): a host that wires no SecurityEvents
// repository simply keeps no audit trail; the recording site is then a no-op.
// The durable outbox emission rail (design §5.2) is DEFERRED (ratified AV10) —
// nothing here imports sdk/capabilities/events or pockets/events.
package securityevent

import (
	"net/netip"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// Event-type vocabulary (design §5.1, salvaged). The invitation terms A5
// reserved are wired here by A6 (logic/invitation): see the invitation block
// below.
const (
	// TypeRegister is a password registration.
	TypeRegister = "register"
	// TypeLogin is a password login attempt (success, failure, or blocked).
	TypeLogin = "login"
	// TypeLogout is a session logout.
	TypeLogout = "logout"
	// TypePasswordChange is a session-gated password change.
	TypePasswordChange = "password_change"
	// TypePasswordReset is a password reset via a reset token. A refused reset
	// carries no UserID: the token resolved no account.
	TypePasswordReset = "password_reset"
	// TypePasswordSet is a set-initial-password on an account that had none,
	// authorized by a consumed set_password recent-auth grant (design §5.2).
	TypePasswordSet = "password_set"
	// TypePasswordRemoveCodeSent is a remove_password code issued and enqueued for
	// delivery to a verified recovery identifier (design §5.3). Details carries no
	// destination.
	TypePasswordRemoveCodeSent = "password_remove_code_sent"
	// TypePasswordRemoved is a completed code-gated password removal (design §5.3).
	TypePasswordRemoved = "password_removed"
	// TypeEmailVerified is a completed email verification.
	TypeEmailVerified = "email_verified"
	// TypeOAuthLogin is an OAuth login into an already-linked identity. A
	// provider callback refused on any branch records it too, as StatusFailure or
	// StatusBlocked with the provider and a reason — or, for a provider the host
	// does not wire, reason unknown_provider and no provider.
	TypeOAuthLogin = "oauth_login"
	// TypeOAuthRegister is an OAuth-driven registration of a new user.
	TypeOAuthRegister = "oauth_register"
	// TypeOAuthLinkVerified is a completed anti-takeover pending-link confirmation.
	TypeOAuthLinkVerified = "oauth_link_verified"
	// TypeOAuthLinked is a session-gated provider link.
	TypeOAuthLinked = "oauth_linked"
	// TypeOAuthPendingLinkSent is an anti-takeover pending-link confirmation
	// issued and enqueued for delivery to the matched account's address. UserID is
	// that account; Details carries the provider only, never the address or the
	// confirmation token.
	TypeOAuthPendingLinkSent = "oauth_pending_link_sent"
	// TypeOAuthUnlinkCodeSent is a provider-bound unlink_oauth code issued and
	// enqueued for delivery to a verified recovery identifier (design §5.4). Details
	// carries the provider only, never a destination or secret.
	TypeOAuthUnlinkCodeSent = "oauth_unlink_code_sent"
	// TypeOAuthUnlinked is a completed code-gated provider unlink (design §5.4).
	TypeOAuthUnlinked = "oauth_unlinked"
	// TypeAPIKeyAuth is an API-key authentication attempt.
	TypeAPIKeyAuth = "apikey_auth"
	// TypeTokenIssued is a bearer-JWT issuance (POST /auth/token): success,
	// failure or blocked, with the same reasons as a password login.
	TypeTokenIssued = "token_issued"
	// TypeRefresh is a refresh-token rotation (POST /auth/refresh). Recorded
	// success on a rotation or a single-use grace refresh (the grace lane carries
	// a "grace" detail).
	TypeRefresh = "refresh"
	// TypeRefreshReuse is a blocked refresh-token reuse: a second arrival on the
	// already-consumed previous slot. It revokes the session and, unlike other
	// events, ALSO emits an unconditional WARN via Config.Logger even when the
	// audit rail is unwired (a nil-audit host must not be blind to token theft).
	TypeRefreshReuse = "refresh_reuse"

	// TypeChallengeLockout is a code challenge locked out after the wrong-attempt
	// budget was spent (design §3.2): the atomic consume deleted the row and the
	// flow is blocked. It is the challenge rail's own security-control event —
	// the business flow behind the challenge records its own domain event
	// separately. Details carries the non-secret purpose label only; the secret
	// code is never recorded.
	TypeChallengeLockout = "challenge_lockout"

	// Invitation vocabulary (design §6, wired by A6's invitation service). Grants are
	// the security-relevant events: TypeInvitationGranted is recorded success (the
	// Granter accepted), failure (the grant could not be confirmed), or blocked
	// with reason superseded (a committed grant is no longer in effect) or fenced
	// (a cancellation fenced the operation first) on accept, direct-add, and
	// resolve-on-registration.
	//
	// TypeInvitationCreated is a pending invitation being minted.
	TypeInvitationCreated = "invitation_created"
	// TypeInvitationGranted is a grant-on-accept attempt (StatusSuccess when the
	// Granter granted, StatusFailure when it rejected — or, with reason
	// invalid_token or policy, when the accept presented a wrong token or came from
	// a user the invitation is not addressed to — StatusBlocked with reason
	// superseded when a committed grant is no longer in effect, or reason fenced
	// when a cancellation fenced the operation before any grant committed).
	TypeInvitationGranted = "invitation_granted"
	// TypeInvitationDeclined is an invitee declining a pending invitations.
	TypeInvitationDeclined = "invitation_declined"
	// TypeInvitationCancelled is an owner cancelling an invitation. Cancelling an
	// accepting claim adds Details claim_status=accepting: StatusSuccess with
	// fence=fenced when the grant was fenced, or StatusBlocked when access exists
	// and the host revokes instead: fence=committed with reason already_granted
	// when the grant had committed, or fence=fenced with reason
	// accepted_after_fence when a completion won despite the fence.
	TypeInvitationCancelled = "invitation_cancelled"

	// TypeStepUpChallengeSent is a recent-authentication step-up code issued and
	// enqueued for delivery to a verified identifier (design §5.0). Details carries
	// the grant purpose (an operation identifier, never a secret or destination).
	TypeStepUpChallengeSent = "step_up_challenge_sent"
	// TypeStepUp is a step-up completion attempt earning a recent-authentication
	// grant (design §5.0): StatusSuccess when a grant was minted, StatusFailure when
	// the presented proof was rejected, StatusBlocked when a protective control
	// (lockout) refused it. Details carries the grant purpose only.
	TypeStepUp = "step_up"

	// TypeEmailChangeCodeSent is an email add/change ownership-proof code issued and
	// enqueued for delivery to the proposed NEW address (design §5.5). Details never
	// carries the address or code.
	TypeEmailChangeCodeSent = "email_change_code_sent"
	// TypeEmailChanged is a confirmed email add/change (design §5.5): the proposed
	// address was proven and claimed through the atomic revision-CAS apply.
	TypeEmailChanged = "email_changed"
	// TypeEmailRemoved is a confirmed email identifier removal (design §5.5).
	TypeEmailRemoved = "email_removed"
	// TypePhoneChangeCodeSent is a phone add/change ownership-proof code issued and
	// enqueued for delivery to the proposed NEW number (design §5.5).
	TypePhoneChangeCodeSent = "phone_change_code_sent"
	// TypePhoneChanged is a confirmed phone add/change (design §5.5).
	TypePhoneChanged = "phone_changed"
	// TypePhoneRemoved is a confirmed phone identifier removal (design §5.5).
	TypePhoneRemoved = "phone_removed"
	// TypeIdentifierUsesChanged is a confirmed identifier use-flag / primary change
	// through the revision-serialized credential rail (design §5.5).
	TypeIdentifierUsesChanged = "identifier_uses_changed"

	// TypeUserDeactivated is an applied account-lifecycle transition to
	// "deactivated" (CHAU-1.1): the subject's sessions and authentication grants
	// were revoked and no new session may be minted. UserID is the TARGET subject
	// and Actor is the administrator who applied it. Details carries the resulting
	// status and the revoked-session count only — never an address, display name,
	// or credential material. A no-op replay records nothing.
	TypeUserDeactivated = "user_deactivated"
	// TypeUserReactivated is an applied account-lifecycle transition back to
	// "active" (CHAU-1.1). It fabricates no session: the subject must authenticate
	// again. Details follow TypeUserDeactivated's rules.
	TypeUserReactivated = "user_reactivated"

	// TypeVerificationResendRequested is a PUBLIC registration-verification resend
	// request (CHAU-2.2): StatusSuccess when the opaque replacement job was
	// admitted, StatusBlocked when a resend budget refused it. It carries NO user
	// id — the public request path never resolves an account — and no address or
	// code. Details carries the identifier kind only.
	TypeVerificationResendRequested = "verification_resend_requested"
	// TypeVerificationResendIssued is a registration-verification code actually
	// re-issued: from the delivery worker after it resolved an active, unverified
	// target, or from the authorized admin resend. UserID identifies the target;
	// Actor is set only on the admin path. Details carries the identifier kind
	// only — never the address or the code.
	TypeVerificationResendIssued = "verification_resend_issued"

	// TypePasswordlessProvisioned is a magic-link redemption that CREATED the
	// account (CHAU-6.6): the address had no owner and the link's captured intent
	// permitted provisioning. UserID is the new subject. Details carry the
	// identifier kind, the challenge purpose, and the outcome class ONLY — never
	// the address or the token. The public response stays generic; this rail is
	// where an operator can tell provisioning from an ordinary login.
	TypePasswordlessProvisioned = "passwordless_provisioned"
	// TypePasswordlessAdopted is a magic-link redemption that VERIFIED and adopted
	// a previously unverified claim, revoking every credential that predated the
	// proof. Details follow TypePasswordlessProvisioned's rules.
	TypePasswordlessAdopted = "passwordless_adopted"

	// TypePasswordlessStart is a passwordless login start (design §4.3):
	// StatusSuccess when the opaque delivery job was accepted (enqueued),
	// StatusBlocked when a start budget refused it. Details carries the identifier
	// kind and the challenge purpose (login_magic_link / login_otp) ONLY — never the
	// identifier value, code, or token. The start never resolves the account on the
	// request path, so the event carries no UserID (enumeration safety, §4.1).
	TypePasswordlessStart = "passwordless_start"
	// TypePasswordlessLogin is a passwordless login completion (design §4.3):
	// StatusSuccess on a minted session, StatusFailure on the single generic 401
	// (unknown/expired/replayed/stale/wrong secret, disabled kind, malformed
	// identifier), and StatusBlocked when a verify/redeem budget refused it —
	// mirroring the password `login` event's three statuses. Details carries the
	// identifier kind and the challenge purpose ONLY — never the identifier value,
	// code, or token.
	TypePasswordlessLogin = "passwordless_login"

	// Machine-identity lifecycle vocabulary (the administrative counterpart to
	// TypeAPIKeyAuth, which records a credential USE). The bundled lifecycle
	// routes run behind a host authorization gate, so every row here is an
	// authorized administrator acting: Actor is the principal resolved from the
	// request context, never a caller-supplied creator string.
	//
	// TypeServiceAccountCreated is a minted machine identity. UserID is the
	// act-as owner when the account acts as a human, empty otherwise. Details
	// carries the service-account id, act_as_user, and delegated (an act-as
	// account whose owner is someone other than its creator).
	TypeServiceAccountCreated = "service_account_created"
	// TypeAPIKeyMinted is an issued API key. Details carries the key PREFIX and
	// the owning service-account id — NEVER the raw key or its hash.
	TypeAPIKeyMinted = "api_key_minted"
	// TypeAPIKeyRevoked is an API key marked revoked. Details carries the key id
	// only (the revoke coordinate is the key itself; the port has no read-by-id).
	TypeAPIKeyRevoked = "api_key_revoked"
	// TypeServiceAccountDisabled is a service account disabled, every
	// unrevoked key revoked with it. Details carry service_account_id and
	// keys_revoked.
	TypeServiceAccountDisabled = "service_account_disabled"
	// TypeServiceAccountEnabled is a disabled service account enabled again;
	// its revoked keys stay revoked. Details carry service_account_id.
	TypeServiceAccountEnabled = "service_account_enabled"

	// External identity vocabulary. Details carry the host provider name, the
	// identifier kind and a reason only — never the token, claims, address or
	// provider payload.
	//
	// TypeExternalLogin is the first successful verification of one provider
	// token, recorded once per token per login-record cache rather than once per
	// request. UserID is the mapped user; Details carries the provider and the
	// assurance the provider asserted (aal1 or aal2).
	TypeExternalLogin = "external_login"
	// TypeExternalSubjectProvisioned is a user created on first verification of
	// an unknown provider subject. UserID is the new user.
	TypeExternalSubjectProvisioned = "external_subject_provisioned"
	// TypeExternalSubjectUnknown is a verified subject with no mirror row on a
	// provider that does not provision.
	TypeExternalSubjectUnknown = "external_subject_unknown"
	// TypeExternalSubjectCollision is a refused provisioning whose address is an
	// existing account's login or recovery identifier. UserID is that account;
	// an administrator links the subject explicitly.
	TypeExternalSubjectCollision = "external_subject_collision"
	// TypeExternalSubjectDenied is a verified subject refused by policy. Details
	// carry the reason: tenant, sign_in_method, unverified_email, no_identifier,
	// invalid_identifier, inactive, revoked, authentication_age_exceeded,
	// authenticated_at_missing (ExternalProvider.MaxAuthenticationAge) or
	// authenticated_at_unasserted (ExternalProvider.RequireAuthenticationTime).
	TypeExternalSubjectDenied = "external_subject_denied"
	// TypeExternalRevocationUnavailable is recorded once per provider outage
	// episode, not once per affected request.
	TypeExternalRevocationUnavailable = "external_revocation_unavailable"

	// TypeMFARequiredDenied is a credential below AAL2 refused where
	// multi-factor authentication is required: StatusBlocked, Budgeted per
	// client IP because an AAL1 holder can drive it. UserID is the principal for
	// the route, admin and machine gates and empty for the provider gate, which
	// refuses before the subject resolves. Details carry bounded values only:
	// gate (route, admin, machine, provider or enrollment), credential
	// (access_token, api_key or external_token), provider (external tokens),
	// assurance (the presented level: aal1, aal2 or unknown), required (aal2),
	// reason (assurance: below AAL2; method: AAL2 from a source MFAConfig.Methods
	// does not admit; api_key; enrollment: the enrollment grace closed without a
	// second factor) and method (the credential's AAL2 source: passkey, totp,
	// idp or none) — never an amr value, a claim or the token. The enrollment
	// gate records once per session, not per request.
	TypeMFARequiredDenied = "mfa_required_denied"

	// Passkey vocabulary (SOC 2 S4b). Details carry bounded values and opaque
	// row ids only — never a public key, credential id, user handle, passkey
	// name, origin or raw ceremony response.
	//
	// TypePasskeyRegistered is a passkey added (success; UserID the owner):
	// passkey_id, user_verified, backup_eligible and aaguid (32 hex digits).
	TypePasskeyRegistered = "passkey_registered"
	// TypePasskeyRemoved is the owner removing a passkey (success): passkey_id
	// and remaining (the passkeys left).
	TypePasskeyRemoved = "passkey_removed"
	// TypePasskeysReset is an administrator deleting every passkey of a user
	// (success; UserID the target, Actor the administrator): removed.
	TypePasskeysReset = "passkeys_reset"
	// TypePasskeyLogin is a verified passkey assertion that minted a session
	// (success; UserID the owner): ceremony (sign_in or step_up), assurance
	// (aal1 or aal2) and backup_state.
	TypePasskeyLogin = "passkey_login"
	// TypePasskeyFailed is a refused passkey ceremony (failure, Budgeted per
	// client IP): ceremony (sign_in, step_up or registration) and reason
	// (unknown_credential, user_mismatch, rejected, uv_required, expired,
	// inactive or conflict). UserID is empty for sign-in and the caller for
	// step-up and registration.
	TypePasskeyFailed = "passkey_failed"
	// TypePasskeySignCountRegressed is an assertion refused because its
	// signature counter did not advance past the stored one — a possible cloned
	// authenticator (blocked, Budgeted per client IP; UserID the owner):
	// passkey_id, stored and presented.
	TypePasskeySignCountRegressed = "passkey_sign_count_regressed"

	// Second-factor vocabulary (SOC 2 S4c). Details carry bounded values and
	// small integers only — never a secret, code, digest, key URI or ciphertext.
	//
	// TypeTOTPEnrolled is an authenticator app confirmed (success; UserID the
	// owner): tier (bootstrap or aal2, the gate the enrollment began under).
	TypeTOTPEnrolled = "totp_enrolled"
	// TypeTOTPRemoved is the owner removing the authenticator app and its
	// recovery codes (success); every session was revoked.
	TypeTOTPRemoved = "totp_removed"
	// TypeTOTPReset is an administrator removing a user's authenticator app
	// (success; UserID the target, Actor the administrator): removed.
	TypeTOTPReset = "totp_reset"
	// TypeTOTPStepUp is a TOTP or recovery-code step-up that re-minted the
	// session (success): method (totp or recovery_code) and assurance.
	TypeTOTPStepUp = "totp_step_up"
	// TypeTOTPFailed is a refused second-factor code (failure, Budgeted per
	// client IP; UserID the caller): method (totp or recovery_code) and reason
	// (invalid, reused, locked, not_enrolled, expired, envelope, inactive or
	// conflict).
	TypeTOTPFailed = "totp_failed"
	// TypeTOTPLocked is the failure that locked the second factor (blocked,
	// Budgeted per client IP): attempts and locked_for_seconds.
	TypeTOTPLocked = "totp_locked"
	// TypeRecoveryCodesGenerated is a recovery-code set minted (success): count
	// and trigger (enrollment or regenerate).
	TypeRecoveryCodesGenerated = "recovery_codes_generated"
	// TypeRecoveryCodeUsed is a recovery code spent on a step-up (success):
	// remaining.
	TypeRecoveryCodeUsed = "recovery_code_used"

	// Session-management vocabulary. Both are recorded on success only; UserID is
	// the session owner.
	//
	// TypeSessionRevoked is one session revoked by its owner. Details carries the
	// session id.
	TypeSessionRevoked = "session_revoked"
	// TypeAllSessionsRevoked is every session of a user revoked, pending
	// approvals fenced with them.
	TypeAllSessionsRevoked = "all_sessions_revoked"
	// TypeAllSessionsRevokedGlobal is every session in the fleet revoked
	// (RevokeAllSessions). Actor is the operator. A StatusStarted row precedes
	// the first batch with batch_size; the StatusSuccess or StatusFailure row
	// after it carries users, sessions, grants, batches and duration_ms — the
	// partial counts and error_kind on failure.
	TypeAllSessionsRevokedGlobal = "all_sessions_revoked_global"
	// TypeSessionIdleExpired is a first-party session ended at refresh by
	// SessionsConfig.IdleTimeout (blocked), recorded once per session row.
	// UserID is the session owner; Details carries session_id, last_activity
	// (UTC RFC 3339), idle_seconds, idle_timeout_seconds and surface
	// ("refresh") — never a token or a hash.
	TypeSessionIdleExpired = "session_idle_expired"

	// TypeUserDirectoryRead is one successful operator-directory read (the page
	// or one user's summary). Actor is the principal resolved from the request
	// context; Details carries page (true for the page read) and, for a summary,
	// target_user_id — never a display name or address.
	TypeUserDirectoryRead = "user_directory_read"
	// TypeAuditRead is one completed read of an audit listing over HTTP — the
	// security-event rail, or the authorization change history a host bridges
	// in. Actor is the reader; Details carries rail (AuditReadRail*), filters
	// (the filter keys present, never their values), limit, returned and
	// strategy. A denied read records nothing new.
	TypeAuditRead = "audit_read"

	// OAuth2 authorization-server vocabulary (logic/authentication/oauth2 keeps
	// its Type* names as aliases). Details carries client_id and, when known,
	// session_id.
	//
	// TypeOAuth2ConsentApproved is an approved consent screen.
	TypeOAuth2ConsentApproved = "oauth2_consent_approved"
	// TypeOAuth2ConsentDenied is a consent screen the user denied.
	TypeOAuth2ConsentDenied = "oauth2_consent_denied"
	// TypeOAuth2ConnectionCreated is an authorization code redeemed into a
	// delegated session.
	TypeOAuth2ConnectionCreated = "oauth2_connection_created"
	// TypeOAuth2Refresh is a delegated refresh-token rotation.
	TypeOAuth2Refresh = "oauth2_refresh"
	// TypeOAuth2RefreshReuse is a blocked reuse of a rotated delegated refresh
	// token; the connection is revoked.
	TypeOAuth2RefreshReuse = "oauth2_refresh_reuse"
	// TypeOAuth2TokenExchanged is a delegated token exchange.
	TypeOAuth2TokenExchanged = "oauth2_token_exchanged"
	// TypeOAuth2RevocationRequested is a client-requested revocation.
	TypeOAuth2RevocationRequested = "oauth2_revocation_requested"
	// TypeOAuth2GrantFailed is a token-endpoint request (code redemption,
	// refresh, exchange, revocation or introspection) refused for its grant,
	// client or a parameter value: StatusFailure with reason invalid_grant,
	// invalid_client, invalid_request, invalid_scope or invalid_target. A
	// refresh reuse records TypeOAuth2RefreshReuse instead.
	TypeOAuth2GrantFailed = "oauth2_grant_failed"

	// Retention and erasure vocabulary (SOC 2 S3). Details carry counts,
	// cutoffs and object coordinates only — never an address, a display name or
	// credential material.
	//
	// TypeUserErased is an applied erasure. UserID is the TARGET subject and
	// Actor the administrator who applied it; Details carry the erasure's bounded
	// counts. The store writes it INSIDE the erase transaction, so the row and
	// the erasure commit together.
	TypeUserErased = "user_erased"
	// TypeServiceAccountDeleted is a deleted machine identity. Details carry
	// service_account_id, key_count, unrevoked_key_count and cascade — counts,
	// never a key prefix, hash or key id.
	TypeServiceAccountDeleted = "service_account_deleted"
	// TypeSecurityEventsPurged is one bounded purge call over this rail,
	// written by the store with Actor{Type: "system", ID: "maintenance"} — one
	// row per call, a zero count included. Details carry cutoff, count,
	// floor_days, max_rows and, on PostgreSQL, session_user.
	TypeSecurityEventsPurged = "security_events_purged"
	// TypeSecurityEventsArchived is one archive object of this rail, written
	// by the maintenance service after the object read back intact and before
	// any row it holds is purged. Details carry object, sha256, rows,
	// first_created_at, first_id, last_created_at, last_id and
	// archive_location.
	TypeSecurityEventsArchived = "security_events_archived"
)

// Status vocabulary (design §5.1).
const (
	// StatusSuccess marks an operation that completed.
	StatusSuccess = "success"
	// StatusFailure marks a denied operation (bad credentials, expired key, an
	// unverified-email gate, etc.).
	StatusFailure = "failure"
	// StatusBlocked marks an operation refused by a protective control (a
	// rate-limited login, a revoked API key).
	StatusBlocked = "blocked"
	// StatusStarted marks the start of a long operation whose outcome a later
	// row records, so the evidence that it began survives a failure mid-way.
	StatusStarted = "started"
)

// The rail an audit_read row names under the Details key "rail".
const (
	// AuditReadRailSecurityEvents is a read of this security-event rail.
	AuditReadRailSecurityEvents = "security_events"
	// AuditReadRailAuthorization is a read of the authorization change history.
	AuditReadRailAuthorization = "authorization"
)

// Reason vocabulary: the bounded value a denial row carries under the Details key
// "reason". A reason names the class of refusal only — never an error message,
// address, code or token.
const (
	// ReasonInvalidCredentials is a password or credential that did not match.
	ReasonInvalidCredentials = "invalid_credentials"
	// ReasonRateLimited is an attempt refused by a rate limit.
	ReasonRateLimited = "rate_limited"
	// ReasonInactive is an attempt on a deactivated account.
	ReasonInactive = "inactive"
	// ReasonEmailUnverified is an attempt refused because the email address is
	// not verified (or, for an OAuth provider, not asserted as verified).
	ReasonEmailUnverified = "email_unverified"
	// ReasonInvalidToken is an unknown, expired, used or unbound single-use token.
	ReasonInvalidToken = "invalid_token"
	// ReasonInvalidCode is a wrong, expired or unbound challenge code.
	ReasonInvalidCode = "invalid_code"
	// ReasonLockout is a challenge whose wrong-attempt budget is spent.
	ReasonLockout = "lockout"
	// ReasonPolicy is a request refused by policy: a rejected new password, a
	// credential-policy refusal, or an invitation addressed to someone else.
	ReasonPolicy = "policy"
	// ReasonInvalidState is an unknown, expired, misrouted or stale OAuth state.
	ReasonInvalidState = "invalid_state"
	// ReasonExchangeFailed is an OAuth code the provider refused to exchange.
	ReasonExchangeFailed = "exchange_failed"
	// ReasonIdentityInvalid is a provider identity that failed validation.
	ReasonIdentityInvalid = "identity_invalid"
	// ReasonLinkConflict is a provider identity or address already claimed, or
	// a link whose account changed under it.
	ReasonLinkConflict = "link_conflict"
	// ReasonInvalidGrant is an OAuth2 code, refresh token or access token that
	// is unknown, expired, revoked or bound to something else.
	ReasonInvalidGrant = "invalid_grant"
	// ReasonInvalidClient is an OAuth2 client that failed resolution or
	// authentication.
	ReasonInvalidClient = "invalid_client"
	// ReasonRevisionConflict is a credential change that lost a concurrent
	// revision race; the proof is spent and a new one is needed.
	ReasonRevisionConflict = "revision_conflict"
	// ReasonPrecondition is an operation the account's credential state refused
	// before any proof was spent: a password set on an account that has one, or
	// removed from an account that has none.
	ReasonPrecondition = "precondition"
	// ReasonClaimConflict is an identifier change whose proven value another
	// account claimed first.
	ReasonClaimConflict = "claim_conflict"
	// ReasonUnknownProvider is an OAuth callback or link start naming a provider
	// the host does not wire.
	ReasonUnknownProvider = "unknown_provider"
	// ReasonRegistrationClosed is an account creation refused because
	// IdentityConfig.Registration is closed.
	ReasonRegistrationClosed = "registration_closed"
	// ReasonInvitationRequired is an account creation refused because
	// IdentityConfig.Registration is invitation_only and no invitation is
	// pending for the address.
	ReasonInvitationRequired = "invitation_required"
	// ReasonServiceAccountDisabled is an API key refused because its service
	// account is disabled.
	ReasonServiceAccountDisabled = "service_account_disabled"
	// ReasonInvalidRequest is an OAuth2 token-endpoint parameter value the
	// server evaluated and refused.
	ReasonInvalidRequest = "invalid_request"
	// ReasonInvalidScope is an OAuth2 scope the server refused.
	ReasonInvalidScope = "invalid_scope"
	// ReasonInvalidTarget is an OAuth2 resource or audience the server refused.
	ReasonInvalidTarget = "invalid_target"
	// ReasonUnknownCredential is a passkey assertion naming no stored passkey.
	ReasonUnknownCredential = "unknown_credential"
	// ReasonUserMismatch is a step-up assertion by another user's passkey.
	ReasonUserMismatch = "user_mismatch"
	// ReasonRejected is a passkey ceremony response or ceremony handle the
	// relying party or the challenge rail refused.
	ReasonRejected = "rejected"
	// ReasonUVRequired is a passkey response without the user verification the
	// relying party required.
	ReasonUVRequired = "uv_required"
	// ReasonExpired is a passkey ceremony finished after its handle expired.
	ReasonExpired = "expired"
	// ReasonConflict is a passkey use that lost a concurrent change: the sign
	// count moved, the passkey was removed, or the account revision advanced.
	ReasonConflict = "conflict"
	// ReasonInvalid is a second-factor code that matched nothing.
	ReasonInvalid = "invalid"
	// ReasonReused is a TOTP code whose time step was already accepted.
	ReasonReused = "reused"
	// ReasonLocked is a second-factor attempt refused by its lockout.
	ReasonLocked = "locked"
	// ReasonNotEnrolled is a second-factor attempt for a user with no confirmed
	// authenticator app.
	ReasonNotEnrolled = "not_enrolled"
	// ReasonEnvelope is a sealed TOTP secret that did not open for its user.
	ReasonEnvelope = "envelope"
)

// Attribution bounds BoundValue applies, in bytes.
const (
	// MaxCorrelationIDLen is the longest RequestID or TraceID the Recorder
	// stamps. A longer id, or one carrying a byte outside printable ASCII, is
	// recorded blank — never truncated and never refused.
	MaxCorrelationIDLen = 128
	// MaxUserAgentLen is the longest UserAgent the Recorder stamps; a longer one
	// is cut on a rune boundary.
	MaxUserAgentLen = 512
	// MaxDetailLen is the longest string value a Details key carries; a longer
	// one is recorded blank.
	MaxDetailLen = 2048
)

// The fields BoundValue knows. Each names the rule applied to one attributed
// value.
const (
	// FieldRequestID is SecurityEvent.RequestID: printable ASCII of at most
	// MaxCorrelationIDLen bytes, else blank.
	FieldRequestID Field = iota + 1
	// FieldTraceID is SecurityEvent.TraceID, under FieldRequestID's rule.
	FieldTraceID
	// FieldIPAddress is SecurityEvent.IPAddress: blank unless it is clean text
	// that netip.ParseAddr accepts.
	FieldIPAddress
	// FieldUserAgent is SecurityEvent.UserAgent: blank unless it is clean text,
	// then cut at MaxUserAgentLen bytes on a rune boundary.
	FieldUserAgent
	// FieldDetail is a top-level string value of SecurityEvent.Details, or an
	// identifier bound for one: blank unless it is clean text of at most
	// MaxDetailLen bytes.
	FieldDetail
)

// types is the registry Types returns: every event type this pocket records.
var types = []string{
	TypeRegister, TypeLogin, TypeLogout, TypePasswordChange, TypePasswordReset,
	TypePasswordSet, TypePasswordRemoveCodeSent, TypePasswordRemoved, TypeEmailVerified,
	TypeOAuthLogin, TypeOAuthRegister, TypeOAuthLinkVerified, TypeOAuthLinked,
	TypeOAuthPendingLinkSent, TypeOAuthUnlinkCodeSent, TypeOAuthUnlinked, TypeAPIKeyAuth, TypeTokenIssued,
	TypeRefresh, TypeRefreshReuse, TypeChallengeLockout,
	TypeInvitationCreated, TypeInvitationGranted, TypeInvitationDeclined, TypeInvitationCancelled,
	TypeStepUpChallengeSent, TypeStepUp,
	TypeEmailChangeCodeSent, TypeEmailChanged, TypeEmailRemoved,
	TypePhoneChangeCodeSent, TypePhoneChanged, TypePhoneRemoved, TypeIdentifierUsesChanged,
	TypeUserDeactivated, TypeUserReactivated,
	TypeVerificationResendRequested, TypeVerificationResendIssued,
	TypePasswordlessProvisioned, TypePasswordlessAdopted, TypePasswordlessStart, TypePasswordlessLogin,
	TypeServiceAccountCreated, TypeAPIKeyMinted, TypeAPIKeyRevoked, TypeServiceAccountDisabled, TypeServiceAccountEnabled,
	TypeExternalSubjectProvisioned, TypeExternalSubjectUnknown, TypeExternalSubjectCollision,
	TypeExternalSubjectDenied, TypeExternalRevocationUnavailable, TypeExternalLogin, TypeMFARequiredDenied,
	TypePasskeyRegistered, TypePasskeyRemoved, TypePasskeysReset, TypePasskeyLogin, TypePasskeyFailed, TypePasskeySignCountRegressed,
	TypeTOTPEnrolled, TypeTOTPRemoved, TypeTOTPReset, TypeTOTPStepUp, TypeTOTPFailed, TypeTOTPLocked,
	TypeRecoveryCodesGenerated, TypeRecoveryCodeUsed,
	TypeSessionRevoked, TypeAllSessionsRevoked, TypeAllSessionsRevokedGlobal, TypeSessionIdleExpired, TypeUserDirectoryRead, TypeAuditRead,
	TypeOAuth2ConsentApproved, TypeOAuth2ConsentDenied, TypeOAuth2ConnectionCreated, TypeOAuth2Refresh,
	TypeOAuth2RefreshReuse, TypeOAuth2TokenExchanged, TypeOAuth2RevocationRequested, TypeOAuth2GrantFailed,
	TypeUserErased, TypeServiceAccountDeleted, TypeSecurityEventsPurged, TypeSecurityEventsArchived,
}

// Field selects the rule BoundValue applies to an attributed value.
type Field uint8

// Principal is the effective actor (an AV5 subject_type/subject_id pair) that
// triggered an event, when one is resolved — a machine identity for an API-key
// authentication, for example. It mirrors auth.Principal's shape structurally;
// this rim keeps its own copy so the append-only domain carries no import edge
// to the internal service. The zero value means "no distinct actor" (the event
// is attributed to UserID, or to no one).
type Principal struct {
	Type string
	ID   string
}

// SecurityEvent is one append-only audit row. UserID and Actor are both optional
// (a failed login for an unknown email has neither a user nor a distinct actor);
// Details is an open bag of identifiers and key prefixes (never secrets — see the
// package doc). RequestID and TraceID correlate the row with the request and
// trace that produced it; empty when the context carried none or the Recorder
// blanked one (MaxCorrelationIDLen). CreatedAt is the ordering key, tie-broken by
// ID.
type SecurityEvent struct {
	ID          string
	UserID      string // optional
	Actor       Principal
	EventType   string
	EventStatus string
	Details     map[string]any
	IPAddress   string
	UserAgent   string
	RequestID   string
	TraceID     string
	CreatedAt   time.Time
}

// Types returns every event type the authentication pocket records, in no
// particular order. event_type is free TEXT at rest: a type a host bridges into
// the rail (an authorization denial, say) is valid there without being listed.
func Types() []string {
	return slices.Clone(types)
}

// BoundValue returns value as the Recorder stamps it into field, or "" when
// field's rule refuses it. It is the ONE attribution rule: Record applies it to
// every attributed value, and a producer that bounds an identifier before
// recording calls it rather than keeping a second copy. Clean text is valid
// UTF-8 with no control character (C0, DEL or C1; NUL included) — the values a
// PostgreSQL TEXT or JSONB column would refuse are among them, so a refused
// value is recorded blank instead of losing the row. Only the User-Agent is
// ever truncated. An unknown field yields "".
func BoundValue(field Field, value string) string {
	switch field {
	case FieldRequestID, FieldTraceID:
		if len(value) > MaxCorrelationIDLen {
			return ""
		}
		for i := 0; i < len(value); i++ {
			if value[i] < 0x20 || value[i] > 0x7e {
				return ""
			}
		}
		return value
	case FieldIPAddress:
		if !cleanText(value) {
			return ""
		}
		if _, err := netip.ParseAddr(value); err != nil {
			return ""
		}
		return value
	case FieldUserAgent:
		if !cleanText(value) {
			return ""
		}
		if len(value) <= MaxUserAgentLen {
			return value
		}
		cut := MaxUserAgentLen
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		return value[:cut]
	case FieldDetail:
		if len(value) > MaxDetailLen || !cleanText(value) {
			return ""
		}
		return value
	}
	return ""
}

// cleanText reports whether value is valid UTF-8 carrying no control character.
func cleanText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// New builds a SecurityEvent of eventType/eventStatus as of now, minting its ID
// from ids (empty under sdk.DatabaseID — the store then assigns the key).
// The caller sets the optional UserID/Actor/Details/IPAddress/UserAgent fields
// (the service's record helper does this from the request's client-info
// carrier).
func New(ids sdk.IDGenerator, eventType, eventStatus string, now time.Time) SecurityEvent {
	return SecurityEvent{
		ID:          ids.MustGenerate(),
		EventType:   eventType,
		EventStatus: eventStatus,
		CreatedAt:   now.UTC(),
	}
}
