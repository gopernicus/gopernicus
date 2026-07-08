# features/events — the durable outbox + SSE gateway

A pluggable, datastore-free events feature: a transactional-outbox domain
(append in the same commit as your domain rows, publish later), a host-driven
poller that drains it onto the shared `sdk/events` bus, and an SSE gateway
that fans bus events out to authenticated browser streams. Built on
`sdk/events` (the bus vocabulary), `sdk/identity` (connect-time identity),
`sdk/web` (SSE primitives + responders), and `sdk/workers` (the poller's
pool). Design of record: `.claude/plans/roadmap/events-feature-design.md`,
executed via `.claude/plans/events-v1/plan.md`.

Package-name note (O5): this package is `events` and so is `sdk/events` —
this module and its hosts alias the sdk one as `sdkevents`.

## Layout (the trio — see `features/README.md` §2 for the contract)

```
events.go                the socket: Repositories, Config, AuthorizeStream,
                         Projector, Service, NewService, Register
poller.go                NewPoller/Poll — the durable rail's drain, exported
                         and host-driven (matches workers.WorkFunc)
domain/                  the hexagon's public rim — entities + ports
  outbox/                Entry, EntryRepository (the port doc comments are
                         the spec; storetest executes them)
internal/
  logic/hub/             the SSE fan-out hub (per-connection buffers,
                         subject caps, metadata projection)
  inbound/http/          the stream routes (sdk/web SSE + responders)
storetest/               executable spec for the outbox port (Run) + the
                         honest in-memory reference that runs it hermetically
stores/turso/            the outbound tier: per-dialect SQL + migrations
stores/pgx/              (source "events"), each its own module
```

## Route surface

`/events/*` is this feature's claimed namespace (charter C1):

| route | when registered | what |
|---|---|---|
| `GET /events` | always | the subject stream — every event the caller may see; `?types=a,b` filters by exact event type |
| `GET /events/{resource_type}/{resource_id}` | only when `Config.Authorize` is set (deny-by-absence) | a resource-scoped stream filtered to one aggregate |

The routes are JSON/SSE only — no HTML, no links — so a host may mount the
feature behind any prefix (`feature.PrefixRegistrar`) without breaking
anything the payloads carry.

## Config — nil semantics (charter item 12)

| field | nil/zero means | notes |
|---|---|---|
| `Config.Bus` | **hard error** — `NewService` returns `ErrBusRequired` | the gateway is a bus consumer; a nil bus is misconfiguration, not a degraded mode |
| `Config.StreamMiddleware` | streams mount ungated by middleware — and then **every request 401s** (no stashed identity) | see the loud requirement below |
| `Config.Authorize` | the resource-scoped route is **not registered** | deny-by-absence |
| `Config.Projector` | metadata-only SSE bodies | raw payloads are never forwarded unless a Projector opts in |
| `Config.Heartbeat` | 25s comment frames | keeps intermediaries from idling the stream out |
| `Config.BufferSize` | 64 events per connection | a slow client's overflow is dropped (SSE is a wake-up channel) |
| `Config.MaxConnAge` | 15m | **cannot be disabled** — see the revocation posture below |
| `Config.MaxConnsPerSubject` | 10 concurrent streams per subject | breach → 429 |
| `Repositories.Outbox` | direct-emit mode: no durable rail, no poller | the gateway still fans best-effort emits out over SSE |

**StreamMiddleware is load-bearing (A-I1 E5).** The gateway reads
connect-time identity from `sdk/identity` and **fails closed**: a request
whose context carries no `identity.Principal` gets a 401, uniformly, on every
stream. The feature ships no identity resolution of its own — a host MUST
pass its identity-stashing middleware (the authentication feature's
`RequireUser` stashes the Principal) on `Config.StreamMiddleware`, or every
stream will 401. That failure mode is deliberate: misconfiguration surfaces
as deny, never as an anonymous-allowed stream.

**MaxConnAge is the revocation posture.** Streams are authorized at connect
time only, so a bounded connection age caps how long a revoked session keeps
a live stream. Zero means the 15m default; v1 offers no "unlimited" — a host
wanting effectively-unlimited sets an explicitly large value (e.g. `8760h`).

## Two emit paths — the guarantee table (design §3, load-bearing)

There are two ways an event leaves a feature, and they carry **different
guarantees**. Every feature author must pick deliberately; the biggest
coherence risk in this design is someone assuming `Emit` is transactional.

| path | API | guarantee | when to use |
|---|---|---|---|
| **best-effort** | `mount.Events.Emit(ctx, evt)` after the domain write returns | at-most-once; lost on crash between commit and emit; lost if no subscriber | wake-up signals: SSE pushes, cache invalidation — anything where the client/consumer re-fetches authoritative state anyway |
| **durable (outbox)** | `[]events.Record` attached to the repository write input; the store adapter persists them **in the same transaction** as the domain rows; the poller publishes them onto the bus later | at-least-once (publish-then-mark; duplicates possible on poller crash — consumers de-dupe on `Record.EventID`) | side effects that must not be lost: security-event logging, future webhooks/email reactions |

Per-rail delivery, as it reaches an SSE client (gate edit 1):

- **Best-effort frames** carry the event's `CorrelationID` as the SSE `id:`.
  There is **no de-dupe guarantee** on this rail — a CorrelationID is shared
  by every event in a request chain, and the rail itself is at-most-once.
- **Durable frames** carry the outbox `EventID` as the SSE `id:` — the
  poller's rehydrated event type surfaces it (`sdk/events.RemoteEvent`
  carries no EventID; the wrapper adds it). At-least-once means duplicates
  are possible; **consumers that act on durable events de-dupe on
  `EventID()`**. Re-fetch triggers (the v1 posture) need no de-dupe —
  duplicates and drops are both harmless when the consumer re-reads
  authoritative state.

**The durable path never touches `Mount.Events`** — it rides `Repositories`.
`Mount.Events` carries only the weaker path, and its doc comment says so.

## The poller — single instance, host-driven

`NewPoller(repo, bus, ...opts)` + `Poll(ctx)` is the durable rail's drain:
read a batch of unpublished entries (CreatedAt ascending), `Emit` each with
`WithSync()`, and `MarkPublished` **only after a successful emit** — an emit
error leaves the entry unpublished for the next poll (redelivery), and a
mark error means a duplicate emit next poll (de-dupe on `EventID()`).
`Poll` matches `workers.WorkFunc` and returns `workers.ErrNoWork` when idle,
so it drops onto an `sdk/workers` pool unadapted.

**Single-poller assumption (v1):** run ONE poller per outbox. `Poll` takes
no lease/claim on entries, so N concurrent pollers would emit every batch N
times. (Consumers on the durable rail de-dupe, so this degrades to noise,
not corruption — but don't do it.) Relatedly, the hub warns at construction
when the bus is not a `Broadcaster` (`Subscribe("*")` on the Memory bus is
single-instance fan-out): multi-instance SSE needs a broadcasting bus
(`integrations/kvstores/goredis`).

The host owns the poller lifecycle — `Register` starts no goroutines. Stop
the poller **before** closing the bus (see the shutdown order in the tour
below): both buses return nil on `WithSync` against a closed bus, so a
poller that outlives the bus would mark entries whose events went nowhere.

## Migrations — the `events` source prerequisite + boot probe

The outbox table belongs to migration source **`events`**, distinct from
`cms`/`auth`/`jobs`. Scaffold a store module's migrations with its
`ExportMigrations` and apply them with your host's runner pre-boot. Both
store constructors **probe for the `event_outbox` table at construction**
and error (wrapped `errs.ErrNotFound` naming the unapplied source) before
the host serves traffic — wiring an appender against an unapplied source is
a runtime failure the probe converts into a boot failure (design §5
mitigation b). The store READMEs state this loudly.

## Wiring: live updates end-to-end

Five stops, one flow. A domain write becomes a browser frame:

```
   ┌─────────────────────────────────────────────────────────────────────┐
   │                            the host (main.go)                       │
   │                                                                     │
   │  [1] bus := sdkevents.NewMemory(...)          the shared bus        │
   │        ▲                            ▲                               │
   │        │ emits (best-effort)        │ Subscribe («the gateway       │
   │        │                            │  is just a consumer»)         │
   │  [2] Mount{Events: bus}       [3] eventsSvc = NewService(repos,     │
   │        │                            Config{Bus: bus, ...})          │
   │        │ cms emits content.*        │   ├── SSE hub → GET /events   │
   │        ▼ post-write                 │   └── poller (durable rail)   │
   │      cms.Register(mount, ...)       │         ▲                     │
   │                                     │         │ drains              │
   │  [4] outbox repo ───────────────────┘─────────┘                     │
   │      ★ SUBSTITUTION POINT: outboxmem (in-memory twin, below)        │
   │        OR stores/turso / stores/pgx (swap snippet, below)           │
   │                                                                     │
   │  [5] workers.NewPool(poller.Poll, WithWakeChannel(wake))            │
   │      append-then-signal: Append(...) → wake ← pool drains promptly  │
   └─────────────────────────────────────────────────────────────────────┘
```

The listing below is the durable-variant run path of
**`examples/auth-cms/cmd/server/main.go` — the executable twin** (`make
check` compiles it; `EVENTS_OUTBOX=memory go run ./cmd/server` runs this
exact tour). Every non-elision line appears verbatim in the twin; host demo
detail is elided at the marked lines.

```go
func run(ctx context.Context, log *slog.Logger) error {
	// [elided: in-memory cms + auth stores, seeding — host detail]

	// Host-owned router + middleware. Both features and the host demo routes mount
	// onto this.
	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	// —— stop 1: the shared in-process event bus (sdk default Memory).
	bus := sdkevents.NewMemory(sdkevents.WithLogger(log))

	pageCache := cacher.NewMemory()

	// —— stop 2: the emit-only Mount field — cms publishes content.* through it.
	mount := feature.Mount{Router: router, Logger: log, Events: bus}

	// [elided: auth Service build + authSvc.Register(mount) — see the twin]

	if err := cms.Register(mount, cmsRepos, cms.Config{
		Views:           cmstempl.New(),
		Cache:           pageCache,
		AdminMiddleware: []web.Middleware{authSvc.RequireUser},
		// [elided: Types/Templates/Mailer fields — see the twin]
	}); err != nil {
		return err
	}

	// A host-side consumer: invalidate the public-page cache on content events.
	if _, err := bus.Subscribe("*", func(ctx context.Context, e sdkevents.Event) error {
		if !strings.HasPrefix(e.Type(), "content.") {
			return nil
		}
		return pageCache.DeletePattern(ctx, "page:*")
	}); err != nil {
		return err
	}

	// —— stop 4 (the substitution point): the outbox repository. Here the
	// example-local in-memory twin; the store-module swap is the snippet below.
	var eventsRepos eventsfeature.Repositories
	var outboxStore *outboxmem.Store
	if durableOutbox() {
		outboxStore = outboxmem.New()
		eventsRepos = eventsfeature.Repositories{Outbox: outboxStore}
	}

	// —— stop 3: the gateway. ONE bus flows to both Mount.Events (emitter side)
	// and Config.Bus (consumer side). RequireUser stashes the identity.Principal
	// the stream handlers read; absent identity fails closed (401).
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

	// —— stop 5: the host owns the poller lifecycle (the feature owns no
	// goroutines). The pool is woken by the canonical append-then-signal pattern:
	// a dedicated cap-1 wake channel the appending handler signals right after
	// Append. The pool runs on its OWN Background-derived context (NOT the
	// request/signal ctx) so shutdown can stop it AFTER HTTP has drained.
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
	}

	// Shutdown order (design §7): HTTP server → poller pool → bus.Close.
	//  1. web.Run blocks until ctx is canceled, then drains in-flight HTTP on its
	//     OWN fresh Background+ShutdownTimeout context, closing every open SSE
	//     stream. By the time web.Run returns, the parent ctx is already canceled.
	//  2. THEN stop the poller pool (its own Background-derived context — a
	//     canceled parent would have torn it down before HTTP finished draining).
	//  3. Close the bus LAST, on a FRESH bounded context (a canceled parent ctx
	//     would make Memory.Close drain nothing). Closing after the poller stops
	//     is why the poller's closed-bus edge never happens.
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
```

### Stop 4, substituted: the turso store module

`outboxmem` above is an example-local twin of the same
`outbox.EntryRepository` port. A production host swaps in a store module —
constructor plus the scaffold-and-own migration step (see
`features/events/stores/turso/README.md`):

```go
import (
	eventsturso "github.com/gopernicus/gopernicus/features/events/stores/turso"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

	db, err := tursodb.Open(tursodb.Config{
		URL:       os.Getenv("TURSO_DATABASE_URL"),
		AuthToken: os.Getenv("TURSO_AUTH_TOKEN"),
	})
	if err != nil {
		return err
	}
	defer db.Close()

	// One-time scaffold: copy the canonical migrations into the dir your host's
	// runner owns, and apply them pre-boot alongside your other feature sources.
	//   eventsturso.ExportMigrations("workshop/migrations/events")
	// New probes for the event_outbox table and errors at wiring time if the
	// "events" migration source has not been applied.
	outboxStore, err := eventsturso.New(db)
	if err != nil {
		return err
	}
	eventsRepos := eventsfeature.Repositories{Outbox: outboxStore}
```

Everything downstream — `NewService`, the poller, the pool, the shutdown
order — is identical: the swap changes one constructor and adds the
migration step. That is the point of the port.

## The unguarded appender seam (know it exists)

Each store module ships a dialect-typed `AppendTx(ctx, tx, recs...)` so a
future emitting feature's store can write domain rows and outbox rows in one
commit. A consuming store declares its own one-method port and the outbox
store satisfies it structurally — zero import edge between store modules.
In v1 **nothing consumes it, and no `make guard` target covers that glue**
(design §5 cost 1): the seam is tested per-store but unguarded. The
abstraction revisit trigger is the third emitting feature.
