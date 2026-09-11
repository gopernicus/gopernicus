# pockets/events — the durable outbox + SSE gateway

A pluggable, datastore-free events pocket: a transactional-outbox domain
(append in the same commit as your domain rows, publish later), a host-driven
poller that drains it onto the shared `sdk/capabilities/events` bus, and an SSE gateway
that fans bus events out to authenticated browser streams. Built on
`sdk/capabilities/events` (the bus vocabulary), `sdk` (connect-time identity),
`sdk/pkg/web` (SSE primitives + responders), and `sdk/pkg/workers` (the poller's
pool). Design of record: `.claude/plans/roadmap/events-pocket-design.md`,
executed via `.claude/plans/events-v1/plan.md`.

Package-name note (O5): this package is `events` and so is `sdk/capabilities/events` —
this module and its hosts alias the sdk one as `sdkevents`.

## Layout

```
events.go               optional New / Components assembly and HTTP mounting
logic/outbox/           Entry and EntryRepository, actual host-driven Poller
logic/streams/          Service and policy-filtered Stream / Frame API
inbound/http/           validated SSE adapter and callable custom-route handlers
internal/hub/           private raw subscription/connection buffering machinery
stores/storetest/       outbox conformance and test-only memory reference
stores/{pgx,turso}/     independent driver modules and migrations
```

The root assembles components; hosts may use each public logic package and the
HTTP adapter directly. The outbox poller implements its handoff itself. Stream
consumers receive visible, projected frames, never raw hub connections or
unfiltered bus events. The hub stays private because its raw connection API does
not enforce per-principal visibility.

`stores/storetest` remains in the core module; driver adapters retain separate
modules. Importing events does not import a driver.

## Route surface

`/events/*` is this pocket's claimed namespace (charter C1):

| route | when registered | what |
|---|---|---|
| `GET /events` | when explicit `WithVisibility` is configured | the subject stream — every event the caller may see; `?types=a,b` filters by exact event type |
| `GET /events/{resource_type}/{resource_id}` | only when `WithAuthorization` is set (deny-by-absence) | a resource-scoped stream filtered to one aggregate |

The routes are JSON/SSE only — no HTML, no links — so a host may mount the
pocket behind any prefix (`pockets.PrefixRegistrar`) without breaking
anything the payloads carry.

## Options — nil semantics (charter item 12)

| field | nil/zero means | notes |
|---|---|---|
| `New bus argument` | **hard error** — `New` returns `streams.ErrBusRequired` | the gateway is a bus consumer; a nil bus is misconfiguration, not a degraded mode |
| `WithVisibility` | **Register fails** with `sdk.ErrInvalidInput` | required host policy for each event and principal, before projection |
| `WithLogger` | `slog.Default()` | receives policy errors and sampled slow-client drops |
| `WithStreamMiddleware` | streams mount ungated by middleware — and then **every request 401s** (no stashed identity) | see the loud requirement below |
| `WithAuthorization` | the resource-scoped route is **not registered** | deny-by-absence |
| `WithProjector` | metadata-only SSE bodies | raw payloads are never forwarded unless a Projector opts in |
| `eventshttp.Policy.Heartbeat` | 25s comment frames | keeps intermediaries from idling the stream out |
| `streams.Limits.BufferSize` | 64 events per connection | a slow client's overflow is dropped (SSE is a wake-up channel) |
| `eventshttp.Policy.MaxConnAge` | 15m | **cannot be disabled** — see the revocation posture below |
| `streams.Limits.MaxConnsPerSubject` | 10 concurrent streams per subject | breach → 429 |
| `WithOutbox` | direct-emit mode: no durable rail, no poller | the gateway still fans best-effort emits out over SSE |

**StreamMiddleware is load-bearing (A-I1 E5).** The gateway reads
connect-time identity from `sdk` and **fails closed**: a request
whose context carries no `sdk.Principal` gets a 401, uniformly, on every
stream. The pocket ships no identity resolution of its own — a host MUST
pass its identity-stashing middleware (the authentication pocket's
`RequireUser` stashes the Principal) on `WithStreamMiddleware`, or every
stream will 401. That failure mode is deliberate: misconfiguration surfaces
as deny, never as an anonymous-allowed stream.

**Visibility is host policy.** `Visible(ctx, principal, event)` runs for every
event that passes the connection's type/resource filters, on that connection's
goroutine, before projection. Return false to deny; errors deny and are logged.
The pocket does not infer tenancy from an aggregate ID. A shared authenticated
feed must explicitly return true. Both `Visible` and `Projector` may run
concurrently across connections and must not mutate the event. `Visible` must
honor context cancellation; a slow policy fills only that connection's bounded
buffer. Projection runs once per allowed connection, so denied events never reach
it. Policy/projector panics are contained, logged without panic details, and denied.

**MaxConnAge bounds the connection.** Identity and coarse resource authorization
are checked at connect time; per-event visibility may recheck current access.
The connection expires after 15m by default and cannot be configured as unlimited.
Its write deadline also respects this lifetime. Zero selects the default.

`WithStreamMiddleware` snapshots its input slice before construction. Stop HTTP streams and the poller,
call `Components.Close()` to release the gateway subscription, then close the
host-owned bus and datastore. `Close` is idempotent, rejects later registration,
and does not terminate existing HTTP streams or close the bus.

## Two emit paths — the guarantee table (design §3, load-bearing)

There are two ways an event leaves a pocket, and they carry **different
guarantees**. Every pocket author must pick deliberately; the biggest
coherence risk in this design is someone assuming `Emit` is transactional.

| path | API | guarantee | when to use |
|---|---|---|---|
| **best-effort** | `mount.Events.Emit(ctx, evt)` after the domain write returns | bounded async admission; no persistence or completion promise; lost on crash between commit and emit or if no subscriber | wake-up signals: SSE pushes, cache invalidation — anything where the client/consumer re-fetches authoritative state anyway |
| **durable (outbox)** | `[]events.Record` attached to the repository write input; the store adapter persists them **in the same transaction** as the domain rows; the poller publishes them onto the bus later | at-least-once (publish-then-mark; duplicates possible on poller crash — consumers de-dupe on `Record.EventID`) | side effects that must not be lost: security-event logging, future webhooks/email reactions |

SSE uses a nonempty `EventID()` when available and falls back to
`CorrelationID()` for events without an ID. `Record.Event()` exposes the outbox
ID directly; Redis also carries stable IDs in its transport envelope. Correlation
IDs group related events and are not deduplication keys. SSE is a best-effort
wake-up channel even when the source is durable: reconnecting clients re-fetch
authoritative state; this gateway does not replay missed frames.

**The durable path never touches `Mount.Events`** — it rides `Repositories`.
`Mount.Events` carries only the weaker path, and its doc comment says so.

### Observations and durable work

Authentication submits delivery work directly to the jobs pocket and emits optional
lifecycle observations after the job state is recorded. `Emit` cannot replace that
queue admission. An outbox can also drive job creation: wire `Memory.Dispatch`,
register a handler that waits for durable enqueue, return enqueue failures, and
deduplicate using `EventID()`. The poller then retains intent until that handoff
succeeds. The host chooses and owns this composition; the bus does not make an
ordinary callback durable by itself.

## The poller — single instance, host-driven

`outbox.NewPoller(repo, deliver, ...opts)` returns `(*Poller, error)` and rejects
missing dependencies or nil options during wiring. Poll reads an oldest-first batch and marks each
entry only after the host's delivery function returns success. Use
`outbox.NewPoller(repo, memory.Dispatch)` for local handler completion, or
`outbox.NewPoller(repo, redisBus.Publish)` for Redis stream acceptance. Never wire
asynchronous `Emit` or a discard callback into a durable handoff. A nil callback
or repository returns `sdk.ErrInvalidInput`.

A failure or cancellation leaves the entry unpublished. A failed mark after
successful delivery causes replay with the same `EventID()`, so consumers must
deduplicate. The poller stops at the first invalid or undeliverable record. It
never silently acknowledges poison rows: monitor poll failures and repair or
quarantine historical malformed records under host policy before restarting the
poller. Later rows remain blocked until the head can be delivered. Register all required handlers before starting the poller. `Poll`
matches `workers.WorkFunc` and returns `workers.ErrNoWork` when idle.

**Single-poller assumption (v1):** run ONE poller per outbox. `Poll` takes
no lease/claim on entries, so N concurrent pollers would emit every batch N
times. (Consumers on the durable rail de-dupe, so this degrades to noise,
not corruption — but don't do it.) Relatedly, the hub warns at construction
when the bus is not a `Broadcaster` (`Subscribe("*")` on the Memory bus is
single-instance fan-out): multi-instance SSE needs a broadcasting bus
(`integrations/kvstores/goredis`).

The host owns the poller lifecycle — `Register` starts no goroutines. Stop the
poller before closing its delivery backend. Closed backends return `events.ErrClosed`,
so an accidental late delivery remains unpublished. `Close(ctx)` drains admitted
work; a timeout returns `ctx.Err()` and a later Close waits for the same drain.

## Migrations — the `events` source prerequisite + boot probe

The outbox table belongs to migration source **`events`**, distinct from
`cms`/`auth`/`jobs`. Scaffold a store module's migrations with its
`ExportMigrations` and apply them with your host's runner pre-boot. Both
store constructors **probe for the `event_outbox` table at construction**
and error (wrapped `sdk.ErrNotFound` naming the unapplied source) before
the host serves traffic — wiring an appender against an unapplied source is
a runtime failure the probe converts into a boot failure (design §5
mitigation b). PostgreSQL additionally requires a `bytea` payload column; export and apply
`0002_event_outbox_payload_bytes.sql` before starting new writers. It preserves
historical JSON text but cannot recover originally empty payloads replaced by
`{}` or formatting already normalized by a custom JSONB schema. Stop old writers
before migration: their JSON casts are incompatible with the new column. Rollback
needs an explicit data conversion and is only possible if every new payload can
be represented by the old JSON contract. Turso now binds BLOB bytes into the
existing column and still reads historical TEXT; its matching 0002 migration is
an explicit no-op, with no table rebuild.

Both stores validate identity/type for the complete batch before inserting any
row, and preserve opaque bytes including empty and non-JSON input. `Append` owns
its transaction. Use the explicit `AppendTx` for the same commit as domain rows;
merely placing a transaction in context does not make `Append` join it.

## Public logic and HTTP wiring

A host can assemble the gateway with `events.New(bus, opts...)`, mount its bundled
routes with `components.Register(mount)`, or request `components.HTTP()` and mount
individual handlers. The configuration fields above retain their meaning.

For direct HTTP composition:

```go
service, err := streams.New(bus, streams.WithVisibility(canReceiveEvent),
    streams.WithProjector(projectEvent)) // nil keeps metadata-only bodies
if err != nil { return err }

adapter, err := eventshttp.New(service, eventshttp.WithMiddleware(resolveIdentity),
    eventshttp.WithAuthorization(canReadResource))
if err != nil { return err }

mux.Handle("GET /activity", adapter.SubjectStream())
resource, err := adapter.ResourceStream()
if err != nil { return err }
mux.Handle("GET /activity/{resource_type}/{resource_id}", resource)
```

Import `eventshttp` from `pockets/events/inbound/http`. Both returned handlers
apply configured middleware and identity requirements. `ResourceStream` errors
when no resource gate exists, and custom routes must populate both named path
values. Direct construction resolves the same bounded lifetime defaults and
rejects missing visibility, closed services, and nil middleware entries.

Non-HTTP consumers use `service.Open(principal, streams.Filter{...})`, defer the
returned stream's `Close`, and call `Next(ctx)`. Next checks visibility before
projection, contains host callback panics, and honors cancellation. Use one reader
per stream and bound its lifetime with the supplied context. This direct logic
API owns no HTTP policy or automatic background worker.

Durable handoff is independent: `outbox.NewPoller(repository, deliver)` returns the
poller and a construction error. Check that error before starting a worker.
Use a completion-aware function such as `memoryBus.Dispatch` or
`redisBus.Publish`, drive `Poll` with a worker pool, and stop it before closing its
backend. For SQL, construct the chosen `stores/pgx` or `stores/turso` outbox after
applying its exported migrations; its public `AppendTx` joins the host's write.

On shutdown: stop HTTP streams and pollers, close the stream service (or assembled
components), then close host-owned buses and stores. `Close` releases the shared
subscription and does not replace HTTP-server or worker lifecycle management.

## The unguarded appender seam (know it exists)

Each store module ships a dialect-typed `AppendTx(ctx, tx, recs...)` so a
future emitting pocket's store can write domain rows and outbox rows in one
commit. A consuming store declares its own one-method port and the outbox
store satisfies it structurally — zero import edge between store modules.
In v1 **nothing consumes it, and no `make guard` target covers that glue**
(design §5 cost 1): the seam is tested per-store but unguarded. The
abstraction revisit trigger is the third emitting pocket.
