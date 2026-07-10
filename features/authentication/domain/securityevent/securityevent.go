// Package securityevent is the append-only audit-rail domain (design §5.1): a
// synchronous record of every sensitive authentication event — registrations,
// logins (success/failure/blocked), logouts, verifications, password changes,
// OAuth flows, API-key authentications, and token issuance. It is deliberately
// append-only: there are no Update or Delete methods on the port (structural,
// not enforced by a flag), because an audit trail that can be rewritten is not
// an audit trail.
//
// Content hygiene (plan-cut amendment, SRE): Details/IPAddress/UserAgent carry
// identifiers and key PREFIXES only — raw API keys, JWTs, session tokens,
// passwords, and OAuth tokens NEVER land in audit content. The service-layer
// writer is the single enforcement point.
//
// The rail is optional (ratified AV9): a host that wires no SecurityEvents
// repository simply keeps no audit trail; the recording site is then a no-op.
// The durable outbox emission rail (design §5.2) is DEFERRED (ratified AV10) —
// nothing here imports sdk/capabilities/events or features/events.
package securityevent

import (
	"time"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
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
	// TypePasswordReset is a completed password reset via a reset token.
	TypePasswordReset = "password_reset"
	// TypeEmailVerified is a completed email verification.
	TypeEmailVerified = "email_verified"
	// TypeOAuthLogin is an OAuth login into an already-linked identity.
	TypeOAuthLogin = "oauth_login"
	// TypeOAuthRegister is an OAuth-driven registration of a new user.
	TypeOAuthRegister = "oauth_register"
	// TypeOAuthLinkVerified is a completed anti-takeover pending-link confirmation.
	TypeOAuthLinkVerified = "oauth_link_verified"
	// TypeOAuthLinked is a session-gated provider link.
	TypeOAuthLinked = "oauth_linked"
	// TypeOAuthUnlinked is a provider unlink.
	TypeOAuthUnlinked = "oauth_unlinked"
	// TypeAPIKeyAuth is an API-key authentication attempt.
	TypeAPIKeyAuth = "apikey_auth"
	// TypeTokenIssued is a bearer-JWT issuance (POST /auth/token).
	TypeTokenIssued = "token_issued"

	// Invitation vocabulary (design §6, wired by A6's invitationsvc). Grants are
	// the security-relevant events: TypeInvitationGranted is recorded success (the
	// Granter accepted) or failure (the Granter rejected — the grant did not
	// happen) on accept, direct-add, and resolve-on-registration.
	//
	// TypeInvitationCreated is a pending invitation being minted.
	TypeInvitationCreated = "invitation_created"
	// TypeInvitationGranted is a grant-on-accept attempt (StatusSuccess when the
	// Granter granted, StatusFailure when it rejected).
	TypeInvitationGranted = "invitation_granted"
	// TypeInvitationDeclined is an invitee declining a pending invitation.
	TypeInvitationDeclined = "invitation_declined"
	// TypeInvitationCancelled is an owner cancelling a pending invitation.
	TypeInvitationCancelled = "invitation_cancelled"
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
)

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
// package doc). CreatedAt is the ordering key, tie-broken by ID.
type SecurityEvent struct {
	ID          string
	UserID      string // optional
	Actor       Principal
	EventType   string
	EventStatus string
	Details     map[string]any
	IPAddress   string
	UserAgent   string
	CreatedAt   time.Time
}

// New builds a SecurityEvent of eventType/eventStatus as of now, minting its ID
// from ids (empty under cryptids.Database — the store then assigns the key).
// The caller sets the optional UserID/Actor/Details/IPAddress/UserAgent fields
// (the service's record helper does this from the request's client-info
// carrier).
func New(ids cryptids.IDGenerator, eventType, eventStatus string, now time.Time) SecurityEvent {
	return SecurityEvent{
		ID:          ids.MustGenerate(),
		EventType:   eventType,
		EventStatus: eventStatus,
		CreatedAt:   now.UTC(),
	}
}
