package authentication

import (
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
)

// Repositories is the set of outbound ports the pocket needs. A store adapter
// (e.g. pockets/authentication/stores/turso) or a host fills it; the pocket stays
// dialect-blind. Passwords is split from Users on purpose — credential material
// is stored and access-controlled independently of general user reads.
type Repositories struct {
	Users user.UserRepository
	// Identifiers backs the v3 identity-discovery rail (design §2.2): active
	// login/recovery lookup, per-user listing, and the revision-CAS
	// ApplyVerifiedChange. The atomic CreateWithPrimaryIdentifier lives on Users
	// because it commits a user and its first identifier together; Users and
	// Identifiers must therefore be backed by one transaction-capable adapter. The
	// slot is frozen here (AV3-1.2); it becomes REQUIRED when registration re-keys
	// onto identifiers (phase 5). Nil is tolerated until then.
	Identifiers identifier.IdentifierRepository
	Passwords   user.PasswordRepository
	Sessions    session.SessionRepository
	// OAuthAccounts and OAuthStates back the OAuth flow (design §3). They may be
	// nil when OAuthConfig.Providers is empty (OAuth off); wiring providers without
	// them is ErrOAuthReposRequired at construction.
	OAuthAccounts oauthaccount.OAuthAccountRepository
	OAuthStates   oauthstate.StateRepository
	// ServiceAccounts and APIKeys back machine identity (design §4.1). They are
	// both-or-neither: both nil → the subsystem is off (routes not registered,
	// the bearer API-key path inert); one without the other →
	// ErrMachineReposRequired at construction.
	ServiceAccounts serviceaccount.ServiceAccountRepository
	APIKeys         apikey.APIKeyRepository
	// SecurityEvents backs the append-only audit rail (design §5.1). It is
	// OPTIONAL (ratified AV9), independently of every other port: nil → the
	// pocket keeps NO audit trail (the synchronous recording site is a no-op),
	// and no construction error is raised. When wired, every sensitive op records
	// a security event synchronously and a write failure is logged at WARN,
	// never failing the auth flow.
	SecurityEvents securityevent.SecurityEventRepository
	// Invitations backs the resource-invitation flow (design §6). It may be nil
	// when InvitationsConfig.Granter is nil (invitations off); wiring a Granter without it is
	// ErrInvitationRepoRequired at construction.
	Invitations invitations.InvitationRepository
	// Challenges backs the atomic secret rail (design §3.2): HMAC-protected OTP
	// codes and SHA-256 magic-link tokens with atomic replace/consume. The slot is
	// frozen here (AV3-0.3); it becomes REQUIRED when the challenge subsystem is
	// enabled (phase 3). Nil is tolerated until then.
	Challenges challenge.Repository
	// PasswordResets backs the atomic password-reset composition (design §5.9):
	// one transaction that consumes the password_reset challenge, sets the typed
	// password row, and revokes all sessions plus outstanding password/reset
	// grants and challenges. It must be backed by the same transaction-capable
	// adapter as Passwords/Sessions/Challenges/AuthenticationGrants. Wired whenever
	// the challenge-backed forgot/reset flow is active; ResetPassword refuses while
	// it is nil (fail closed).
	PasswordResets passwordreset.Repository
	// ContactChanges backs the pending-value flow state of an identifier add/change
	// (design §2.4): the PendingChange row holding the new normalized value and
	// requested uses between a change flow's start and its confirm, as an atomic
	// replace-per-(user, kind) with single-use Consume. It carries no secret — the
	// code/token and its lockout ride Challenges. The slot is frozen here (AV3-1.3);
	// it becomes REQUIRED when the identifier-management flows are wired (phase 6).
	// Nil is tolerated until then.
	ContactChanges contactchange.Repository
	// AuthenticationGrants backs recent-authentication / step-up grants (design
	// §5.0): the single-use, session-bound proof a sensitive mutation requires.
	// The slot is frozen here (AV3-0.3); it becomes REQUIRED when the credential
	// suite is enabled (phase 6). Nil is tolerated until then.
	AuthenticationGrants authgrant.Repository
	// CredentialMutations backs the revision-serialized credential/identifier
	// mutation rail (design §5.6): Snapshot reads the typed MethodSet +
	// auth_revision and Apply performs one revision-CAS typed mutation atomically.
	// The slot is frozen here (AV3-0.4); it becomes REQUIRED when the credential
	// suite is enabled (phase 6). Nil is tolerated until then.
	CredentialMutations credential.MutationRepository

	// UserAdmin is the OPTIONAL user-administration capability (CHAU-1.1): the
	// paginated operator directory (user.Summary pages) plus the ATOMIC lifecycle
	// transition that writes the status, increments auth_revision, and deletes
	// every session and authentication grant in one store transaction.
	//
	// Presence alone does NOT mount an HTTP surface. The bundled admin routes
	// require AdministrationConfig.UserAdminCheck as well, so a bundled store adapter can return
	// a complete Repositories without any authorization surface appearing. When it
	// is nil the trusted service methods fail closed with sdk.ErrNotFound.
	UserAdmin user.AdminRepository

	// ActiveSessions is the OPTIONAL fenced session-minting capability (CHAU-1.1):
	// it inserts a proposed session only while the owning user is atomically proven
	// active under the same serialization boundary, so a concurrent deactivation
	// cannot leave a session created after it.
	//
	// When wired, EVERY session mint — password login, /auth/token, OAuth,
	// passwordless, and the password-mutation remint — routes through it. When nil,
	// minting falls back to the ordinary Sessions.Create and the race is open,
	// which is why enabling the admin lifecycle routes without it is
	// ErrUserAdminReposRequired: a host must not advertise deactivation it cannot
	// honor.
	//
	// It must be backed by the same transaction-capable adapter as Users and
	// Sessions.
	ActiveSessions session.ActiveUserRepository

	// Passwordless is the OPTIONAL atomic magic-link redemption capability
	// (CHAU-6.1): ONE transaction that consumes the link's challenge, decides
	// login / adopt / provision, performs the identity mutation and any revocation,
	// and inserts the session.
	//
	// It is REQUIRED only when PasswordlessConfig.PasswordlessProvisionOnRedeem is enabled,
	// because provisioning is what makes the multi-table atomicity load-bearing: a
	// service-level consume-then-create sequence would leave a half-provisioned
	// account behind any failure. It must be backed by the same
	// transaction-capable adapter as Users, Identifiers, Passwords, Sessions,
	// Challenges, and AuthenticationGrants.
	Passwordless passwordless.Repository
}
