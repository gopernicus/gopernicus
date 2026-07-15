// Command server is the auth-v2 A9 / events-v1 proof host: it mounts
// features/cms, features/authentication, AND features/events onto one host
// router, with in-memory stores and no datastore driver in its module graph
// (verify: `GOWORK=off go list -m all | grep -i libsql` is empty). The host is
// the only party that imports the features — no feature imports another
// (constitution rule 6); the cross-feature flow rides sdk vocabulary
// (sdk/foundation/web.Middleware, sdk/foundation/identity, sdk/capabilities/events) the host wires between them.
//
// The cross-feature wiring is the point: cms's admin surface (the CRUD routes)
// is gated by auth's identity middleware via cms.Config.AdminMiddleware ←
// authSvc.RequireUser. Neither feature imports the other; structural typing on
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
// posture (Z4 commit 2): invitation-accept writes a real ReBAC tuple via
// authorizer.CreateRelationships, retiring the A9 toy membership map; the
// memstore-backed engine keeps the host zero-infra (no libsql). The host-local
// demo routes (demo.go) are gated variously on a resolved principal, an engine
// Check, a LookupResources enumeration, and a roles-kind HasRole check.
//
// features/events adds the SSE gateway at GET /events (authenticated via
// authSvc.RequireUser on StreamMiddleware): a cms edit fans out as a
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authjobs"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/deliveryhealth"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authpages"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/memstore"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/outboxmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	authorization "github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	cmstempl "github.com/gopernicus/gopernicus/features/cms/views/templ"
	eventsfeature "github.com/gopernicus/gopernicus/features/events"
	"github.com/gopernicus/gopernicus/features/jobs"
	jobsmem "github.com/gopernicus/gopernicus/features/jobs/memstore"
	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
	"github.com/gopernicus/gopernicus/sdk/foundation/logging"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/foundation/workers"
)

func main() {
	_ = environment.LoadEnv()

	log := logging.New(logging.Options{
		Level:  environment.GetEnvOrDefault("LOG_LEVEL", "INFO"),
		Format: environment.GetEnvOrDefault("LOG_FORMAT", "text"),
		Output: environment.GetEnvOrDefault("LOG_OUTPUT", "STDERR"),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.ErrorContext(ctx, "server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
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

	// Delivery mode selection (authv3-delivery-refactor AV3D-5.3). DELIVERY_MODE selects
	// the host's outbound-delivery composition: the generic-jobs-mode wiring (default) or the
	// bounded in-process mode. On THIS proof host BOTH are non-durable — jobs mode backs its
	// fenced queue with jobsmem.NewFencedQueue (in-memory), so a real durable posture is a
	// store swap a production host makes (features/jobs/stores/{pgx,turso}). The bounded mode
	// is never hidden — it announces its crash-loss + per-process posture LOUDLY at startup
	// (see the WARN below where the config is flipped).
	mode := deliveryMode()

	// Host operational health for delivery (AV3D-5.3): a secret-free, bounded, host-COMPOSED
	// surface (internal/deliveryhealth). It observes the runtime lifecycle (host-owned), the
	// secret-free delivery lifecycle events (wrapping Config.DeliveryEventsEmitter), jobs-mode
	// admissions (wrapping Config.DeliveryDispatcher), and the in_process queue depth (the
	// auth Service's InProcessQueueDepth read). It carries counters/gauges/enums only — never
	// a recipient, payload, or logical key. The host mounts it at GET /healthz/delivery.
	health := deliveryhealth.New(string(mode))

	// Outbound delivery on the generic jobs feature (authv3-delivery-refactor
	// AV3D-3.1), wired ONLY in jobs mode: authentication submits encrypted
	// delivery commands to a generic jobs fenced queue, and the host runs the jobs
	// FencedRuntime that invokes auth's delivery processor. The in-memory fenced queue
	// (jobsmem.NewFencedQueue) is the zero-infra stand-in, so jobs mode is NON-DURABLE here —
	// queued work is lost on restart with no cross-instance coordination; a durable posture is
	// a pgx/turso FencedQueue store swap a real host makes. The composition adapter
	// (internal/authjobs) is the ONE place that imports BOTH features; neither feature core
	// imports the other (constitution rule 6).
	var (
		deliveryJobs       *jobs.Service
		deliveryDispatcher *authjobs.Dispatcher
	)
	if mode == auth.DeliveryModeJobs {
		dj, err := jobs.NewService(jobs.Repositories{FencedQueue: jobsmem.NewFencedQueue()}, jobs.Config{Logger: log})
		if err != nil {
			return err
		}
		deliveryJobs = dj
		deliveryDispatcher = authjobs.NewDispatcher(dj)
	}

	// Host-owned router + middleware. Both features and the host demo routes mount
	// onto this.
	router := web.NewWebHandler(web.WithLogging(log))
	// TrustProxies runs OUTER of every feature mount so auth's inbound reads the
	// host-resolved client IP (web.ClientIP) instead of the spoofable leftmost
	// X-Forwarded-For hop. TRUSTED_PROXY_COUNT=0 (default) trusts only RemoteAddr,
	// so a forged X-Forwarded-For can no longer rotate rate-limit keys or poison
	// security-event audit rows; a proxied deployment sets it to its trusted-proxy
	// hop count.
	router.Use(web.TrustProxies(trustedProxyCount()), web.RequestID(), web.Logger(log), web.Panics(log))

	// Shared in-process event bus (sdk default Memory). cms is the emitter (it
	// publishes content.* post-write through mount.Events); the host subscribes
	// below to invalidate the public-page cache. Delivery is async (O3): an
	// emitter's latency never depends on its slowest subscriber.
	bus := sdkevents.NewMemory(sdkevents.WithLogger(log))

	// The public-page cache, held in a variable (it previously flowed straight
	// into cms.Config.Cache) so the host's content-event subscriber can drop it.
	pageCache := cacher.NewMemory()

	mount := feature.Mount{Router: router, Logger: log, Events: bus}

	// The authorization feature (authorization-v1 Z4 commit 2 — the FLAGSHIP
	// posture), now GUARDED (AZ3-4.1): BOTH kinds wired, memstore-backed, so the host
	// stays zero-infra (no driver in the graph — GOWORK=off go list -m all still has no
	// libsql). newAuthorization composes the schema (manage_access declared), the
	// project-scoped guardian minimum, and the host MutationGuard (manage_access +
	// platform-admin over the DecisionView) — the testable composition seam run() and
	// the guarded-composition tests share.
	authzComponents, err := newAuthorization()
	if err != nil {
		return err
	}
	// Actor-facing writes are GUARDED (Config.Guard = hostMutationGuard): HTTP handlers
	// receive only the Service, and every actor-facing mutation is authorized inside the
	// atomic boundary. The trusted SystemMutator is held apart and passed deliberately to
	// the trusted seams (the boot owner/platform-admin seed and invitation
	// grant-on-accept) — never to an actor-facing gate. Ordinary host code (the
	// authorizer below) has no raw write and no constructible system actor
	// (authorization_test.go pins both).
	authorizer := authzComponents.Service
	systemMutator := authzComponents.SystemMutator
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

	// Auth config, assembled in the testable composition seam buildAuthConfig
	// (AV3-8.6): development posture, bundled templ Views, browser-safe Origin
	// allowlist, passwordless enablement, magic-link base URL, and every development
	// secret from a distinct env var. The invitation grant-on-accept seam is the
	// host-local relationshipGranter over the authorization engine.
	authCfg, err := buildAuthConfig(log, relationshipGranter{system: systemMutator})
	if err != nil {
		return err
	}
	// Apply the selected delivery mode to the auth config. buildAuthConfig returns the
	// jobs-mode posture (in-memory fenced queue on this host); DELIVERY_MODE=in_process flips
	// it to the bounded EPHEMERAL pool here — and announces that posture LOUDLY. Neither mode
	// is durable on this proof host (both use in-memory stores).
	switch mode {
	case auth.DeliveryModeInProcess:
		authCfg.DeliveryMode = auth.DeliveryModeInProcess
		authCfg.DeliveryJobsAcknowledged = false
		authCfg.DeliveryEphemeralAcknowledged = true
		authCfg.DeliveryDispatcher = nil // in_process owns its bounded pool; no dispatcher
		log.WarnContext(ctx, "DELIVERY_MODE=in_process: EPHEMERAL bounded delivery selected — "+
			"accepted in-flight work is LOST on crash or restart, there is NO cross-instance "+
			"coordination, and running multiple instances de-duplicates on NEITHER (a user may "+
			"receive duplicate messages). DELIVERY_MODE=jobs on this proof host is ALSO in-memory "+
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

	// authSvc is the auth feature's driving surface (FS2): its RequireUser method
	// value is the middleware cms gates its admin routes on, and RequirePrincipal/
	// CurrentPrincipal back the host demo routes. The feature's own HTTP routes are
	// the optional adapter over that surface — built once here, mounted once via
	// authSvc.Register(mount).
	authSvc, err := auth.NewService(authRepos, authCfg)
	if err != nil {
		return err
	}
	if err := authSvc.Register(mount); err != nil {
		return err
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
		// schedule/lifecycle below; the feature purges nothing on its own.
		purgeCfg := deliveryPurgeConfigFromEnv(log)
		deliveryPurge = newDeliveryPurge(deliveryJobs, deliveryRuntime, purgeCfg, time.Now)
		deliveryPurgeInterval = purgeCfg.Interval
	}

	if err := cms.Register(mount, cmsRepos, cms.Config{
		Views:           cmstempl.New(),                          // FS3 one-line default: the bundled views module
		Types:           []content.ContentType{productType()},    // host-registered custom type (zero migration)
		Templates:       []cms.TemplateBinding{productBinding()}, // its dev-authored renderer
		Cache:           pageCache,
		Mailer:          email.NewConsole(log),
		MailFrom:        "cms@localhost",
		ContactTo:       "ops@localhost",
		AdminMiddleware: []web.Middleware{authSvc.RequireUser}, // auth gates cms's admin surface
	}); err != nil {
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

	// The events feature's SSE gateway, best-effort/direct-emit (design §6 wiring
	// note): the SAME bus instance flows to both Mount.Events (cms is the emitter)
	// and events.Config.Bus (the gateway is the consumer) — one fan-out, no second
	// bus. A content.* frame fans out to any open stream the moment cms emits. The
	// gateway reads connect-time identity from sdk/foundation/identity, stashed by
	// authSvc.RequireUser on StreamMiddleware (A-I1 E2: no Identity field — absent
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
	var eventsRepos eventsfeature.Repositories
	var outboxStore *outboxmem.Store
	if durableOutbox() {
		outboxStore = outboxmem.New()
		eventsRepos = eventsfeature.Repositories{Outbox: outboxStore}
	}
	eventsSvc, err := eventsfeature.NewService(eventsRepos, eventsfeature.Config{
		Bus:              bus,
		StreamMiddleware: []web.Middleware{authSvc.RequireUser},
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
	})
	if err != nil {
		return err
	}
	if err := eventsSvc.Register(mount); err != nil {
		return err
	}

	// Durable-outbox variant plumbing (EVENTS_OUTBOX=memory): the host owns the
	// poller lifecycle (the feature owns no goroutines — D4). The poller runs on
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
		poller := eventsfeature.NewPoller(outboxStore, bus)
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

	// Host-local demo + debug routes (host code, not feature surface). The demo routes
	// are READ-ONLY (AZ3-4.1): the session-only authorization-mutation routes
	// (POST /demo/roles/{assign,unassign}, POST /demo/admin/bootstrap) were REMOVED — no
	// shipped HTTP route mutates authorization with session presence alone. Trusted
	// seeding runs at boot (seedAuthorization) and invitation acceptance rides the
	// SystemMutator (membership.go); the guarded actor path is proven by
	// authorization_test.go, not a browser flow.
	registerDemoRoutes(router, authSvc, authorizer)
	registerDebugRoutes(router, authSvc, authRepos, log)

	// Host-local liveness probe (host route, not feature surface). Mounted on
	// the root router with no middleware, outside every gated group and
	// unwrapped by RequireUser — unauthenticated by design, since a readiness
	// probe can't log in.
	router.Handle(http.MethodGet, "/healthz", healthzHandler())

	// Host-local delivery operational health (AV3D-5.3): a secret-free, bounded status
	// surface distinguishing runtime not-started/running, backlog/saturation, provider retry
	// + dead-letter activity, and observer emit failures. Counters/gauges/enums only — no
	// recipient, payload, or logical key. Unauthenticated like /healthz (an operator probe
	// cannot log in); it exposes nothing sensitive.
	router.Handle(http.MethodGet, "/healthz/delivery", health.Handler())

	// The selected delivery runtime drains the auth delivery queue off the request path —
	// the host owns its lifecycle (the feature starts no goroutine). Jobs mode runs the
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
	runErr := web.Run(hostCtx, router, serverConfig(), log)

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

// deliveryMode selects the host's outbound-delivery composition from DELIVERY_MODE
// (authv3-delivery-refactor AV3D-5.3). The default (unset or "jobs") is the generic-jobs-mode
// wiring — non-durable on this proof host (in-memory fenced queue). "in_process" selects the
// bounded EPHEMERAL pool, whose crash-loss + per-process posture run() announces LOUDLY at
// startup. Any other value falls back to jobs — fail-safe: the host never silently selects a
// different mode. (Neither mode is durable here; durability is a pgx/turso store swap.)
func deliveryMode() auth.DeliveryMode {
	if environment.GetEnvOrDefault("DELIVERY_MODE", "") == "in_process" {
		return auth.DeliveryModeInProcess
	}
	return auth.DeliveryModeJobs
}

// trustedProxyCount is the number of trusted reverse proxies in front of the
// server, from TRUSTED_PROXY_COUNT. Default 0 (unset/invalid) trusts only
// RemoteAddr — the safe default for a directly-exposed host.
func trustedProxyCount() int {
	v := environment.GetEnvOrDefault("TRUSTED_PROXY_COUNT", "")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// outboxDemoHandler is the host-owned demo trigger for the EVENTS_OUTBOX=memory
// variant (the jobs-minimal POST /enqueue precedent): it appends one record to
// the example-local outbox, then wakes the poller with the canonical
// append-then-signal pattern (gate edit 2) so the drain runs promptly instead of
// waiting out the pool's idle interval. cms itself never touches the outbox (O2)
// — this is a host route, not feature surface. The frame that reaches the open
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

// healthzHandler is this host's liveness probe. Both feature stores are
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
		{"Two features, one host", "auth gates cms's admin surface with zero cross-import.", "features/cms never imports features/authentication; only this host's main imports both — constitution rule 6, proved with two real feature modules."},
		{"Bring your own stores", "Both features run on in-memory stores; no libsql in the graph.", "Swap datastores without forking a feature — the whole point of the module split."},
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

// buildAuthConfig assembles the authentication feature's Config for this proof host
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
//     (AV3-4.3), so the feature is told run() actually runs the generic-jobs delivery
//     runtime (jobs.FencedRuntime, below); and
//   - every development secret (JWT signer, challenge pepper, delivery + identifier +
//     token-encryption keys) from a DISTINCT env var, never a committed constant, and
//     never printing key material (see demo.go builders).
//
// The granter is the invitation grant-on-accept seam (nil → invitations off). It
// builds no goroutines and reads no host lifecycle; run() owns the worker + shutdown.
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

	return auth.Config{
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
		// (authpages.New) embeds the bundled templ Views and overrides only the Login
		// page with Gopernicus-CMS branding — presentation changes only, the JSON API
		// and every route/service/redirect policy are unchanged (AV3-8.9, proven
		// isolation-safe in AV3-8.5). Every non-overridden page is the promoted default.
		Views: authpages.New(),
		// The DISTINCT second override system (design §6.2): a host email LayerApp
		// content override that rebrands the verification email body. It swaps an EMAIL,
		// not a page — a different facility from Views, wired through a different Config
		// field into the delivery router. The code ({{.Secret}}) still renders, so the
		// verification flow is unbroken; only the copy is host-branded.
		EmailContentTemplates: []auth.EmailContentTemplate{authpages.EmailOverride()},
		// The exact-match Origin allowlist the browser-safe mutation gate validates
		// cookie-authenticated sensitive mutations and HTML form posts against; defaults
		// to this host's own origin (design §9.1).
		AllowedOrigins: allowedOrigins(),
		// Passwordless login for email + phone (design §4.2): magic link + OTP.
		Passwordless: passwordlessKinds(),
		// The magic-link / redemption-page base URL (design §6.4), config-only.
		PublicAuthBaseURL: publicAuthBaseURL(),
		// The queue is the only send path; affirm run() runs the generic-jobs delivery
		// runtime (jobs.FencedRuntime) (authv3-delivery-refactor AV3D-0.1).
		DeliveryJobsAcknowledged: true,
		Providers:                []oauth.Provider{fakeOAuthProvider{}},
		OAuthCallbackBase:        callbackBase(),
		RedirectAllowlist:        []string{"/"},
		TokenEncrypter:           encrypter,
		TokenSigner:              signer,
		AccessTokenTTL:           accessTokenTTL(),
		RefreshTTL:               refreshTTL(),
		Granter:                  granter,
		// The phone-kind console notifier makes phone a supported delivery kind
		// (deny-by-absence; the dev stand-in for SMS — the token lands in the log).
		Notifiers: []notify.Notifier{notify.NewConsole(identity.KindPhone, log)},
		Logger:    log,
	}, nil
}

func serverConfig() web.ServerConfig {
	return web.ServerConfig{
		Host:            environment.GetEnvOrDefault("HOST", "localhost"),
		Port:            environment.GetEnvOrDefault("PORT", "8082"),
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}
