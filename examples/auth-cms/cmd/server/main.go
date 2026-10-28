// Command server is the auth-v2 A9 / events-v1 proof host: it mounts
// pockets/cms, pockets/authentication, AND pockets/events onto one host
// router, with in-memory stores and no datastore driver in its module graph
// (verify: `GOWORK=off go list -m all | grep -i libsql` is empty). The host is
// the only party that imports the pockets — no pocket imports another
// (constitution rule 6); the cross-pocket flow rides sdk vocabulary
// (sdk/pkg/web.Middleware, sdk, sdk/capabilities/events) the host wires between them.
//
// The cross-pocket wiring is the point: cms's admin surface (the CRUD routes)
// is gated by auth's identity middleware via cms.Config.AdminMiddleware ←
// authSvc.HTTP.RequireAccessToken(). Neither pocket imports the other; structural typing on
// sdk/pkg/web.Middleware and the auth Service is what lets the host connect them.
// Public cms routes (the home page, published singles) stay ungated.
//
// On top of v1, this host exercises the whole auth-v2 surface for the A9 proof
// protocol (see README): the verified-email login gate (RequireVerifiedEmail),
// a host-local fake OAuth provider (oauthfake.go), machine identity (API keys +
// service accounts), access JWTs + rotating refresh tokens signed host-side by
// the golang-jwt HS256 integration, security-event audit
// rows surfaced through a
// DEFAULT-OFF debug route, and invitations that grant through the authorization
// engine's integrityRelationshipGranter (membership.go): invitation acceptance
// writes a real ReBAC tuple and atomically records its operation ID so a retry
// cannot restore revoked access. The A9 toy membership map is retired; the
// memstore-backed engine keeps the host zero-infra (no libsql). The host-local
// demo routes (demo.go) are gated variously on a resolved principal, an engine
// Check, a LookupAllResourceIDs enumeration, and a roles-kind HasRole check.
//
// pockets/events adds the SSE gateway at GET /events (authenticated via
// authSvc.HTTP.RequireAccessToken() on StreamMiddleware): a cms edit fans out as a
// content.updated frame to any open stream. Two rails prove out here. The
// DEFAULT variant is direct-emit/best-effort — cms emits straight onto the bus
// (SSE id: = CorrelationID). The DURABLE variant (EVENTS_OUTBOX=memory) routes a
// host-owned POST /outbox-demo append through an example-local in-memory outbox
// (pockets/events/outbound/outboxmem) and a host-driven outbox.Poller on an sdk/pkg/workers pool:
// outbox -> poll -> emit -> SSE, id: = the durable outbox EventID. The shutdown
// order is HTTP server -> delivery runtime -> terminal-purge scheduler -> inquiry purge
// scheduler -> poller pool -> bus.Close (see run's tail comment); HTTP and the delivery
// runtime are supervised as one lifecycle, so an unexpected delivery-runtime exit drives
// the same drain.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	access "github.com/gopernicus/gopernicus/examples/auth-cms/pockets/access/inbound"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"

	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/inbound/authpages"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/inbound/http/oauth2demo"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/deliveryhealth"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/authentication/outbound/oauth2fixture"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/cms/outbound/memstore"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/events/outbound/outboxmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/retention/inbound/runner"
	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	gowebauthn "github.com/gopernicus/gopernicus/integrations/cryptids/go-webauthn"
	rscqr "github.com/gopernicus/gopernicus/integrations/encoding/rsc-qr"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	authgoth "github.com/gopernicus/gopernicus/pockets/authentication/views/goth"
	"github.com/gopernicus/gopernicus/pockets/cms"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/menus"
	cmsgoth "github.com/gopernicus/gopernicus/pockets/cms/views/goth"
	eventspocket "github.com/gopernicus/gopernicus/pockets/events"
	eventshttp "github.com/gopernicus/gopernicus/pockets/events/inbound/http"
	outbox "github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	eventstreams "github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	"github.com/gopernicus/gopernicus/pockets/jobs"
	queue "github.com/gopernicus/gopernicus/pockets/jobs/logic/queue"
	jobsmem "github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/logging"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
	uigoth "github.com/gopernicus/gopernicus/ui/goth"
	uigothassets "github.com/gopernicus/gopernicus/ui/goth/assets"
)

// authAssetBasePath is the public URL prefix this host serves the ui/goth
// fingerprinted assets under; newAuthBundle pins it so buildAuthConfig's mapped CSP
// and the asset route agree (ui-goth GOTH-7.2).
const authAssetBasePath = "/assets/goth"

// defaultPort is this host's own default bind port, pre-seeded into the
// web.ServerConfig literal before ParseEnvTags so PORT= (or an unset PORT)
// keeps 8082 instead of falling back to the sdk tag default.
const defaultPort = "8082"

// newAuthBundle constructs the immutable ui/goth presentation bundle the auth pages
// render through. StylesOnly is the smallest safe profile for the native auth forms;
// the externalized fragment-reader script is served separately and covered by the
// adapter's script-src 'self'.
func newAuthBundle() (*uigoth.Bundle, error) {
	return uigoth.New(uigoth.WithAssetBasePath(authAssetBasePath))
}

func main() {
	// The runtime mode comes from the process environment only; .env is loaded in
	// development and never opened in production. A missing .env is allowed;
	// malformed configuration must stop startup.
	runtimeMode, err := environment.LoadEnvForMode("AUTH_RUNTIME_MODE")
	if err != nil {
		slog.Error("load environment", "error", err)
		os.Exit(1)
	}

	logOpts := logging.Options{Format: "text"}
	if err := environment.ParseEnvTags("", &logOpts); err != nil {
		slog.Error("configure logging", "error", err)
		os.Exit(1)
	}
	log := logging.New(logOpts)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log, runtimeMode); err != nil {
		log.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, runtimeMode environment.Mode) error {
	if err := refuseDebugRouteOutsideDevelopment(runtimeMode); err != nil {
		return err
	}
	demoAdmin, err := demoAdminEmail(runtimeMode, log)
	if err != nil {
		return err
	}
	// The HTTP server config, read the same way. It is resolved here rather than at
	// the web.Run call because TrustProxies (below) and every host-derived URL take
	// their values from it.
	srv := web.ServerConfig{Port: defaultPort}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return err
	}
	if runtimeMode.IsProduction() {
		if err := srv.ValidateProduction(); err != nil {
			return err
		}
	}

	// Both stores are in-memory: no driver, no migrations, no datastore module.
	cmsStore := memstore.New()
	cmsRepos := cmsStore.Repositories()
	if err := seed(ctx, cmsRepos); err != nil {
		return err
	}
	// authmem now fills all twelve auth ports (v1 + the v2 ports the A9 protocol
	// drives). The Store is kept so the debug route can read the audit rail.
	authStore := authmem.New()
	authRepos := authStore.Repositories()

	// Delivery mode selection (authv3-delivery-refactor AV3D-5.3). AUTH_DELIVERY_MODE
	// selects the host's outbound-delivery composition: the generic-jobs-mode wiring
	// (this host's pre-seeded default) or the bounded in-process mode. On THIS proof host
	// BOTH are non-durable — jobs mode backs its fenced queue with jobsmem.NewFencedQueue
	// (in-memory), so a real durable posture is a store swap a production host makes
	// (pockets/jobs/stores/{pgx,turso}). The bounded mode is never hidden — it announces
	// its crash-loss + per-process posture LOUDLY at startup (see the WARN below where the
	// config is flipped).
	//
	// The value is the authentication pocket's own AUTH_DELIVERY_MODE tag, read here —
	// ahead of buildAuthConfig — because the jobs-mode dispatcher must be built before the
	// config is assembled. buildAuthConfig pre-seeds the same default and reads the same
	// key, so the two never disagree; an unrecognized value is auth.New's loud
	// construction error rather than a silent fallback to jobs.
	deliverySelection := authenticationConfig{
		DeliveryMode: delivery.ModeJobs,
	}
	if err := environment.ParseEnvTags("", &deliverySelection); err != nil {
		return err
	}
	mode := deliverySelection.DeliveryMode

	// Host operational health for delivery (AV3D-5.3): a secret-free, bounded, host-COMPOSED
	// surface (pockets/authentication/outbound/deliveryhealth). It observes the runtime lifecycle (host-owned), the
	// secret-free delivery lifecycle events (wrapping Config.DeliveryEventsEmitter), jobs-mode
	// admissions (wrapping Config.DeliveryDispatcher), and the in_process queue depth (the
	// auth Service's InProcessQueueDepth read). It carries counters/gauges/enums only — never
	// a recipient, payload, or logical key. The host mounts it at GET /healthz/delivery.
	health := deliveryhealth.New(string(mode))

	// Outbound delivery on the generic jobs pocket (authv3-delivery-refactor
	// AV3D-3.1), wired ONLY in jobs mode: authentication submits encrypted
	// delivery commands to a generic jobs fenced queue, and the host runs the jobs
	// FencedRuntime that invokes auth's delivery processor. The in-memory fenced queue
	// (jobsmem.NewFencedQueue) is the zero-infra stand-in, so jobs mode is NON-DURABLE here —
	// queued work is lost on restart with no cross-instance coordination; a durable posture is
	// a pgx/turso FencedQueue store swap a real host makes. The composition adapter
	// (pockets/authentication/outbound/authjobs) is the ONE place that imports BOTH pockets; neither pocket core
	// imports the other (constitution rule 6).
	var (
		deliveryJobs       *jobs.Components
		deliveryDispatcher *authjobs.Dispatcher
	)
	if mode == delivery.ModeJobs {
		settings := struct {
			MaxAttempts   int `env:"JOBS_MAX_ATTEMPTS"`
			ScheduleBatch int `env:"JOBS_SCHEDULE_BATCH"`
		}{}
		if err := environment.ParseEnvTags("", &settings); err != nil {
			return err
		}
		dj, err := jobs.New(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()},
			jobs.WithMaxAttempts(settings.MaxAttempts), jobs.WithScheduleBatchSize(settings.ScheduleBatch))
		if err != nil {
			return err
		}
		deliveryJobs = dj
		deliveryDispatcher = authjobs.NewDispatcher(dj.Queue)
	}

	// Host-owned router + middleware. Both pockets and the host demo routes mount
	// onto this.
	router := web.NewWebHandler()
	// TrustProxies runs OUTER of every pocket mount so auth's inbound reads the
	// host-resolved client IP (web.ClientIP) instead of the spoofable leftmost
	// X-Forwarded-For hop. TRUSTED_PROXY_COUNT=0 (default) trusts only RemoteAddr,
	// so a forged X-Forwarded-For can no longer rotate rate-limit keys or poison
	// security-event audit rows; a proxied deployment sets it to its trusted-proxy
	// hop count. SecurityHeaders sends HSTS and the browser defaults; a pocket page
	// that owns a stricter policy (the auth pages' CSP and no-referrer) keeps it.
	router.Use(web.TrustProxies(srv.TrustedProxyCount), web.SecurityHeaders(), web.RequestID(), web.Logger(log), web.Panics(log))

	// Serve the ui/goth fingerprinted assets (the auth pages' stylesheet), the
	// externalized fragment-reader script the reset/magic-link landings load, and the
	// passkey script the passkey controls load. All are same-origin, so the auth adapter's mapped CSP (style-src/script-src 'self')
	// covers them; the kit owns no route, so the host mounts them (ui-goth GOTH-7.2).
	uigothStatic := web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))
	uigothStatic.AddRoutes(router, authAssetBasePath)
	router.Handle(http.MethodGet, authgoth.DefaultFragmentScriptPath, authgoth.FragmentScriptHandler().ServeHTTP)
	router.Handle(http.MethodGet, authgoth.DefaultPasskeyScriptPath, authgoth.PasskeyScriptHandler().ServeHTTP)

	// Shared in-process event bus (sdk default Memory). cms is the emitter (it
	// publishes content.* post-write through mount.Events); the host subscribes
	// below to invalidate the public-page cache. Delivery is async (O3): an
	// emitter's latency never depends on its slowest subscriber.
	bus := sdkevents.NewMemory(sdkevents.WithLogger(log))

	// The public-page cache, held in a variable (it previously flowed straight
	// into cms.Config.Cache) so the host's content-event subscriber can drop it.
	pageCache := cacher.NewMemory()

	mount := pockets.Mount{Router: router, Logger: log, Events: bus}

	roleRoutesGate := &deferredMiddleware{}
	// The decision bridge records through the authentication pocket's one
	// recorder, which auth.New builds below.
	securityEvents := &deferredRecorder{}
	authzComponents, err := newAuthorization(roleRoutesGate.middleware, securityEvents, runtimeMode, log)
	if err != nil {
		return err
	}

	authorizer := authzComponents.Decisions
	tupleWriter := authzComponents.Mutations
	if err := authzComponents.Register(mount); err != nil {
		return err
	}

	if err := seedAuthorization(ctx, tupleWriter); err != nil {
		return err
	}

	// The host's authoritative resource registry: only the host knows whether a resource
	// still exists (authorization tuples merely describe host-owned resources). Seeded with
	// the demo project so an invitation-accept validates existence before the Granter writes
	// a tuple; a production host consults its own datastore instead (membership.go).
	hostResources := newHostResourceRegistry(resourceKey(demoResourceType, demoResourceID))

	// Auth config, assembled in the testable composition seam buildAuthConfig
	// (AV3-8.6): development posture, bundled templ Views, browser-safe Origin
	// allowlist, passwordless enablement, magic-link base URL, and every development
	// secret from a distinct env var. The invitation grant-on-accept seam is the
	// host-local integrityRelationshipGranter over the authorization operation ledger, carrying the host
	// resource-existence seam so acceptance against a deleted resource fails loudly.
	authCfg, err := buildAuthConfig(log, runtimeMode, integrityRelationshipGranter{
		system: tupleWriter,
		exists: hostResources.Exists,
	})
	if err != nil {
		return err
	}
	// A Granter enables invitations, so the pocket REQUIRES a relation-aware host
	// authorization policy (authenticationConfig.InviteCheck, design D3) — the two are wired
	// together. hostInviteCheck consults the authorizer so a member-capable manager cannot
	// escalate by inviting an owner; a nil InviteCheck here would be ErrInviteCheckRequired
	// at NewService, never an allow-by-default.
	authCfg.InviteCheck = hostInviteCheck(authorizer)
	// The Fencer settles an accepting claim on cancel: it fences the Granter's ledger
	// operation (same namespace, invitationOperationID) or reports a committed grant.
	authCfg.Fencer = integrityOperationFencer{system: tupleWriter}
	// The bundled machine-identity lifecycle routes (/auth/service-accounts*,
	// /auth/api-keys/{id}/revoke) mint and revoke credentials, so the pocket refuses to
	// guess a policy: with MachineRoutesGate nil they are NOT mounted (404) and NewService
	// WARNs. This host names the platform-admin coordinate already declared in authorizationModel
	// (platform/admin on platform:main), so each route runs the pocket's
	// MachineLifecycle authenticator, then this gate — human credential, live
	// session, platform admin. Set here rather than
	// in buildAuthConfig because the gate is a method value on the authorizer, which the
	// composition seam does not receive (the DeliveryMode post-set precedent below).
	authCfg.MachineRoutesGate = authzComponents.HTTP.Require(authorizationhttp.Can("admin", authorizationhttp.Fixed(platformResourceType, platformResourceID)))
	// The bundled user administration routes, behind the same platform-admin
	// coordinate, and the opt-in erase route (SOC 2 S3): erasure is irreversible,
	// so it mounts only because this host asks for it, with the action
	// UserAdminErase reaching the check.
	authCfg.UserAdminCheck = access.New(authorizer).UserAdmin
	authCfg.UserErasureRoute = true
	// The erase revokes the person's role assignments and relationships in every
	// scope first, through the audited unassign command with the administrator
	// as actor; the last project owner refuses it as 409 with nothing erased.
	authCfg.BeforeErase = beforeErase(authzComponents.Roles, authzComponents.Mutations, authorizationIntegrityPolicy())
	// The security-event listing (SOC 2 S7), behind the same check with the
	// action UserAdminReadAudit; each page read records one audit_read row.
	authCfg.SecurityEventsRoute = true
	// The fleet-wide revoke (SOC 2 S4d): POST /auth/admin/sessions/revoke-all,
	// behind the same check with the action UserAdminRevokeAllSessions and the
	// admins MFA gate. AUTH_GLOBAL_SESSION_REVOCATION_ROUTE=false unmounts it.
	// The service-account status (AUTH_SERVICE_ACCOUNT_STATUS) and the
	// registration mode (AUTH_REGISTRATION) are the pocket's own keys, read
	// with the rest in buildAuthConfig.
	globalRevoke := struct {
		Enabled bool `env:"AUTH_GLOBAL_SESSION_REVOCATION_ROUTE" default:"true"`
	}{}
	if err := environment.ParseEnvTags("", &globalRevoke); err != nil {
		return err
	}
	authCfg.GlobalSessionRevocationRoute = globalRevoke.Enabled
	// Apply the selected delivery mode to the auth config. buildAuthConfig returns the
	// jobs-mode posture (in-memory fenced queue on this host); AUTH_DELIVERY_MODE=in_process flips
	// it to the bounded EPHEMERAL pool here — and announces that posture LOUDLY. Neither mode
	// is durable on this proof host (both use in-memory stores).
	switch mode {
	case delivery.ModeInProcess:
		authCfg.DeliveryMode = delivery.ModeInProcess
		authCfg.DeliveryJobsAcknowledged = false
		authCfg.DeliveryEphemeralAcknowledged = true
		authCfg.DeliveryDispatcher = nil // in_process owns its bounded pool; no dispatcher
		log.WarnContext(ctx, "AUTH_DELIVERY_MODE=in_process: EPHEMERAL bounded delivery selected — "+
			"accepted in-flight work is LOST on crash or restart, there is NO cross-instance "+
			"coordination, and running multiple instances de-duplicates on NEITHER (a user may "+
			"receive duplicate messages). AUTH_DELIVERY_MODE=jobs on this proof host is ALSO in-memory "+
			"(non-durable); a durable posture requires a pgx/turso FencedQueue store, not this demo.",
			"delivery_mode", "in_process")
	default:
		// Route delivery through the generic-jobs dispatcher built above (AV3D-3.1), wrapped
		// by the health admission counter so the operational surface can report backlog. The
		// base config already selects DeliveryMode "jobs" + the runtime acknowledgment.
		authCfg.DeliveryDispatcher = health.Dispatcher(deliveryDispatcher)
	}
	// Publish the optional, secret-free delivery lifecycle events (delivered, skipped,
	// retried, dead_lettered, purged) onto the shared bus (AV3D-3.4) THROUGH the health
	// counter, which classifies each bounded transition and forwards to the bus. Observation
	// is best-effort: a dropped or failed event never changes delivery state, and a forward
	// failure surfaces on the health endpoint as observer_failures.
	authCfg.DeliveryEventsEmitter = health.Emitter(bus)

	// authSvc is the auth pocket's driving surface (FS2): its RequireAccessToken()
	// middleware is what cms gates its admin routes on, and
	// RequireAccessTokenOrAPIKey() / CurrentPrincipal back the host demo routes. The pocket's own HTTP routes are
	// the optional adapter over that surface — built once here, mounted once via
	// authSvc.HTTP.Register(mount).
	oauthDemoConfig := struct {
		Enabled bool `env:"AUTH_OAUTH2_DEMO"`
	}{}
	if err := environment.ParseEnvTags("", &oauthDemoConfig); err != nil {
		return err
	}
	authOptions := authCfg.options()
	var oauthFixture *oauth2fixture.Resolver
	if oauthDemoConfig.Enabled {
		if authCfg.RuntimeMode != environment.ModeDevelopment {
			return fmt.Errorf("AUTH_OAUTH2_DEMO requires development mode")
		}
		oauthFixture, err = oauth2fixture.New(srv.Origin())
		if err != nil {
			return err
		}
		oauthViews, ok := authCfg.Views.(authenticationhttp.OAuthViews)
		if !ok {
			return fmt.Errorf("OAuth demo requires consent and session views")
		}
		authOptions = append(authOptions, auth.WithOAuth2(auth.OAuth2Config{Server: oauthFixture.Config(), Clients: oauthFixture, Views: oauthViews,
			CapabilityDescription: "Read your user and connection identifiers through the local MCP demonstration. This demo does not change application data."}))
	}
	// AUTH_EXTERNAL_DEMO=1 (development only) wires a dev identity provider beside
	// the native credentials and mounts the hybrid routes after the pocket (demo.go).
	externalDemoConfig := struct {
		Enabled bool `env:"AUTH_EXTERNAL_DEMO"`
	}{}
	if err := environment.ParseEnvTags("", &externalDemoConfig); err != nil {
		return err
	}
	var external *externalDemo
	if externalDemoConfig.Enabled {
		external, err = newExternalDemo(ctx, srv.Origin(), authCfg.RuntimeMode, log)
		if err != nil {
			return err
		}
		authOptions = append(authOptions, auth.WithExternal(external.config))
	}
	// AUTH_DEMO_API_KEYS_EXEMPT=1 (development only) lets API keys pass the MFA
	// gates again (MFAConfig.APIKeysExempt), so the S4d drill can show the
	// hatch; a real host sets the literal in code, never from the environment.
	exemptDemo := struct {
		Enabled bool `env:"AUTH_DEMO_API_KEYS_EXEMPT"`
	}{}
	if err := environment.ParseEnvTags("", &exemptDemo); err != nil {
		return err
	}
	if exemptDemo.Enabled {
		if authCfg.RuntimeMode != environment.ModeDevelopment {
			return fmt.Errorf("AUTH_DEMO_API_KEYS_EXEMPT requires development mode")
		}
		authOptions = append(authOptions, auth.WithMFA(auth.MFAConfig{APIKeysExempt: true}))
	}
	// AUTH_DEMO_ADMIN_EMAIL (development only) grants platform admin to the
	// account that verifies that address (demoadmin.go).
	authRepos = withDemoAdmin(authRepos, tupleWriter, demoAdmin, log)
	authSvc, err := auth.New(authRepos, authCfg.TokenSigner, authCfg.RuntimeMode, authCfg.DeliveryMode, authOptions...)
	if err != nil {
		return err
	}
	securityEvents.set(authSvc.SecurityEvents)
	retention, err := runner.New(envPositiveDuration(log, "MAINTENANCE_INTERVAL", defaultMaintenanceInterval), log,
		runner.MaintenanceStep(authSvc.Maintenance), runner.OperationsStep(authzComponents.Mutations, defaultOperationExpiryBatch))
	if err != nil {
		return err
	}
	// Host-owned inquiry retention (SOC 2 S3), every delivery mode: a bounded PurgeBefore on the
	// eraser this host wired as cmsRepos.InquiryMaintenance, on its own runner and interval.
	inquiryCfg := inquiryPurgeConfigFromEnv(log)
	var inquiries *runner.Runner
	if cmsRepos.InquiryMaintenance != nil {
		inquiries, err = newInquiryRunner(inquiryCfg, newInquiryPurge(cmsRepos.InquiryMaintenance, inquiryCfg, time.Now), log)
		if err != nil {
			return err
		}
	}
	// The client-attribution stamp on every host route, so a denial the bridge
	// records there (the role routes, the debug route) carries the client's IP
	// and User-Agent. A later Use, so it runs inside TrustProxies.
	router.Use(authSvc.HTTP.ClientInfoMiddleware())
	if err := authSvc.HTTP.Register(mount); err != nil {
		return err
	}
	if oauthFixture != nil {
		demo, err := oauth2demo.New(oauth2demo.Config{BaseURL: srv.Origin(), ClientID: oauthFixture.Client().ID,
			ConfidentialClientID: oauthFixture.Config().ConfidentialClientID, ConfidentialSecret: oauthFixture.ConfidentialSecret()}, authSvc.HTTP, authSvc.Authentication)
		if err != nil {
			return err
		}
		demo.Register(mount)
		log.InfoContext(ctx, "Local OAuth connection demo enabled", "url", srv.Origin()+"/oauth-demo")
	}
	if external != nil {
		if err := external.register(ctx, router, authSvc, log); err != nil {
			return err
		}
	}
	// Close the role-routes ordering seam: the bundled /authorization/roles* surface
	// now runs behind the live human session plus the platform-admin permission — the
	// same coordinate MachineRoutesGate names. Assigned before the host serves, so no
	// request can reach the routes ahead of their gate (which fails closed anyway).
	roleRoutesGate.set(roleAdministrationGate(
		authSvc.HTTP.RequireAccessTokenLive(),
		authSvc.HTTP.RequireBrowserSafeMutation(),
		authzComponents.HTTP.Require(authorizationhttp.Can("admin", authorizationhttp.Fixed(platformResourceType, platformResourceID))),
	))
	// Boot fails LOUDLY if the chain never landed, matching the construction-matrix
	// posture the pockets already give this host: a gate is a security control, and
	// a refactor that reorders or drops the assignment must not reach production as
	// a per-request 500. The middleware's own fail-closed 500 stays the floor, not
	// the notification.
	if !roleRoutesGate.installed() {
		return errRoleRoutesGateNotInstalled
	}
	if err := registerDocumentRoutes(ctx, router, authSvc.HTTP.RequireAccessTokenLive(), authorizer, authzComponents.Authorizer, tupleWriter); err != nil {
		return err
	}

	// In the bounded in_process mode the health surface reads the live queue depth from the
	// auth Service (a secret-free counts-only seam) so it can report backlog/saturation.
	if mode == delivery.ModeInProcess {
		health.SetDepthSource(authSvc.Delivery.QueueDepth)
	}

	// The delivery processor is fully attached now (its account resolver is this built
	// authSvc). In jobs mode, ONLY NOW read the registered job kind/handler seam and build
	// the jobs FencedRuntime over it (AV3D-3.1) — so no handler can run against a half-built
	// auth Service. In in_process mode the host runs authSvc.Delivery.Run instead. The runtime
	// is built here but STARTED explicitly by the host, below.
	var deliveryFenced *queue.FencedRuntime
	// deliveryPurge is the jobs-mode host-owned terminal-purge pass (IX-10). In in_process
	// mode it stays nil: that mode's queue is ephemeral and its latest-by-key status map is
	// already self-bounding (a finite max-entry count + TTL), so nothing accumulates to purge.
	var deliveryPurge func(context.Context) (int, error)
	var deliveryPurgeInterval time.Duration
	if mode == delivery.ModeJobs {
		deliveryRuntime, ok := authSvc.Delivery.JobRuntime()
		if !ok {
			return fmt.Errorf("auth delivery job runtime unavailable: jobs-mode dispatcher not wired")
		}
		// Bound each provider attempt inside the default 30-second claim lease. The
		// fenced queue has no enqueue wake, so a one-second idle interval keeps mail
		// prompt instead of waiting out the 30-second default.
		df, err := authjobs.NewRuntime(deliveryJobs.Queue, deliveryRuntime,
			queue.WithFencedRuntimeLogger(log),
			queue.WithFencedRuntimePolicy(queue.FencedRuntimePolicy{PollInterval: time.Second, IdleInterval: time.Second, ProcessTimeout: 20 * time.Second}))
		if err != nil {
			return err
		}
		deliveryFenced = df
		// Bind the bounded terminal-purge pass over the SAME jobs Service. Each pass removes
		// at most Batch terminal delivery rows older than the retention window and emits the
		// purged lifecycle observation (which the health surface counts). The host owns the
		// schedule/lifecycle below; the pocket purges nothing on its own.
		purgeCfg := deliveryPurgeConfigFromEnv(log)
		deliveryPurge = newDeliveryPurge(deliveryJobs.Queue, deliveryRuntime, purgeCfg, time.Now)
		deliveryPurgeInterval = purgeCfg.Interval
	}

	// The CMS pages render through the same ui/goth bundle as auth (assets already
	// served under authAssetBasePath above). The admin entries list is HTMX-enhanced
	// (ui-goth GOTH-7.3); auth's RequireAccessToken() gates that admin surface below.
	cmsBundle, err := newAuthBundle()
	if err != nil {
		return err
	}
	cmsViews, err := cmsgoth.New(cmsBundle)
	if err != nil {
		return err
	}

	cmsCfg := cms.Config{
		Views:           cmsViews,                                // the ui/goth-backed bundled default
		Types:           []content.ContentType{productType()},    // host-registered custom type (zero migration)
		Templates:       []cms.TemplateBinding{productBinding()}, // its dev-authored renderer
		Cache:           pageCache,
		Mailer:          email.NewConsole(log),
		MailFrom:        "cms@localhost",
		ContactTo:       "ops@localhost",
		AdminMiddleware: []web.Middleware{authSvc.HTTP.RequireAccessToken()}, // auth gates cms's admin surface
		RuntimeMode:     runtimeMode,
	}
	// CMS_MAIL_FROM / CMS_CONTACT_TO win over the two addresses seeded above.
	if err := environment.ParseEnvTags("", &cmsCfg); err != nil {
		return err
	}
	if err := cms.Register(mount, cmsRepos, cmsCfg); err != nil {
		return err
	}

	// Host cache-invalidation subscriber (S5/O6): subscribe to every event ("*")
	// and filter content.* in the handler — the bus stays a plain fan-out with no
	// prefix routing. On a cms content event, drop the whole public-page cache
	// (cacher.Pages uses versioned keys under "page:");
	// invalidation is best effort: an in-flight render may repopulate an old page.
	// Before this wiring the page was purely
	// TTL-bound: an edit within the 60s TTL kept serving stale bytes. Because cms
	// emits are async (O3), this runs shortly AFTER the admin write returns rather
	// than synchronously with it — a re-fetch trigger, not a transactional write.
	if _, err := bus.Subscribe("*", func(ctx context.Context, e sdkevents.Event) error {
		if !strings.HasPrefix(e.Type(), "content.") {
			return nil
		}
		return pageCache.DeletePrefix(ctx, "page:")
	}); err != nil {
		return err
	}

	// The events pocket's SSE gateway, best-effort/direct-emit (design §6 wiring
	// note): the SAME bus instance flows to both Mount.Events (cms is the emitter)
	// and events.New(bus, ...) (the gateway is the consumer) — one fan-out, no second
	// bus. A content.* frame fans out to any open stream the moment cms emits. The
	// gateway reads connect-time identity from sdk, stashed by
	// authSvc.HTTP.RequireAccessToken() on StreamMiddleware (A-I1 E2: no Identity field — absent
	// principal fails closed with 401). WithOutbox omitted ⇒ direct-emit mode
	// (no durable rail, no poller). Authorize is wired below through the
	// authorization ENGINE (the flagship posture — authorization-v1 Z4 commit 2),
	// so the resource-scoped /events/{resource_type}/{resource_id} route IS
	// registered. The subject stream lands at GET /events (host mounts at root, no
	// prefix — same as cms/auth).
	// Variant selection (design §8): the DEFAULT is direct-emit (WithOutbox
	// omitted — cms emits straight onto the bus). With EVENTS_OUTBOX=memory the host
	// instead wires an example-local in-memory outbox and drives a poller that
	// drains it onto the SAME bus — the durable at-least-once rail. Either way the
	// gateway is a plain bus consumer; only the emit path in front of the bus
	// changes.
	var outboxStore *outboxmem.Store
	eventOptions := []eventspocket.Option{
		eventspocket.WithLogger(log),
		// This demo explicitly shares its notification feed with authenticated users.
		eventspocket.WithVisibility(func(context.Context, sdk.Principal, sdkevents.Event) (bool, error) { return true, nil }),
		eventspocket.WithStreamMiddleware(authSvc.HTTP.RequireAccessToken()),
		eventspocket.WithAuthorization(func(ctx context.Context, p sdk.Principal, resourceType, resourceID string) (bool, error) {
			return access.New(authorizer).Can(ctx, p, demoPermission, resourceType, resourceID)
		}),
	}
	if durableOutbox() {
		outboxStore = outboxmem.New()
		eventOptions = append(eventOptions, eventspocket.WithOutbox(outboxStore))
	}
	// The host loads policy values; dependencies stay in the wiring above.
	var streamLimits eventstreams.Limits
	var streamHTTP eventshttp.Policy
	if err := environment.ParseEnvTags("", &streamLimits); err != nil {
		return err
	}
	if err := environment.ParseEnvTags("", &streamHTTP); err != nil {
		return err
	}
	eventOptions = append(eventOptions, eventspocket.WithStreamLimits(streamLimits), eventspocket.WithHTTPPolicy(streamHTTP))
	eventsSvc, err := eventspocket.New(bus, eventOptions...)
	if err != nil {
		return err
	}
	defer eventsSvc.Close()
	if err := eventsSvc.Register(mount); err != nil {
		return err
	}

	// Durable-outbox variant plumbing (EVENTS_OUTBOX=memory): the host owns the
	// poller lifecycle (the pocket owns no goroutines — D4). The poller runs on
	// an sdk/pkg/workers pool woken by the canonical append-then-signal pattern
	// (gate edit 2): a dedicated cap-1 wake channel the POST /outbox-demo handler
	// signals right after Append, so a fresh record drains sub-second instead of
	// waiting out the pool's idle interval. The pool runs on its OWN
	// Background-derived context (NOT the request/signal ctx) so shutdown can stop
	// it AFTER HTTP has drained, in the documented order below.
	var (
		cancelPool context.CancelFunc
		poolDone   chan struct{}
	)
	if outboxStore != nil {
		poller, err := outbox.NewPoller(outboxStore, bus.Dispatch)
		if err != nil {
			return err
		}
		wake := make(chan struct{}, 1)
		router.Handle(http.MethodPost, "/outbox-demo", outboxDemoHandler(outboxStore, wake, log))

		pool := workers.NewPool(poller.Poll,
			workers.WithName("outbox-poller"),
			workers.WithWakeChannel(wake),
			workers.WithLogger(log),
		)
		var poolCtx context.Context
		poolCtx, cancelPool = context.WithCancel(context.Background())
		poolDone = make(chan struct{})
		go func() {
			defer close(poolDone)
			_ = pool.Run(poolCtx)
		}()
		log.InfoContext(ctx, "events durable outbox variant ENABLED (EVENTS_OUTBOX=memory)",
			"outbox", "in-memory (pockets/events/outbound/outboxmem)", "trigger", "POST /outbox-demo")
	}

	registerDemoRoutes(router, authSvc.HTTP, authorizer, authzComponents.Roles, authzComponents.HTTP)
	registerMFADemoRoutes(router, authSvc.HTTP, authSvc.Authentication)
	registerDebugRoutes(router, authSvc.HTTP, authzComponents.HTTP, authRepos, log)

	// Host-local liveness probe (host route, not pocket surface). Mounted on
	// the root router with no middleware, outside every gated group and
	// unwrapped by any authenticator — unauthenticated by design, since a readiness
	// probe can't log in.
	router.Handle(http.MethodGet, "/healthz", healthzHandler())

	// Host-local delivery operational health (AV3D-5.3): a secret-free, bounded status
	// surface distinguishing runtime not-started/running, backlog/saturation, provider retry
	// + dead-letter activity, and observer emit failures. Counters/gauges/enums only — no
	// recipient, payload, or logical key. Unauthenticated like /healthz (an operator probe
	// cannot log in); it exposes nothing sensitive.
	router.Handle(http.MethodGet, "/healthz/delivery", health.Handler())

	// The selected delivery runtime drains the auth delivery queue off the request path —
	// the host owns its lifecycle (the pocket starts no goroutine). Jobs mode runs the
	// generic-jobs FencedRuntime (AV3D-3.1); in_process mode runs authSvc.Delivery.Run (the
	// bounded ephemeral pool, AV3D-4.1). It runs on its OWN Background-derived context (never
	// the parent ctx) so shutdown stops it AFTER HTTP has drained, mirroring the poller order
	// below. MarkStarted/MarkStopped bracket the goroutine so the health surface reports
	// not-started vs running.
	var deliveryRun func(context.Context) error
	switch mode {
	case delivery.ModeInProcess:
		deliveryRun = authSvc.Delivery.Run
	default:
		deliveryRun = deliveryFenced.Run
	}

	// IX-02: supervise HTTP + delivery as ONE lifecycle. web.Run blocks on hostCtx — a
	// cancelable child of the incoming signal ctx — so BOTH a signal AND an UNEXPECTED
	// delivery-runtime exit drive the SAME ordered drain below. The delivery runtime still
	// runs on its own Background-derived deliveryCtx (canceled AFTER HTTP drains, like the
	// poller). If deliveryRun returns while deliveryCtx is NOT canceled that is an unexpected
	// exit (error OR nil): the host must not keep admitting work against a dead delivery
	// runtime, so the supervisor cancels hostCtx (web.Run drains) and records the cause so run
	// returns nonzero. This mechanism is chosen over a health-503-only reaction because this
	// file's shutdown idiom already funnels every stop through web.Run's context — reusing it
	// keeps ONE documented drain order for signal-stop and delivery-failure-stop alike (the
	// health surface still flips to not_started via the supervisor's MarkStopped).
	hostCtx, cancelHost := context.WithCancel(ctx)
	defer cancelHost()
	deliveryCtx, cancelDelivery := context.WithCancel(context.Background())
	supervisor := deliveryhealth.Supervise(deliveryCtx, cancelHost, deliveryRun, health, log)

	// Host-owned scheduled terminal purge (IX-10), jobs mode only. Without it the durable
	// delivery rows and their encrypted metadata grow without bound despite the documented
	// retention posture. It runs on its OWN Background-derived context (never the parent ctx)
	// so shutdown stops it AFTER HTTP drains, in the documented order below — exactly like the
	// delivery runtime and the poller. A purge-pass error is logged and the loop continues; a
	// purge is never on the request path.
	var (
		cancelPurge context.CancelFunc
		purgeDone   chan struct{}
	)
	if deliveryPurge != nil {
		var purgeCtx context.Context
		purgeCtx, cancelPurge = context.WithCancel(context.Background())
		purgeDone = make(chan struct{})
		go func() {
			defer close(purgeDone)
			runDeliveryPurgeLoop(purgeCtx, deliveryPurgeInterval, deliveryPurge, log)
		}()
		log.InfoContext(ctx, "delivery terminal purge scheduler ENABLED (jobs mode)",
			"interval", deliveryPurgeInterval)
	}

	// The inquiry runner built above; stopped beside the delivery purge.
	var stopInquiryPurge func()
	if inquiries != nil {
		stopInquiryPurge = startInquiryPurge(inquiryCfg, inquiries, log)
	}

	// Host-owned retention (SOC 2 S3): the retention host pocket's runner ticks the
	// authentication maintenance pass and the authorization operation expiry (the pocket
	// keeps every operation record until ExpireOperations removes those past its
	// retention), in both delivery modes, at start and every MAINTENANCE_INTERVAL, on
	// its own Background-derived context so shutdown stops it after HTTP drains.
	// MaintenanceConfig.MaintenanceRunnerAcknowledged asserts exactly this.
	retentionCtx, cancelRetention := context.WithCancel(context.Background())
	retentionDone := make(chan struct{})
	go func() {
		defer close(retentionDone)
		retention.Run(retentionCtx)
	}()

	// Shutdown order (design §7, phase 5 — with the poller, corrected context idiom
	// P3):
	//  1. web.Run blocks until hostCtx is canceled (by the signal ctx OR by the delivery
	//     supervisor on an unexpected runtime exit), then drains in-flight HTTP on its
	//     OWN fresh Background+ShutdownTimeout context (run.go). At the deadline it
	//     closes remaining connections, canceling their request contexts (including
	//     SSE). Host cancellation alone does not interrupt a normal drain.
	//  2. THEN stop the poller pool. It runs on its OWN Background-derived context
	//     (never the parent ctx — a canceled parent would tear it down before HTTP
	//     finished draining), so cancel that context now and wait, bounded, for the
	//     in-flight batch to finish.
	//  3. Close the bus LAST, on a FRESH bounded context (a canceled parent ctx
	//     stops waiting immediately). Closing after the poller stops
	//     lets the final outbox handoff finish before new admissions are refused.
	runErr := web.Run(hostCtx, router, srv, log)

	// Stop the delivery runtime after HTTP drains (its own context, like the poller).
	log.InfoContext(context.Background(), "stopping delivery runtime")
	cancelDelivery()
	if !supervisor.Wait(5 * time.Second) {
		log.WarnContext(context.Background(), "delivery runtime did not stop within 5s")
	}
	log.InfoContext(context.Background(), "delivery runtime stopped")
	// If the delivery runtime exited unexpectedly (it drove this shutdown, not a signal),
	// surface its cause so run returns nonzero even though web.Run drained cleanly.
	if runErr == nil {
		runErr = supervisor.ExitErr()
	}

	// Stop the terminal-purge scheduler after the delivery runtime (its own context, like the
	// poller). Purging after delivery has stopped means no new terminal rows arrive mid-purge.
	if cancelPurge != nil {
		log.InfoContext(context.Background(), "stopping delivery terminal purge scheduler")
		cancelPurge()
		select {
		case <-purgeDone:
		case <-time.After(5 * time.Second):
			log.WarnContext(context.Background(), "delivery terminal purge scheduler did not stop within 5s")
		}
		log.InfoContext(context.Background(), "delivery terminal purge scheduler stopped")
	}
	if stopInquiryPurge != nil {
		stopInquiryPurge()
	}

	log.InfoContext(context.Background(), "stopping retention runner")
	cancelRetention()
	select {
	case <-retentionDone:
	case <-time.After(5 * time.Second):
		log.WarnContext(context.Background(), "retention runner did not stop within 5s")
	}

	if cancelPool != nil {
		log.InfoContext(context.Background(), "stopping outbox poller pool")
		cancelPool()
		select {
		case <-poolDone:
		case <-time.After(5 * time.Second):
			log.WarnContext(context.Background(), "outbox poller pool did not stop within 5s")
		}
		log.InfoContext(context.Background(), "outbox poller pool stopped")
	}

	log.InfoContext(context.Background(), "closing event bus")
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = eventsSvc.Close()
	_ = bus.Close(closeCtx)

	return runErr
}

// durableOutbox reports whether the durable-outbox second variant is selected
// (EVENTS_OUTBOX=memory). Default (unset/other) keeps the direct-emit rail.
func durableOutbox() bool {
	return environment.GetEnvOrDefault("EVENTS_OUTBOX", "") == "memory"
}

// outboxDemoHandler is the host-owned demo trigger for the EVENTS_OUTBOX=memory
// variant (the jobs-minimal POST /enqueue precedent): it appends one record to
// the example-local outbox, then wakes the poller with the canonical
// append-then-signal pattern (gate edit 2) so the drain runs promptly instead of
// waiting out the pool's idle interval. cms itself never touches the outbox (O2)
// — this is a host route, not pocket surface. The frame that reaches the open
// stream carries the durable outbox EventID as its SSE id: (the poller's
// rehydrated event surfaces it), distinct in provenance from the direct-emit
// rail's CorrelationID.
func outboxDemoHandler(store *outboxmem.Store, wake chan<- struct{}, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		evt := sdkevents.NewBaseEvent("demo.outbox").WithAggregate("demo", "outbox-demo")
		rec, err := sdkevents.NewRecord(evt)
		if err != nil {
			writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": "build record"})
			return
		}
		if err := store.Append(r.Context(), rec); err != nil {
			writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// Non-blocking cap-1 send: coalesced signals never block the handler, and
		// the pool's idle interval is the backstop for any dropped signal.
		select {
		case wake <- struct{}{}:
		default:
		}
		log.InfoContext(r.Context(), "outbox demo appended", "event_id", rec.EventID)
		writeHostJSON(w, http.StatusAccepted, map[string]string{"event_id": rec.EventID})
	}
}

// healthzHandler is this host's liveness probe. Both pocket stores are
// memory-backed, so there is no DB to probe — reaching the handler is itself
// the liveness signal.
func healthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

// seed populates a little content so the public site renders something. No user
// is seeded on purpose: registration is part of the proof flow.
// ids seeds demo content with the default entity-ID strategy, matching the
// zero-value cms.Config.IDs the host wires.
var ids = sdk.IDGenerator{}

func seed(ctx context.Context, repos cms.Repositories) error {
	now := time.Now().UTC()

	menu, err := menus.NewMenu(ids, "Main", now)
	if err != nil {
		return err
	}
	if _, err := repos.Menus.CreateMenu(ctx, menu); err != nil {
		return err
	}
	for i, link := range []struct{ label, url string }{{"Home", "/"}, {"About", "/about"}} {
		item, err := menus.NewMenuItem(ids, menu.ID, link.label, link.url, "", i, now)
		if err != nil {
			return err
		}
		if _, err := repos.Menus.AddItem(ctx, item); err != nil {
			return err
		}
	}

	// Content is the Registry model: Articles and the About Page are content.Entry
	// rows on the shared spine, distinguished by Type — no per-type tables.
	articles := []struct{ title, excerpt, body string }{
		{"Two pockets, one host", "auth gates cms's admin surface with zero cross-import.", "pockets/cms never imports pockets/authentication; only this host's main imports both — constitution rule 6, proved with two real pocket modules."},
		{"Bring your own stores", "Both pockets run on in-memory stores; no libsql in the graph.", "Swap datastores without forking a pocket — the whole point of the module split."},
	}
	for _, a := range articles {
		e, err := content.NewEntry(ids, "article", a.title, a.excerpt, a.body, "demo", content.StatusPublished, "", now)
		if err != nil {
			return err
		}
		if _, err := repos.Entries.Create(ctx, e); err != nil {
			return err
		}
	}

	page, err := content.NewEntry(ids, "page", "About", "", "This page is served from memory — no SQL involved.", "", content.StatusPublished, "", now)
	if err != nil {
		return err
	}
	if _, err := repos.Entries.Create(ctx, page); err != nil {
		return err
	}

	// A product: the host's custom type, with EAV custom fields, on the same rail.
	prod, err := content.NewEntry(ids, "product", "Widget 3000", "The flagship widget.", "Built to last; ships worldwide.", "", content.StatusPublished, "", now)
	if err != nil {
		return err
	}
	prod.Fields = content.Fields{
		"subtitle": {Kind: content.KindText, Raw: "Now with more widgets"},
		"price":    {Kind: content.KindNumber, Raw: "49.99"},
	}
	if _, err := repos.Entries.Create(ctx, prod); err != nil {
		return err
	}
	return nil
}

// buildAuthConfig assembles the authentication pocket's Config for this proof host
// (the AV3-8.6 composition seam, factored out so startup/production-negative tests
// share the exact wiring run() uses). It wires:
//
//   - the DEVELOPMENT runtime posture: the console email Sender and phone Notifier
//     are development-only transports (they log bodies), which production RuntimeMode
//     rejects (design §6.3) — the startup WARN is expected, and the production
//     negative test proves construction fails when this same wiring flips to
//     production;
//   - the bundled default HTML surface (authtempl.New()) into Config.Views (design
//     §9.2/R12/V16): normal HTML GET pages + form handling mount alongside the
//     UNCHANGED JSON API. Nil would keep this host API-only; the sibling templ module
//     is the zero-value default, overridable per method (AV3-8.9);
//   - the browser-safe mutation Origin allowlist (design §9.1) so same-origin browser
//     forms pass the cookie-mutation gate while cross-site credentialed POSTs are
//     refused;
//   - passwordless login for both v3 kinds (design §4.2): email magic link + OTP and
//     phone OTP through the console notifier, on the atomic challenge rail + durable
//     outbox + link-capable PublicAuthBaseURL wired here;
//   - the magic-link base URL (design §6.4), built ONLY from configuration — request
//     Host/forwarded headers never participate;
//   - DeliveryMode "jobs" + DeliveryJobsAcknowledged: the queue is the only send path
//     (AV3-4.3), so the pocket is told run() actually runs the generic-jobs delivery
//     runtime (jobs.FencedRuntime, below); and
//   - every secret (JWT signer, challenge pepper, delivery + identifier +
//     token-encryption keys) from a DISTINCT hex env var through one
//     authentication.NewKeys call, never a committed constant, and never printing
//     key material (demo.go buildKeyedCollaborators): production refuses an unset
//     key, development mints an ephemeral one.
//
// mode is the host's runtime mode, resolved by main from the process environment.
// The granter is the invitation grant-on-accept seam (nil → invitations off). When run()
// passes a non-nil Granter it also sets the REQUIRED relation-aware authenticationConfig.InviteCheck
// (hostInviteCheck) right after this seam returns — the two are wired together (design D3).
// It builds no goroutines and reads no host lifecycle; run() owns the worker + shutdown.
func buildAuthConfig(log *slog.Logger, mode environment.Mode, granter invitations.Granter) (authenticationConfig, error) {
	// The REQUIRED access-JWT signer, optional provider-token encrypter, REQUIRED
	// challenge protector (authmem wires Challenges), REQUIRED delivery-outbox
	// encrypter (jobs mode seals every command envelope), and identifier keyer — each
	// from its own distinct env var (demo.go).
	keyed, err := buildKeyedCollaborators(mode, log)
	if err != nil {
		return authenticationConfig{}, err
	}

	// The ui/goth HTML surface: build the presentation bundle, then this host's
	// partial override (authpages.New) which embeds the ui/goth Views and overrides
	// only Login with Gopernicus-CMS branding. The embedded Views promotes HTMLPolicy(),
	// which maps the bundle's browser Requirements (+ the fragment-reader script-src)
	// into the pocket's CSP so the auth pages load their assets under default-src
	// 'none' — the host never hand-writes that CSP (ui-goth GOTH-7.2).
	bundle, err := newAuthBundle()
	if err != nil {
		return authenticationConfig{}, err
	}
	authViews, err := authpages.New(bundle, authgoth.WithQR(rscqr.Modules))
	if err != nil {
		return authenticationConfig{}, err
	}

	// This host's own externally visible origin, resolved from the SAME
	// web.ServerConfig tags run() serves on (PUBLIC_BASE_URL, else http://HOST:PORT).
	// Every host-DERIVED default below — the OAuth callback base, the magic-link and
	// password-reset landings, and the Origin-allowlist fallback — is pre-seeded from
	// it, so an environment that leaves those keys empty keeps them.
	srv := web.ServerConfig{Port: defaultPort}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return authenticationConfig{}, err
	}
	origin := srv.Origin()

	cfg := authenticationConfig{
		TokenSigner: keyed.signer,
		RuntimeMode: mode,
		// Delivery on the generic jobs runtime (authv3-delivery-refactor
		// AV3D-0.1). run() wires the generic-jobs dispatcher (authCfg.DeliveryDispatcher)
		// over an in-memory fenced queue for this demo, so it is NON-DURABLE here (a real
		// durable posture is a pgx/turso FencedQueue store swap); the production-negative
		// matrix proves the SAME wiring flipped to production fails closed on an
		// unacknowledged runtime.

		DeliveryMode: delivery.ModeJobs,
		Logger:       log,
		PasswordConfig: auth.PasswordConfig{
			Hasher:               bcrypt.New(),
			RequireVerifiedEmail: true,
		},
		IdentityConfig: auth.IdentityConfig{
			ChallengeProtector: keyed.challengeProtector,
			IdentifierKeyer:    keyed.identifierKeyer,
		},
		DeliveryConfig: auth.DeliveryConfig{
			Mailer:   email.NewConsole(log),
			MailFrom: "auth@localhost",
			// The phone-kind console notifier makes phone a supported delivery kind
			// (deny-by-absence; the dev stand-in for SMS — the token lands in the log).

			BodySenders:       map[string]delivery.BodySender{sdk.AddressKindPhone: notify.NewConsole(log)},
			DeliveryEncrypter: keyed.deliveryEncrypter,
			// The queue is the only send path; affirm run() runs the generic-jobs delivery
			// runtime (jobs.FencedRuntime) (authv3-delivery-refactor AV3D-0.1).

			DeliveryJobsAcknowledged: true,
		},
		MessagesConfig: auth.MessagesConfig{
			// The DISTINCT second override system (design §6.2): a host email LayerApp
			// content override that rebrands the verification email body. It swaps an EMAIL,
			// not a page — a different facility from Views, wired through a different Config
			// field into the delivery router. The code ({{.Secret}}) still renders, so the
			// verification flow is unbroken; only the copy is host-branded.

			EmailContentTemplates: []delivery.TemplateOverride{authpages.EmailOverride()},
		},
		OAuthConfig: auth.OAuthConfig{
			Providers:         []oauth.Provider{fakeOAuthProvider{}},
			TokenEncrypter:    keyed.tokenEncrypter,
			OAuthCallbackBase: origin,
			// This host explicitly trusts its local demonstration identity source.

			TrustOAuthEmail: func(provider string, _ oauth.UserInfo) bool { return provider == "fake" },
		},
		PasswordlessConfig: auth.PasswordlessConfig{
			// Passwordless login for email + phone (design §4.2): magic link + OTP. Each
			// listed kind needs a wired delivery channel (email via the Mailer, phone via the
			// console Notifier) or construction fails LOUDLY; AUTH_PASSWORDLESS narrows it.

			Passwordless: []string{sdk.AddressKindEmail, sdk.AddressKindPhone},
		},
		LinksConfig: auth.LinksConfig{
			// The magic-link / redemption-page base URL (design §6.4), config-only: the
			// framework appends "#token=<token>", so it points at the bundled fragment-reading
			// landing GET this host mounts at /auth/magic. AUTH_PUBLIC_BASE_URL overrides it;
			// request Host/forwarded headers NEVER participate.

			PublicAuthBaseURL: origin + "/auth/magic",
			// The password-reset landing route the reset mail links to (CHAU-5.1),
			// config-only: the link is built in the delivery worker from THIS value,
			// never from a request Host/forwarded header. Production requires it.
			// AUTH_PASSWORD_RESET_URL overrides this host's own reset page.

			PasswordResetURL: origin + "/auth/password/reset",
			// The OAuth pending-link landing URL the anti-takeover confirmation mail links
			// to (oauth-pending-link plan D1), config-only. Empty by default, keeping the
			// mail's bare-token line — but this demo DOES serve the pocket's bundled
			// fragment-reading landing (public GET /auth/oauth/link, mounted with Views +
			// a provider), so http://HOST:PORT/auth/oauth/link is a working value here. A
			// real host points AUTH_OAUTH_LINK_URL at that route or its own SPA route; the
			// empty pre-seed is what that key overrides.

			OAuthLinkBaseURL: "",
			// The invitation landing URL (SOC 2 S7) an invitation links to when the
			// inviter named no allowlisted redirect, before "#token=<token>" is
			// appended. This demo serves no invitation landing page, so it points at
			// its own home page and the token is read from the mail's link by hand;
			// a real host points AUTH_INVITATION_URL at the SPA route that reads
			// location.hash and POSTs /auth/invitations/accept. Production requires it.

			InvitationURL: origin + "/",
			// Absolute post-flow destinations only; safe same-origin relative paths
			// (e.g. the account page's ?redirect=/auth/account) need no entry.

			RedirectAllowlist: []string{"/"},
		},
		BrowserConfig: auth.BrowserConfig{
			// The exact-match Origin allowlist the browser-safe mutation gate validates
			// cookie-authenticated sensitive mutations and HTML form posts against; defaults
			// to this host's own origin (design §9.1), overridden by AUTH_ALLOWED_ORIGINS.

			AllowedOrigins: []string{origin},
			// The optional HTML surface (design §9.2): this host's REAL partial override
			// (authpages.New) embeds the ui/goth Views and overrides only the Login page
			// with Gopernicus-CMS branding — presentation changes only, the JSON API and
			// every route/service/redirect policy are unchanged (AV3-8.9, proven
			// isolation-safe in AV3-8.5). Every non-overridden page is the promoted ui/goth
			// default rendered from the fingerprinted assets under authAssetBasePath.

			Views: authViews,
			// The resource policy the ui/goth adapter derives from the bundle: it widens the
			// pocket's strict CSP exactly far enough to load the GOTH stylesheet and the
			// same-origin fragment-reader script (script-src 'self' + the per-render nonce),
			// and can never remove the pocket-owned fixed protections (ui-goth GOTH-7.2).

			HTMLPolicy: authViews.HTMLPolicy(),
		},
		InvitationsConfig: auth.InvitationsConfig{
			// AccessTokenTTL / RefreshTTL are left zero: AUTH_ACCESS_TOKEN_TTL and
			// AUTH_REFRESH_TTL carry the pocket's own tag defaults (15m / 168h), which are
			// the values the pocket resolves a zero field to anyway.

			Granter: granter,
		},
		// Retention (SOC 2 S3): run() ticks Components.Maintenance on the retention
		// runner, which the acknowledgement asserts. INVITATION_RETENTION shortens
		// the invitation window for a run-and-look; unset keeps the 90-day default.
		// No archive sink: this in-memory host purges its rail unarchived.
		MaintenanceConfig: auth.MaintenanceConfig{
			Config:                        maintenance.Config{InvitationRetention: envPositiveDuration(log, "INVITATION_RETENTION", 0)},
			MaintenanceRunnerAcknowledged: true,
		},
	}
	// The pocket's own env tags, applied over the literal: AUTH_MAIL_FROM,
	// AUTH_ALLOWED_ORIGINS, AUTH_PASSWORDLESS, AUTH_PUBLIC_BASE_URL,
	// AUTH_PASSWORD_RESET_URL, AUTH_OAUTH_LINK_URL, AUTH_OAUTH_CALLBACK_BASE,
	// AUTH_REDIRECT_ALLOWLIST, AUTH_REQUIRE_VERIFIED_EMAIL,
	// AUTH_DELIVERY_MODE, AUTH_ACCESS_TOKEN_TTL, AUTH_REFRESH_TTL, AUTH_IDLE_TIMEOUT
	// (authmem implements session.ActivityRepository, so setting it wires idle
	// expiry), AUTH_LIST_STRATEGY, the AUTH_COOKIE_* nested cookie keys, and the
	// remaining flags. An empty value
	// keeps what the literal seeded; an unrecognized mode or strategy is the pocket's
	// loud construction error.
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		return authenticationConfig{}, err
	}
	// Passkeys (SOC 2 S4b) over the go-webauthn integration: the RP ID is this
	// host's own origin host and the browser origins are its Origin allowlist.
	// Views mounts the manage and step-up pages from the same partial override.
	rp, err := passkeyRelyingParty(origin, cfg.AllowedOrigins)
	if err != nil {
		return authenticationConfig{}, err
	}
	cfg.Passkeys = auth.PasskeyConfig{RelyingParty: rp, Views: authViews}
	// TOTP and recovery codes (SOC 2 S4c): secrets sealed under the AUTH_TOTP_KEYS
	// ring. Views mounts the manage, enrollment (its QR code drawn through
	// rsc-qr), recovery-code and chooser pages; with them wired, a Browser() +
	// RequireMFA() denial lands on the chooser instead of the passkey page.
	cfg.TOTP = auth.TOTPConfig{Encrypter: keyed.totpEncrypter, Issuer: "Gopernicus CMS", Views: authViews}
	return cfg, nil
}

// passkeyRelyingParty builds the WebAuthn relying party for origin. The RP ID is
// permanent for a deployment — changing it orphans every registered passkey —
// and every allowed origin must sit within it, or gowebauthn.New refuses boot.
func passkeyRelyingParty(origin string, allowed []string) (*gowebauthn.RelyingParty, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return nil, fmt.Errorf("auth-cms: passkey origin %q: %w", origin, err)
	}
	policy, err := web.NewOriginPolicy(web.OriginPolicyConfig{Exact: allowed})
	if err != nil {
		return nil, err
	}
	return gowebauthn.New(webauthn.Config{RPID: u.Hostname(), RPName: "Gopernicus CMS", Origins: policy})
}
