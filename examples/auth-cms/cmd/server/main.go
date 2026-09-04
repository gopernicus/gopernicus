// Command server is the auth-v2 A9 / events-v1 proof host: it mounts
// pockets/cms, pockets/authentication, AND pockets/events onto one host
// router, with in-memory stores and no datastore driver in its module graph
// (verify: `GOWORK=off go list -m all | grep -i libsql` is empty). The host is
// the only party that imports the pockets — no pocket imports another
// (constitution rule 6); the cross-pocket flow rides sdk vocabulary
// (sdk/foundation/web.Middleware, sdk/foundation/identity, sdk/capabilities/events) the host wires between them.
//
// The cross-pocket wiring is the point: cms's admin surface (the CRUD routes)
// is gated by auth's identity middleware via cms.Config.AdminMiddleware ←
// authSvc.RequireAccessToken(). Neither pocket imports the other; structural typing on
// sdk/foundation/web.Middleware and the auth Service is what lets the host connect them.
// Public cms routes (the home page, published singles) stay ungated.
//
// On top of v1, this host exercises the whole auth-v2 surface for the A9 proof
// protocol (see README): the verified-email login gate (RequireVerifiedEmail),
// a host-local fake OAuth provider (oauthfake.go), machine identity (API keys +
// service accounts), access JWTs + rotating refresh tokens signed host-side by
// the sdk stdlib HS256 default (sdk/foundation/cryptids), security-event audit
// rows surfaced through a
// DEFAULT-OFF debug route, and invitations that grant through the authorization
// engine's relationshipGranter (membership.go) — authorization-v1's FLAGSHIP
// posture (Z4 commit 2): ordinary invitation-accept writes a real ReBAC tuple
// via the separately held baseline RelationshipWriter, retiring the A9 toy membership map; the
// memstore-backed engine keeps the host zero-infra (no libsql). The host-local
// demo routes (demo.go) are gated variously on a resolved principal, an engine
// Check, a LookupResources enumeration, and a roles-kind HasRole check.
//
// pockets/events adds the SSE gateway at GET /events (authenticated via
// authSvc.RequireAccessToken() on StreamMiddleware): a cms edit fans out as a
// content.updated frame to any open stream. Two rails prove out here. The
// DEFAULT variant is direct-emit/best-effort — cms emits straight onto the bus
// (SSE id: = CorrelationID). The DURABLE variant (EVENTS_OUTBOX=memory) routes a
// host-owned POST /outbox-demo append through an example-local in-memory outbox
// (internal/outboxmem) and a host-driven events.Poller on an sdk/foundation/workers pool:
// outbox -> poll -> emit -> SSE, id: = the durable outbox EventID. The shutdown
// order is HTTP server -> delivery runtime -> terminal-purge scheduler -> poller pool
// -> bus.Close (see run's tail comment); HTTP and the delivery runtime are supervised
// as one lifecycle, so an unexpected delivery-runtime exit drives the same drain.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authpages"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/deliveryhealth"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/memstore"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/outboxmem"
	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authgoth "github.com/gopernicus/gopernicus/pockets/authentication/views/goth"
	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/cms"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/menus"
	cmsgoth "github.com/gopernicus/gopernicus/pockets/cms/views/goth"
	eventspocket "github.com/gopernicus/gopernicus/pockets/events"
	"github.com/gopernicus/gopernicus/pockets/jobs"
	jobsmem "github.com/gopernicus/gopernicus/pockets/jobs/memstore"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
	"github.com/gopernicus/gopernicus/sdk/pocket"
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
	return uigoth.New(uigoth.Config{AssetBasePath: authAssetBasePath})
}

func main() {
	_ = environment.LoadEnv()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// Config comes from the environment through the sdk's struct tags: the literal
	// pre-seeds this host's own defaults, the environment wins over them, and an
	// empty value (KEY=) keeps what is already set. This host logs text by default.
	logOpts := logging.Options{Format: "text"}
	if err := environment.ParseEnvTags("", &logOpts); err != nil {
		return err
	}
	log := logging.New(logOpts)

	// The HTTP server config, read the same way. It is resolved here rather than at
	// the web.Run call because TrustProxies (below) and every host-derived URL take
	// their values from it.
	srv := web.ServerConfig{Port: defaultPort}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return err
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
	// key, so the two never disagree; an unrecognized value is auth.NewService's loud
	// construction error rather than a silent fallback to jobs.
	deliverySelection := auth.Config{DeliveryMode: auth.DeliveryModeJobs}
	if err := environment.ParseEnvTags("", &deliverySelection); err != nil {
		return err
	}
	mode := deliverySelection.DeliveryMode

	// Host operational health for delivery (AV3D-5.3): a secret-free, bounded, host-COMPOSED
	// surface (internal/deliveryhealth). It observes the runtime lifecycle (host-owned), the
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
	// (internal/authjobs) is the ONE place that imports BOTH pockets; neither pocket core
	// imports the other (constitution rule 6).
	var (
		deliveryJobs       *jobs.Service
		deliveryDispatcher *authjobs.Dispatcher
	)
	if mode == auth.DeliveryModeJobs {
		jobsCfg := jobs.Config{Logger: log}
		if err := environment.ParseEnvTags("", &jobsCfg); err != nil {
			return err
		}
		dj, err := jobs.NewService(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()}, jobsCfg)
		if err != nil {
			return err
		}
		deliveryJobs = dj
		deliveryDispatcher = authjobs.NewDispatcher(dj)
	}

	// Host-owned router + middleware. Both pockets and the host demo routes mount
	// onto this.
	router := web.NewWebHandler(web.WithLogging(log))
	// TrustProxies runs OUTER of every pocket mount so auth's inbound reads the
	// host-resolved client IP (web.ClientIP) instead of the spoofable leftmost
	// X-Forwarded-For hop. TRUSTED_PROXY_COUNT=0 (default) trusts only RemoteAddr,
	// so a forged X-Forwarded-For can no longer rotate rate-limit keys or poison
	// security-event audit rows; a proxied deployment sets it to its trusted-proxy
	// hop count.
	router.Use(web.TrustProxies(srv.TrustedProxyCount), web.RequestID(), web.Logger(log), web.Panics(log))

	// Serve the ui/goth fingerprinted assets (the auth pages' stylesheet) and the
	// externalized fragment-reader script the reset/magic-link landings load. Both are
	// same-origin, so the auth adapter's mapped CSP (style-src/script-src 'self')
	// covers them; the kit owns no route, so the host mounts them (ui-goth GOTH-7.2).
	uigothStatic := web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))
	uigothStatic.AddRoutes(router, authAssetBasePath)
	router.Handle(http.MethodGet, authgoth.DefaultFragmentScriptPath, authgoth.FragmentScriptHandler().ServeHTTP)

	// Shared in-process event bus (sdk default Memory). cms is the emitter (it
	// publishes content.* post-write through mount.Events); the host subscribes
	// below to invalidate the public-page cache. Delivery is async (O3): an
	// emitter's latency never depends on its slowest subscriber.
	bus := sdkevents.NewMemory(sdkevents.WithLogger(log))

	// The public-page cache, held in a variable (it previously flowed straight
	// into cms.Config.Cache) so the host's content-event subscriber can drop it.
	pageCache := cacher.NewMemory()

	mount := pocket.Mount{Router: router, Logger: log, Events: bus}

	// The authorization pocket (authorization-v1 Z4 commit 2 — the FLAGSHIP
	// posture), now GUARDED (AZ3-4.1): BOTH kinds wired, memstore-backed, so the host
	// stays zero-infra (no driver in the graph — GOWORK=off go list -m all still has no
	// libsql). newAuthorization composes the schema (manage_access declared), the
	// project-scoped guardian minimum, and the host MutationGuard (manage_access +
	// platform-admin over the DecisionView) — the testable composition seam run() and
	// the guarded-composition tests share.
	//
	// The bundled role-administration routes (/authorization/roles*) need a gate
	// composed from BOTH pockets, and the authorization pocket is built first (the
	// authorizer is an input to the auth config below). roleRoutesGate is that
	// ordering seam: named here, resolved per request, assigned once right after
	// authSvc exists and long before the host serves.
	roleRoutesGate := &deferredMiddleware{}
	authzComponents, err := newAuthorization(roleRoutesGate.middleware)
	if err != nil {
		return err
	}
	// Actor-facing writes are GUARDED (Config.Guard = hostMutationGuard): HTTP handlers
	// receive only the Service, and every actor-facing mutation is authorized inside the
	// atomic boundary. The advanced SystemMutator is held apart for the sensitive
	// boot owner/platform-admin seed. The baseline RelationshipWriter is separately
	// passed to the ordinary-member invitation adapter. Neither capability is
	// recoverable from Service or automatically exposed through HTTP.
	authorizer := authzComponents.Service
	systemMutator := authzComponents.SystemMutator
	relationshipWriter := authzComponents.RelationshipWriter
	if err := authorizer.Register(mount); err != nil {
		return err
	}
	// Bootstrap the ownable scope through the TRUSTED SystemMutator BEFORE serving:
	// establish project:demo#owner (the guardian minimum) and the platform:main#admin
	// data tuple, so the host runs under the ratified owner-minimum posture with an owner
	// already in place and invitation member-grants are never invariant-blocked
	// (member-first on a fresh protected resource is blocked by design). This replaces
	// the retired session-only POST /demo/admin/bootstrap route (AZ3-4.1): first owner is
	// inherently a trusted operation (it cannot yet prove it manages the resource).
	if err := seedAuthorization(ctx, systemMutator); err != nil {
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
	// host-local relationshipGranter over the authorization engine, carrying the host
	// resource-existence seam so acceptance against a deleted resource fails loudly.
	authCfg, err := buildAuthConfig(log, relationshipGranter{
		writer: relationshipWriter,
		reader: authorizer,
		exists: hostResources.Exists,
	})
	if err != nil {
		return err
	}
	// A Granter enables invitations, so the pocket REQUIRES a relation-aware host
	// authorization policy (auth.Config.InviteCheck, design D3) — the two are wired
	// together. hostInviteCheck consults the authorizer so a member-capable manager cannot
	// escalate by inviting an owner; a nil InviteCheck here would be ErrInviteCheckRequired
	// at NewService, never an allow-by-default.
	authCfg.InviteCheck = hostInviteCheck(authorizer)
	// The bundled machine-identity lifecycle routes (/auth/service-accounts*,
	// /auth/api-keys/{id}/revoke) mint and revoke credentials, so the pocket refuses to
	// guess a policy: with MachineRoutesGate nil they are NOT mounted (404) and NewService
	// WARNs. This host names the platform-admin coordinate already declared in authzSchema
	// (platform/admin on platform:main), so each route runs the pocket's
	// MachineLifecycle authenticator, then this gate — human credential, live
	// session, platform admin. Set here rather than
	// in buildAuthConfig because the gate is a method value on the authorizer, which the
	// composition seam does not receive (the DeliveryMode post-set precedent below).
	authCfg.MachineRoutesGate = authorizer.RequirePermissionFixed(platformResourceType, "admin", platformResourceID)
	// Apply the selected delivery mode to the auth config. buildAuthConfig returns the
	// jobs-mode posture (in-memory fenced queue on this host); AUTH_DELIVERY_MODE=in_process flips
	// it to the bounded EPHEMERAL pool here — and announces that posture LOUDLY. Neither mode
	// is durable on this proof host (both use in-memory stores).
	switch mode {
	case auth.DeliveryModeInProcess:
		authCfg.DeliveryMode = auth.DeliveryModeInProcess
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
	// authSvc.Register(mount).
	authSvc, err := auth.NewService(authRepos, authCfg)
	if err != nil {
		return err
	}
	if err := authSvc.Register(mount); err != nil {
		return err
	}
	// Close the role-routes ordering seam: the bundled /authorization/roles* surface
	// now runs behind the live human session plus the platform-admin permission — the
	// same coordinate MachineRoutesGate names. Assigned before the host serves, so no
	// request can reach the routes ahead of their gate (which fails closed anyway).
	roleRoutesGate.set(roleAdministrationGate(
		authSvc.RequireAccessTokenLive(),
		authorizer.RequirePermissionFixed(platformResourceType, "admin", platformResourceID),
	))
	// Boot fails LOUDLY if the chain never landed, matching the construction-matrix
	// posture the pockets already give this host: a gate is a security control, and
	// a refactor that reorders or drops the assignment must not reach production as
	// a per-request 500. The middleware's own fail-closed 500 stays the floor, not
	// the notification.
	if !roleRoutesGate.installed() {
		return errRoleRoutesGateNotInstalled
	}

	// In the bounded in_process mode the health surface reads the live queue depth from the
	// auth Service (a secret-free counts-only seam) so it can report backlog/saturation.
	if mode == auth.DeliveryModeInProcess {
		health.SetDepthSource(authSvc.InProcessQueueDepth)
	}

	// The delivery processor is fully attached now (its account resolver is this built
	// authSvc). In jobs mode, ONLY NOW read the registered job kind/handler seam and build
	// the jobs FencedRuntime over it (AV3D-3.1) — so no handler can run against a half-built
	// auth Service. In in_process mode the host runs authSvc.RunDelivery instead. The runtime
	// is built here but STARTED explicitly by the host, below.
	var deliveryFenced *jobs.FencedRuntime
	// deliveryPurge is the jobs-mode host-owned terminal-purge pass (IX-10). In in_process
	// mode it stays nil: that mode's queue is ephemeral and its latest-by-key status map is
	// already self-bounding (a finite max-entry count + TTL), so nothing accumulates to purge.
	var deliveryPurge func(context.Context) (int, error)
	var deliveryPurgeInterval time.Duration
	if mode == auth.DeliveryModeJobs {
		deliveryRuntime, ok := authSvc.DeliveryJobRuntime()
		if !ok {
			return fmt.Errorf("auth delivery job runtime unavailable: jobs-mode dispatcher not wired")
		}
		df, err := jobs.NewFencedRuntime(deliveryJobs, authjobs.FencedRuntimeConfig(deliveryRuntime,
			func(c *jobs.FencedRuntimeConfig) {
				c.Logger = log
				c.PollInterval = time.Second
				// Provider timeout safely inside the claim lease (AV3D-3.4): a stuck send is
				// cancelled well before the 30s default lease lapses and a second worker could
				// reclaim the job. NewFencedRuntime rejects a ProcessTimeout >= LeaseFor.
				c.ProcessTimeout = 20 * time.Second
			}))
		if err != nil {
			return err
		}
		deliveryFenced = df
		// Bind the bounded terminal-purge pass over the SAME jobs Service. Each pass removes
		// at most Batch terminal delivery rows older than the retention window and emits the
		// purged lifecycle observation (which the health surface counts). The host owns the
		// schedule/lifecycle below; the pocket purges nothing on its own.
		purgeCfg := deliveryPurgeConfigFromEnv(log)
		deliveryPurge = newDeliveryPurge(deliveryJobs, deliveryRuntime, purgeCfg, time.Now)
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
		AdminMiddleware: []web.Middleware{authSvc.RequireAccessToken()}, // auth gates cms's admin surface
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
	// (cacher.Pages keys pages "page:"+RequestURI, so "page:*" clears them all);
	// the next request re-renders fresh. Before this wiring the page was purely
	// TTL-bound: an edit within the 60s TTL kept serving stale bytes. Because cms
	// emits are async (O3), this runs shortly AFTER the admin write returns rather
	// than synchronously with it — a re-fetch trigger, not a transactional write.
	if _, err := bus.Subscribe("*", func(ctx context.Context, e sdkevents.Event) error {
		if !strings.HasPrefix(e.Type(), "content.") {
			return nil
		}
		return pageCache.DeletePattern(ctx, "page:*")
	}); err != nil {
		return err
	}

	// The events pocket's SSE gateway, best-effort/direct-emit (design §6 wiring
	// note): the SAME bus instance flows to both Mount.Events (cms is the emitter)
	// and events.Config.Bus (the gateway is the consumer) — one fan-out, no second
	// bus. A content.* frame fans out to any open stream the moment cms emits. The
	// gateway reads connect-time identity from sdk/foundation/identity, stashed by
	// authSvc.RequireAccessToken() on StreamMiddleware (A-I1 E2: no Identity field — absent
	// principal fails closed with 401). Repositories.Outbox nil ⇒ direct-emit mode
	// (no durable rail, no poller). Authorize is wired below through the
	// authorization ENGINE (the flagship posture — authorization-v1 Z4 commit 2),
	// so the resource-scoped /events/{resource_type}/{resource_id} route IS
	// registered. The subject stream lands at GET /events (host mounts at root, no
	// prefix — same as cms/auth).
	// Variant selection (design §8): the DEFAULT is direct-emit (Repositories.Outbox
	// nil — cms emits straight onto the bus). With EVENTS_OUTBOX=memory the host
	// instead wires an example-local in-memory outbox and drives a poller that
	// drains it onto the SAME bus — the durable at-least-once rail. Either way the
	// gateway is a plain bus consumer; only the emit path in front of the bus
	// changes.
	var eventsRepos eventspocket.Repositories
	var outboxStore *outboxmem.Store
	if durableOutbox() {
		outboxStore = outboxmem.New()
		eventsRepos = eventspocket.Repositories{Outbox: outboxStore}
	}
	eventsCfg := eventspocket.Config{
		Bus:              bus,
		StreamMiddleware: []web.Middleware{authSvc.RequireAccessToken()},
		// Authorize (the FLAGSHIP posture — authorization-v1 Z4 commit 2): the SAME
		// events Check seam, now backed by the authorization ENGINE instead of the
		// retired toy map (commit 1). The host stays zero-infra (the authorizer is
		// memstore-backed — no libsql). A non-nil Authorize registers the
		// resource-scoped GET /events/{resource_type}/{resource_id} route; the
		// closure maps the stream's identity.Principal onto an authorization.PrincipalRef
		// unadapted and asks the engine for the `view` permission on the (type, id).
		Authorize: func(ctx context.Context, p identity.Principal, resourceType, resourceID string) (bool, error) {
			res, err := authorizer.Check(ctx, authorization.CheckRequest{
				Principal:  authorization.PrincipalRef{Type: p.Type, ID: p.ID},
				Permission: demoPermission,
				Resource:   authorization.Resource{Type: resourceType, ID: resourceID},
			})
			return res.Allowed, err
		},
	}
	// EVENTS_HEARTBEAT / EVENTS_BUFFER_SIZE / EVENTS_MAX_CONN_AGE /
	// EVENTS_MAX_CONNS_PER_SUBJECT tune the gateway; unset keeps the pocket defaults.
	if err := environment.ParseEnvTags("", &eventsCfg); err != nil {
		return err
	}
	eventsSvc, err := eventspocket.NewService(eventsRepos, eventsCfg)
	if err != nil {
		return err
	}
	if err := eventsSvc.Register(mount); err != nil {
		return err
	}

	// Durable-outbox variant plumbing (EVENTS_OUTBOX=memory): the host owns the
	// poller lifecycle (the pocket owns no goroutines — D4). The poller runs on
	// an sdk/foundation/workers pool woken by the canonical append-then-signal pattern
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
		poller := eventspocket.NewPoller(outboxStore, bus)
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
			"outbox", "in-memory (internal/outboxmem)", "trigger", "POST /outbox-demo")
	}

	// Host-local demo + debug routes (host code, not pocket surface). The demo routes
	// are READ-ONLY (AZ3-4.1): the session-only authorization-mutation routes
	// (POST /demo/roles/{assign,unassign}, POST /demo/admin/bootstrap) were REMOVED — no
	// shipped HTTP route mutates authorization with session presence alone. Trusted
	// seeding runs at boot (seedAuthorization) and ordinary invitation acceptance rides
	// the baseline RelationshipWriter (membership.go); the guarded actor path is proven by
	// authorization_test.go, not a browser flow.
	registerDemoRoutes(router, authSvc, authorizer)
	registerDebugRoutes(router, authSvc, authRepos, log)

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
	// generic-jobs FencedRuntime (AV3D-3.1); in_process mode runs authSvc.RunDelivery (the
	// bounded ephemeral pool, AV3D-4.1). It runs on its OWN Background-derived context (never
	// the parent ctx) so shutdown stops it AFTER HTTP has drained, mirroring the poller order
	// below. MarkStarted/MarkStopped bracket the goroutine so the health surface reports
	// not-started vs running.
	var deliveryRun func(context.Context) error
	switch mode {
	case auth.DeliveryModeInProcess:
		deliveryRun = authSvc.RunDelivery
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
	supervisor := superviseDelivery(deliveryCtx, cancelHost, deliveryRun, health, log)

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

	// Shutdown order (design §7, phase 5 — with the poller, corrected context idiom
	// P3):
	//  1. web.Run blocks until hostCtx is canceled (by the signal ctx OR by the delivery
	//     supervisor on an unexpected runtime exit), then drains in-flight HTTP on its
	//     OWN fresh Background+ShutdownTimeout context (run.go), closing every open
	//     SSE stream via its request context. By the time web.Run returns, hostCtx is
	//     already canceled.
	//  2. THEN stop the poller pool. It runs on its OWN Background-derived context
	//     (never the parent ctx — a canceled parent would tear it down before HTTP
	//     finished draining), so cancel that context now and wait, bounded, for the
	//     in-flight batch to finish.
	//  3. Close the bus LAST, on a FRESH bounded context (a canceled parent ctx
	//     would make Memory.Close drain nothing). Closing after the poller stops is
	//     why the poller's closed-bus edge (Poll emitting into a closed bus) never
	//     happens.
	runErr := web.Run(hostCtx, router, srv, log)

	// Stop the delivery runtime after HTTP drains (its own context, like the poller).
	log.InfoContext(context.Background(), "stopping delivery runtime")
	cancelDelivery()
	if !supervisor.wait(5 * time.Second) {
		log.WarnContext(context.Background(), "delivery runtime did not stop within 5s")
	}
	log.InfoContext(context.Background(), "delivery runtime stopped")
	// If the delivery runtime exited unexpectedly (it drove this shutdown, not a signal),
	// surface its cause so run returns nonzero even though web.Run drained cleanly.
	if runErr == nil {
		runErr = supervisor.exitErr()
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
var ids = cryptids.IDGenerator{}

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
//   - every development secret (JWT signer, challenge pepper, delivery + identifier +
//     token-encryption keys) from a DISTINCT env var, never a committed constant, and
//     never printing key material (see demo.go builders).
//
// The granter is the invitation grant-on-accept seam (nil → invitations off). When run()
// passes a non-nil Granter it also sets the REQUIRED relation-aware auth.Config.InviteCheck
// (hostInviteCheck) right after this seam returns — the two are wired together (design D3).
// It builds no goroutines and reads no host lifecycle; run() owns the worker + shutdown.
func buildAuthConfig(log *slog.Logger, granter auth.Granter) (auth.Config, error) {
	// The REQUIRED access-JWT signer, optional provider-token encrypter, REQUIRED
	// challenge protector (authmem wires Challenges), REQUIRED delivery-outbox
	// encrypter (jobs mode seals every command envelope), and identifier keyer — each
	// from its own distinct env var (demo.go).
	signer, err := buildTokenSigner(log)
	if err != nil {
		return auth.Config{}, err
	}
	encrypter, err := buildTokenEncrypter()
	if err != nil {
		return auth.Config{}, err
	}
	challengeProtector, err := buildChallengeProtector(log)
	if err != nil {
		return auth.Config{}, err
	}
	deliveryEncrypter, err := buildDeliveryEncrypter(log)
	if err != nil {
		return auth.Config{}, err
	}
	identifierKeyer, err := buildIdentifierKeyer(log)
	if err != nil {
		return auth.Config{}, err
	}

	// The ui/goth HTML surface: build the presentation bundle, then this host's
	// partial override (authpages.New) which embeds the ui/goth Views and overrides
	// only Login with Gopernicus-CMS branding. The embedded Views promotes HTMLPolicy(),
	// which maps the bundle's browser Requirements (+ the fragment-reader script-src)
	// into the pocket's CSP so the auth pages load their assets under default-src
	// 'none' — the host never hand-writes that CSP (ui-goth GOTH-7.2).
	bundle, err := newAuthBundle()
	if err != nil {
		return auth.Config{}, err
	}
	authViews, err := authpages.New(bundle)
	if err != nil {
		return auth.Config{}, err
	}

	// This host's own externally visible origin, resolved from the SAME
	// web.ServerConfig tags run() serves on (PUBLIC_BASE_URL, else http://HOST:PORT).
	// Every host-DERIVED default below — the OAuth callback base, the magic-link and
	// password-reset landings, and the Origin-allowlist fallback — is pre-seeded from
	// it, so an environment that leaves those keys empty keeps them.
	srv := web.ServerConfig{Port: defaultPort}
	if err := environment.ParseEnvTags("", &srv); err != nil {
		return auth.Config{}, err
	}
	origin := srv.Origin()

	cfg := auth.Config{
		Hasher:               bcrypt.New(),
		Mailer:               email.NewConsole(log),
		MailFrom:             "auth@localhost",
		RequireVerifiedEmail: true,
		RuntimeMode:          auth.RuntimeModeDevelopment,
		// Delivery on the generic jobs runtime (authv3-delivery-refactor
		// AV3D-0.1). run() wires the generic-jobs dispatcher (authCfg.DeliveryDispatcher)
		// over an in-memory fenced queue for this demo, so it is NON-DURABLE here (a real
		// durable posture is a pgx/turso FencedQueue store swap); the production-negative
		// matrix proves the SAME wiring flipped to production fails closed on an
		// unacknowledged runtime.
		DeliveryMode:       auth.DeliveryModeJobs,
		ChallengeProtector: challengeProtector,
		DeliveryEncrypter:  deliveryEncrypter,
		IdentifierKeyer:    identifierKeyer,
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
		// The DISTINCT second override system (design §6.2): a host email LayerApp
		// content override that rebrands the verification email body. It swaps an EMAIL,
		// not a page — a different facility from Views, wired through a different Config
		// field into the delivery router. The code ({{.Secret}}) still renders, so the
		// verification flow is unbroken; only the copy is host-branded.
		EmailContentTemplates: []auth.EmailContentTemplate{authpages.EmailOverride()},
		// The exact-match Origin allowlist the browser-safe mutation gate validates
		// cookie-authenticated sensitive mutations and HTML form posts against; defaults
		// to this host's own origin (design §9.1), overridden by AUTH_ALLOWED_ORIGINS.
		AllowedOrigins: []string{origin},
		// Passwordless login for email + phone (design §4.2): magic link + OTP. Each
		// listed kind needs a wired delivery channel (email via the Mailer, phone via the
		// console Notifier) or construction fails LOUDLY; AUTH_PASSWORDLESS narrows it.
		Passwordless: []string{identity.KindEmail, identity.KindPhone},
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
		// The queue is the only send path; affirm run() runs the generic-jobs delivery
		// runtime (jobs.FencedRuntime) (authv3-delivery-refactor AV3D-0.1).
		DeliveryJobsAcknowledged: true,
		Providers:                []oauth.Provider{fakeOAuthProvider{}},
		OAuthCallbackBase:        origin,
		// Absolute post-flow destinations only; safe same-origin relative paths
		// (e.g. the account page's ?redirect=/auth/account) need no entry.
		RedirectAllowlist: []string{"/"},
		TokenEncrypter:    encrypter,
		TokenSigner:       signer,
		// AccessTokenTTL / RefreshTTL are left zero: AUTH_ACCESS_TOKEN_TTL and
		// AUTH_REFRESH_TTL carry the pocket's own tag defaults (15m / 168h), which are
		// the values the pocket resolves a zero field to anyway.
		Granter: granter,
		// The phone-kind console notifier makes phone a supported delivery kind
		// (deny-by-absence; the dev stand-in for SMS — the token lands in the log).
		Notifiers: []notify.Notifier{notify.NewConsole(identity.KindPhone, log)},
		Logger:    log,
	}
	// The pocket's own env tags, applied over the literal: AUTH_MAIL_FROM,
	// AUTH_ALLOWED_ORIGINS, AUTH_PASSWORDLESS, AUTH_PUBLIC_BASE_URL,
	// AUTH_PASSWORD_RESET_URL, AUTH_OAUTH_LINK_URL, AUTH_OAUTH_CALLBACK_BASE,
	// AUTH_REDIRECT_ALLOWLIST, AUTH_REQUIRE_VERIFIED_EMAIL, AUTH_RUNTIME_MODE,
	// AUTH_DELIVERY_MODE, AUTH_ACCESS_TOKEN_TTL, AUTH_REFRESH_TTL, AUTH_LIST_STRATEGY,
	// the AUTH_COOKIE_* nested cookie keys, and the remaining flags. An empty value
	// keeps what the literal seeded; an unrecognized mode or strategy is the pocket's
	// loud construction error.
	if err := environment.ParseEnvTags("", &cfg); err != nil {
		return auth.Config{}, err
	}
	return cfg, nil
}
