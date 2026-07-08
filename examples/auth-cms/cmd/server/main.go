// Command server is the auth-v2 A9 / events-v1 proof host: it mounts
// features/cms, features/authentication, AND features/events onto one host
// router, with in-memory stores and no datastore driver in its module graph
// (verify: `GOWORK=off go list -m all | grep -i libsql` is empty). The host is
// the only party that imports the features — no feature imports another
// (constitution rule 6); the cross-feature flow rides sdk vocabulary
// (sdk/web.Middleware, sdk/identity, sdk/events) the host wires between them.
//
// The cross-feature wiring is the point: cms's admin surface (the CRUD routes)
// is gated by auth's identity middleware via cms.Config.AdminMiddleware ←
// authSvc.RequireUser. Neither feature imports the other; structural typing on
// sdk/web.Middleware and the auth Service is what lets the host connect them.
// Public cms routes (the home page, published singles) stay ungated.
//
// On top of v1, this host exercises the whole auth-v2 surface for the A9 proof
// protocol (see README): the verified-email login gate (RequireVerifiedEmail),
// a host-local fake OAuth provider (oauthfake.go), machine identity (API keys +
// service accounts), stateless bearer JWTs signed host-side by
// integrations/cryptids/golang-jwt, security-event audit rows surfaced through a
// DEFAULT-OFF debug route, and invitations that grant through a TOY in-memory
// membership Granter (membership.go) — the demonstration of ratified AV4:
// invitations work with NO ReBAC anywhere in this host's module graph. The two
// host-local demo routes (demo.go) are gated on a resolved principal and on toy
// membership respectively.
//
// features/events adds the SSE gateway at GET /events (authenticated via
// authSvc.RequireUser on StreamMiddleware): a cms edit fans out as a
// content.updated frame to any open stream. Two rails prove out here. The
// DEFAULT variant is direct-emit/best-effort — cms emits straight onto the bus
// (SSE id: = CorrelationID). The DURABLE variant (EVENTS_OUTBOX=memory) routes a
// host-owned POST /outbox-demo append through an example-local in-memory outbox
// (internal/outboxmem) and a host-driven events.Poller on an sdk/workers pool:
// outbox -> poll -> emit -> SSE, id: = the durable outbox EventID. The shutdown
// order is HTTP server -> poller pool -> bus.Close (see run's tail comment).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/authmem"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/memstore"
	"github.com/gopernicus/gopernicus/examples/auth-cms/internal/outboxmem"
	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	cmstempl "github.com/gopernicus/gopernicus/features/cms/views/templ"
	eventsfeature "github.com/gopernicus/gopernicus/features/events"
	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	"github.com/gopernicus/gopernicus/sdk/cacher"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/environment"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/oauth"
	"github.com/gopernicus/gopernicus/sdk/web"
	"github.com/gopernicus/gopernicus/sdk/workers"
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
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	// Shared in-process event bus (sdk default Memory). cms is the emitter (it
	// publishes content.* post-write through mount.Events); the host subscribes
	// below to invalidate the public-page cache. Delivery is async (O3): an
	// emitter's latency never depends on its slowest subscriber.
	bus := sdkevents.NewMemory(sdkevents.WithLogger(log))

	// The public-page cache, held in a variable (it previously flowed straight
	// into cms.Config.Cache) so the host's content-event subscriber can drop it.
	pageCache := cacher.NewMemory()

	mount := feature.Mount{Router: router, Logger: log, Events: bus}

	// The TOY membership Granter (design §6, ratified AV4): invitations grant
	// through this in-memory map with NO ReBAC in the module graph.
	members := newMembership()

	// Host-side collaborators built from the environment (see demo.go): the JWT
	// signer (golang-jwt, or nil when AUTH_JWT_DISABLED=1, or an ephemeral key
	// when AUTH_JWT_SECRET is unset) and the optional provider-token encrypter.
	signer, err := buildTokenSigner(log)
	if err != nil {
		return err
	}
	encrypter, err := buildTokenEncrypter()
	if err != nil {
		return err
	}

	// Auth config. Hasher + Mailer are REQUIRED; a nil RateLimiter defaults to
	// in-memory. RequireVerifiedEmail is ON (A9): login/token refuse an unverified
	// user with 403. Providers/TokenSigner/Granter are the v2 subsystems, each
	// deny-by-absence when its collaborator is nil.
	authCfg := auth.Config{
		Hasher:               bcrypt.New(),
		Mailer:               email.NewConsole(log),
		MailFrom:             "auth@localhost",
		RequireVerifiedEmail: true,
		Providers:            []oauth.Provider{fakeOAuthProvider{}},
		OAuthCallbackBase:    callbackBase(),
		RedirectAllowlist:    []string{"/"},
		TokenEncrypter:       encrypter,
		TokenSigner:          signer,
		TokenTTL:             tokenTTL(),
		Granter:              members,
		Logger:               log,
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
	// (web.CachePages keys pages "page:"+RequestURI, so "page:*" clears them all);
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
	// gateway reads connect-time identity from sdk/identity, stashed by
	// authSvc.RequireUser on StreamMiddleware (A-I1 E2: no Identity field — absent
	// principal fails closed with 401). Repositories.Outbox nil ⇒ direct-emit mode
	// (no durable rail, no poller). Authorize left nil ⇒ the resource-scoped
	// /events/{resource_type}/{resource_id} route is NOT registered (deny by
	// absence). The subject stream lands at GET /events (host mounts at root, no
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
	})
	if err != nil {
		return err
	}
	if err := eventsSvc.Register(mount); err != nil {
		return err
	}

	// Durable-outbox variant plumbing (EVENTS_OUTBOX=memory): the host owns the
	// poller lifecycle (the feature owns no goroutines — D4). The poller runs on
	// an sdk/workers pool woken by the canonical append-then-signal pattern
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
	registerDemoRoutes(router, authSvc, members)
	registerDebugRoutes(router, authSvc, authRepos, log)

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

// seed populates a little content so the public site renders something. No user
// is seeded on purpose: registration is part of the proof flow.
func seed(ctx context.Context, repos cms.Repositories) error {
	now := time.Now().UTC()

	menu, err := menus.NewMenu("Main", now)
	if err != nil {
		return err
	}
	if _, err := repos.Menus.CreateMenu(ctx, menu); err != nil {
		return err
	}
	for i, link := range []struct{ label, url string }{{"Home", "/"}, {"About", "/about"}} {
		item, err := menus.NewMenuItem(menu.ID, link.label, link.url, "", i, now)
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
		e, err := content.NewEntry("article", a.title, a.excerpt, a.body, "demo", content.StatusPublished, "", now)
		if err != nil {
			return err
		}
		if _, err := repos.Entries.Create(ctx, e); err != nil {
			return err
		}
	}

	page, err := content.NewEntry("page", "About", "", "This page is served from memory — no SQL involved.", "", content.StatusPublished, "", now)
	if err != nil {
		return err
	}
	if _, err := repos.Entries.Create(ctx, page); err != nil {
		return err
	}

	// A product: the host's custom type, with EAV custom fields, on the same rail.
	prod, err := content.NewEntry("product", "Widget 3000", "The flagship widget.", "Built to last; ships worldwide.", "", content.StatusPublished, "", now)
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
