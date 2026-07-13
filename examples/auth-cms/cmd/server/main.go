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
// order is HTTP server -> poller pool -> bus.Close (see run's tail comment).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authpages"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/memstore"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/outboxmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	authorization "github.com/gopernicus/gopernicus/features/authorization"
	authzmem "github.com/gopernicus/gopernicus/features/authorization/memstore"
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	cmstempl "github.com/gopernicus/gopernicus/features/cms/views/templ"
	eventsfeature "github.com/gopernicus/gopernicus/features/events"
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
	// posture): BOTH kinds wired, memstore-backed, so the host stays zero-infra (no
	// driver in the graph — GOWORK=off go list -m all still has no libsql). The
	// relationship kind's schema declares a `project` resource type (owner/member
	// relations, view = AnyOf(owner, member)) and a `platform` resource type whose
	// `admin` relation backs the platform-admin DATA tuple (platform-admin is data,
	// never Config). The roles kind (demo.go) needs no model. Register logs only.
	authzModel := authorization.NewSchema([]authorization.ResourceSchema{
		{Name: demoResourceType, Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				demoPermission: authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("member")),
			},
		}},
		{Name: "platform", Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			// The `admin` permission makes platform-admin an ordinary
			// schema-declared check the host runs first in its own Check
			// closure (see requireMembership / demoMyProjects). The engine no
			// longer bypasses on this tuple — the host composes it.
			Permissions: map[string]authorization.PermissionRule{
				"admin": authorization.AnyOf(authorization.Direct("admin")),
			},
		}},
	})
	authorizer, err := authorization.NewService(authorization.Repositories{
		Relationships: authzmem.NewRelationships(),
		Roles:         authzmem.NewRoles(),
	}, authorization.Config{Model: authzModel})
	if err != nil {
		return err
	}
	if err := authorizer.Register(mount); err != nil {
		return err
	}

	// Auth config, assembled in the testable composition seam buildAuthConfig
	// (AV3-8.6): development posture, bundled templ Views, browser-safe Origin
	// allowlist, passwordless enablement, magic-link base URL, and every development
	// secret from a distinct env var. The invitation grant-on-accept seam is the
	// host-local relationshipGranter over the authorization engine.
	authCfg, err := buildAuthConfig(log, relationshipGranter{authorizer: authorizer})
	if err != nil {
		return err
	}

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
		// closure maps the stream's identity.Principal onto an authorization.Subject
		// unadapted and asks the engine for the `view` permission on the (type, id).
		Authorize: func(ctx context.Context, p identity.Principal, resourceType, resourceID string) (bool, error) {
			res, err := authorizer.Check(ctx, authorization.CheckRequest{
				Subject:    authorization.Subject{Type: p.Type, ID: p.ID},
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

	// Host-local demo + debug routes (host code, not feature surface).
	registerDemoRoutes(router, authSvc, authorizer)
	registerDebugRoutes(router, authSvc, authRepos, log)

	// Host-local liveness probe (host route, not feature surface). Mounted on
	// the root router with no middleware, outside every gated group and
	// unwrapped by RequireUser — unauthenticated by design, since a readiness
	// probe can't log in.
	router.Handle(http.MethodGet, "/healthz", healthzHandler())

	// The durable delivery worker (design §6.1.1) drains the auth outbox off the
	// request path — the host owns its lifecycle (the feature starts no goroutine).
	// It runs on its OWN Background-derived context (never the parent ctx) so
	// shutdown stops it AFTER HTTP has drained, mirroring the poller order below.
	deliveryCtx, cancelDelivery := context.WithCancel(context.Background())
	deliveryDone := make(chan struct{})
	go func() {
		defer close(deliveryDone)
		if err := authSvc.RunDeliveryWorker(deliveryCtx); err != nil {
			log.ErrorContext(deliveryCtx, "delivery worker stopped with error", "error", err)
		}
	}()

	// Shutdown order (design §7, phase 5 — with the poller, corrected context idiom
	// P3):
	//  1. web.Run blocks until ctx is canceled, then drains in-flight HTTP on its
	//     OWN fresh Background+ShutdownTimeout context (run.go), closing every open
	//     SSE stream via its request context. By the time web.Run returns, the
	//     parent ctx is already canceled.
	//  2. THEN stop the poller pool. It runs on its OWN Background-derived context
	//     (never the parent ctx — a canceled parent would tear it down before HTTP
	//     finished draining), so cancel that context now and wait, bounded, for the
	//     in-flight batch to finish.
	//  3. Close the bus LAST, on a FRESH bounded context (a canceled parent ctx
	//     would make Memory.Close drain nothing). Closing after the poller stops is
	//     why the poller's closed-bus edge (Poll emitting into a closed bus) never
	//     happens.
	runErr := web.Run(ctx, router, serverConfig(), log)

	// Stop the delivery worker after HTTP drains (its own context, like the poller).
	log.InfoContext(context.Background(), "stopping delivery worker")
	cancelDelivery()
	select {
	case <-deliveryDone:
	case <-time.After(5 * time.Second):
		log.WarnContext(context.Background(), "delivery worker did not stop within 5s")
	}
	log.InfoContext(context.Background(), "delivery worker stopped")

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
//   - DeliveryWorkerAcknowledged: the outbox is the only send path (AV3-4.3), so the
//     feature is told run() actually runs RunDeliveryWorker (it does, below); and
//   - every development secret (JWT signer, challenge pepper, delivery + identifier +
//     token-encryption keys) from a DISTINCT env var, never a committed constant, and
//     never printing key material (see demo.go builders).
//
// The granter is the invitation grant-on-accept seam (nil → invitations off). It
// builds no goroutines and reads no host lifecycle; run() owns the worker + shutdown.
func buildAuthConfig(log *slog.Logger, granter auth.Granter) (auth.Config, error) {
	// The REQUIRED access-JWT signer, optional provider-token encrypter, REQUIRED
	// challenge protector (authmem wires Challenges), REQUIRED delivery-outbox
	// encrypter (authmem wires DeliveryJobs), and identifier keyer — each from its
	// own distinct env var (demo.go).
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
		ChallengeProtector:   challengeProtector,
		DeliveryEncrypter:    deliveryEncrypter,
		IdentifierKeyer:      identifierKeyer,
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
		// The outbox is the only send path; affirm run() runs the worker (design §8).
		DeliveryWorkerAcknowledged: true,
		Providers:                  []oauth.Provider{fakeOAuthProvider{}},
		OAuthCallbackBase:          callbackBase(),
		RedirectAllowlist:          []string{"/"},
		TokenEncrypter:             encrypter,
		TokenSigner:                signer,
		AccessTokenTTL:             accessTokenTTL(),
		RefreshTTL:                 refreshTTL(),
		Granter:                    granter,
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
