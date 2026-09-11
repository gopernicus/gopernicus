// Package authentication implements credential, account and session use cases over
// host-provided repositories. Applications may construct Service directly or use
// the pocket root's Components. Transport adapters own HTTP headers and cookies;
// credential verification and account security rules stay here.
//
// Secret hygiene: passwords are only ever compared through the Hasher (bcrypt's
// compare is constant-time by construction); verification codes and reset
// tokens are opaque values matched by keyed lookup, so no secret is branched on
// byte-by-byte here. Secrets are never logged, and forgot-password never
// reveals whether an email is registered.
package authentication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
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
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	// passwordResetTTL is how long a password-reset token stays valid.
	passwordResetTTL = time.Hour
	// minPasswordCodePoints is the minimum length of a single-factor password in
	// Unicode code points (design §5.9): the v3 floor replaces the old eight-byte
	// minimum. Length is the only strength rule — no arbitrary composition or
	// periodic-rotation requirements are imposed.
	minPasswordCodePoints = 15
	// maxPasswordCodePoints is the maximum accepted password length in Unicode
	// code points (design §5.9: "at least 64"). A generous ceiling so passphrases
	// are welcome; it exists only to bound work, not to weaken long secrets.
	maxPasswordCodePoints = 64
	// maxPasswordInputBytes is the finite pre-hash input cap (design §5.9: inputs
	// are length-bounded before expensive hashing). A 64-code-point password is at
	// most 256 UTF-8 bytes, so this rejects only pathological over-long input
	// before it reaches the counter or the hasher; over-cap input is REJECTED, never
	// silently truncated (the bcrypt integration also errors past its 72-byte limit
	// rather than truncating — the no-silent-truncation contract holds end to end).
	maxPasswordInputBytes = 256
	// defaultAccessTokenTTL is the access-JWT lifetime when SessionsConfig.AccessTokenTTL
	// is unset (§1.1, D8). Kept short: it bounds the revocation-asymmetry window
	// on stateless routes (a deleted session still honors an outstanding access
	// JWT for ≤ this).
	defaultAccessTokenTTL = 15 * time.Minute
	// defaultRefreshTTL is the refresh-token / session horizon when
	// SessionsConfig.RefreshTTL is unset (§1.1, D8). Fixed at mint; rotation never
	// extends it (D2).
	defaultRefreshTTL = 7 * 24 * time.Hour
	// refreshAttemptsPerMinute caps per-session refresh attempts (the by-session
	// arm of the §6 refresh rate limit); the by-IP arm is a route middleware.
	refreshAttemptsPerMinute = 30
)

// TokenPair is the credential pair a session mint produces (§1.1): a
// self-validating access JWT and its absolute expiry, plus the opaque refresh
// token. RefreshToken is empty on the grace lane (§1.3 branch 4), where only a
// new access token is issued and the client keeps its existing refresh token.
type TokenPair struct {
	AccessToken     string
	AccessExpiresAt time.Time
	RefreshToken    string
}

// ErrRateLimited reports an exhausted host-configured authentication request
// budget. HTTP adapters map it to 429 without exposing the limiting key.
var ErrRateLimited = errors.New("too many authentication requests")

// ErrPasswordFlowsDisabled is returned by every password entry point when
// PasswordConfig.PasswordFlowsDisabled is set. It wraps sdk.ErrNotFound so a host that
// reaches a use-case directly gets the same answer the unmounted route gives.
var ErrPasswordFlowsDisabled = fmt.Errorf("auth: password flows are disabled on this host: %w", sdk.ErrNotFound)

// ErrEmailNotVerified is returned by Login when PasswordConfig.RequireVerifiedEmail is
// set and the caller's email is unverified. It wraps sdk.ErrForbidden so the
// transport maps it to 403 (design §7.1). Checked with errors.Is.
var ErrEmailNotVerified = fmt.Errorf("email not verified: %w", sdk.ErrForbidden)

// ErrRegistrationVerificationConflict is returned by Verify when the
// verify_registration code was consumed but the atomic identifier apply lost a
// revision-CAS (design §5.6): the code is spent, but no partial state was written,
// so the flow is safely restartable — the caller reissues a registration code and
// tries again. It wraps sdk.ErrConflict so the transport maps it to 409. Checked
// with errors.Is.
var ErrRegistrationVerificationConflict = fmt.Errorf("registration verification could not be applied, please request a new code: %w", sdk.ErrConflict)

// ErrPasswordResetInvalid is the single generic failure ResetPassword returns for
// an unknown, expired, or already-used reset token (design §5.8 enumeration/anti-
// probing): every non-live token collapses to one error so a response can never
// distinguish "no such token" from "expired" from "already used", and the secret
// is never named. It wraps sdk.ErrNotFound so the transport maps it to 404,
// preserving the existing external reset contract. Checked with errors.Is.
var ErrPasswordResetInvalid = fmt.Errorf("password reset token is invalid or expired: %w", sdk.ErrNotFound)

// ErrPasswordCompromised is returned by the password policy when a wired
// CompromisedPasswordChecker reports the candidate as breached/blocklisted
// (design §5.9). It wraps sdk.ErrInvalidInput so the transport maps it to 400 and
// the caller is asked to choose a different password. Checked with errors.Is. A
// checker that cannot COMPLETE (an infrastructure error) is governed separately by
// the fail-closed/fail-open policy, not this error.
var ErrPasswordCompromised = fmt.Errorf("password is known to be compromised: %w", sdk.ErrInvalidInput)

// passwordResetPurgePurposes are the outstanding challenge purposes a successful
// reset purges for the user in the same transaction (design §5.9): the just-
// consumed reset row (idempotent) plus any live remove-password challenge, so a
// pending credential-mutation flow cannot survive a reset.
var passwordResetPurgePurposes = []string{challenge.PurposePasswordReset, challenge.PurposeRemovePassword}

// Hasher hashes and verifies passwords. Hosts supply their chosen algorithm.
type Hasher interface {
	HashPassword(password string) (string, error)
	VerifyPassword(hash, password string) error
}

// CompromisedPasswordChecker reports whether a candidate password is known-compromised
// (present in a breach corpus or a host blocklist, design §5.9). It is
// optional — nil disables the breach check — and the pocket core ships none, so
// the core adds no network dependency; a local blocklist or a future remote
// breach-check adapter both satisfy it.
type CompromisedPasswordChecker interface {
	// IsCompromised reports whether password is known-compromised. A non-nil error
	// means the check could not complete (e.g. an unreachable remote corpus); the
	// service's fail-closed/fail-open policy then decides the outcome.
	IsCompromised(ctx context.Context, password string) (bool, error)
}

// invitationResolver is the SINGLE narrow port authentication service holds on the sibling
// invitation service (design §6 pin): verified-identity flows call it to grant
// the verified email's pending auto-accept invitations. It is
// declared here (structural) so authentication service carries NO import edge to invitation service
// and never holds the Granter. Nil → invitations are off (a no-op).
type invitationResolver interface {
	ResolveInvitations(ctx context.Context, email, subjectType, subjectID string) (int, error)
}

// constructorConfig holds storage and resolved host policy.
// New validates enabled capabilities and snapshots mutable configuration.
type constructorConfig struct {
	RuntimeMode             environment.Mode
	AuthenticationLimits    AuthenticationLimits
	Users                   user.UserRepository
	Identifiers             identifier.IdentifierRepository
	Normalizer              identifier.Normalizer
	Passwords               user.PasswordRepository
	Sessions                session.SessionRepository
	UserAdmin               user.AdminRepository
	UserAdminCheck          UserAdminCheck
	PasswordlessRedeem      passwordless.Repository
	ProvisionOnRedeem       bool
	ActiveSessions          session.ActiveUserRepository
	Challenges              challenge.Repository
	Protector               challengeProtector
	PasswordResets          passwordreset.Repository
	ContactChanges          contactchange.Repository
	CredentialMutations     credential.MutationRepository
	AuthenticationGrants    authgrant.Repository
	CredentialPolicy        credential.Policy
	Hasher                  Hasher
	ValidatePassword        func(context.Context, string) error
	Compromised             CompromisedPasswordChecker
	CompromisedFailOpen     bool
	Deliver                 *delivery.Router
	Queue                   deliveryQueue
	IdentifierKeyer         identifierKeyer
	Limiter                 ratelimiter.Limiter
	Clock                   func() time.Time
	Logger                  *slog.Logger
	IDs                     sdk.IDGenerator
	PasswordFlowsDisabled   bool
	RequireVerifiedEmail    bool
	SecurityEvents          securityevent.SecurityEventRepository
	Invitations             invitationResolver
	OAuthAccounts           oauthaccount.OAuthAccountRepository
	OAuthStates             oauthstate.StateRepository
	Providers               []oauth.Provider
	TokenEncrypter          cryptids.Encrypter
	OAuthCallbackBase       string
	OAuthNativeRedirectURIs []string
	TrustOAuthEmail         func(string, oauth.UserInfo) bool
	RedirectAllowlist       []string
	ServiceAccounts         serviceaccount.ServiceAccountRepository
	APIKeys                 apikey.APIKeyRepository
	TokenSigner             cryptids.JWTSigner
	AccessTokenTTL          time.Duration
	RefreshTTL              time.Duration
	Passwordless            []string
	PublicAuthBaseURL       string
	PasswordResetURL        string
	OAuthLinkBaseURL        string
}

// Service implements the auth use cases over the repository ports.
type Service struct {
	authenticationLimits AuthenticationLimits
	users                user.UserRepository
	// identifiers backs the v3 identity-discovery rail (design §2.2): registration
	// creates the unverified primary email identifier, login/token resolve through
	// GetLogin, and verification claims/verifies via the atomic ApplyVerifiedChange.
	identifiers identifier.IdentifierRepository
	// normalizer is the single injected identifier-value canonicalizer (design
	// §2.2); nil-defaulted to identifier.DefaultNormalizer in New.
	normalizer identifier.Normalizer
	passwords  user.PasswordRepository
	sessions   session.SessionRepository
	// challenges backs the atomic secret rail (design §3.2); protector protects
	// its codes/tokens (design §3.3). Both nil → the challenge service methods
	// refuse (the subsystem is off).
	challenges challenge.Repository
	protector  challengeProtector
	// passwordResets backs the atomic password-reset composition (design §5.9).
	// Nil → ResetPassword refuses (the forgot/reset rail is off, fail closed).
	passwordResets passwordreset.Repository
	// contactChanges backs the pending-value flow state of an identifier add/change
	// (design §2.4). Nil → the identifier add/change flows fail closed.
	contactChanges contactchange.Repository
	// credentialMutations backs the revision-serialized credential-mutation rail
	// (design §5.6); the OAuth adoption-revocation path removes a squatter password
	// through it (design §5.7/V5). Nil → the adoption path fails closed.
	credentialMutations credential.MutationRepository
	// authGrants backs recent-authentication / step-up grants (design §5.0). Nil →
	// the step-up service methods fail closed (ErrStepUpUnavailable).
	authGrants authgrant.Repository
	// credentialPolicy evaluates a proposed credential/identifier mutation (design
	// §5.6): the /auth/methods removable hints and the sensitive mutations consult
	// it. New defaults a nil policy to the bundled credential.DefaultPolicy.
	credentialPolicy  credential.Policy
	hasher            Hasher
	passwordValidator func(context.Context, string) error
	// compromised is the optional breach/blocklist checker (design §5.9); nil →
	// no breach check. compromisedFailOpen selects the outcome when it errors.
	compromised         CompromisedPasswordChecker
	compromisedFailOpen bool
	// deliver is the shared kind-aware delivery renderer/router (DeliveryConfig.Deliver): send
	// sites render an envelope through it and enqueue it on queue. Nil until wired.
	deliver *delivery.Router
	// queue is the durable delivery outbox (DeliveryConfig.Queue) send sites enqueue through;
	// identifierKeyer derives its PII-free idempotency keys. Both nil → outbound off.
	queue                deliveryQueue
	identifierKeyer      identifierKeyer
	limiter              ratelimiter.Limiter
	now                  func() time.Time
	logger               *slog.Logger
	requireVerifiedEmail bool
	passwordFlowsEnabled bool
	// ids is the app-chosen entity-ID strategy (WithIDs); zero value → default
	// nanoids. Entity keys only, never secrets.
	ids sdk.IDGenerator
	// securityEvents is the optional append-only audit rail (design §5.1). Nil →
	// the recordSecurityEvent helper is a no-op (ratified AV9).
	securityEvents securityevent.SecurityEventRepository
	// invitations resolves grants for proven email ownership. Nil disables it.
	invitations invitationResolver

	// OAuth flow state (design §3). providers is keyed by Provider.Name();
	// oauthAccounts/oauthStates are the flow's repositories; tokenEncrypter is
	// nil when provider tokens are dropped; redirects guards post-flow
	// destinations. All are zero/empty when the subsystem is off.
	oauthAccounts      oauthaccount.OAuthAccountRepository
	oauthStates        oauthstate.StateRepository
	providers          map[string]oauth.Provider
	tokenEncrypter     cryptids.Encrypter
	callbackBase       string
	nativeRedirectURIs []string
	trustOAuthEmail    func(string, oauth.UserInfo) bool
	redirects          redirect.Allowlist

	// Machine-identity state (design §4.1). Both nil when the subsystem is off.
	serviceAccounts serviceaccount.ServiceAccountRepository
	apiKeys         apikey.APIKeyRepository

	// Access-JWT signer and TTLs (§1.1). tokenSigner is always wired (the public
	// constructor requires it, D3). accessTTL is the access-JWT lifetime;
	// refreshTTL is the fixed refresh/session horizon (rotation never extends it).
	tokenSigner cryptids.JWTSigner
	accessTTL   time.Duration
	refreshTTL  time.Duration

	// passwordless is the resolved set of enabled passwordless kinds (PasswordlessConfig.Passwordless),
	// keyed by kind for O(1) lookups. Empty when passwordless is off; the transport
	// registers the passwordless routes only when PasswordlessEnabled reports true
	// (deny-by-absence, design §4.2).
	passwordless map[string]bool
	// publicBaseURL is the absolute base URL magic links are built from (LinksConfig.PublicAuthBaseURL,
	// design §6.4). Empty unless passwordless is enabled; package auth validates it.
	publicBaseURL string
	// passwordResetURL is the absolute reset landing route the reset mail links to
	// (LinksConfig.PasswordResetURL, CHAU-5.1). Empty → the legacy raw-token template.
	// Validated by package auth at construction; never derived from a request.
	passwordResetURL string
	// oauthLinkBase is the absolute SPA landing URL the OAuth pending-link email
	// links to (LinksConfig.OAuthLinkBaseURL, oauth-pending-link plan D1). Empty → the
	// legacy bare-token line. Validated by package auth at construction; never
	// derived from a request.
	oauthLinkBase string

	// User-administration state (CHAU-1.1). userAdmin is the optional directory +
	// atomic lifecycle repository; userAdminCheck is the host authorization seam.
	// Both nil → the subsystem is off. The repository alone enables the TRUSTED
	// service methods; the bundled admin ROUTES additionally require the check.
	userAdmin      user.AdminRepository
	userAdminCheck UserAdminCheck
	// activeSessions is the optional fenced session-minting capability (CHAU-1.1):
	// it inserts a session only while the owning user is atomically proven active.
	// Nil → mintSession falls back to the ordinary sessions.Create, which cannot
	// fence a concurrent deactivation; package auth refuses to enable the admin
	// routes in that configuration.
	activeSessions session.ActiveUserRepository

	// passwordlessRedeem is the optional atomic magic-link redemption repository
	// and provisionOnRedeem the host switch (CHAU-6.1). Both nil/false → the
	// historical login-only redemption path.
	passwordlessRedeem passwordless.Repository
	provisionOnRedeem  bool
}

// newService assembles already validated dependencies; isolated package tests
// also use it to exercise unavailable-capability behavior.
func newService(d constructorConfig) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	providers := make(map[string]oauth.Provider, len(d.Providers))
	for _, p := range d.Providers {
		providers[p.Name()] = p
	}
	passwordless := make(map[string]bool, len(d.Passwordless))
	for _, k := range d.Passwordless {
		passwordless[k] = true
	}
	accessTTL := d.AccessTokenTTL
	if accessTTL <= 0 {
		accessTTL = defaultAccessTokenTTL
	}
	refreshTTL := d.RefreshTTL
	if refreshTTL <= 0 {
		refreshTTL = defaultRefreshTTL
	}
	normalizer := d.Normalizer
	if normalizer == nil {
		normalizer = identifier.DefaultNormalizer{}
	}
	credentialPolicy := d.CredentialPolicy
	if credentialPolicy == nil {
		credentialPolicy = credential.NewDefaultPolicy(credential.PolicyConfig{})
	}
	return &Service{
		authenticationLimits: d.AuthenticationLimits.withDefaults(),
		users:                d.Users,
		identifiers:          d.Identifiers,
		normalizer:           normalizer,
		passwords:            d.Passwords,
		sessions:             d.Sessions,
		challenges:           d.Challenges,
		protector:            d.Protector,
		passwordResets:       d.PasswordResets,
		contactChanges:       d.ContactChanges,
		credentialMutations:  d.CredentialMutations,
		authGrants:           d.AuthenticationGrants,
		credentialPolicy:     credentialPolicy,
		hasher:               d.Hasher,
		passwordValidator:    d.ValidatePassword,
		compromised:          d.Compromised,
		compromisedFailOpen:  d.CompromisedFailOpen,
		deliver:              d.Deliver,
		queue:                d.Queue,
		identifierKeyer:      d.IdentifierKeyer,
		limiter:              d.Limiter,
		now:                  clock,
		logger:               logger,
		requireVerifiedEmail: d.RequireVerifiedEmail,
		passwordFlowsEnabled: !d.PasswordFlowsDisabled,
		ids:                  d.IDs,
		securityEvents:       d.SecurityEvents,
		invitations:          d.Invitations,

		oauthAccounts:      d.OAuthAccounts,
		oauthStates:        d.OAuthStates,
		providers:          providers,
		tokenEncrypter:     d.TokenEncrypter,
		callbackBase:       d.OAuthCallbackBase,
		nativeRedirectURIs: append([]string(nil), d.OAuthNativeRedirectURIs...),
		trustOAuthEmail:    d.TrustOAuthEmail,
		redirects:          redirect.New(d.RedirectAllowlist),
		serviceAccounts:    d.ServiceAccounts,
		apiKeys:            d.APIKeys,
		tokenSigner:        d.TokenSigner,
		accessTTL:          accessTTL,
		refreshTTL:         refreshTTL,
		passwordless:       passwordless,
		publicBaseURL:      d.PublicAuthBaseURL,
		passwordResetURL:   d.PasswordResetURL,
		oauthLinkBase:      d.OAuthLinkBaseURL,
		userAdmin:          d.UserAdmin,
		userAdminCheck:     d.UserAdminCheck,
		activeSessions:     d.ActiveSessions,
		passwordlessRedeem: d.PasswordlessRedeem,
		provisionOnRedeem:  d.ProvisionOnRedeem,
	}
}

// Register creates an unverified user together with its primary email identifier
// in one atomic operation (CreateWithPrimaryIdentifier, design §2.2), stores the
// password hash, issues a verify_registration challenge code on the atomic secret
// rail (design §3.2), and mails it. The primary identifier is login-, recovery-,
// and notification-enabled but UNVERIFIED while the registration challenge is
// pending (design §2.3): identity lives in user_identifiers from account creation,
// so Verify records the proof time rather than adding the identifier. A mail
// failure still leaves an account the user can later verify (the error is returned
// so the caller knows delivery failed). A duplicate email — a lost authentication
// claim on the identifier value — surfaces as sdk.ErrAlreadyExists from the store.
func (s *Service) Register(ctx context.Context, emailAddr, password, displayName string) (user.User, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return user.User{}, err
	}
	if s.queue == nil || s.deliver == nil || s.challenges == nil || s.protector == nil {
		return user.User{}, ErrDeliveryDisabled
	}
	if err := s.validatePassword(ctx, password); err != nil {
		return user.User{}, err
	}
	now := s.now()
	// The primary identifier is normalized through the single injected policy so a
	// later GetLogin/GetRecovery lookup resolves the same stored value; it also
	// validates the address, failing before any user row is minted.
	ident, err := identifier.NewRegistrationEmail(s.ids, s.normalizer, "", emailAddr, now)
	if err != nil {
		return user.User{}, err
	}
	u := user.NewUser(s.ids, displayName, now)
	hash, err := s.hasher.HashPassword(password)
	if err != nil {
		return user.User{}, fmt.Errorf("hash password: %w", err)
	}

	created, createdIdent, err := s.users.Provision(ctx, u, ident, user.InitialCredentials{PasswordHash: hash})
	if err != nil {
		return user.User{}, err
	}
	primaryEmail := createdIdent.NormalizedValue

	code, issued, err := s.issueChallenge(ctx, created.ID, challenge.PurposeVerifyRegistration)
	if err != nil {
		return user.User{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: created.ID,
		Type:   securityevent.TypeRegister,
		Status: securityevent.StatusSuccess,
	})
	// The account is resolved (just created), so registration verification is not
	// enumeration-sensitive: render the code on the request path and enqueue the
	// sealed message on the durable outbox. The worker delivers it; a provider outage
	// no longer blocks or fails registration beyond the enqueue itself. A failed
	// enqueue is returned (like the prior mail-failure contract) with the created
	// account, which the user can verify with a later resend.
	key := s.idempotencyKey(sdk.AddressKindEmail, primaryEmail, delivery.PurposeRegistrationVerification) + ":" + issued.ID
	if err := s.enqueueRendered(ctx, delivery.PurposeRegistrationVerification, key, delivery.Request{
		Kind:            sdk.AddressKindEmail,
		Purpose:         delivery.PurposeRegistrationVerification,
		Destination:     primaryEmail,
		ResolutionInput: primaryEmail,
		Secret:          code,
	}); err != nil {
		return created, err
	}
	return created, nil
}

// Verify consumes a verify_registration challenge code for the account behind
// emailAddr, then claims and verifies its primary email identifier under the
// atomic revision-CAS ApplyVerifiedChange (design §2.3, §5.9). A wrong code counts
// an attempt and eventually locks out (ErrTooManyAttempts); an expired code is
// ErrChallengeExpired; every other non-match — an unknown account included — is the
// single generic ErrChallengeInvalid, so the response cannot enumerate accounts
// (design §5.8). A post-consume apply conflict is ErrRegistrationVerificationConflict
// (409): the code is spent but no partial state was written, so the caller reissues
// a code and retries (design §5.6).
func (s *Service) Verify(ctx context.Context, emailAddr, code string) error {
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		return ErrChallengeInvalid // uniform: never reveal a malformed/unknown identity
	}
	// Resolve the account through its login-enabled primary identifier — the
	// unverified registration email created atomically at Register (design §2.3).
	ident, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), normalized)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return ErrChallengeInvalid // uniform: no such account is indistinguishable from a wrong code
		}
		return err
	}
	u, err := s.users.Get(ctx, ident.UserID)
	if err != nil {
		return err
	}
	if !u.Active() {
		return ErrChallengeInvalid
	}
	current, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), normalized)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return ErrChallengeInvalid
		}
		return err
	}
	if current.ID != ident.ID || current.UserID != u.ID {
		return ErrChallengeInvalid
	}
	ident = current
	if _, err := s.consumeChallenge(ctx, ident.UserID, challenge.PurposeVerifyRegistration, code); err != nil {
		return err
	}
	now := s.now()
	// Retire the unverified registration identifier and claim its verified
	// replacement in one revision-CAS operation (design §2.2). ReplacesIdentifierID
	// frees the partial authentication-claim index before the verified row claims it.
	input := identifier.ApplyVerifiedChangeInput{
		UserID:               ident.UserID,
		Kind:                 identifier.KindEmail,
		NormalizedValue:      normalized,
		LoginEnabled:         ident.LoginEnabled,
		RecoveryEnabled:      ident.RecoveryEnabled,
		NotificationEnabled:  ident.NotificationEnabled,
		MakePrimary:          ident.IsPrimary,
		ReplacesIdentifierID: ident.ID,
	}
	if _, err := s.identifiers.ApplyVerifiedChange(ctx, input, u.AuthRevision, now); err != nil {
		if errors.Is(err, sdk.ErrConflict) || errors.Is(err, sdk.ErrAlreadyExists) {
			return ErrRegistrationVerificationConflict
		}
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: u.ID,
		Type:   securityevent.TypeEmailVerified,
		Status: securityevent.StatusSuccess,
	})
	// The verified identifier is now the authoritative claim; resolve the invitee's
	// pending auto-accept invitations against its normalized value.
	s.resolvePendingInvitations(ctx, normalized, u.ID)
	return nil
}

// resolvePendingInvitations processes invitations after verified email ownership.
// Resolver errors are logged without failing authentication; durable acceptance
// claims let the invitation service resume an interrupted grant.
func (s *Service) resolvePendingInvitations(ctx context.Context, email, userID string) {
	if s.invitations == nil {
		return
	}
	if _, err := s.invitations.ResolveInvitations(ctx, email, PrincipalUser, userID); err != nil {
		s.logger.Warn("resolve invitations failed", "error_kind", ErrorKind(err))
	}
}

// ActiveVerifiedIdentifier returns the normalized value of userID's active,
// VERIFIED identifier of kind — primary first, then oldest — resolved through the
// v3 identifier rail (design §7). It is the single kind-aware accessor that
// replaced the EmailForUser/VerifiedPhoneForUser proliferation: the invitation
// HTTP handlers key "mine" and the accept-time identifier match on it, and the
// accept-time phone match resolves the caller's verified phone through it, so
// invitation service stays decoupled from the identifier store (the auth pocket owns
// user identity). No active verified identifier of that kind → sdk.ErrNotFound.
func (s *Service) ActiveVerifiedIdentifier(ctx context.Context, userID, kind string) (string, error) {
	addresses, err := s.projectAddresses(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, a := range addresses {
		if a.Kind == kind {
			return a.Value, nil
		}
	}
	return "", fmt.Errorf("no active verified %s identifier for user: %w", kind, sdk.ErrNotFound)
}

// Login rate-limits FIRST on (email, client-IP), then resolves the login-enabled
// email identifier (GetLogin, design §2.2), verifies the password, and mints a
// session, returning the access/refresh TokenPair (the session row holds only the
// refresh token's hash — see mintSession). Password login stays email-only in v3
// (design §4.1); identity is resolved through user_identifiers, not the legacy
// email column. Rate-limit exhaustion returns ErrRateLimited; any credential
// mismatch (unknown identifier, missing password, wrong password) returns the same
// generic sdk.ErrUnauthorized so the response cannot distinguish them.
//
// When PasswordConfig.RequireVerifiedEmail is set, a caller with correct credentials but an
// unverified identifier is refused with ErrEmailNotVerified (403) — the check runs
// AFTER password verification, on the identifier's proof state, so it never leaks a
// verified/unverified signal to an unauthenticated attacker. Default off (design §7.1).
//
// The rate-limit IP is read from the request's client-info carrier (WithClientInfo,
// set by the pocket middleware) — the single source of truth for IP (design §5.1
// WI4); there is no clientIP parameter. Every exit records a security event: a
// rate-limited attempt is `blocked`, a credential/verification denial is
// `failure`, and a minted session is `success`.
func (s *Service) Login(ctx context.Context, emailAddr, password string) (TokenPair, user.User, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return TokenPair{}, user.User{}, err
	}
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		s.recordLogin(ctx, "", emailAddr, securityevent.StatusFailure)
		return TokenPair{}, user.User{}, invalidCredentials()
	}

	u, _, err := s.provePassword(ctx, normalized, password)
	if err != nil {
		status := securityevent.StatusFailure
		if errors.Is(err, ErrRateLimited) {
			status = securityevent.StatusBlocked
		}
		s.recordLogin(ctx, u.ID, normalized, status)
		return TokenPair{}, user.User{}, err
	}

	pair, err := s.mintSession(ctx, u.ID, u.AuthRevision, s.primaryAuthentication(session.MethodPassword))
	if err != nil {
		// A deactivated account is denied as ordinary bad credentials (CHAU-1.5):
		// the admin lifecycle must not become an enumeration oracle.
		if errors.Is(err, session.ErrUserNotActive) {
			s.recordLogin(ctx, u.ID, normalized, securityevent.StatusBlocked)
		}
		return TokenPair{}, user.User{}, genericIfNotActive(err, invalidCredentials())
	}
	s.recordLogin(ctx, u.ID, normalized, securityevent.StatusSuccess)
	return pair, u, nil
}

// recordLogin appends a `login` audit row. The attempted email is an identifier
// (never a secret), so it rides Details to make failed-login auditing useful; a
// resolved userID is attributed when one is known.
func (s *Service) recordLogin(ctx context.Context, userID, email, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Type:    securityevent.TypeLogin,
		Status:  status,
		Details: map[string]any{"email": email},
	})
}

// Logout revokes the session behind the caller's credentials and is idempotent
// (a missing/already-gone session is not an error). It resolves the session id
// through two lanes (§1.5):
//
//   - Primary: the refresh token (browser refresh cookie, or an API body). Hashed
//     and resolved via GetByRefreshHash; the matched row's id is deleted.
//   - Fallback: a verified, unexpired access JWT (API bearer, no refresh token).
//     An expired access token requires the refresh credential to revoke its
//     session. Unverified claims never identify a session for deletion.
//
// A blank refresh token and a blank access token together are a no-op success.
func (s *Service) Logout(ctx context.Context, refreshToken, accessToken string) error {
	sessionID := ""
	if refreshToken != "" {
		hash, err := s.hashSessionToken(refreshToken)
		if err != nil {
			return err
		}
		sess, _, err := s.sessions.GetByRefreshHash(ctx, hash)
		switch {
		case err == nil:
			sessionID = sess.ID
		case !errors.Is(err, sdk.ErrNotFound):
			return err
		}
	}
	if sessionID == "" && accessToken != "" {
		_, id, ok := s.verifyBearerClaims(accessToken)
		if !ok || id == "" {
			return invalidCredentials()
		}
		sessionID = id
	}
	if sessionID != "" {
		if err := s.sessions.Delete(ctx, sessionID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return err
		}
		// Revoking a session invalidates its recent-authentication grants (design
		// §5.0): the DeleteBySession cascade is best-effort defense-in-depth — a live
		// session is already required to consume a grant, so a leftover grant on a
		// deleted session is unusable regardless.
		if s.authGrants != nil {
			if err := s.authGrants.DeleteBySession(ctx, sessionID); err != nil {
				s.logger.Warn("delete session grants failed", "error_kind", ErrorKind(err))
			}
		}
	}
	// The user id is best-effort: a principal stashed on ctx attributes the row.
	uid, _ := s.CurrentUser(ctx)
	details := map[string]any(nil)
	if sessionID != "" {
		details = map[string]any{"session_id": sessionID}
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  uid,
		Type:    securityevent.TypeLogout,
		Status:  securityevent.StatusSuccess,
		Details: details,
	})
	return nil
}

// ChangePassword verifies the current password, then atomically replaces its hash,
// advances auth_revision and revokes sessions, grants and password-reset challenges.
// A credential change after verification returns sdk.ErrConflict. The fresh caller
// session is admitted against the revision returned by the password transaction.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (TokenPair, error) {
	if err := s.requirePasswordFlows(); err != nil {
		return TokenPair{}, err
	}
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
	hash, err := s.passwords.Get(ctx, userID)
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.hasher.VerifyPassword(hash, currentPassword); err != nil {
		return TokenPair{}, fmt.Errorf("current password is incorrect: %w", sdk.ErrUnauthorized)
	}
	if err := s.validatePassword(ctx, newPassword); err != nil {
		return TokenPair{}, err
	}
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return TokenPair{}, fmt.Errorf("hash password: %w", err)
	}
	revision, err := s.passwords.Change(ctx, userID, user.PasswordChange{ExpectedAuthRevision: u.AuthRevision, ExpectedHash: hash, NewHash: newHash, Now: s.now()})
	if err != nil {
		return TokenPair{}, err
	}
	pair, err := s.mintSession(ctx, userID, revision, s.primaryAuthentication(session.MethodPassword))
	if err != nil {
		return TokenPair{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: userID,
		Type:   securityevent.TypePasswordChange,
		Status: securityevent.StatusSuccess,
	})
	return pair, nil
}

// ForgotPassword is the enumeration-safe unauthenticated start (design §4.1/§6.1.1):
// it normalizes the address and enqueues an OPAQUE delivery command carrying only the
// normalized identifier — it never resolves the account, issues a challenge, or calls
// a provider on the request path. The worker (Service.Initialize) later resolves the
// active VERIFIED recovery identifier, issues the password_reset token, renders, and
// delivers; an unknown or unverified address resolves nothing there, so known and
// unknown addresses share one bounded request path with identical repository calls.
// A malformed address returns nil (uniform). Recovery stays email-only in v3.
func (s *Service) ForgotPassword(ctx context.Context, emailAddr string) error {
	if err := s.requirePasswordFlows(); err != nil {
		return err
	}
	normalized, err := s.normalizeEmail(emailAddr)
	if err != nil {
		return nil // never reveal validity/existence
	}
	if err := s.allowAuthenticationRequest(ctx, "password_reset", normalized, s.authenticationLimits.PasswordReset); err != nil {
		return err
	}
	if s.queue == nil {
		return ErrDeliveryDisabled
	}
	key := s.idempotencyKey(sdk.AddressKindEmail, normalized, delivery.PurposePasswordReset)
	_, err = s.queue.Enqueue(ctx, delivery.Command{
		Kind:           sdk.AddressKindEmail,
		Purpose:        delivery.PurposePasswordReset,
		IdempotencyKey: key,
		Envelope:       delivery.Envelope{ResolutionInput: normalized},
	})
	return err
}

// ResetPassword redeems a reset token through the atomic passwordreset
// composition (design §5.9): in one transaction it consumes the live
// password_reset challenge, sets the new password hash, revokes every session,
// and revokes the user's outstanding recent-authentication grants and
// password/reset challenges. It NEVER logs the caller in — no session is minted —
// so the next sensitive action requires fresh step-up. A too-short password
// returns sdk.ErrInvalidInput; an unknown, expired, or already-used token is the
// single generic ErrPasswordResetInvalid (enumeration/anti-probing).
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := s.requirePasswordFlows(); err != nil {
		return err
	}
	if err := s.validatePassword(ctx, newPassword); err != nil {
		return err
	}
	if s.passwordResets == nil || s.protector == nil {
		return fmt.Errorf("password reset subsystem not wired: %w", sdk.ErrForbidden)
	}
	if token == "" {
		return ErrPasswordResetInvalid // an empty token never matches a live challenge
	}
	newHash, err := s.hasher.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	res, err := s.passwordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose:                challenge.PurposePasswordReset,
		TokenDigest:            s.protector.DigestToken(token),
		NewPasswordHash:        newHash,
		PurgeChallengePurposes: passwordResetPurgePurposes,
		Now:                    s.now(),
	})
	if err != nil {
		// A non-live token (unknown/expired/used) is the single generic failure; no
		// state was changed. Anything else is infrastructure.
		if errors.Is(err, sdk.ErrNotFound) || errors.Is(err, sdk.ErrExpired) {
			return ErrPasswordResetInvalid
		}
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: res.UserID,
		Type:   securityevent.TypePasswordReset,
		Status: securityevent.StatusSuccess,
	})
	return nil
}

// ValidateSession returns the live session for sessionID — the Live() tier's
// lookup (§1.4). A blank id returns sdk.ErrUnauthorized; unknown/expired sessions
// surface sdk.ErrNotFound / sdk.ErrExpired from the store (Get is keyed by the
// app-minted id now, so no hashing is involved).
func (s *Service) ValidateSession(ctx context.Context, sessionID string) (session.Session, error) {
	if sessionID == "" {
		return session.Session{}, fmt.Errorf("no session: %w", sdk.ErrUnauthorized)
	}
	return s.sessions.Get(ctx, sessionID)
}

// CurrentUser returns the authenticated user id stashed by RequirePrincipal, if any.
// It is the cross-pocket identity port other pockets consume structurally
// (pockets/README.md §5's CurrentUser).
func (s *Service) CurrentUser(ctx context.Context) (string, bool) {
	p, ok := sdk.PrincipalFromContext(ctx)
	if !ok || p.Type != sdk.PrincipalTypeUser {
		return "", false
	}
	return p.ID, true
}

// mintSession creates a fresh session for userID and returns its access/refresh
// TokenPair (§1.1). It app-mints the session (id + raw refresh token), stores the
// row under the refresh token's SHA-256 HASH (the raw token is never persisted),
// and signs an access JWT carrying {user_id, session_id}. It is the single mint
// path Login, ChangePassword, IssueToken, and the OAuth flows share, so no call
// site persists a raw token or hand-rolls the pair.
//
// auth records how and when the primary authentication that minted the session
// happened (design §5.0): a successful login stamps its method/time/assurance so a
// sufficiently recent login can satisfy a recent-authentication grant without an
// extra step-up prompt. A zero value means none recorded (the shortcut never fires
// for that session).
func (s *Service) mintSession(ctx context.Context, userID string, expectedAuthRevision int64, auth session.AuthenticationMetadata) (TokenPair, error) {
	sess, rawRefresh := session.NewSession(userID, s.refreshTTL, s.now())
	refreshHash, err := s.hashSessionToken(rawRefresh)
	if err != nil {
		return TokenPair{}, err
	}
	sess.RefreshTokenHash = refreshHash
	sess.Authentication = auth
	created, err := s.createSession(ctx, sess, expectedAuthRevision)
	if err != nil {
		return TokenPair{}, err
	}
	access, expiresAt, err := s.signAccessToken(userID, created.ID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access, AccessExpiresAt: expiresAt, RefreshToken: rawRefresh}, nil
}

// createSession never falls back to an unfenced insert. Every credential proof
// carries the user revision read before that proof was resolved.
func (s *Service) createSession(ctx context.Context, sess session.Session, expectedAuthRevision int64) (session.Session, error) {
	if s.activeSessions == nil {
		return session.Session{}, ErrIdentityUnavailable
	}
	return s.activeSessions.CreateForActiveUser(ctx, sess, expectedAuthRevision)
}

// provePassword binds the password and identifier reads to a user revision.
func (s *Service) provePassword(ctx context.Context, normalized, password string) (user.User, identifier.Identifier, error) {
	if err := s.allowAuthenticationRequest(ctx, "login", normalized, s.authenticationLimits.Login); err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
	ident, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), normalized)
	if err != nil {
		return user.User{}, identifier.Identifier{}, invalidCredentials()
	}
	u, err := s.users.Get(ctx, ident.UserID)
	if err != nil {
		return user.User{}, identifier.Identifier{}, invalidCredentials()
	}
	// Re-read after the revision: a retired/reassigned address cannot authorize a
	// login merely because its old row was read before a concurrent mutation.
	current, err := s.identifiers.GetLogin(ctx, string(identifier.KindEmail), normalized)
	if err != nil || current.ID != ident.ID || current.UserID != u.ID {
		return u, identifier.Identifier{}, invalidCredentials()
	}
	hash, err := s.passwords.Get(ctx, u.ID)
	if err != nil {
		return u, current, invalidCredentials()
	}
	if err := s.hasher.VerifyPassword(hash, password); err != nil {
		return u, current, invalidCredentials()
	}
	if s.requireVerifiedEmail && !current.Verified() {
		return u, current, ErrEmailNotVerified
	}
	return u, current, nil
}

// primaryAuthentication builds the session authentication metadata for a primary
// login performed with kind (design §5.0). It stamps the honest method descriptor
// and its assurance as of now so the recent-primary-login shortcut can later judge
// method/age/assurance against an operation's policy. An unknown method kind
// records no descriptor and the zero assurance, so the shortcut never treats an
// unrecognized method as sufficient.
func (s *Service) primaryAuthentication(kind session.MethodKind) session.AuthenticationMetadata {
	meta := session.AuthenticationMetadata{AuthenticatedAt: s.now().UTC()}
	if d, ok := session.DescribeMethod(kind); ok {
		meta.Methods = []session.AuthenticationMethod{d}
		meta.Assurance = d.Assurance
	}
	return meta
}

// signAccessToken signs an access JWT carrying {user_id, session_id} at
// AccessTokenTTL (§1.1). The signer stamps exp (from the returned expiry) and
// iat; this call sets the identity claims. session_id backs the Live() tier and
// the logout fallback.
func (s *Service) signAccessToken(userID, sessionID string) (string, time.Time, error) {
	expiresAt := s.now().Add(s.accessTTL)
	token, err := s.tokenSigner.Sign(map[string]any{
		tokenClaimUserID:    userID,
		tokenClaimSessionID: sessionID,
	}, expiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return token, expiresAt, nil
}

// hashSessionToken returns the stored form of a refresh token — its SHA-256 hex
// digest. This is the ONLY place a refresh token is hashed (design §7.3):
// mintSession's create, Rotate's new-hash, Refresh's resolve, and Logout's
// primary lane all route through it, so the raw token never reaches a repository
// and no second call site can drift.
func (s *Service) hashSessionToken(token string) (string, error) {
	return cryptids.SHA256(token)
}

// normalizeEmail canonicalizes an email address through the single injected
// identifier normalizer (design §2.2) so a login/verify/recovery lookup resolves
// the same stored value registration claimed. It is the one email-normalization
// path the identity-bearing flows share; a rejected value wraps sdk.ErrInvalidInput.
func (s *Service) normalizeEmail(value string) (string, error) {
	return s.normalizer.Normalize(string(identifier.KindEmail), value)
}

// loginKey derives the PII-free rate-limit key for a login attempt: a
// non-reversible identifier digest (design §4.4 — a raw email/phone never enters a
// limiter key) combined with the trusted client IP. The IP comes from the
// client-info carrier, which routes.go resolves from web.TrustProxies or
// RemoteAddr and NEVER from a raw X-Forwarded-For header, so a spoofed forwarding
// header cannot rotate an attacker off a victim's bucket. Equivalent normalized
// values digest to one bucket, and an unknown identifier keys the same shape as a
// known one, so the limiter leaks no existence signal before account resolution.
func (s *Service) loginKey(kind, normalizedValue, clientIP string) string {
	return "login:" + s.identifierDigest(kind, normalizedValue) + "|" + clientIP
}

// invalidCredentials is the single generic error returned for every credential
// mismatch so the response cannot distinguish "no such user" from "wrong
// password".
func invalidCredentials() error {
	return fmt.Errorf("invalid email or password: %w", sdk.ErrUnauthorized)
}

// genericIfNotActive collapses a lifecycle refusal from the fenced session mint
// into the calling flow's OWN generic public failure (CHAU-1.5). A deactivated
// account must be indistinguishable from a wrong password, an unknown address, or
// a stale magic link on every PUBLIC credential endpoint: returning
// session.ErrUserNotActive verbatim would map to a distinctive 403 and turn the
// admin console's deactivation into an account-enumeration oracle.
//
// Any other error — including an infrastructure failure — passes through
// unchanged, so a store outage still surfaces as a 500 rather than being
// misreported as bad credentials.
//
// Operator surfaces are deliberately NOT routed through this: the admin
// directory reports the real status, because the caller there has already been
// authorized to see it.
func genericIfNotActive(err error, generic error) error {
	if errors.Is(err, session.ErrUserNotActive) {
		return generic
	}
	return err
}

// validatePassword applies the host's new-password policy, or the default length
// limits, before the independently configured breach/blocklist check.
func (s *Service) validatePassword(ctx context.Context, pw string) error {
	validator := s.passwordValidator
	if validator == nil {
		validator = validatePasswordLength
	}
	if err := validator(ctx, pw); err != nil {
		return err
	}
	if s.compromised == nil {
		return nil
	}
	bad, err := s.compromised.IsCompromised(ctx, pw)
	if err != nil {
		if s.compromisedFailOpen {
			s.logger.Warn("compromised-password check failed open", "error_kind", ErrorKind(err))
			return nil
		}
		return fmt.Errorf("compromised-password check unavailable: %w", err)
	}
	if bad {
		return ErrPasswordCompromised
	}
	return nil
}

func validatePasswordLength(_ context.Context, pw string) error {
	if len(pw) > maxPasswordInputBytes {
		return fmt.Errorf("password must be at most %d bytes: %w", maxPasswordInputBytes, sdk.ErrInvalidInput)
	}
	n := utf8.RuneCountInString(pw)
	if n < minPasswordCodePoints {
		return fmt.Errorf("password must be at least %d characters: %w", minPasswordCodePoints, sdk.ErrInvalidInput)
	}
	if n > maxPasswordCodePoints {
		return fmt.Errorf("password must be at most %d characters: %w", maxPasswordCodePoints, sdk.ErrInvalidInput)
	}
	return nil
}

// PasswordFlowsEnabled reports whether the password credential is on. The
// inbound layer registers the password/registration/verification routes only
// when it is; every password entry point refuses when it is not.
func (s *Service) PasswordFlowsEnabled() bool { return s.passwordFlowsEnabled }

// requirePasswordFlows is the guard every password entry point runs first.
func (s *Service) requirePasswordFlows() error {
	if !s.passwordFlowsEnabled {
		return ErrPasswordFlowsDisabled
	}
	return nil
}

// SessionLifetime is the fixed session horizon, also used for refresh cookies.
func (s *Service) SessionLifetime() time.Duration { return s.refreshTTL }
