// Package authentication is the public surface of the authentication feature module: the
// registration entry point (Register), the cross-feature identity capability
// (Service / NewService / RequireUser / CurrentUser), the host-filled ports
// (Repositories), the feature-owned PasswordHasher port, and the customization
// config (Config). Implementation lives in internal/; the domain type and
// repository-interface packages (user, session, verification) are public
// because hosts and store adapters reference them, but the services and
// handlers stay internal.
//
// The feature is datastore-free and view-free: it depends on its repository
// ports and sdk facilities only, never on a concrete store, an integration, or
// a view library. v1 is JSON-API only (see internal/http).
//
// Host-facing surface, all in this file per the feature charter's "<name>.go is
// the feature's entire host-facing surface" rule:
//
//   - Repositories — the five outbound ports a store adapter or host fills.
//   - PasswordHasher — the feature-owned hashing port (integrations/cryptids/
//     bcrypt satisfies it structurally).
//   - Config — required Hasher + Mailer (nil errors at construction), optional
//     RateLimiter (nil → in-memory), MailFrom, SessionCookie.
//   - NewService / Service.RequireUser / Service.CurrentUser — the surface a
//     host wires into another feature (e.g. cms admin gating).
//   - Register — mounts the feature's own HTTP routes.
package authentication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordreset"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	inbound "github.com/gopernicus/gopernicus/features/authentication/internal/inbound/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/invitationsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// ErrHasherRequired and ErrMailerRequired are returned by NewService/Register
// when the corresponding required Config field is nil. Unlike cms's safe
// silent defaults (nil Cache disables caching), a password feature with no
// hasher, or one that silently drops verification/reset mail, is a security
// foot-gun — so these degrade loudly at construction, never silently.
var (
	ErrHasherRequired = errors.New("auth: Config.Hasher is required")
	ErrMailerRequired = errors.New("auth: Config.Mailer is required")
	// ErrTokenSignerRequired is returned by NewService/Register when
	// Config.TokenSigner is nil. The access credential is a signed JWT (D3), so a
	// signer is REQUIRED — the core never synthesizes an ephemeral key (that
	// convenience lives in example hosts only). It degrades loudly at construction,
	// mirroring ErrHasherRequired.
	ErrTokenSignerRequired = errors.New("auth: Config.TokenSigner is required")
)

// ErrOAuthReposRequired is returned by NewService/Register when Config.Providers
// is non-empty but Repositories.OAuthAccounts or Repositories.OAuthStates is nil.
// OAuth is deny-by-absence — no providers means no routes and the oauth repos may
// be nil — but wiring providers without their stores is a loud partial-wiring
// error (design §3, the Hasher/Mailer precedent), never a silent half-on state.
var ErrOAuthReposRequired = errors.New("auth: Config.Providers set but Repositories.OAuthAccounts/OAuthStates is nil")

// ErrMachineReposRequired is returned by NewService/Register when exactly one of
// Repositories.ServiceAccounts and Repositories.APIKeys is wired. The machine
// identity subsystem (API keys + service accounts, design §4.1) is both-or-
// neither: both nil → subsystem off (routes not registered, the bearer API-key
// path inert); both set → on; one without the other is a loud construction error
// (cut refinement 5), never a silent half-on state.
var ErrMachineReposRequired = errors.New("auth: Repositories.ServiceAccounts and Repositories.APIKeys must be wired together (both or neither)")

// ErrInvitationRepoRequired is returned by NewService/Register when Config.Granter
// is wired but Repositories.Invitations is nil. Invitations are deny-by-absence —
// no Granter means no routes and Invitations may be nil — but wiring a Granter
// without its store is a loud partial-wiring error (design §6), never a silent
// half-on state.
var ErrInvitationRepoRequired = errors.New("auth: Config.Granter set but Repositories.Invitations is nil")

// ErrInviteCheckRequired is returned by NewService/Register when Config.Granter
// enables invitations but Config.InviteCheck is nil (design §6/D3). The relation-
// aware host policy is REQUIRED with invitations — a nil check is never an
// allow-by-default or a silently unprotected create/list route — so it degrades
// LOUDLY at construction alongside ErrInvitationRepoRequired, mirroring the
// Hasher/Mailer required posture.
var ErrInviteCheckRequired = errors.New("auth: Config.Granter set but Config.InviteCheck is nil")

// ErrInviteCheckWithoutGranter is returned by NewService/Register when
// Config.InviteCheck is wired but Config.Granter is nil (invitations off). A
// policy for a subsystem that will never run is a contradictory wiring that gives
// false confidence, so — matching the ErrDeliveryOffButDeliverable / partial-
// wiring posture — it fails LOUDLY rather than silently ignoring the dead check.
var ErrInviteCheckWithoutGranter = errors.New("auth: Config.InviteCheck set but Config.Granter is nil (invitations off)")

// ErrInvalidListStrategy is returned by NewService/Register when
// Config.ListStrategy is set to a value other than "cursor" or "offset". Like
// the Hasher/Mailer requirements it degrades loudly at construction (the Config
// posture), never silently defaulting a typo.
var ErrInvalidListStrategy = errors.New(`auth: Config.ListStrategy must be "cursor" or "offset"`)

// ErrInvitationsDisabled is returned by the invitation use-cases (Create, Accept,
// …) on a Service built with no Config.Granter: the invitation subsystem is off,
// so — mirroring the transport, which registers no invitation routes and 404s
// the whole surface (design §6) — the driving surface wraps sdk.ErrNotFound.
var ErrInvitationsDisabled = fmt.Errorf("auth: invitations are disabled (no Config.Granter): %w", sdk.ErrNotFound)

// ErrDuplicateNotifierKind is returned by NewService/Register when Config.Notifiers
// contains more than one notifier declaring the same kind. Unlike auth's OAuth
// provider map, which silently last-wins, the notifier set degrades LOUDLY at
// construction (the ErrOAuthReposRequired posture): a duplicate kind is an
// ambiguous delivery route, never a silent pick.
var ErrDuplicateNotifierKind = errors.New("auth: Config.Notifiers has more than one notifier for the same kind")

// ErrKindNotSupported is returned (as the wrapped cause) by Service.Create for an
// invitation identifier kind the host is not set up to deliver to
// (deny-by-absence, ruling 6): a kind is supported iff it is identity.KindEmail
// with the Mailer wired, OR a notifier of that kind is wired in Config.Notifiers.
// It wraps sdk.ErrInvalidInput, so the transport maps it to 400, and the
// invitation is NOT created. Hosts detect it with errors.Is(err,
// auth.ErrKindNotSupported).
var ErrKindNotSupported = invitationsvc.ErrKindNotSupported

// Granter is the ReBAC-decoupled grant-on-accept seam for invitations (design
// §2.2/§6, ratified AV4, structured at D1): Grant(ctx, GrantInput). A host adapts
// it to whatever authorizer it runs — a ReBAC CreateRelationships, a role-column
// write, or the proof host's toy membership map — or wires none.
//
// Success contract (D2, strengthened): a nil return means the EXACT requested
// relation was applied or was already exactly present. A DIFFERENT existing
// relation is NOT success; an invariant refusal is NOT success; a missing or
// deleted host resource is NOT success — each must return an error (a ReBAC host
// maps a conflicting/invariant outcome to sdk.ErrConflict and a missing resource
// to sdk.ErrNotFound). Infrastructure/command failures propagate. The grant must
// be idempotent for the exact tuple (a duplicate accept of the SAME relation must
// not error), but the Granter must NOT implicitly replace, upgrade, or downgrade
// an existing relation — the feature cannot decide that an invitation may change a
// standing membership. Aliased from invitationsvc so the sibling service can call
// it without an import cycle.
type Granter = invitationsvc.Granter

// GrantInput is the structured request the Granter receives for one invitation
// grant (D1). OperationID is an opaque, non-empty, non-secret identifier of the
// logical grant: the persisted invitation row ID for accept/resolve (a retry
// reuses it; a later invitation row for the same tuple gets a different ID) and a
// freshly minted high-entropy value for direct-add. A host MAY derive its own
// advanced mutation identity from a fixed purpose, OperationID, and the tuple
// fields; a baseline state writer may ignore it.
// Aliased from invitationsvc per the Granter precedent.
type GrantInput = invitationsvc.GrantInput

// MemberCheck is the optional duplicate-membership predicate consulted before a
// direct-add grant (design §6). Nil → no dup check. Aliased from invitationsvc.
type MemberCheck = invitationsvc.MemberCheck

// InviteAction is the invitation operation a host InviteCheck policy is asked
// about (design §6/D3): InviteCreate or InviteList. Aliased from invitationsvc.
type InviteAction = invitationsvc.InviteAction

// InviteCreate and InviteList are the two InviteAction values the feature's
// create/list invitation handlers pass to InviteCheck (design §6/D3). InviteCreate
// carries the exact requested relation; InviteList carries an empty relation.
const (
	InviteCreate = invitationsvc.InviteCreate
	InviteList   = invitationsvc.InviteList
)

// InviteCheckRequest is the parsed, principal-resolved authorization question the
// feature poses to a host InviteCheck (design §6/D3): the Principal, the Action,
// the resource, and — for InviteCreate — the exact validated Relation (empty for
// InviteList). Aliased from invitationsvc per the CreateInput precedent so a host
// names one type across the public and internal packages.
type InviteCheckRequest = invitationsvc.InviteCheckRequest

// InviteCheck is the relation-aware host authorization seam for invitation
// create/list (design §6/D3). It runs in the feature's own parsed request path —
// after live-session validation, principal resolution, and request parsing — so
// the host sees the caller, resource, action, and validated relation a
// RouteRegistrar wrapper cannot. It is REQUIRED whenever Config.Granter enables
// invitations: a nil InviteCheck is ErrInviteCheckRequired at construction, never
// an allow-by-default. A nil return authorizes; a denial (wrap sdk.ErrForbidden)
// or an infrastructure error fails closed through the normal web/sdk mapping.
// Authority is issuance-time: authorizing at creation issues a durable capability,
// and acceptance does not re-run inviter authority. Aliased from invitationsvc.
type InviteCheck = invitationsvc.InviteCheck

// CreateInput is the input to Service.Create (an invitation). Aliased from
// invitationsvc per the Principal precedent so a host wiring its own invitation
// handler names one type across the public and internal packages.
type CreateInput = invitationsvc.CreateInput

// CreateResult reports the outcome of Service.Create: DirectlyAdded true when a
// known invitee was granted immediately, else Invitation is the pending record.
type CreateResult = invitationsvc.CreateResult

// AcceptInput is the input to Service.Accept: the mailed Token plus the accepting
// caller's SubjectType/SubjectID and Identifier (email). Aliased from invitationsvc.
type AcceptInput = invitationsvc.AcceptInput

// AcceptResult reports the granted tuple's resource/relation. Aliased from invitationsvc.
type AcceptResult = invitationsvc.AcceptResult

// ErrEmailNotVerified is returned (as the wrapped cause) by login when
// Config.RequireVerifiedEmail is set and the caller's email is unverified. It
// wraps sdk.ErrForbidden, so the transport maps it to 403. Hosts detect it with
// errors.Is(err, auth.ErrEmailNotVerified).
var ErrEmailNotVerified = authsvc.ErrEmailNotVerified

// Principal is the effective caller resolved from a credential (a session, an
// API key, or — when Config.TokenSigner is wired — a bearer JWT). AV5
// pins it as the one value type: actor references are (subject_type, subject_id)
// string pairs everywhere, with no principals registry table. Type is a string
// convention (Service.AuthenticateAPIKey yields "user" for an act-as-user key or
// "service_account" otherwise); the alias keeps exactly one type across the
// public and internal packages.
type Principal = authsvc.Principal

// OAuthResult is the outcome of Service.OAuthCallback / Service.VerifyLink: the
// Action taken, the access Token and RefreshToken (both empty for a pending
// link), the resolved User, and the validated RedirectTo. Aliased from authsvc
// per the Principal precedent.
type OAuthResult = authsvc.OAuthResult

// TokenPair is the access/refresh credential pair a session mint produces (§1.1):
// the access JWT and its expiry, plus the opaque refresh token (empty on the
// grace refresh lane). Login, ChangePassword, IssueToken, and Refresh return it.
// Aliased from authsvc per the Principal precedent.
type TokenPair = authsvc.TokenPair

// PasswordHasher hashes and verifies passwords. It is feature-owned (not an sdk
// facility) because it has one consumer today and none genuinely foreseen
// elsewhere. integrations/cryptids/bcrypt satisfies it structurally, with zero
// import in either direction.
type PasswordHasher interface {
	// HashPassword returns a self-describing hash of password.
	HashPassword(password string) (string, error)
	// VerifyPassword reports whether password matches hash; a mismatch returns
	// a non-nil error. Implementations must compare in constant time.
	VerifyPassword(hash, password string) error
}

// CompromisedPasswordChecker reports whether a candidate password is known to be
// compromised — present in a breach corpus or a host blocklist (design §5.9). It
// is OPTIONAL and host-injected (Config.CompromisedPasswordChecker); the feature
// core ships none and adds NO network dependency, so a local blocklist and a
// future remote breach-check integration both satisfy it. When wired, every
// password entry point (register, set, change, reset) consults it identically.
type CompromisedPasswordChecker interface {
	// IsCompromised reports whether password is known-compromised. A non-nil error
	// means the check could not complete (e.g. an unreachable remote corpus); the
	// Config.CompromisedPasswordFailOpen policy then decides the outcome.
	IsCompromised(ctx context.Context, password string) (bool, error)
}

// DeliveryDispatcher is the transport-neutral outbound-delivery seam for
// DeliveryMode "jobs" (authv3-delivery-refactor AV3D-3.1). A host composes an
// adapter over the generic jobs feature and wires it here (Config.DeliveryDispatcher)
// so authentication runs its encrypted delivery work on durable generic jobs while
// the authentication core imports NO sibling feature (constitution rule 6): every
// method is STDLIB-TYPED, so the adapter — which lives in a composition boundary that
// imports both features — satisfies it structurally.
//
// payload is the opaque sealed command.Envelope the queue stores and never
// interprets; kind and purpose are the secret-free routing metadata; logicalKey is
// the PII-free receipt key that makes a duplicate Submit idempotent and lets Replace
// supersede exactly the prior active generation. LatestStatus returns a generic job
// lifecycle string; the feature normalizes it into the stable, secret-free
// DeliveryStatus projection. Submit admits work under logicalKey exactly once; Replace
// supersedes every active generation and admits a fresh one.
//
// Replace fences a superseded worker's checkpoint and completion (both lease-fenced,
// so a stale claim conflicts and cannot clobber the fresh generation), but it CANNOT
// retract a provider call already in flight: a superseded worker that has already
// checkpointed and begun its send delivers the old proof at-least-once and only then
// fails to record success. This is the unavoidable in-flight race — delivery is
// at-least-once, not exactly-once. The freshly issued challenge REPLACES the old proof
// (a new challenge for the same purpose supersedes the prior one), so the stale
// delivery is harmless, and status always reflects the latest generation.
type DeliveryDispatcher interface {
	Submit(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (executionID string, err error)
	Replace(ctx context.Context, kind, purpose, logicalKey string, payload []byte) (executionID string, err error)
	LatestStatus(ctx context.Context, logicalKey string) (state string, err error)
}

// DeliveryJobKind is the single generic-jobs kind an authentication delivery command
// is submitted under in DeliveryMode "jobs": one kind, handled by the one
// transport-neutral delivery processor, regardless of the message rail (email/SMS) —
// the rail and purpose travel inside the sealed envelope, not as the queue's routing
// kind. A composition adapter registers DeliveryJobRuntime().Handle under this kind.
const DeliveryJobKind = delivery.JobKind

// DeliveryClaim is one claimed delivery job a jobs-mode transport hands the
// authentication processor (AV3D-3.1). It is stdlib-typed so a composition adapter
// builds it from a generic job without the authentication core importing the jobs
// feature. Payload is the sealed command envelope; Attempt is the number of process
// attempts already spent; Checkpoint persists a freshly rendered sealed payload under
// the current claim fence before any provider send.
type DeliveryClaim struct {
	// ExecutionID is the opaque unit-of-work ID (never a recipient or the logical key),
	// used only for the best-effort lifecycle observation the transport emits.
	ExecutionID string
	Payload     []byte
	Attempt     int
	Checkpoint  func(ctx context.Context, sealed []byte) error
}

// DeliveryJobRuntime is the narrow, stdlib-typed seam a composition adapter registers
// on the generic jobs runtime for DeliveryMode "jobs" (AV3D-3.1). Kind is the job kind
// (DeliveryJobKind); Handle runs the delivery processor over one claim (returning nil
// for a completed/skipped outcome, a plain secret-free error for a transient retry, and
// a permanent-classified error — DeliveryErrorPermanent reports true — for an immediate
// dead-letter); Discard is the per-kind terminal hook the runtime invokes AFTER it
// records a dead-letter, voiding the undeliverable challenge. Purged is the batch hook
// a host calls after driving the generic terminal purge, so the optional lifecycle
// observer emits a purged event. Service.DeliveryJobRuntime returns it only when the
// jobs-mode processor is wired.
type DeliveryJobRuntime struct {
	Kind    string
	Handle  func(ctx context.Context, claim DeliveryClaim) error
	Discard func(ctx context.Context, executionID string, payload []byte) error
	Purged  func(ctx context.Context, count int)
}

// DeliveryErrorPermanent reports whether a DeliveryJobRuntime.Handle error carries the
// permanent disposition — the signal a jobs-mode composition adapter uses to route the
// failure onto the generic runtime's IMMEDIATE dead-letter path rather than a bounded
// retry (authv3-delivery-refactor AV3D-3.4). A plain (transient) handle error reports
// false and is retried with capped exponential backoff.
func DeliveryErrorPermanent(err error) bool { return delivery.HandleErrorPermanent(err) }

// Repositories is the set of outbound ports the feature needs. A store adapter
// (e.g. features/authentication/stores/turso) or a host fills it; the feature stays
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
	// nil when Config.Providers is empty (OAuth off); wiring providers without
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
	// feature keeps NO audit trail (the synchronous recording site is a no-op),
	// and no construction error is raised. When wired, every sensitive op records
	// a security event synchronously and a write failure is logged at WARN,
	// never failing the auth flow.
	SecurityEvents securityevent.SecurityEventRepository
	// Invitations backs the resource-invitation flow (design §6). It may be nil
	// when Config.Granter is nil (invitations off); wiring a Granter without it is
	// ErrInvitationRepoRequired at construction.
	Invitations invitation.InvitationRepository
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
}

// CookieConfig is the session-cookie policy. Zero values are safe: an empty Name
// defaults to "session" and an empty Path to "/". MaxAge is in seconds and also
// sets the session lifetime; a non-positive MaxAge yields a browser session
// cookie backed by a 7-day server session. Secure/Domain are host deployment
// choices (Secure should be true behind TLS). Cookies are always HttpOnly with
// SameSite=Lax.
type CookieConfig struct {
	Name   string
	Path   string
	Domain string
	Secure bool
	MaxAge int
}

// InProcessDeliveryConfig tunes the bounded, EPHEMERAL in-process delivery runtime
// (DeliveryMode "in_process", authv3-delivery-refactor AV3D-4.5). Every knob is
// NIL-SAFE: a ZERO value selects the package default, so the zero InProcessDeliveryConfig
// is a valid, fully-defaulted configuration. A NEGATIVE bound (or a StatusMaxEntries
// smaller than QueueCapacity) fails LOUDLY at construction with a typed error wrapping
// sdk.ErrInvalidInput — an invalid bound is never silently coerced to a default. The
// whole struct is meaningful ONLY for DeliveryMode "in_process".
//
// MULTI-INSTANCE WARNING: these bounds are PER PROCESS. The in-process runtime keeps its
// queue, its fixed worker pool, its submit-once de-duplication, its replace/generation
// arbiter, and its latest-by-key status entirely in one process's memory. Two instances
// of the host do NOT share any of it: the same logical delivery admitted on two
// instances is de-duplicated on NEITHER, so BOTH can render and BOTH can send — a user
// may receive two messages. There is no cross-instance coordination and no durability;
// accepted, in-flight work is lost on a crash or restart. For a multi-instance
// deployment use DeliveryMode "jobs" (durable, cross-instance de-duplicated).
type InProcessDeliveryConfig struct {
	// Workers is the FIXED worker-pool size (never one goroutine per request); 0 selects
	// the package default. Negative → ErrInProcessWorkersInvalid.
	Workers int
	// QueueCapacity is the FINITE admission-queue depth; 0 selects the package default.
	// Negative → ErrInProcessCapacityInvalid.
	QueueCapacity int
	// AdmissionDeadline bounds how long an enqueue waits for a free slot before returning
	// a typed capacity error; 0 selects the package default. Negative →
	// ErrInProcessAdmissionDeadlineInvalid.
	AdmissionDeadline time.Duration
	// ShutdownDeadline bounds how long RunDelivery waits for in-flight workers to drain
	// after the host cancels its context; 0 selects the package default. Negative →
	// ErrInProcessShutdownDeadlineInvalid.
	ShutdownDeadline time.Duration
	// MaxAttempts caps process-local delivery attempts before a transient failure becomes
	// a terminal dead-letter; 0 selects the package default. Negative →
	// ErrInProcessMaxAttemptsInvalid.
	MaxAttempts int
	// StatusMaxEntries bounds the latest-by-key status map so retention never grows with
	// process lifetime; 0 selects the package default. It must be at least QueueCapacity
	// (a queued generation is never evicted): a smaller value →
	// ErrInProcessStatusRetentionTooSmall. Negative → ErrInProcessStatusMaxEntriesInvalid.
	StatusMaxEntries int
	// StatusTTL bounds how long a terminal latest-by-key status is retained before it
	// reads as unknown; 0 selects the package default. Negative →
	// ErrInProcessStatusTTLInvalid.
	StatusTTL time.Duration
}

// HTMLResourcePolicy is the technology-neutral, validated HTML resource policy a host
// hands the feature through Config.HTMLPolicy (design §9.2, GOTH-0.4). It carries a
// deterministically ordered set of ADDITIONAL Content-Security-Policy resource
// directives (script, style, image, font, connect, media, worker) so a selected HTML
// view can load the assets it needs. It WIDENS resource loading and can never remove
// the fixed protections every auth HTML page and redirect carries (no-store,
// no-referrer, X-Frame-Options: DENY, X-Content-Type-Options: nosniff, and the CSP
// default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
// prefix). A nil *HTMLResourcePolicy selects the historical asset-free CSP exactly.
// The feature core never imports templ or ui/goth: this is a plain, feature-owned
// value the future ui/goth authentication adapter constructs from
// goth.Bundle.Requirements(). Aliased from the internal inbound package (the
// MutationSecurity precedent) so a host names one type across the public and internal
// surfaces.
type HTMLResourcePolicy = inbound.HTMLResourcePolicy

// HTMLResourceDirective is one requested CSP resource directive — a Kind, its Sources,
// and an optional per-render Nonce — passed to NewHTMLResourcePolicy. Aliased from the
// internal inbound package per the HTMLResourcePolicy precedent.
type HTMLResourceDirective = inbound.HTMLResourceDirective

// HTMLResourceKind is the CSP resource class an HTMLResourceDirective widens. Only the
// frozen widenable classes below are valid; the fixed protection directives are not
// members, so a policy structurally cannot name them. Aliased from the internal inbound
// package.
type HTMLResourceKind = inbound.HTMLResourceKind

// The frozen widenable CSP resource classes (GOTH-0.4): script, style, image, font,
// connect, media, worker. Re-exported from the internal inbound package so a host
// builds directives without importing internal.
const (
	HTMLScriptSrc  = inbound.HTMLScriptSrc
	HTMLStyleSrc   = inbound.HTMLStyleSrc
	HTMLImgSrc     = inbound.HTMLImgSrc
	HTMLFontSrc    = inbound.HTMLFontSrc
	HTMLConnectSrc = inbound.HTMLConnectSrc
	HTMLMediaSrc   = inbound.HTMLMediaSrc
	HTMLWorkerSrc  = inbound.HTMLWorkerSrc
)

// ErrHTMLPolicyWithoutViews is returned by NewService/Register when Config.HTMLPolicy
// is set while Config.Views is nil (design §9.2, GOTH-0.4). The HTML surface is gated
// entirely on Views: with a nil Views no HTML page renders, so a resource policy that
// can never be consulted is a contradictory wiring giving false confidence. Matching
// the ErrInviteCheckWithoutGranter / partial-wiring posture, it degrades LOUDLY at
// construction rather than silently ignoring the dead policy.
var ErrHTMLPolicyWithoutViews = errors.New("auth: Config.HTMLPolicy set but Config.Views is nil (no HTML surface)")

// ErrBrowserLoginPathInvalid is returned by NewService/Register when a non-empty
// Config.BrowserLoginPath is not a safe root-relative path (design §9.2): it must
// start with a single "/", carry no protocol-relative "//" prefix, no scheme, no
// backslash, and no control character, so the browser identity gates can never be
// configured into an off-site open redirect. An empty value defaults to "/auth/login"
// and never trips this. Checked at construction, the loud-Config posture.
var ErrBrowserLoginPathInvalid = errors.New("auth: Config.BrowserLoginPath must be a safe root-relative path (leading /, no //, scheme, backslash, or control character)")

// NewHTMLResourcePolicy validates the requested resource directives and returns an
// immutable HTMLResourcePolicy, or an error wrapping sdk.ErrInvalidInput for an
// unknown/fixed directive key, a directive with neither a source nor a nonce, an empty
// source, or a source carrying a control character, whitespace, ';', or ',' (the
// header-injection guard). It is the feature-owned constructor a host or the ui/goth
// authentication adapter calls to build Config.HTMLPolicy. It re-exports the internal
// inbound constructor so a host never imports internal.
func NewHTMLResourcePolicy(directives ...HTMLResourceDirective) (*HTMLResourcePolicy, error) {
	return inbound.NewHTMLResourcePolicy(directives...)
}

// Config carries host-provided collaborators. The Hasher and Mailer are
// REQUIRED (nil → ErrHasherRequired / ErrMailerRequired at construction);
// everything else is optional with a safe default.
type Config struct {
	// Hasher is REQUIRED; nil → ErrHasherRequired.
	Hasher PasswordHasher
	// CompromisedPasswordChecker is the OPTIONAL breach/blocklist checker consulted
	// by the shared password policy (design §5.9). Nil → no breach check (the
	// length policy still applies). When wired, register/set/change/reset all
	// consult it identically; the feature core ships none, so wiring it never adds
	// a network dependency to the core.
	CompromisedPasswordChecker CompromisedPasswordChecker
	// CompromisedPasswordFailOpen selects the policy when a wired
	// CompromisedPasswordChecker cannot complete a check (returns an error).
	// Default false = FAIL CLOSED: an unavailable breach service rejects the
	// password rather than becoming a silent bypass — the documented production
	// posture (design §5.9, V15 fail-closed profile). Set true only to trade breach
	// coverage for availability (a development/self-hosted convenience); the WARN is
	// logged on Config.Logger.
	CompromisedPasswordFailOpen bool
	// Mailer is REQUIRED; nil → ErrMailerRequired. Delivers verification and
	// password-reset messages.
	Mailer email.Sender
	// MailFrom is the From address on verification/reset mail.
	MailFrom string
	// RateLimiter throttles login attempts; nil → ratelimiter.NewMemory()
	// (safe-by-default: an in-process limiter, not "unlimited").
	RateLimiter ratelimiter.Limiter
	// SessionCookie configures the session cookie; the zero value is usable.
	SessionCookie CookieConfig
	// AllowedOrigins is the exact-match Origin allowlist that the browser-safe
	// mutation gate on cookie-authenticated sensitive routes (step-up, credential and
	// identifier management) validates against (design §9.1). A "*" entry never
	// authorizes a credentialed cross-origin mutation. Empty leaves the gate to
	// reject every cross-site cookie mutation and any request carrying a
	// disallowed Origin; bearer-only (API) callers skip the gate entirely.
	AllowedOrigins []string
	// BrowserLoginPath is the login destination the browser identity gates
	// (Service.RequirePrincipalBrowser / RequireLiveSessionBrowser) 303 to on an
	// authentication denial (design §9.2). Empty (default) → "/auth/login". A non-empty
	// value MUST be a safe root-relative path (leading "/", no "//" prefix, no scheme,
	// no backslash, no control character) or construction fails with
	// ErrBrowserLoginPathInvalid — so a browser gate can never be pointed off-site. It
	// configures ONLY the browser gates: the existing JSON RequirePrincipal /
	// RequireLiveSession middleware keep their byte-stable 401 behavior regardless.
	BrowserLoginPath string `env:"AUTH_BROWSER_LOGIN_PATH"`
	// RequireVerifiedEmail, when true, makes login refuse an unverified user
	// with a 403 (ErrEmailNotVerified). Default false (design §7.1, AV8):
	// flipping it on requires a working Mailer so users can verify.
	RequireVerifiedEmail bool

	// RuntimeMode is the REQUIRED fail-closed posture (auth v3 §8). It has no
	// default: empty → ErrRuntimeModeRequired, unknown → ErrRuntimeModeInvalid,
	// so a host cannot accidentally inherit the development posture. "production"
	// rejects development-only delivery transports (email/notify console senders,
	// design §6.3); "development" warns instead.
	RuntimeMode RuntimeMode `env:"AUTH_RUNTIME_MODE"`

	// DeliveryMode is the REQUIRED explicit selection of the outbound-delivery execution
	// model (authv3-delivery-refactor AV3D-0.1). It has no default: empty →
	// ErrDeliveryModeRequired, unknown → ErrDeliveryModeInvalid, so construction never
	// infers a mode from a non-nil collaborator. "jobs" runs delivery on the durable
	// generic jobs runtime (requires Config.DeliveryDispatcher + DeliveryEncrypter;
	// production requires DeliveryJobsAcknowledged); "in_process" runs the bounded,
	// EPHEMERAL in-process pool (production requires DeliveryEphemeralAcknowledged);
	// "off" runs no delivery runtime (rejected when a delivery dispatcher is wired or
	// passwordless is enabled). Register starts no worker in any mode — the host owns
	// the runtime lifecycle.
	DeliveryMode DeliveryMode `env:"AUTH_DELIVERY_MODE"`

	// ChallengeProtector protects short codes (HMAC pepper) and digests tokens
	// (auth v3 §3.3). The slot is frozen here; it becomes REQUIRED when the
	// challenge subsystem is enabled (phase 3, ErrChallengeProtectorRequired).
	// AV3-0.2 ships the bundled HMACChallengeProtector.
	ChallengeProtector ChallengeProtector
	// IdentifierNormalizer canonicalizes identifier values everywhere (auth v3
	// §2.2). Nil selects the bundled strict default (AV3-1.1).
	IdentifierNormalizer IdentifierNormalizer
	// IdentifierKeyer derives PII-free rate-limit/idempotency keys under a key
	// distinct from the challenge pepper, JWT, and encryption keys (auth v3 §4.4).
	// Production-required once privacy-keyed limits are wired (phase 5,
	// ErrIdentifierKeyerRequired).
	IdentifierKeyer IdentifierKeyer
	// CredentialPolicy evaluates a proposed credential/identifier mutation against
	// the current and proposed MethodSet (auth v3 §5.6). Nil selects the bundled
	// safe default (credential.NewDefaultPolicy: one direct login method + one
	// verified recovery method, PSTN restricted) when the credential suite is
	// enabled (phase 6); a host may supply stronger rules. The slot is frozen here
	// (AV3-0.4); ErrCredentialPolicyRequired covers the strict-production posture
	// that disables the default without a replacement.
	CredentialPolicy CredentialPolicy
	// DeliveryEncrypter encrypts the delivery-outbox payload envelope (auth v3
	// §6.1.1). REQUIRED once the outbox is enabled (phase 4,
	// ErrDeliveryEncrypterRequired); bundled cryptids.AESGCM satisfies it with a
	// distinct key.
	DeliveryEncrypter cryptids.Encrypter
	// DeliveryDispatcher is the generic-jobs delivery transport for DeliveryMode
	// "jobs" (authv3-delivery-refactor AV3D-3.1). It is the delivery queue: producers
	// submit sealed command envelopes through it and the host runs the generic jobs
	// runtime that invokes DeliveryJobRuntime().Handle. A jobs-mode host wires this
	// stdlib-typed seam, keeping the authentication core free of any jobs import.
	// REQUIRED for DeliveryMode "jobs" (ErrDeliveryQueueRequired); requires
	// DeliveryEncrypter (the payload is always sealed) and, in production,
	// DeliveryJobsAcknowledged.
	DeliveryDispatcher DeliveryDispatcher
	// DeliveryEventsEmitter is the OPTIONAL, secret-free delivery lifecycle observer's
	// event rail for DeliveryMode "jobs" (authv3-delivery-refactor AV3D-3.4). When set,
	// the jobs-mode transport emits a bounded lifecycle event (delivered, skipped,
	// retried, dead_lettered, purged) per transition onto this emitter. Emission is
	// strictly best-effort and observation-only: it is never on the path that records
	// delivery state, so a nil emitter, an emit error, or a panic never loses, retries,
	// duplicates, or fails accepted delivery work. Leave nil to run delivery with no
	// observation. It is meaningful only for DeliveryMode "jobs".
	DeliveryEventsEmitter sdkevents.Emitter
	// DeliveryJobsAcknowledged affirms that the host runs the durable jobs delivery
	// runtime in its process lifecycle (authv3-delivery-refactor AV3D-0.1). It is
	// meaningful only for DeliveryMode "jobs". The queue is the ONLY send path, so a
	// jobs-mode host that enqueues without running the runtime silently swallows every
	// verification, reset, and magic-link message. The feature cannot observe the host
	// lifecycle, so production REQUIRES this explicit acknowledgment
	// (ErrDeliveryJobsUnacknowledged) rather than failing open on a stalled queue.
	// Development tolerates the zero value (a test or manual drain may run the runtime).
	// It is a wiring assertion set in the composition root, not an env knob.
	DeliveryJobsAcknowledged bool
	// DeliveryEphemeralAcknowledged affirms that the host accepts the crash-loss
	// guarantee of DeliveryMode "in_process" (authv3-delivery-refactor AV3D-0.1). The
	// in-process pool is process-local: accepted, in-flight delivery work is lost on a
	// crash. The feature cannot make an ephemeral send path durable, so production
	// REFUSES in_process without this explicit acknowledgment
	// (ErrDeliveryEphemeralUnacknowledged) — the recommended production posture is
	// "jobs". Development tolerates the zero value. It is a wiring assertion set in the
	// composition root, not an env knob.
	DeliveryEphemeralAcknowledged bool
	// InProcessDelivery tunes the bounded, EPHEMERAL in-process delivery runtime
	// (authv3-delivery-refactor AV3D-4.5). Meaningful ONLY for DeliveryMode
	// "in_process"; the zero value is a valid, fully-defaulted configuration. Every knob
	// is nil-safe (zero → default) and every invalid bound (a negative value, or a
	// StatusMaxEntries below QueueCapacity) fails LOUDLY at construction. See
	// InProcessDeliveryConfig for the per-process (NOT cross-instance) semantics: two
	// instances each keep their own queue, de-duplication, and status, so both can send.
	InProcessDelivery InProcessDeliveryConfig
	// PublicAuthBaseURL is the absolute base URL magic links and redemption pages
	// are built from (auth v3 §6.4). REQUIRED when a link flow is enabled
	// (phase 7, ErrPublicAuthBaseURLRequired); production requires HTTPS. Request
	// Host/forwarded headers never participate.
	PublicAuthBaseURL string `env:"AUTH_PUBLIC_BASE_URL"`

	// Passwordless enables login-only passwordless authentication for the listed
	// identifier kinds (auth v3 §4.2). Empty (default) → the passwordless routes are
	// NOT registered (deny-by-absence — there is no natural nil collaborator, so the
	// knob is explicit). Allowed v3 kinds are "email" and "phone" (identity.KindEmail
	// / KindPhone); any other value is ErrPasswordlessKindInvalid at construction.
	// Each listed kind must have a wired delivery channel — email via the required
	// Mailer (or an email-kind Notifier), phone via a wired phone-kind Notifier — else
	// ErrPasswordlessKindUnsupported; in production the wired transport must also be
	// production-capable. Enabling passwordless requires the atomic challenge rail
	// (Repositories.Challenges + ChallengeProtector) and a delivery runtime — a
	// DeliveryMode of "jobs" (Config.DeliveryDispatcher) or "in_process" — since starts
	// issue challenges and enqueue asynchronously (V14), plus a valid
	// Config.PublicAuthBaseURL for magic links (HTTPS in production). Listing a kind permits its active verified login-enabled
	// identifiers as direct methods under the §5.6 credential policy. It NEVER
	// auto-provisions and NEVER enables phone+password login (phone stays
	// passwordless-only, V10).
	Passwordless []string

	// IDs is the app's entity-ID strategy, decided once at wiring (amended D9):
	// it mints the keys of users, service accounts, API-key records, security
	// events, and invitations. The zero value generates default nanoids;
	// cryptids.Database delegates key generation to the database (the bundled
	// stores omit the id column and read it back with RETURNING); an
	// integration's GenerateFunc (e.g. google-uuid) chooses another shape. It
	// never mints SECRETS — session tokens, verification codes/reset tokens,
	// OAuth state, API-key material, and invitation secrets keep their own
	// unconditional high-entropy generator regardless of this strategy.
	IDs cryptids.IDGenerator

	// ListStrategy is the DEFAULT pagination strategy the feature's JSON list
	// endpoints (service accounts, API keys, invitations) apply when a request
	// names neither a cursor nor an offset param (sdk/foundation/crud ParseListRequest).
	// "cursor" (the default) or "offset"; empty is treated as "cursor". A host
	// populates it from an env-tagged config field
	// (`env:"AUTH_LIST_STRATEGY" default:"cursor"` via sdk/config ParseEnvTags),
	// never from os.Getenv inside the feature. Any other value is
	// ErrInvalidListStrategy at construction (the loud-Config posture).
	ListStrategy string `env:"AUTH_LIST_STRATEGY" default:"cursor"`

	// Providers are the wired OAuth/OIDC providers (integrations/oauth/* satisfy
	// oauth.Provider). Empty/nil → the OAuth subsystem is OFF and its routes are
	// NOT registered (deny-by-absence); Repositories.OAuthAccounts/OAuthStates
	// may then be nil. Non-empty → both oauth repositories are required
	// (ErrOAuthReposRequired).
	Providers []oauth.Provider
	// TokenEncrypter encrypts provider access/refresh tokens at rest. Nil →
	// provider tokens are NOT persisted (login and linking still work; there is
	// no offline provider-API access) — a safe, documented silent degradation.
	// Wire cryptids.AESGCM to store them.
	TokenEncrypter cryptids.Encrypter
	// OAuthCallbackBase is the absolute origin (e.g. "https://app.example.com")
	// the provider callback URL is built from. Only meaningful when Providers is
	// set.
	OAuthCallbackBase string
	// RedirectAllowlist is the exact-match allowlist of post-flow redirect
	// destinations (open-redirect guard). The same-origin default ("/") is always
	// allowed; any other requested target must appear verbatim here or it falls
	// back to "/".
	RedirectAllowlist []string

	// TokenSigner signs and verifies the access JWT — the primary access
	// credential (§1.1, D3). It is REQUIRED; nil → ErrTokenSignerRequired at
	// construction. sdk/foundation/cryptids ships a stdlib HS256 default;
	// integrations/cryptids/golang-jwt satisfies it for RS256/ES256.
	//
	// Operational truth (§1.6): a MULTI-INSTANCE host MUST share the signing key
	// (AUTH_JWT_SECRET) across every instance — per-instance ephemeral keys cannot
	// cross-verify, and behind a load balancer that is a continuous auth-flap on
	// every request, with /auth/refresh round-robining into the same wall. An
	// ephemeral key is a SINGLE-INSTANCE DEV convenience only (example hosts),
	// where restart kills access JWTs and clients recover via /auth/refresh.
	// Verification applies a small clock-skew leeway (30–60s). Revocation is
	// asymmetric and now BOUNDED per route: stateless RequireUser routes honor an
	// outstanding access JWT for ≤ AccessTokenTTL after a session is revoked;
	// RequireLiveSession routes revoke immediately.
	TokenSigner cryptids.JWTSigner
	// AccessTokenTTL is the access-JWT lifetime (§1.1, D8). Zero → 15m. Keep it
	// short: it bounds the revocation-asymmetry window on stateless routes.
	AccessTokenTTL time.Duration `env:"AUTH_ACCESS_TOKEN_TTL" default:"15m"`
	// RefreshTTL is the fixed refresh-token / session horizon (§1.1, D2/D8). Zero →
	// 7d. It is set at mint and NEVER extended by rotation (fixed horizon); a
	// stolen refresh token therefore cannot outlive it.
	RefreshTTL time.Duration `env:"AUTH_REFRESH_TTL" default:"168h"`

	// Granter is the ReBAC-decoupled grant-on-accept seam for invitations (design
	// §6, ratified AV4). Nil → the invitation subsystem is OFF and its routes are
	// NOT registered (deny-by-absence); Repositories.Invitations may then be nil.
	// Non-nil → Repositories.Invitations is required (ErrInvitationRepoRequired),
	// and Register/verify resolve pending auto-accept invitations for the invitee.
	Granter Granter
	// InviteCheck is the relation-aware host authorization seam the feature's
	// create/list invitation handlers call after live-session validation, principal
	// resolution, and request parsing (design §6/D3). It is REQUIRED whenever Granter
	// enables invitations — nil → ErrInviteCheckRequired at construction, never an
	// allow-by-default. Wiring it without a Granter (invitations off) is the
	// contradictory ErrInviteCheckWithoutGranter. A nil return authorizes; a denial
	// or infrastructure error fails closed through the normal web/sdk mapping.
	InviteCheck InviteCheck
	// MemberCheck is the optional duplicate-membership predicate for the direct-add
	// path (known invitee + AutoAccept). Nil → no dup check (idempotent grants
	// absorb duplicates). Meaningful only when Granter is wired.
	MemberCheck MemberCheck
	// Notifiers is the host's wired delivery set for invitation delivery (ruling
	// 6). The email kind is always deliverable via the required Mailer with zero
	// entries here; each additional wired kind (identity.KindPhone, "slack", …)
	// enables invitations of that kind (deny-by-absence — an unwired kind is
	// ErrKindNotSupported at Create). A wired email-kind notifier also routes
	// invitation mail through notify instead of the Mailer directly
	// (verification/reset mail stays on the Mailer). Duplicate kinds →
	// ErrDuplicateNotifierKind at construction. Meaningful only when Granter is
	// wired; sdk/capabilities/notify ships Console (any kind); the email-kind bridge is integrations/notify/mailer.
	Notifiers []notify.Notifier

	// Views is the OPTIONAL HTML rendering port (design §9.2, R12/V16). Nil (default)
	// → the HTML surface is absent: the HTML GET pages and form decoding are NOT
	// registered and the shared POST routes accept JSON only, so the feature is
	// API-only with no view technology in the host's module graph. Non-nil → the
	// bundled/overridden HTML GET pages mount alongside the UNCHANGED JSON API (the
	// JSON DTO/status/body/cookie contracts are byte-compatible either way). The
	// feature core never imports templ: this is a technology-neutral web.Renderer
	// port. The bundled default lives in the sibling module
	// features/authentication/views/templ (authtempl.New()); the blessed override
	// path is embedding that default and overriding individual methods. A host may
	// instead satisfy the port with html/template via sdk/foundation/web.Template.
	Views Views

	// HTMLPolicy is the OPTIONAL, technology-neutral HTML resource policy (design
	// §9.2, GOTH-0.4). Nil (default) → the historical asset-free CSP: every auth HTML
	// page and redirect keeps script-src nonce-only with no external asset origins,
	// the secure default. Non-nil → the SAME fixed protections plus the policy's
	// validated widening resource directives (script/style/image/font/connect/media/
	// worker), so a selected HTML view can load the styles, scripts, fonts, and images
	// it declares. A policy only WIDENS; it can never remove a fixed protection
	// (no-store, no-referrer, X-Frame-Options: DENY, X-Content-Type-Options: nosniff,
	// default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none').
	// Build it with NewHTMLResourcePolicy (validated at construction); the ui/goth
	// authentication adapter maps goth.Bundle.Requirements() into one. Setting
	// HTMLPolicy while Views is nil is ErrHTMLPolicyWithoutViews at construction — a
	// policy for an absent HTML surface is contradictory wiring, never a silent no-op.
	// The feature core never imports templ or ui/goth: HTMLResourcePolicy is a plain
	// feature-owned value.
	HTMLPolicy *HTMLResourcePolicy

	// EmailContentTemplates registers host overrides of the feature's default email
	// content at email.LayerApp (design §6.2) — the DISTINCT second override system
	// alongside Views (which overrides HTML pages). Empty (default) → the bundled
	// LayerCore templates render unchanged. Each entry's Namespace must be
	// EmailContentNamespace to override a bundled template; its embed.FS is walked
	// from "templates/", each "<name>.html" replacing the core "<name>". This changes
	// email BODIES only — never a page, route, service policy, or the JSON API.
	EmailContentTemplates []EmailContentTemplate

	// Logger receives the best-effort WARN line when a security-event audit write
	// fails (design §5.1 — audit-write failures never fail the auth flow). Nil →
	// slog.Default(); Register defaults it to the Mount's logger when unset.
	Logger *slog.Logger
}

// Service is the auth feature's driving surface — every use-case as a method
// (session lifecycle, passwords, OAuth, machine identity, tokens, invitations),
// plus the cross-feature identity seams (RequireUser middleware, CurrentUser
// port) a host wires into another feature. It holds no mutable state beyond the
// shared Repositories/Config values. The shipped HTTP layer is an optional
// adapter over exactly this surface (FS2): a host may mount it (Register), mount
// part of it (subsystem deny-by-absence), or skip it and call these methods from
// its own handlers.
type Service struct {
	svc *authsvc.Service
	// inv is the invitation service, nil when Config.Granter is unset. Register
	// mounts its routes and NewService injects it into authsvc as the
	// resolve-on-registration collaborator; both use this single instance.
	inv *invitationsvc.Service
	// jobsProcessor is the jobs-mode delivery processor (AV3D-3.1), non-nil only when
	// DeliveryMode is "jobs" and Config.DeliveryDispatcher is wired. The host reaches
	// it through DeliveryJobRuntime() to register the handler on the generic jobs
	// runtime; the feature starts no goroutine.
	jobsProcessor *delivery.JobsProcessor
	// inProcessRuntime is the bounded, EPHEMERAL in-process delivery runtime (AV3D-4.1),
	// non-nil only when DeliveryMode is "in_process". It owns a fixed worker pool that
	// drains a finite admission queue; the host runs it via RunDelivery. The feature
	// starts no goroutine at construction. Accepted, in-flight work is LOST on a crash
	// or restart — this mode is process-local and never claims durability or
	// cross-instance coordination.
	inProcessRuntime *delivery.InProcessRuntime
	// inProcessQueue is the bounded, EPHEMERAL in-process admission queue (AV3D-4.1),
	// non-nil only when DeliveryMode is "in_process". It is retained solely so
	// InProcessQueueDepth can expose the bounded, secret-free queue depth for host
	// operational health (AV3D-5.3); it is never a delivery seam callers reach directly.
	inProcessQueue *delivery.InProcessQueue
	// inviteCheck is the host's relation-aware invitation authorization seam
	// (Config.InviteCheck, design §6/D3) the shipped HTTP adapter's create/list
	// invitation handlers call after live-session validation, principal resolution,
	// and parsing (Register → Mount → handlers). Non-nil exactly when inv is non-nil
	// (NewService requires it whenever a Granter enables invitations).
	inviteCheck invitationsvc.InviteCheck
	// listStrategy is the resolved Config.ListStrategy the shipped HTTP adapter
	// passes as the transport-edge DefaultStrategy (Register → Mount → handlers).
	listStrategy crud.Strategy
	// mutationSecurity is the browser-safe-mutation policy the shipped HTTP adapter
	// applies to cookie-authenticated sensitive routes (design §9.1): the Origin
	// allowlist and the session cookie name that marks a request browser-driven.
	mutationSecurity inbound.MutationSecurity
	// views is the optional HTML rendering port (design §9.2). Nil → the shipped
	// adapter mounts the JSON API only; non-nil → it also mounts the HTML GET pages.
	views inbound.Views
	// htmlPolicy is the optional HTML resource policy the shipped adapter applies to
	// every HTML page and redirect (design §9.2, GOTH-0.4). Nil → the historical
	// asset-free CSP; non-nil → the fixed protections plus the policy's widening
	// resource directives. Threaded Register → Mount → handlers.
	htmlPolicy *inbound.HTMLResourcePolicy
}

// DeliveryStatus is the read-only delivery-status projection a session-gated caller
// polls with the Receipt key it received (design §6.1.1). It exposes only lifecycle
// (State/Attempt/Pending/Failed), never the destination or secret. Aliased from the
// internal delivery package per the Principal precedent.
type DeliveryStatus = delivery.Status

// EmailContentTemplate is a host override of the feature's default email CONTENT,
// registered at email.LayerApp (design §6.2). It is the second, DISTINCT
// customization system alongside Config.Views: Views overrides HTML pages rendered
// to the browser, while EmailContentTemplate overrides the transactional email
// bodies rendered by the delivery router — different facilities, different Config
// fields, no shared type. Namespace must be EmailContentNamespace to override a
// bundled template; FS is walked from its "templates" subdirectory, each
// "<name>.html" replacing the core "<name>" template. Aliased from the internal
// delivery package per the DeliveryStatus precedent.
type EmailContentTemplate = delivery.TemplateOverride

// EmailContentNamespace is the namespace the feature registers its default email
// content templates under; a host LayerApp override targets a core template as
// EmailContentNamespace + ":" + name (e.g. "authentication:verification").
const EmailContentNamespace = delivery.Namespace

// resolveListStrategy validates Config.ListStrategy and maps it to a
// crud.Strategy. Empty (the zero value of a literally-built Config) resolves to
// the cursor default; "cursor"/"offset" pass through; anything else is
// ErrInvalidListStrategy.
func resolveListStrategy(s string) (crud.Strategy, error) {
	switch s {
	case "", string(crud.StrategyCursor):
		return crud.StrategyCursor, nil
	case string(crud.StrategyOffset):
		return crud.StrategyOffset, nil
	default:
		return "", ErrInvalidListStrategy
	}
}

// inProcessQueueConfig maps the host's nil-safe in-process tuning knobs onto the
// bounded delivery queue's config (AV3D-4.5). Zero knobs pass through as zero, which
// the delivery constructor reads as "use the package default".
func inProcessQueueConfig(cfg Config) delivery.InProcessQueueConfig {
	return delivery.InProcessQueueConfig{
		Capacity:          cfg.InProcessDelivery.QueueCapacity,
		AdmissionDeadline: cfg.InProcessDelivery.AdmissionDeadline,
		StatusMaxEntries:  cfg.InProcessDelivery.StatusMaxEntries,
		StatusTTL:         cfg.InProcessDelivery.StatusTTL,
	}
}

// inProcessRuntimeConfig maps the host's nil-safe in-process tuning knobs onto the
// bounded delivery runtime's config (AV3D-4.5). The Logger is filled at the construction
// site (it is not an InProcessDeliveryConfig knob).
func inProcessRuntimeConfig(cfg Config) delivery.InProcessRuntimeConfig {
	return delivery.InProcessRuntimeConfig{
		Workers:          cfg.InProcessDelivery.Workers,
		ShutdownDeadline: cfg.InProcessDelivery.ShutdownDeadline,
		MaxAttempts:      cfg.InProcessDelivery.MaxAttempts,
	}
}

// NewService builds the auth Service, validating the required Config fields and
// defaulting a nil RateLimiter to an in-memory one. It does not mount HTTP
// routes (see Register for that).
func NewService(repos Repositories, cfg Config) (*Service, error) {
	if cfg.Hasher == nil {
		return nil, ErrHasherRequired
	}
	if cfg.Mailer == nil {
		return nil, ErrMailerRequired
	}
	if cfg.TokenSigner == nil {
		return nil, ErrTokenSignerRequired
	}
	// RuntimeMode is a required enum with no default, so a host can never
	// accidentally inherit the development posture (design §8). Validated after
	// the required-collaborator checks above so their errors are not masked.
	if err := validateRuntimeMode(cfg.RuntimeMode); err != nil {
		return nil, err
	}
	// DeliveryMode is a required enum with no default (the RuntimeMode precedent), so a
	// host explicitly selects the outbound-delivery execution model and never inherits
	// one from a non-nil collaborator (AV3D-0.1). Validated here for the loud
	// empty/unknown failure; the mode-specific capability/acknowledgment matrix is
	// enforced in the delivery block below.
	if err := validateDeliveryMode(cfg.DeliveryMode); err != nil {
		return nil, err
	}
	if len(cfg.Providers) > 0 && (repos.OAuthAccounts == nil || repos.OAuthStates == nil) {
		return nil, ErrOAuthReposRequired
	}
	if (repos.ServiceAccounts == nil) != (repos.APIKeys == nil) {
		return nil, ErrMachineReposRequired
	}
	if cfg.Granter != nil && repos.Invitations == nil {
		return nil, ErrInvitationRepoRequired
	}
	// Relation-aware host policy is required with invitations (design §6/D3): a
	// Granter enables invitations, so a nil InviteCheck would leave create/list
	// unprotected — that fails loudly, not allow-by-default. Wiring InviteCheck with
	// invitations off is the contradictory-wiring error (the ErrInvitationRepoRequired
	// posture), so a dead policy never gives false confidence.
	if cfg.Granter != nil && cfg.InviteCheck == nil {
		return nil, ErrInviteCheckRequired
	}
	if cfg.Granter == nil && cfg.InviteCheck != nil {
		return nil, ErrInviteCheckWithoutGranter
	}
	// A resource policy is only ever consulted by the HTML surface, which is gated on
	// Views (design §9.2, GOTH-0.4). Setting HTMLPolicy with a nil Views is a policy
	// that can never render — the contradictory-wiring error (the
	// ErrInviteCheckWithoutGranter posture), so a dead policy never gives false
	// confidence.
	if cfg.HTMLPolicy != nil && cfg.Views == nil {
		return nil, ErrHTMLPolicyWithoutViews
	}
	// A non-empty browser login path must be a safe root-relative target (design §9.2):
	// the browser identity gates 303 to it on denial, so a scheme/protocol-relative/
	// backslash/control-character value would be an off-site open redirect. Empty
	// defaults to "/auth/login" in authsvc and never trips this. Validated through the
	// same shared redirect.SafeRelativePath the gates and the form lane use.
	if cfg.BrowserLoginPath != "" && redirect.SafeRelativePath(cfg.BrowserLoginPath) != cfg.BrowserLoginPath {
		return nil, ErrBrowserLoginPathInvalid
	}
	// Enable-time validation for the challenge subsystem (design §3.3): wiring the
	// Challenges repository enables the atomic secret rail, which REQUIRES a
	// ChallengeProtector to protect its codes/tokens. Nil is tolerated only while
	// the subsystem is off (repos.Challenges == nil).
	if repos.Challenges != nil && cfg.ChallengeProtector == nil {
		return nil, ErrChallengeProtectorRequired
	}
	// Delivery-mode matrix (authv3-delivery-refactor AV3D-0.1). DeliveryMode is the
	// host's explicit selection of the outbound-delivery execution model — never
	// inferred from a non-nil collaborator. The payload envelope is ALWAYS sealed
	// wherever delivery can happen (retryable work temporarily carries a rendered
	// secret/destination), so a wired jobs dispatcher REQUIRES a DeliveryEncrypter;
	// checked first so a missing encrypter is reported before the mode-specific
	// acknowledgment posture. in_process additionally builds a feature-internal bounded
	// delivery queue (the ephemeral runtime, AV3D-4.1), which also seals its payload —
	// so it requires the encrypter even without a wired collaborator.
	deliveryWired := cfg.DeliveryDispatcher != nil
	inProcessDelivery := cfg.DeliveryMode == DeliveryModeInProcess
	if (deliveryWired || inProcessDelivery) && cfg.DeliveryEncrypter == nil {
		return nil, ErrDeliveryEncrypterRequired
	}
	switch cfg.DeliveryMode {
	case DeliveryModeOff:
		// off: no delivery runtime. A wired generic-jobs dispatcher means a configured
		// flow could deliver, so off is contradictory and fails closed. Passwordless — the
		// other deliverable flow — is caught by validatePasswordless below
		// (ErrPasswordlessDeliveryRequired). Other flows (verification, forgot-password,
		// invitations) all route through the same queue, so off uniformly disables them
		// rather than erroring.
		if deliveryWired {
			return nil, ErrDeliveryOffButDeliverable
		}
	case DeliveryModeJobs:
		// jobs: durable delivery on the generic jobs runtime. The queue capability is
		// mandatory — the stdlib-typed Config.DeliveryDispatcher (the composition adapter
		// over generic jobs, AV3D-3.1) is the delivery transport. Production fails closed
		// on an unacknowledged runtime (the queue is the only send path); durability is
		// the generic jobs store's responsibility, asserted by DeliveryJobsAcknowledged.
		if cfg.DeliveryDispatcher == nil {
			return nil, ErrDeliveryQueueRequired
		}
		if cfg.RuntimeMode == RuntimeModeProduction && !cfg.DeliveryJobsAcknowledged {
			return nil, ErrDeliveryJobsUnacknowledged
		}
	case DeliveryModeInProcess:
		// in_process: bounded, process-local, EPHEMERAL delivery. It is explicitly
		// non-durable; production requires the explicit crash-loss acknowledgment, since
		// in-flight work is lost on a restart.
		if cfg.RuntimeMode == RuntimeModeProduction && !cfg.DeliveryEphemeralAcknowledged {
			return nil, ErrDeliveryEphemeralUnacknowledged
		}
		// Bounded-runtime knob validation (AV3D-4.5): every knob is nil-safe (zero →
		// default), but a NEGATIVE bound (or a status retention smaller than the queue it
		// must cover) fails LOUDLY here with a typed error wrapping sdk.ErrInvalidInput —
		// never silently coerced to a default. The validated configs are reused at the
		// queue/runtime construction sites below.
		if err := inProcessQueueConfig(cfg).Validate(); err != nil {
			return nil, err
		}
		if err := inProcessRuntimeConfig(cfg).Validate(); err != nil {
			return nil, err
		}
	}
	listStrategy, err := resolveListStrategy(cfg.ListStrategy)
	if err != nil {
		return nil, err
	}
	// Build the notifier lookup keyed by kind, rejecting duplicate kinds LOUDLY
	// (the ErrOAuthReposRequired posture — NOT the OAuth provider map's silent
	// last-wins). A duplicate delivery route is a wiring bug, never a silent pick.
	notifiers := make(map[string]notify.Notifier, len(cfg.Notifiers))
	for _, n := range cfg.Notifiers {
		kind := n.Kind()
		if _, dup := notifiers[kind]; dup {
			return nil, ErrDuplicateNotifierKind
		}
		notifiers[kind] = n
	}
	// Fail closed on delivery transport security (design §6.3): in production a
	// development-only or metadata-less Mailer/Notifier is rejected; in
	// development a development-only transport warns. The Mailer is validated non-
	// nil above.
	transportLog := cfg.Logger
	if transportLog == nil {
		transportLog = slog.Default()
	}
	if err := validateDeliveryTransports(cfg.RuntimeMode, cfg.Mailer, cfg.Notifiers, transportLog); err != nil {
		return nil, err
	}
	limiter := cfg.RateLimiter
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	// Fail closed on a per-process rate limiter in production (design §4.4/§8):
	// login rate limiting is always active, so a multi-instance deployment needs a
	// shared/durable limiter — an in-process one (the ratelimiter.Memory default)
	// enforces only a per-process budget. Development warns instead. cfg.RateLimiter
	// (not the defaulted limiter) is passed so a nil is read as the in-process
	// default.
	if err := validateRateLimiter(cfg.RuntimeMode, cfg.RateLimiter, transportLog); err != nil {
		return nil, err
	}
	// PII-free rate-limit/idempotency keys are always active, so production requires
	// the shared HMAC IdentifierKeyer (design §4.4/§8): without it the digest falls
	// back to a per-instance SHA-256 — still PII-free, but not the shared keyed
	// digest a multi-instance deployment needs to key one identifier to one bucket.
	// Development tolerates the fallback.
	if cfg.RuntimeMode == RuntimeModeProduction && cfg.IdentifierKeyer == nil {
		return nil, ErrIdentifierKeyerRequired
	}

	// The shared delivery renderer/router (design §6.1): one kind-aware policy
	// consumed by BOTH authsvc and invitationsvc so the outbound email/SMS content
	// and the email/notify kind fork have a single definition instead of two
	// drifting copies. It renders an encrypted-job-ready Envelope; the durable
	// worker (phase 4) sends it. The Mailer is validated non-nil above, so the
	// router is always buildable here.
	deliveryRouter, err := delivery.NewRouter(delivery.Deps{
		Mailer:       cfg.Mailer,
		MailFrom:     cfg.MailFrom,
		Notifiers:    notifiers,
		AppTemplates: cfg.EmailContentTemplates,
		Logger:       cfg.Logger,
	})
	if err != nil {
		return nil, err
	}

	// The delivery queue (design §6.1.1): every auth outbound message enqueues here
	// instead of a request-time provider send, so account resolution and provider
	// latency happen in the host-owned delivery runtime off the request path. It is
	// built only for a mode that can deliver (the encrypter is required alongside it,
	// validated above); nil leaves the send sites fail-closed (ErrDeliveryDisabled)
	// rather than silently synchronous. The payload is ALWAYS sealed as the versioned
	// command envelope, so the same transport-neutral command.Engine both modes drive
	// opens exactly what admission sealed.
	var deliveryQueue *delivery.Service
	var inProcessQueue *delivery.InProcessQueue
	switch {
	case cfg.DeliveryMode == DeliveryModeInProcess:
		// in_process mode (AV3D-4.1): build the feature-internal bounded admission queue.
		// It is the ephemeral runtime's Dispatcher — a fixed worker pool drains it, built
		// after authService below and run by the host via RunDelivery. Accepted work is
		// process-local and does NOT survive a restart.
		inProcessQueue = delivery.NewInProcessQueue(inProcessQueueConfig(cfg))
		deliveryQueue, err = delivery.NewService(delivery.ServiceDeps{
			Dispatcher: inProcessQueue,
			Encrypter:  cfg.DeliveryEncrypter,
		})
		if err != nil {
			return nil, err
		}
	case cfg.DeliveryMode == DeliveryModeJobs && cfg.DeliveryDispatcher != nil:
		// jobs mode over generic jobs (AV3D-3.1): submit sealed command envelopes through
		// the host-composed dispatcher; the jobs-mode processor opens exactly what the
		// service seals.
		deliveryQueue, err = delivery.NewService(delivery.ServiceDeps{
			Dispatcher: cfg.DeliveryDispatcher,
			Encrypter:  cfg.DeliveryEncrypter,
		})
		if err != nil {
			return nil, err
		}
	}

	// Passwordless enablement matrix (design §4.1/§4.2/§6.4/§8). Empty → the
	// passwordless routes are absent (deny-by-absence); when a host opts in, every
	// listed kind must be a valid v3 kind with a wired delivery channel (the router's
	// deny-by-absence Supports seam), the atomic challenge rail and durable outbox
	// must be wired (async starts issue challenges and enqueue — V14), and a
	// link-capable PublicAuthBaseURL is required (HTTPS in production). The always-on
	// production durable-limiter / identifier-keyer / worker-acknowledgment gates are
	// validated above, so a passwordless-enabled production host inherits them. A
	// half-wired passwordless config would strand the users it is enabled for.
	if err := validatePasswordless(cfg.RuntimeMode, cfg.Passwordless, deliveryRouter, repos.Challenges != nil, deliveryWired || inProcessDelivery, cfg.PublicAuthBaseURL); err != nil {
		return nil, err
	}

	// authService is declared here and assigned below (authsvc.NewService), so the
	// invitation service's accept-time identifier accessor can bind to it: the two
	// services reference each other (authsvc holds invitationsvc for resolve-on-
	// registration; invitationsvc holds authsvc's ActiveVerifiedIdentifier for the
	// V11 phone accept-time match), a construction cycle broken by this late-bound
	// closure — it is only invoked at request time, long after both are wired.
	var authService *authsvc.Service

	// The invitation service is built only when a Granter is wired (deny-by-
	// absence). Its Granter is injected HERE, never into authsvc (design §6 pin).
	// The single injected identifier normalizer (design §2.2), nil-defaulted to the
	// bundled strict policy, shared by the direct-add userLookup below and both
	// service Deps so registration, login, recovery, and invitations canonicalize
	// identically.
	idNormalizer := identifier.Normalizer(identifier.DefaultNormalizer{})
	if cfg.IdentifierNormalizer != nil {
		idNormalizer = cfg.IdentifierNormalizer
	}

	var invSvc *invitationsvc.Service
	if cfg.Granter != nil {
		invDeps := invitationsvc.Deps{
			Invitations: repos.Invitations,
			Granter:     cfg.Granter,
			MemberCheck: cfg.MemberCheck,
			UserLookup:  userLookup(repos.Identifiers, idNormalizer),
			// CallerIdentifiers resolves the accepting caller's active verified
			// identifier of a kind for the V11 phone accept-time match, through the same
			// kind-aware accessor the invitation HTTP handlers use (design §7).
			CallerIdentifiers: func(ctx context.Context, userID, kind string) (string, error) {
				return authService.ActiveVerifiedIdentifier(ctx, userID, kind)
			},
			Mailer:         cfg.Mailer,
			MailFrom:       cfg.MailFrom,
			Deliver:        deliveryRouter,
			Redirects:      redirect.New(cfg.RedirectAllowlist),
			SecurityEvents: repos.SecurityEvents,
			Logger:         cfg.Logger,
			IDs:            cfg.IDs,
			Notifiers:      notifiers,
		}
		// Wire the injected identifier normalizer only when the host supplies one, so
		// invitationsvc nil-defaults to the same bundled strict identifier.Default
		// Normalizer authsvc uses: one policy canonicalizes invitation identifiers,
		// including the strict E.164 the V11 phone match depends on.
		if cfg.IdentifierNormalizer != nil {
			invDeps.Normalizer = cfg.IdentifierNormalizer
		}
		// Set the outbox only when built, so the invitationsvc field stays a genuine
		// nil interface (not a typed-nil) when the outbox is off.
		if deliveryQueue != nil {
			invDeps.Queue = deliveryQueue
		}
		invSvc = invitationsvc.New(invDeps)
	}

	deps := authsvc.Deps{
		Users:                repos.Users,
		Identifiers:          repos.Identifiers,
		Passwords:            repos.Passwords,
		Sessions:             repos.Sessions,
		Challenges:           repos.Challenges,
		PasswordResets:       repos.PasswordResets,
		ContactChanges:       repos.ContactChanges,
		CredentialMutations:  repos.CredentialMutations,
		AuthenticationGrants: repos.AuthenticationGrants,
		CredentialPolicy:     cfg.CredentialPolicy,
		Hasher:               cfg.Hasher,
		CompromisedFailOpen:  cfg.CompromisedPasswordFailOpen,
		Deliver:              deliveryRouter,
		Limiter:              limiter,
		Cookie: authsvc.CookieConfig{
			Name:   cfg.SessionCookie.Name,
			Path:   cfg.SessionCookie.Path,
			Domain: cfg.SessionCookie.Domain,
			Secure: cfg.SessionCookie.Secure,
			MaxAge: cfg.SessionCookie.MaxAge,
		},
		RequireVerifiedEmail: cfg.RequireVerifiedEmail,
		OAuthAccounts:        repos.OAuthAccounts,
		OAuthStates:          repos.OAuthStates,
		Providers:            cfg.Providers,
		TokenEncrypter:       cfg.TokenEncrypter,
		OAuthCallbackBase:    cfg.OAuthCallbackBase,
		RedirectAllowlist:    cfg.RedirectAllowlist,
		ServiceAccounts:      repos.ServiceAccounts,
		APIKeys:              repos.APIKeys,
		SecurityEvents:       repos.SecurityEvents,
		TokenSigner:          cfg.TokenSigner,
		AccessTokenTTL:       cfg.AccessTokenTTL,
		RefreshTTL:           cfg.RefreshTTL,
		Passwordless:         cfg.Passwordless,
		PublicAuthBaseURL:    cfg.PublicAuthBaseURL,
		BrowserLoginPath:     cfg.BrowserLoginPath,
		Logger:               cfg.Logger,
		IDs:                  cfg.IDs,
	}
	// Set the resolve-on-registration collaborator only when built, so the
	// authsvc field stays a genuine nil interface when invitations are off.
	if invSvc != nil {
		deps.Invitations = invSvc
	}
	// Wire the injected identifier normalizer only when the host supplies one, so
	// authsvc nil-defaults to the bundled strict identifier.DefaultNormalizer; one
	// policy canonicalizes registration, login, verify, and recovery values.
	if cfg.IdentifierNormalizer != nil {
		deps.Normalizer = cfg.IdentifierNormalizer
	}
	// Wire the challenge protector only when one is supplied, so the authsvc field
	// stays a genuine nil interface when the challenge subsystem is off (validated
	// required alongside repos.Challenges above).
	if cfg.ChallengeProtector != nil {
		deps.Protector = cfg.ChallengeProtector
	}
	// Wire the compromised-password checker only when one is supplied, so the
	// authsvc field stays a genuine nil interface (no breach check) when unset.
	if cfg.CompromisedPasswordChecker != nil {
		deps.Compromised = cfg.CompromisedPasswordChecker
	}
	// Wire the durable outbox and its PII-free keyer only when built/supplied, so the
	// authsvc fields stay genuine nil interfaces (send sites fail closed) when off.
	if deliveryQueue != nil {
		deps.Queue = deliveryQueue
	}
	if cfg.IdentifierKeyer != nil {
		deps.IdentifierKeyer = cfg.IdentifierKeyer
	}

	authService = authsvc.NewService(deps)

	// The outbound delivery executor is built AFTER authService so its Initializer —
	// the auth service itself, which resolves accounts and issues challenges for opaque
	// start jobs — is fully attached before any handler can run. The feature starts no
	// goroutine at construction; the host runs the selected runtime.
	//
	//   - jobs mode over generic jobs (AV3D-3.1): build the transport-neutral
	//     command.Engine processor and expose it through DeliveryJobRuntime(); the host
	//     registers it on the generic jobs runtime.
	//   - in_process mode (AV3D-4.1): build the same processor behind a bounded pool the
	//     host runs via RunDelivery.
	var jobsProcessor *delivery.JobsProcessor
	var inProcessRuntime *delivery.InProcessRuntime
	switch {
	case cfg.DeliveryMode == DeliveryModeInProcess:
		// in_process mode (AV3D-4.1/4.3): build the delivery processor (the transport-neutral
		// command.Engine, wrapped with the Router/Initializer adapters) over the fully-built
		// authService, then attach it to the bounded pool that drains the admission queue
		// built above. The processor is the SAME one jobs mode runs, so the bounded pool
		// applies the identical provider timeout, error classification, attempt cap,
		// context-cancellable backoff, observer transitions, and terminal challenge discard.
		// The feature starts no goroutine — the host runs RunDelivery.
		inProcDeps := delivery.JobsProcessorDeps{
			Encrypter:   cfg.DeliveryEncrypter,
			Router:      deliveryRouter,
			Initializer: authService,
		}
		// The optional, best-effort lifecycle observer emits secret-free delivery events
		// (delivered/skipped/retried and, after a recorded dead-letter, dead_lettered) onto
		// the host's rail; a nil emitter leaves Observer a true nil interface (a no-op path).
		if cfg.DeliveryEventsEmitter != nil {
			inProcDeps.Observer = delivery.NewEventObserver(cfg.DeliveryEventsEmitter, cfg.Logger)
		}
		var proc *delivery.JobsProcessor
		proc, err = delivery.NewJobsProcessor(inProcDeps)
		if err != nil {
			return nil, err
		}
		rtCfg := inProcessRuntimeConfig(cfg)
		rtCfg.Logger = cfg.Logger
		inProcessRuntime, err = delivery.NewInProcessRuntime(inProcessQueue, proc, rtCfg)
		if err != nil {
			return nil, err
		}
	case cfg.DeliveryMode == DeliveryModeJobs && cfg.DeliveryDispatcher != nil:
		jobsDeps := delivery.JobsProcessorDeps{
			Encrypter:   cfg.DeliveryEncrypter,
			Router:      deliveryRouter,
			Initializer: authService,
		}
		// The optional, best-effort lifecycle observer emits secret-free delivery events
		// onto the host's rail; a nil emitter leaves Observer a true nil interface (a
		// no-op path) rather than a typed-nil.
		if cfg.DeliveryEventsEmitter != nil {
			jobsDeps.Observer = delivery.NewEventObserver(cfg.DeliveryEventsEmitter, cfg.Logger)
		}
		jobsProcessor, err = delivery.NewJobsProcessor(jobsDeps)
		if err != nil {
			return nil, err
		}
	}

	return &Service{
		svc:              authService,
		inv:              invSvc,
		inviteCheck:      cfg.InviteCheck,
		jobsProcessor:    jobsProcessor,
		inProcessRuntime: inProcessRuntime,
		inProcessQueue:   inProcessQueue,
		listStrategy:     listStrategy,
		mutationSecurity: inbound.MutationSecurity{
			AllowedOrigins:    cfg.AllowedOrigins,
			SessionCookieName: authService.SessionCookieName(),
		},
		views:      cfg.Views,
		htmlPolicy: cfg.HTMLPolicy,
	}, nil
}

// userLookup builds the internal email→subject resolver invitationsvc uses for
// the direct-add path, backed by the identifier discovery rail (design §2.2/§7).
// It normalizes through the single injected policy and resolves the owning
// subject through an active login- then recovery-enabled email identifier. It is
// wired here (package auth has the repos) so invitationsvc stays decoupled from
// the identifier store; an invalid or unknown email resolves to no user
// (found=false), never an error.
func userLookup(idents identifier.IdentifierRepository, norm identifier.Normalizer) invitationsvc.UserLookup {
	kind := string(identifier.KindEmail)
	return func(ctx context.Context, emailAddr string) (string, bool, error) {
		normalized, err := norm.Normalize(kind, emailAddr)
		if err != nil {
			return "", false, nil
		}
		ident, err := idents.GetLogin(ctx, kind, normalized)
		if err == nil {
			return ident.UserID, true, nil
		}
		if !errors.Is(err, sdk.ErrNotFound) {
			return "", false, err
		}
		ident, err = idents.GetRecovery(ctx, kind, normalized)
		if err != nil {
			if errors.Is(err, sdk.ErrNotFound) {
				return "", false, nil
			}
			return "", false, err
		}
		return ident.UserID, true, nil
	}
}

// RequireUser is HTTP middleware gating a route on a valid session. It satisfies
// sdk/foundation/web.Middleware via the method value authSvc.RequireUser, so a host passes
// it to another feature (e.g. cms.Config.AdminMiddleware) without either feature
// importing the other.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return s.svc.RequireUser(next)
}

// CurrentUser returns the authenticated user id on ctx, if any. It structurally
// satisfies a consuming feature's identity port (features/README.md §5's
// CurrentUser) with zero import in either direction.
func (s *Service) CurrentUser(ctx context.Context) (userID string, ok bool) {
	return s.svc.CurrentUser(ctx)
}

// RequireServiceAccount is HTTP middleware gating a route on an API-key bearer
// credential (design §4.3). A host wires it like RequireUser; it stashes the
// resolved Principal, read via CurrentPrincipal.
func (s *Service) RequireServiceAccount(next http.Handler) http.Handler {
	return s.svc.RequireServiceAccount(next)
}

// RequirePrincipal is HTTP middleware gating a route on either credential class
// (session or API-key bearer, plus a bearer JWT when Config.TokenSigner is
// wired). It stashes the resolved Principal, read via CurrentPrincipal.
func (s *Service) RequirePrincipal(next http.Handler) http.Handler {
	return s.svc.RequirePrincipal(next)
}

// RequireLiveSession is HTTP middleware for sensitive routes that gates on a LIVE
// session — one PK lookup per request (§1.4, D1), the immediate-revocation tier
// above RequireUser's stateless JWT check. A user JWT's session_id is looked up
// (deleted/expired → deny); an API key passes (already DB-checked, no session
// row); a repository error DENIES (fails CLOSED). A host wires it like RequireUser
// on password-change, key-minting, invitation, or secret-read routes.
func (s *Service) RequireLiveSession(next http.Handler) http.Handler {
	return s.svc.RequireLiveSession(next)
}

// RequirePrincipalBrowser is the browser-facing sibling of RequirePrincipal for HTML
// routes (design §9.2). It resolves the SAME credential classes and stashes the SAME
// Principal (read via CurrentPrincipal), but on an authentication denial it 303s to
// Config.BrowserLoginPath (default "/auth/login") instead of writing a JSON 401 — a
// denied GET/HEAD carrying a validated return_to of the original path+query, an unsafe
// method none. It never sniffs Accept or Fetch Metadata; mount it deliberately on the
// HTML routes a host renders login pages for.
func (s *Service) RequirePrincipalBrowser(next http.Handler) http.Handler {
	return s.svc.RequirePrincipalBrowser(next)
}

// RequireLiveSessionBrowser is the browser-facing sibling of RequireLiveSession for
// HTML routes (design §9.2). It enforces the SAME immediate-revocation live-session
// matrix and stashes the SAME Principal/session id, but on denial it 303s to
// Config.BrowserLoginPath (default "/auth/login") instead of writing a JSON 401 (same
// validated return_to rule as RequirePrincipalBrowser). A statelessly-valid but
// revoked user session passes RequirePrincipalBrowser and is denied here. Mount it on
// unsafe HTML routes that need immediate revocation.
func (s *Service) RequireLiveSessionBrowser(next http.Handler) http.Handler {
	return s.svc.RequireLiveSessionBrowser(next)
}

// AuthenticateAPIKey resolves the effective Principal for a raw API key (design
// §4.1): a personal act-as-user key yields Principal{Type: "user"}, otherwise
// Principal{Type: "service_account"}. Revoked, expired, or unknown keys return a
// generic sdk.ErrUnauthorized.
func (s *Service) AuthenticateAPIKey(ctx context.Context, rawKey string) (Principal, error) {
	return s.svc.AuthenticateAPIKey(ctx, rawKey)
}

// CurrentPrincipal returns the effective Principal stashed by
// RequireServiceAccount / RequirePrincipal, if any — the machine-or-human
// identity port a consuming feature reads alongside CurrentUser.
func (s *Service) CurrentPrincipal(ctx context.Context) (Principal, bool) {
	return s.svc.CurrentPrincipal(ctx)
}

// resolverAssertion is the compile-time proof that the auth feature satisfies the
// generic sdk/foundation/identity Resolver port: a host wires this Service anywhere a
// Resolver is expected, unadapted.
var _ identity.Resolver = (*Service)(nil)

// Resolve implements identity.Resolver: it turns a Principal into its display and
// contact Info. A user principal resolves to its DisplayName (else the primary
// email local part) carrying every active verified identifier as an Address,
// primary-first (design §7); a service-account principal resolves to its Name. An
// unknown principal type, a missing record, or an off machine subsystem (nil
// ServiceAccounts) returns an error satisfying sdk.ErrNotFound — fail-closed,
// nil-guarded, never a panic.
func (s *Service) Resolve(ctx context.Context, p identity.Principal) (identity.Info, error) {
	return s.svc.Resolve(ctx, p)
}

// RegisterUser creates an account and dispatches the email-verification code.
func (s *Service) RegisterUser(ctx context.Context, email, password, displayName string) (user.User, error) {
	return s.svc.Register(ctx, email, password, displayName)
}

// Login verifies credentials and returns the access/refresh TokenPair to set.
func (s *Service) Login(ctx context.Context, email, password string) (pair TokenPair, u user.User, err error) {
	return s.svc.Login(ctx, email, password)
}

// Logout revokes the session behind the caller's credentials (idempotent). It
// resolves the session id from the refresh token (primary) or the access JWT's
// session_id read ignoring expiry (fallback) — see §1.5.
func (s *Service) Logout(ctx context.Context, refreshToken, accessToken string) error {
	return s.svc.Logout(ctx, refreshToken, accessToken)
}

// Refresh rotates the presented refresh token per the §1.3 contract, returning a
// fresh TokenPair (RefreshToken empty on the grace lane).
func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	return s.svc.Refresh(ctx, refreshToken)
}

// Verify redeems a registration verification code for the account behind email,
// claiming and verifying its primary email identifier (design §2.3, §3.2).
func (s *Service) Verify(ctx context.Context, email, code string) error {
	return s.svc.Verify(ctx, email, code)
}

// ChangePassword verifies the current password, sets the new one, revokes all sessions, and returns a fresh TokenPair.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (pair TokenPair, err error) {
	return s.svc.ChangePassword(ctx, userID, currentPassword, newPassword)
}

// ForgotPassword enqueues an enumeration-safe password-reset start: it normalizes
// the address and enqueues an opaque delivery command without resolving the account
// or calling a provider (design §4.1/§6.1.1). Known and unknown addresses share one
// bounded request path; the worker resolves the recovery identifier and delivers.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	return s.svc.ForgotPassword(ctx, email)
}

// RunDelivery runs the bounded, EPHEMERAL in-process delivery runtime until ctx is
// canceled (DeliveryMode "in_process", authv3-delivery-refactor AV3D-4.1). The host
// owns the lifecycle: call it in a goroutine and cancel ctx to stop. It launches a
// FIXED worker pool over a FINITE admission queue — never one goroutine per request —
// and, on cancellation, stops admission, lets in-flight provider calls observe
// cancellation, and drains within a bounded shutdown window. When the in-process
// runtime is not wired (any other DeliveryMode) it is a no-op (returns nil
// immediately), so a host may call it unconditionally.
//
// This mode is process-local and EPHEMERAL: accepted, in-flight delivery work is LOST
// on a crash or restart, there is no cross-instance coordination, and its process-local
// de-duplication (submit-once/replace by logical key), generation fencing, and bounded
// latest-by-key status are all lost on restart (AV3D-4.2). Running the host as MULTIPLE
// instances gives each its OWN queue, de-duplication, and status, so the same logical
// delivery admitted on two instances is de-duplicated on neither — both can render and
// both can send (a user may receive two messages). The durable, cross-instance
// de-duplicated posture is DeliveryMode "jobs".
func (s *Service) RunDelivery(ctx context.Context) error {
	if s.inProcessRuntime == nil {
		return nil
	}
	return s.inProcessRuntime.Run(ctx)
}

// InProcessQueueDepth reports the bounded in-process delivery queue's current depth and
// capacity for host operational health (DeliveryMode "in_process", AV3D-5.3). ok is true
// only in in_process mode; in every other mode it returns 0, 0, false — the backlog of
// the durable "jobs" mode lives in the generic jobs store, not process-local, so it is not
// observable here. The two counts are bounded and secret-free: they carry no recipient,
// payload, or logical key. A queued count at capacity indicates a saturated, backlogged
// queue.
func (s *Service) InProcessQueueDepth() (queued, capacity int, ok bool) {
	if s.inProcessQueue == nil {
		return 0, 0, false
	}
	queued, capacity = s.inProcessQueue.Depth()
	return queued, capacity, true
}

// DeliveryJobRuntime returns the narrow, stdlib-typed jobs-mode delivery seam a
// composition adapter registers on the generic jobs runtime (authv3-delivery-refactor
// AV3D-3.1). ok is true only when DeliveryMode is "jobs" and Config.DeliveryDispatcher
// is wired — the processor is fully built (its collaborators, including this Service's
// account resolver, are attached) BEFORE this returns, so a handler can never run
// against a half-built service. In every other mode ok is false and the host wires no
// handler. It starts no goroutine: the host owns the jobs runtime lifecycle.
func (s *Service) DeliveryJobRuntime() (DeliveryJobRuntime, bool) {
	if s.jobsProcessor == nil {
		return DeliveryJobRuntime{}, false
	}
	p := s.jobsProcessor
	return DeliveryJobRuntime{
		Kind: DeliveryJobKind,
		Handle: func(ctx context.Context, claim DeliveryClaim) error {
			return p.Handle(ctx, claim.ExecutionID, claim.Payload, claim.Attempt, claim.Checkpoint)
		},
		Discard: func(ctx context.Context, executionID string, payload []byte) error {
			return p.Discard(ctx, executionID, payload)
		},
		Purged: func(ctx context.Context, count int) {
			p.ObservePurge(ctx, count)
		},
	}, true
}

// DeliveryStatus returns the current delivery status for a receipt key (design
// §6.1.1). A session-gated caller polls it to learn that delivery failed without
// holding the start request open; the caller's handler must enforce the live
// session. An unknown key is sdk.ErrNotFound; the outbox being off is a wrapped
// sdk.ErrForbidden.
func (s *Service) DeliveryStatus(ctx context.Context, receiptKey string) (DeliveryStatus, error) {
	return s.svc.DeliveryStatus(ctx, receiptKey)
}

// ResetPassword redeems a reset token and sets the new password.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	return s.svc.ResetPassword(ctx, token, newPassword)
}

// StartOAuth returns the provider authorization URL for an unauthenticated login/register flow.
func (s *Service) StartOAuth(ctx context.Context, provider, redirectTo string) (authURL string, err error) {
	return s.svc.StartOAuth(ctx, provider, redirectTo)
}

// StartLink returns the provider authorization URL for linking a provider to the signed-in user.
func (s *Service) StartLink(ctx context.Context, userID, provider, redirectTo string) (authURL string, err error) {
	return s.svc.StartLink(ctx, userID, provider, redirectTo)
}

// OAuthCallback processes a provider callback (code + state) into an OAuthResult.
func (s *Service) OAuthCallback(ctx context.Context, provider, code, state string) (OAuthResult, error) {
	return s.svc.OAuthCallback(ctx, provider, code, state)
}

// VerifyLink completes a pending account link from its emailed token.
func (s *Service) VerifyLink(ctx context.Context, token string) (OAuthResult, error) {
	return s.svc.VerifyLink(ctx, token)
}

// ListLinked returns the user's linked provider accounts.
func (s *Service) ListLinked(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	return s.svc.ListLinked(ctx, userID)
}

// The code-gated OAuth unlink (design §5.4) is exposed through the mounted HTTP
// routes only (POST /auth/oauth/{provider}/unlink/start and .../unlink), matching
// the route-only credential-suite mutations (set/remove password); it is not a
// method on the host-facing Service.

// CreateServiceAccount creates a machine identity, optionally acting as ownerUserID.
func (s *Service) CreateServiceAccount(ctx context.Context, createdBy, name, description string, actAsUser bool, ownerUserID string) (serviceaccount.ServiceAccount, error) {
	return s.svc.CreateServiceAccount(ctx, createdBy, name, description, actAsUser, ownerUserID)
}

// ListServiceAccounts pages the service accounts.
func (s *Service) ListServiceAccounts(ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	return s.svc.ListServiceAccounts(ctx, req)
}

// MintAPIKey issues a key for a service account, returning the record and the one-time plaintext key.
func (s *Service) MintAPIKey(ctx context.Context, serviceAccountID, name string, expiresAt time.Time) (apikey.APIKey, string, error) {
	return s.svc.MintAPIKey(ctx, serviceAccountID, name, expiresAt)
}

// ListAPIKeys pages a service account's API keys.
func (s *Service) ListAPIKeys(ctx context.Context, serviceAccountID string, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	return s.svc.ListAPIKeys(ctx, serviceAccountID, req)
}

// RevokeAPIKey revokes an API key by id (idempotent).
func (s *Service) RevokeAPIKey(ctx context.Context, keyID string) error {
	return s.svc.RevokeAPIKey(ctx, keyID)
}

// IssueToken authenticates login-shaped credentials and mints a session-backed
// TokenPair (the API-flow twin of Login).
func (s *Service) IssueToken(ctx context.Context, email, password string) (pair TokenPair, err error) {
	return s.svc.IssueToken(ctx, email, password)
}

// Create invites an identifier to a resource; ErrInvitationsDisabled when no
// Granter is wired. This is a TRUSTED composition call: unlike the shipped HTTP
// create handler, it does NOT apply the Config.InviteCheck host authorization
// policy (design §6/D3) — a host driving the Service directly owns that decision.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if s.inv == nil {
		return CreateResult{}, ErrInvitationsDisabled
	}
	return s.inv.Create(ctx, in)
}

// ListByResource pages a resource's invitations; ErrInvitationsDisabled when no
// Granter is wired. This is a TRUSTED composition call: unlike the shipped HTTP
// list handler, it does NOT apply the Config.InviteCheck host authorization policy
// (design §6/D3) — a host driving the Service directly owns that decision.
func (s *Service) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	if s.inv == nil {
		return crud.Page[invitation.Invitation]{}, ErrInvitationsDisabled
	}
	return s.inv.ListByResource(ctx, resourceType, resourceID, req)
}

// Mine pages the caller's own invitations by identifier; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Mine(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	if s.inv == nil {
		return crud.Page[invitation.Invitation]{}, ErrInvitationsDisabled
	}
	return s.inv.Mine(ctx, identifier, req)
}

// Accept redeems an invitation token for the calling subject; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Accept(ctx context.Context, in AcceptInput) (AcceptResult, error) {
	if s.inv == nil {
		return AcceptResult{}, ErrInvitationsDisabled
	}
	return s.inv.Accept(ctx, in)
}

// Decline declines a pending invitation by id + token; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Decline(ctx context.Context, id, token string) error {
	if s.inv == nil {
		return ErrInvitationsDisabled
	}
	return s.inv.Decline(ctx, id, token)
}

// Cancel cancels a pending invitation the caller owns; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Cancel(ctx context.Context, id, currentUserID string) error {
	if s.inv == nil {
		return ErrInvitationsDisabled
	}
	return s.inv.Cancel(ctx, id, currentUserID)
}

// Resend regenerates and re-mails a pending invitation the caller owns; ErrInvitationsDisabled when no Granter is wired.
func (s *Service) Resend(ctx context.Context, id, currentUserID, redirectTo string) (invitation.Invitation, error) {
	if s.inv == nil {
		return invitation.Invitation{}, ErrInvitationsDisabled
	}
	return s.inv.Resend(ctx, id, currentUserID, redirectTo)
}

// SetSessionCookies writes BOTH browser credential cookies for a mint (§1.1, D4):
// the access-JWT cookie always, and the refresh cookie (Path=/auth, HttpOnly,
// SameSite=Lax) only when pair.RefreshToken is non-empty.
func (s *Service) SetSessionCookies(w http.ResponseWriter, pair TokenPair) {
	s.svc.SetSessionCookies(w, pair)
}

// ClearSessionCookies expires BOTH the access and refresh cookies.
func (s *Service) ClearSessionCookies(w http.ResponseWriter) {
	s.svc.ClearSessionCookies(w)
}

// SessionCookieName returns the configured access (session) cookie name.
func (s *Service) SessionCookieName() string {
	return s.svc.SessionCookieName()
}

// RefreshCookieName returns the refresh cookie name.
func (s *Service) RefreshCookieName() string {
	return s.svc.RefreshCookieName()
}

// RateLimitByIP returns middleware throttling a public route on the client IP.
func (s *Service) RateLimitByIP(keyPrefix string, perMinute int) web.Middleware {
	return s.svc.RateLimitByIP(keyPrefix, perMinute)
}

// Register mounts the auth feature's shipped HTTP adapter — the /auth/* route
// surface — onto the host's Mount, over this already-built Service (FS2: build
// once via NewService, mount once). It is the optional convenience adapter over
// the Service's use-case methods: subsystems the Service was built without
// register no routes (deny-by-absence), and a host may skip Register entirely
// and drive the methods from its own handlers. Migrations are registered by the
// store adapter (features/authentication/stores/turso), not here — the core is
// dialect-blind. The audit rail's best-effort WARN sink (Config.Logger) is
// captured at NewService time; set it there — it defaults to slog.Default() when
// unset, no longer to the Mount logger, since the Service is built before Mount.
func (s *Service) Register(m feature.Mount) error {
	// Pass the invitation service as a GENUINE nil interface when it is off, so
	// Mount's deny-by-absence check (design §6) is not fooled by a typed nil.
	var inv inbound.InvitationService
	if s.inv != nil {
		inv = s.inv
	}
	inbound.Mount(m.Router, s.svc, inv, s.inviteCheck, s.listStrategy, s.mutationSecurity, s.views, s.htmlPolicy)
	return nil
}
