package authentication

import (
	"errors"
)

// ErrHasherRequired and ErrMailerRequired are returned by New
// when the corresponding required constructorConfig field is nil. Unlike cms's safe
// silent defaults (nil Cache disables caching), a password pocket with no
// hasher, or one that silently drops verification/reset mail, is a security
// foot-gun — so these degrade loudly at construction, never silently.
var (
	ErrHasherRequired = errors.New("auth: PasswordConfig.Hasher is required")
	ErrMailerRequired = errors.New("auth: DeliveryConfig.Mailer is required")
	// ErrTokenSignerRequired is returned by New when
	// signer is nil. The access credential is a signed JWT (D3), so a
	// signer is REQUIRED — the core never synthesizes an ephemeral key (that
	// convenience lives in example hosts only). It degrades loudly at construction,
	// mirroring ErrHasherRequired.
	ErrTokenSignerRequired = errors.New("auth: signer is required")
)

// ErrOAuthReposRequired is returned by New when OAuthConfig.Providers
// is non-empty but Repositories.OAuthAccounts or Repositories.OAuthStates is nil.
// OAuth is deny-by-absence — no providers means no routes and the oauth repos may
// be nil — but wiring providers without their stores is a loud partial-wiring
// error (design §3, the Hasher/Mailer precedent), never a silent half-on state.
var ErrOAuthReposRequired = errors.New("auth: OAuthConfig.Providers set but Repositories.OAuthAccounts/OAuthStates is nil")

// ErrMachineReposRequired is returned by New when exactly one of
// Repositories.ServiceAccounts and Repositories.APIKeys is wired. The machine
// identity subsystem (API keys + service accounts, design §4.1) is both-or-
// neither: both nil → subsystem off (routes not registered, the bearer API-key
// path inert); both set → on; one without the other is a loud construction error
// (cut refinement 5), never a silent half-on state.
var ErrMachineReposRequired = errors.New("auth: Repositories.ServiceAccounts and Repositories.APIKeys must be wired together (both or neither)")

// ErrInvitationRepoRequired is returned by New when InvitationsConfig.Granter
// is wired but Repositories.Invitations is nil. Invitations are deny-by-absence —
// no Granter means no routes and Invitations may be nil — but wiring a Granter
// without its store is a loud partial-wiring error (design §6), never a silent
// half-on state.
var ErrInvitationRepoRequired = errors.New("auth: InvitationsConfig.Granter set but Repositories.Invitations is nil")

// ErrUserAdminReposRequired is returned by New when
// AdministrationConfig.UserAdminCheck enables the bundled user-administration routes but the
// repositories that make them safe are not both wired (CHAU-1.1).
//
// BOTH are required, and the second is the load-bearing one:
//
//   - Repositories.UserAdmin supplies the directory and the atomic transition; and
//   - Repositories.ActiveSessions fences session minting against a concurrent
//     deactivation.
//
// Without ActiveSessions a host would expose a "deactivate" button while a login
// racing it could still mint a session — advertising a revocation the wiring
// cannot honor. That fails LOUDLY at construction rather than shipping a
// half-closed door. Checked with errors.Is.
var ErrUserAdminReposRequired = errors.New("auth: AdministrationConfig.UserAdminCheck set but Repositories.UserAdmin/ActiveSessions is nil (the admin lifecycle routes require both the administration repository and the fenced session mint)")

// ErrPasswordlessProvisionWiring is returned by New when
// PasswordlessConfig.PasswordlessProvisionOnRedeem is enabled but the wiring that makes
// provision-on-consumption safe is incomplete (CHAU-6.1).
//
// Provisioning turns a magic link into an account-CREATION credential, so every
// piece it depends on is required, not best-effort: the email passwordless kind
// (the only rail it applies to), the atomic challenge rail and its protector (the
// link's single-use secret), an IdentifierKeyer (the stable PII-free subject key
// an unknown address is keyed under — without one, a resend to an unknown address
// could not replace its predecessor), a delivery runtime, a valid
// PublicAuthBaseURL, Repositories.Passwordless (the ONE-transaction redemption),
// and Repositories.ActiveSessions (the lifecycle fence every mint rides).
//
// Half-wired provisioning would create accounts through a path that cannot
// guarantee atomicity, so it fails at construction. Checked with errors.Is.
var ErrPasswordlessProvisionWiring = errors.New("auth: PasswordlessConfig.PasswordlessProvisionOnRedeem requires the email passwordless kind, the atomic challenge rail, a challenge protector, an identifier keyer, a delivery runtime, a valid PublicAuthBaseURL, Repositories.Passwordless, and Repositories.ActiveSessions")

// ErrInviteCheckRequired is returned by New when InvitationsConfig.Granter
// enables invitations but InvitationsConfig.InviteCheck is nil (design §6/D3). The relation-
// aware host policy is REQUIRED with invitations — a nil check is never an
// allow-by-default or a silently unprotected create/list route — so it degrades
// LOUDLY at construction alongside ErrInvitationRepoRequired, mirroring the
// Hasher/Mailer required posture.
var ErrInviteCheckRequired = errors.New("auth: InvitationsConfig.Granter set but InvitationsConfig.InviteCheck is nil")

// ErrInviteCheckWithoutGranter is returned by New when
// InvitationsConfig.InviteCheck is wired but InvitationsConfig.Granter is nil (invitations off). A
// policy for a subsystem that will never run is a contradictory wiring that gives
// false confidence, so — matching the ErrDeliveryOffButDeliverable / partial-
// wiring posture — it fails LOUDLY rather than silently ignoring the dead check.
var ErrInviteCheckWithoutGranter = errors.New("auth: InvitationsConfig.InviteCheck set but InvitationsConfig.Granter is nil (invitations off)")

// ErrInvalidListStrategy is returned by New when
// AdministrationConfig.ListStrategy is set to a value other than "cursor" or "offset". Like
// the Hasher/Mailer requirements it degrades loudly at construction (the constructorConfig
// posture), never silently defaulting a typo.
var ErrInvalidListStrategy = errors.New(`auth: AdministrationConfig.ListStrategy must be "cursor" or "offset"`)

// ErrHTMLPolicyWithoutViews is returned by New when BrowserConfig.HTMLPolicy
// is set while BrowserConfig.Views is nil (design §9.2, GOTH-0.4). The HTML surface is gated
// entirely on Views: with a nil Views no HTML page renders, so a resource policy that
// can never be consulted is a contradictory wiring giving false confidence. Matching
// the ErrInviteCheckWithoutGranter / partial-wiring posture, it degrades LOUDLY at
// construction rather than silently ignoring the dead policy.
var ErrHTMLPolicyWithoutViews = errors.New("auth: BrowserConfig.HTMLPolicy set but BrowserConfig.Views is nil (no HTML surface)")

// ErrMachineRoutesGateWithoutRepos is returned by New when
// AdministrationConfig.MachineRoutesGate is set while the machine subsystem is unwired (BOTH
// Repositories.ServiceAccounts and APIKeys nil — a half-wired pair fails earlier
// with ErrMachineReposRequired). The bundled lifecycle routes
// mount only when the machine subsystem is wired, so a gate without the
// repositories is an authorization policy that can never be consulted — the
// contradictory-wiring error (the ErrHTMLPolicyWithoutViews posture), so a dead
// gate never gives false confidence that machine identities are protected.
var ErrMachineRoutesGateWithoutRepos = errors.New("auth: AdministrationConfig.MachineRoutesGate set but Repositories.ServiceAccounts/APIKeys are nil (no machine subsystem)")

// ErrBrowserLoginPathInvalid is returned by New when a non-empty
// BrowserConfig.BrowserLoginPath is not a safe root-relative path (design §9.2): it must
// start with a single "/", carry no protocol-relative "//" prefix, no scheme, no
// backslash, and no control character, so the browser identity gates can never be
// configured into an off-site open redirect. An empty value defaults to "/auth/login"
// and never trips this. Checked at construction, the loud-constructorConfig posture.
var ErrBrowserLoginPathInvalid = errors.New("auth: BrowserConfig.BrowserLoginPath must be a safe root-relative path (leading /, no //, scheme, backslash, or control character)")

// ErrRefreshCookiePathInvalid is returned by New when a non-empty
// BrowserConfig.RefreshCookiePath is not a valid absolute cookie path: it must start with
// "/", carry no query ("?") or fragment ("#") marker, no control character and no
// header-delimiter character (";", ",", space, quote), and no trailing slash unless
// it is the root "/" itself. An empty value defaults to "/auth" and never trips
// this. net/http silently DROPS invalid bytes when it sanitizes a cookie
// attribute, so a malformed value would scope the refresh cookie somewhere the
// host never asked for — it fails LOUDLY at construction instead (the loud-constructorConfig
// posture).
var ErrRefreshCookiePathInvalid = errors.New(`auth: BrowserConfig.RefreshCookiePath must be an absolute cookie path (leading /, no query/fragment/control/delimiter character, no trailing slash except "/")`)
