package authentication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// resolveListStrategy validates AdministrationConfig.ListStrategy and maps it to a
// list.Strategy. Empty (the zero value of a literally-built constructorConfig) resolves to
// the cursor default; "cursor"/"offset" pass through; anything else is
// ErrInvalidListStrategy.
func resolveListStrategy(s string) (list.Strategy, error) {
	switch s {
	case "", string(list.StrategyCursor):
		return list.StrategyCursor, nil
	case string(list.StrategyOffset):
		return list.StrategyOffset, nil
	default:
		return "", ErrInvalidListStrategy
	}
}

// validRefreshCookiePath reports whether p is a valid absolute cookie path for
// BrowserConfig.RefreshCookiePath: a leading "/", no query ("?") or fragment ("#")
// marker, no control character, no header-delimiter character (";", ",", space,
// quote), and no trailing slash unless p is the root "/" itself. Only a non-empty
// value is checked; empty means "use the /auth default".
func validRefreshCookiePath(p string) bool {
	if p[0] != '/' {
		return false
	}
	if len(p) > 1 && p[len(p)-1] == '/' {
		return false
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c < 0x20, c == 0x7f:
			return false
		case c == '?', c == '#', c == ';', c == ',', c == ' ', c == '"':
			return false
		}
	}
	return true
}

// New assembles the named authentication, invitation, HTTP and delivery components.
// It validates selected capabilities, applies defaults and starts no worker.
// Hosts optionally mount Components.HTTP and run Components.Delivery.
func New(repos Repositories, signer cryptids.JWTSigner, runtimeMode environment.Mode, deliveryMode delivery.Mode, opts ...Option) (*Components, error) {
	cfg := constructorConfig{}
	cfg.TokenSigner = signer
	cfg.RuntimeMode = runtimeMode
	cfg.DeliveryMode = deliveryMode
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authentication New: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}

	if !cfg.PasswordFlowsDisabled && nilDependency(cfg.Hasher) {
		return nil, ErrHasherRequired
	}
	if cfg.DeliveryMode != delivery.ModeOff && nilDependency(cfg.Mailer) {
		return nil, ErrMailerRequired
	}
	if nilDependency(cfg.TokenSigner) {
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
	if err := validateOAuthConfig(cfg); err != nil {
		return nil, err
	}
	if (repos.ServiceAccounts == nil) != (repos.APIKeys == nil) {
		return nil, ErrMachineReposRequired
	}
	// The machine-route gate is only ever consulted by the bundled lifecycle
	// routes, which mount only when both machine repositories are wired (design
	// §4.1). Setting the gate without them is a policy that can never run — the
	// contradictory-wiring error (the ErrHTMLPolicyWithoutViews posture). The
	// reverse is NOT an error: repositories without a gate is the deny-by-absence
	// posture (no routes, key authentication unaffected) and only WARNs below.
	if cfg.MachineRoutesGate != nil && (repos.ServiceAccounts == nil || repos.APIKeys == nil) {
		return nil, ErrMachineRoutesGateWithoutRepos
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
	// The bundled user-administration routes mount ONLY on an explicit host
	// authorization decision (CHAU-1.1), and only when the wiring can actually
	// honor a deactivation: the administration repository supplies the atomic
	// transition, and the fenced session mint closes the deactivate-versus-login
	// race. A host that sets the check without both would ship a revocation
	// button a concurrent mint could defeat, so this fails LOUDLY.
	//
	// The reverse is NOT an error: wiring the repositories without a check is the
	// normal posture for a bundled store adapter, and simply leaves the routes
	// unmounted while the trusted service methods stay available.
	if cfg.UserAdminCheck != nil && (repos.UserAdmin == nil || repos.ActiveSessions == nil) {
		return nil, ErrUserAdminReposRequired
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
	// defaults to "/auth/login" in authentication service and never trips this. Validated through the
	// same shared redirect.SafeRelativePath the gates and the form lane use.
	if cfg.BrowserLoginPath != "" && redirect.SafeRelativePath(cfg.BrowserLoginPath) != cfg.BrowserLoginPath {
		return nil, ErrBrowserLoginPathInvalid
	}
	// A non-empty refresh-cookie path must be a valid absolute cookie path (evidence
	// §2): net/http sanitizes a cookie attribute by DROPPING invalid bytes, so a
	// malformed value would silently scope the refresh cookie somewhere the host never
	// asked for and cookie-driven refresh would die without a symptom. Empty defaults
	// to "/auth" in authentication service and never trips this.
	if cfg.RefreshCookiePath != "" && !validRefreshCookiePath(cfg.RefreshCookiePath) {
		return nil, ErrRefreshCookiePathInvalid
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
	// acknowledgment posture. in_process additionally builds a pocket-internal bounded
	// delivery queue (the ephemeral runtime, AV3D-4.1), which also seals its payload —
	// so it requires the encrypter even without a wired collaborator.
	deliveryWired := cfg.DeliveryDispatcher != nil
	inProcessDelivery := cfg.DeliveryMode == delivery.ModeInProcess
	if (deliveryWired || inProcessDelivery) && cfg.DeliveryEncrypter == nil {
		return nil, ErrDeliveryEncrypterRequired
	}
	switch cfg.DeliveryMode {
	case delivery.ModeOff:
		// off: no delivery runtime. A wired generic-jobs dispatcher means a configured
		// flow could deliver, so off is contradictory and fails closed. Passwordless — the
		// other deliverable flow — is caught by validatePasswordless below
		// (ErrPasswordlessDeliveryRequired). Other flows (verification, forgot-password,
		// invitations) all route through the same queue, so off uniformly disables them
		// rather than erroring.
		if deliveryWired {
			return nil, ErrDeliveryOffButDeliverable
		}
	case delivery.ModeJobs:
		// jobs: durable delivery on the generic jobs runtime. The queue capability is
		// mandatory — the stdlib-typed DeliveryConfig.DeliveryDispatcher (the composition adapter
		// over generic jobs, AV3D-3.1) is the delivery transport. Production fails closed
		// on an unacknowledged runtime (the queue is the only send path); durability is
		// the generic jobs store's responsibility, asserted by DeliveryJobsAcknowledged.
		if cfg.DeliveryDispatcher == nil {
			return nil, ErrDeliveryQueueRequired
		}
		if cfg.RuntimeMode == environment.ModeProduction && !cfg.DeliveryJobsAcknowledged {
			return nil, ErrDeliveryJobsUnacknowledged
		}
	case delivery.ModeInProcess:
		// in_process: bounded, process-local, EPHEMERAL delivery. It is explicitly
		// non-durable; production requires the explicit crash-loss acknowledgment, since
		// in-flight work is lost on a restart.
		if cfg.RuntimeMode == environment.ModeProduction && !cfg.DeliveryEphemeralAcknowledged {
			return nil, ErrDeliveryEphemeralUnacknowledged
		}
		// Bounded-runtime knob validation (AV3D-4.5): every knob is nil-safe (zero →
		// default), but a NEGATIVE bound (or a status retention smaller than the queue it
		// must cover) fails LOUDLY here with a typed error wrapping sdk.ErrInvalidInput —
		// never silently coerced to a default. The validated configs are reused at the
		// queue/runtime construction sites below.
		if err := cfg.InProcessDelivery.Validate(); err != nil {
			return nil, err
		}
	}
	listStrategy, err := resolveListStrategy(cfg.ListStrategy)
	if err != nil {
		return nil, err
	}
	bodySenders, err := delivery.CopyBodySenders(cfg.BodySenders)
	if err != nil {
		return nil, err
	}
	// Fail closed on delivery transport security (design §6.3): in production a
	// development-only or metadata-less Mailer/BodySender is rejected; in
	// development a development-only transport warns. The Mailer is validated non-
	// nil above.
	transportLog := cfg.Logger
	if transportLog == nil {
		transportLog = slog.Default()
	}
	if cfg.Mailer != nil {
		if err := validateDeliveryTransports(cfg.RuntimeMode, cfg.Mailer, bodySenders, transportLog); err != nil {
			return nil, err
		}
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
	if cfg.RuntimeMode == environment.ModeProduction && cfg.IdentifierKeyer == nil {
		return nil, ErrIdentifierKeyerRequired
	}

	// The shared delivery renderer/router (design §6.1): one kind-aware policy
	// consumed by BOTH authentication service and invitation service so the outbound email/SMS content
	// and the email/notify kind fork have a single definition instead of two
	// drifting copies. It renders an encrypted-job-ready Envelope; the durable
	// worker (phase 4) sends it. The Mailer is validated non-nil above, so the
	// router is always buildable here.
	var deliveryRouter *delivery.Router
	if cfg.Mailer != nil {
		deliveryRouter, err = delivery.NewRouter(cfg.Mailer,
			delivery.WithTemplates(delivery.TemplatesConfig{AppTemplates: cfg.EmailContentTemplates, AppLayouts: cfg.EmailLayouts, Branding: cfg.EmailBranding, Subjects: cfg.EmailSubjects, SMSBodies: cfg.SMSBodies}),
			delivery.WithMailFrom(cfg.MailFrom),
			delivery.WithBodySenders(bodySenders),
			delivery.WithDataHook(cfg.DeliveryData),
			delivery.WithRouterLogger(cfg.Logger))
		if err != nil {
			return nil, err
		}
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
	case cfg.DeliveryMode == delivery.ModeInProcess:
		// in_process mode (AV3D-4.1): build the pocket-internal bounded admission queue.
		// It is the ephemeral runtime's Dispatcher — a fixed worker pool drains it, built
		// after authService below and run by the host via Runtime.Run. Accepted work is
		// process-local and does NOT survive a restart.
		inProcessQueue, err = delivery.NewInProcessQueue(
			delivery.WithQueueAdmission(delivery.QueueAdmissionConfig{Capacity: cfg.InProcessDelivery.QueueCapacity, AdmissionDeadline: cfg.InProcessDelivery.AdmissionDeadline}),
			delivery.WithQueueRetention(delivery.QueueRetentionConfig{StatusMaxEntries: cfg.InProcessDelivery.StatusMaxEntries, StatusTTL: cfg.InProcessDelivery.StatusTTL}),
		)
		if err != nil {
			return nil, err
		}
		deliveryQueue, err = delivery.NewService(inProcessQueue, cfg.DeliveryEncrypter)
		if err != nil {
			return nil, err
		}
	case cfg.DeliveryMode == delivery.ModeJobs && cfg.DeliveryDispatcher != nil:
		// jobs mode over generic jobs (AV3D-3.1): submit sealed command envelopes through
		// the host-composed dispatcher; the jobs-mode processor opens exactly what the
		// service seals.
		deliveryQueue, err = delivery.NewService(cfg.DeliveryDispatcher, cfg.DeliveryEncrypter)
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

	// Provision-on-consumption turns a magic link into an account-CREATION
	// credential (CHAU-6.1). Every dependency that makes that safe is required, and
	// a half-wired configuration fails here rather than creating accounts through a
	// path that cannot guarantee atomicity.
	//
	// The passwordless-kind and public-URL checks below run after this, so the
	// error a host sees names the provisioning wiring specifically rather than a
	// generic passwordless gap.
	if cfg.PasswordlessProvisionOnRedeem {
		emailEnabled := false
		for _, k := range cfg.Passwordless {
			if k == sdk.AddressKindEmail {
				emailEnabled = true
			}
		}
		switch {
		case !emailEnabled,
			repos.Passwordless == nil,
			repos.ActiveSessions == nil,
			repos.Challenges == nil,
			cfg.ChallengeProtector == nil,
			cfg.IdentifierKeyer == nil,
			!(deliveryWired || inProcessDelivery),
			cfg.PublicAuthBaseURL == "":
			return nil, ErrPasswordlessProvisionWiring
		}
	}

	// The password-reset landing URL (CHAU-5.1). It is validated whenever the
	// challenge-backed forgot/reset rail is actually wired — a host with no reset
	// rail is never asked for it.
	//
	// A non-empty value is validated in every mode (shape errors are shape errors);
	// an EMPTY value is a production error and a development WARN, which is the
	// ratified compatibility posture: a raw-token-only reset mail is not an
	// acceptable production experience, but forcing it on a local console flow
	// mid-migration would be gratuitous.
	if repos.PasswordResets != nil && cfg.ChallengeProtector != nil {
		switch {
		case cfg.PasswordResetURL != "":
			if err := validatePasswordResetURL(cfg.RuntimeMode, cfg.PasswordResetURL); err != nil {
				return nil, err
			}
		case cfg.RuntimeMode == environment.ModeProduction:
			return nil, ErrPasswordResetURLRequired
		default:
			transportLog.Warn("auth: LinksConfig.PasswordResetURL is unset; password-reset mail will print the RAW TOKEN instead of a clickable link. Production requires this field — set it before deploying")
		}
	}

	// The OAuth pending-link landing URL (oauth-pending-link plan D1/D5). It is
	// relevant only when OAuth providers are wired — a host with no providers never
	// takes the pending-link branch, so it is never asked for it.
	//
	// A non-empty value is validated in EVERY mode (shape errors are shape errors);
	// an EMPTY value degrades to the historical bare-token email line with ONE
	// startup WARN in every mode. Unlike the reset URL this is never a production
	// boot requirement: it changes presentation, not the anti-takeover guarantee.
	if len(cfg.Providers) > 0 {
		switch {
		case cfg.OAuthLinkBaseURL != "":
			if err := validateOAuthLinkBaseURL(cfg.RuntimeMode, cfg.OAuthLinkBaseURL); err != nil {
				return nil, err
			}
		default:
			transportLog.Warn("auth: LinksConfig.OAuthLinkBaseURL is unset; the OAuth pending-link email will print the RAW TOKEN instead of a clickable link. Set AUTH_OAUTH_LINK_URL to the SPA route that reads the fragment token and POSTs verify-link")
		}
	}

	// The machine subsystem wired without an authorization gate is a SILENT posture
	// change (D1): key authentication keeps working, but the bundled lifecycle
	// routes are not mounted and answer 404. An upgrading host learns that at boot
	// rather than from production 404s. The reverse wiring is the loud error above.
	if repos.ServiceAccounts != nil && repos.APIKeys != nil && cfg.MachineRoutesGate == nil {
		transportLog.Warn("auth: machine repositories are wired but AdministrationConfig.MachineRoutesGate is unset; the bundled lifecycle routes are NOT mounted (404) — set a gate or serve your own routes over the Service methods")
	}

	if err := validateRepositories(repos, cfg); err != nil {
		return nil, err
	}
	cfg = snapshotConfig(cfg)

	// authService is declared here and assigned below (authlogic.New), so the
	// invitation service's accept-time identifier accessor can bind to it: the two
	// services reference each other (authentication service holds invitation service for resolve-on-
	// verified-address resolution; invitation service holds authentication service's ActiveVerifiedIdentifier for the
	// V11 phone accept-time match), a construction cycle broken by this late-bound
	// closure — it is only invoked at request time, long after both are wired.
	var authService *authlogic.Service

	// The invitation service is built only when a Granter is wired (deny-by-
	// absence). Its Granter is injected HERE, never into authentication service (design §6 pin).
	// The single injected identifier normalizer (design §2.2), nil-defaulted to the
	// bundled strict policy, shared by the direct-add userLookup below and both
	// service Deps so registration, login, recovery, and invitations canonicalize
	// identically.
	idNormalizer := identifier.Normalizer(identifier.DefaultNormalizer{})
	if cfg.IdentifierNormalizer != nil {
		idNormalizer = cfg.IdentifierNormalizer
	}

	var invSvc *invitations.Service
	if cfg.Granter != nil {
		invDelivery := invitations.DeliveryConfig{Mailer: cfg.Mailer, MailFrom: cfg.MailFrom, Deliver: deliveryRouter, BodySenders: bodySenders}
		// Preserve a nil interface when delivery is disabled.
		if deliveryQueue != nil {
			invDelivery.Queue = deliveryQueue
		}
		invSvc, err = invitations.New(repos.Invitations, cfg.Granter,
			invitations.WithAccess(invitations.AccessConfig{
				MemberCheck: cfg.MemberCheck,
				UserLookup:  userLookup(repos.Identifiers, idNormalizer),
				InviteCheck: cfg.InviteCheck,
				CallerIdentifiers: func(ctx context.Context, userID, kind string) (string, error) {
					return authService.ActiveVerifiedIdentifier(ctx, userID, kind)
				},
			}),
			invitations.WithDelivery(invDelivery),
			invitations.WithNormalizer(cfg.IdentifierNormalizer),
			invitations.WithRedirectAllowlist(cfg.RedirectAllowlist),
			invitations.WithSecurityEvents(repos.SecurityEvents),
			invitations.WithLogger(cfg.Logger),
			invitations.WithIDs(cfg.IDs),
		)
		if err != nil {
			return nil, err
		}
	}

	logicDelivery := authlogic.DeliveryConfig{Deliver: deliveryRouter}
	if deliveryQueue != nil {
		logicDelivery.Queue = deliveryQueue
	}
	logicOptions := []authlogic.Option{
		authlogic.WithPassword(authlogic.PasswordConfig{Hasher: cfg.Hasher, ValidatePassword: cfg.ValidatePassword, Compromised: cfg.CompromisedPasswordChecker, CompromisedFailOpen: cfg.CompromisedPasswordFailOpen, PasswordFlowsDisabled: cfg.PasswordFlowsDisabled, RequireVerifiedEmail: cfg.RequireVerifiedEmail}),
		authlogic.WithSessions(authlogic.SessionsConfig{AccessTokenTTL: cfg.AccessTokenTTL, RefreshTTL: cfg.RefreshTTL}),
		authlogic.WithIdentity(authlogic.IdentityConfig{Normalizer: cfg.IdentifierNormalizer, IdentifierKeyer: cfg.IdentifierKeyer, CredentialPolicy: cfg.CredentialPolicy}),
		authlogic.WithDelivery(logicDelivery),
		authlogic.WithOAuth(authlogic.OAuthConfig{Providers: cfg.Providers, TokenEncrypter: cfg.TokenEncrypter, OAuthCallbackBase: cfg.OAuthCallbackBase, OAuthNativeRedirectURIs: cfg.OAuthNativeRedirectURIs, TrustOAuthEmail: cfg.TrustOAuthEmail}),
		authlogic.WithPasswordless(authlogic.PasswordlessConfig{Passwordless: cfg.Passwordless, ProvisionOnRedeem: cfg.PasswordlessProvisionOnRedeem}),
		authlogic.WithLinks(authlogic.LinksConfig{PublicAuthBaseURL: cfg.PublicAuthBaseURL, PasswordResetURL: cfg.PasswordResetURL, OAuthLinkBaseURL: cfg.OAuthLinkBaseURL, RedirectAllowlist: cfg.RedirectAllowlist}),
		authlogic.WithChallengeProtector(cfg.ChallengeProtector),
		authlogic.WithUserAdminCheck(cfg.UserAdminCheck),
		authlogic.WithLimits(cfg.AuthenticationLimits),
		authlogic.WithLogger(cfg.Logger),
		authlogic.WithIDs(cfg.IDs),
	}
	if invSvc != nil {
		logicOptions = append(logicOptions, authlogic.WithInvitations(invSvc))
	}
	authComponents, err := authlogic.New(authlogic.Repositories{
		Users: repos.Users, Identifiers: repos.Identifiers, Passwords: repos.Passwords, Sessions: repos.Sessions,
		UserAdmin: repos.UserAdmin, PasswordlessRedeem: repos.Passwordless, ActiveSessions: repos.ActiveSessions,
		Challenges: repos.Challenges, PasswordResets: repos.PasswordResets, ContactChanges: repos.ContactChanges,
		CredentialMutations: repos.CredentialMutations, AuthenticationGrants: repos.AuthenticationGrants,
		SecurityEvents: repos.SecurityEvents, OAuthAccounts: repos.OAuthAccounts, OAuthStates: repos.OAuthStates,
		ServiceAccounts: repos.ServiceAccounts, APIKeys: repos.APIKeys,
	}, cfg.TokenSigner, cfg.RuntimeMode, limiter, logicOptions...)
	if err != nil {
		return nil, err
	}
	authService = authComponents.Service

	// The outbound delivery executor is built AFTER authService so its Initializer —
	// the separate initializer capability, which resolves accounts and issues challenges for opaque
	// start jobs — is fully attached before any handler can run. The pocket starts no
	// goroutine at construction; the host runs the selected runtime.
	//
	//   - jobs mode over generic jobs (AV3D-3.1): build the transport-neutral
	//     command.Engine processor and expose it through DeliveryJobRuntime(); the host
	//     registers it on the generic jobs runtime.
	//   - in_process mode (AV3D-4.1): build the same processor behind a bounded pool the
	//     host runs via Runtime.Run.
	var jobsProcessor *delivery.JobsProcessor
	var inProcessRuntime *delivery.InProcessRuntime
	processorOptions := []delivery.ProcessorOption{delivery.WithProcessorInitializer(authComponents.DeliveryInitializer)}
	if cfg.DeliveryEventsEmitter != nil {
		processorOptions = append(processorOptions, delivery.WithProcessorObserver(delivery.NewEventObserver(cfg.DeliveryEventsEmitter, cfg.Logger)))
	}
	switch {
	case cfg.DeliveryMode == delivery.ModeInProcess:
		processor, err := delivery.NewJobsProcessor(cfg.DeliveryEncrypter, deliveryRouter, processorOptions...)
		if err != nil {
			return nil, err
		}
		inProcessRuntime, err = delivery.NewInProcessRuntime(inProcessQueue, processor,
			delivery.WithWorkerCount(cfg.InProcessDelivery.Workers),
			delivery.WithShutdownDeadline(cfg.InProcessDelivery.ShutdownDeadline),
			delivery.WithRetryPolicy(delivery.RetryPolicyConfig{MaxAttempts: cfg.InProcessDelivery.MaxAttempts}),
			delivery.WithRuntimeLogger(cfg.Logger),
		)
		if err != nil {
			return nil, err
		}
	case cfg.DeliveryMode == delivery.ModeJobs && cfg.DeliveryDispatcher != nil:
		jobsProcessor, err = delivery.NewJobsProcessor(cfg.DeliveryEncrypter, deliveryRouter, processorOptions...)
		if err != nil {
			return nil, err
		}
	}

	deliveryRuntime, err := delivery.NewRuntime(jobsProcessor, inProcessRuntime, inProcessQueue, deliveryQueue)
	if err != nil {
		return nil, err
	}
	adapter, err := inbound.New(authService,
		cfg.RuntimeMode,
		inbound.WithBrowser(inbound.BrowserConfig{RefreshCookiePath: cfg.RefreshCookiePath, AllowedOrigins: cfg.AllowedOrigins, Views: cfg.Views, HTMLPolicy: cfg.HTMLPolicy}),
		inbound.WithInvitations(invSvc),
		inbound.WithAuthenticatorPolicy(inbound.AuthenticatorPolicy{Cookie: cfg.SessionCookie, BrowserLoginPath: cfg.BrowserLoginPath, Limiter: limiter, Logger: cfg.Logger}),
		inbound.WithListStrategy(listStrategy),
		inbound.WithMachineGate(cfg.MachineRoutesGate),
		inbound.WithRouteAuthentication(cfg.BundledRouteAuth))
	if err != nil {
		return nil, err
	}
	return &Components{Authentication: authService, Invitations: invSvc, HTTP: adapter, Delivery: deliveryRuntime}, nil

}

// userLookup builds the internal email→subject resolver invitation service uses for
// the direct-add path, backed by the identifier discovery rail (design §2.2/§7).
// It normalizes through the single injected policy and resolves the owning
// subject through an active login- then recovery-enabled email identifier. It is
// wired here (package auth has the repos) so invitation service stays decoupled from
// the identifier store; an invalid or unknown email resolves to no user
// (found=false), never an error.
func userLookup(idents identifier.IdentifierRepository, norm identifier.Normalizer) invitations.UserLookup {
	kind := string(identifier.KindEmail)
	return func(ctx context.Context, emailAddr string) (string, bool, error) {
		normalized, err := norm.Normalize(kind, emailAddr)
		if err != nil {
			return "", false, nil
		}
		ident, err := idents.GetLogin(ctx, kind, normalized)
		if err == nil && ident.Verified() {
			return ident.UserID, true, nil
		}
		if err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return "", false, err
		}
		ident, err = idents.GetRecovery(ctx, kind, normalized)
		if err != nil {
			if errors.Is(err, sdk.ErrNotFound) {
				return "", false, nil
			}
			return "", false, err
		}
		if !ident.Verified() {
			return "", false, nil
		}
		return ident.UserID, true, nil
	}
}
