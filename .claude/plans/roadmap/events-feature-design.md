# gopernicus events — design (bus port + transactional outbox + SSE gateway)

Status: **RATIFIED 2026-07-02 (jrazmi)** — O1–O8 to their proposed defaults
(R9), as amended by `00-intersections.md` R4 (conformance suite named
`features/events/storetest`, not `outbox/outboxtest`) and R5 (the Transactor
gap is owned by the portability plan's addendum). Amendments applied
in-place below.
Status amendment (2026-07-08, events-v1 CLOSED): **phases 3–8 EXECUTED** via
`.claude/plans/events-v1/plan.md` — see that plan for the operational record:
its supersession log (S1–S6), the eight ratification gate edits, the P1–P5
post-gate amendments, and the A-I1 (`sdk/identity`; `Config.Identity`
removed, absent identity fails closed 401) and A-R1
(`features/authentication` rename) amendments, all executed. **P5
micro-amendment of record: §6/O7's "hosts can set 0 explicitly" sentence is
superseded — `MaxConnAge` is no-disable in v1** (zero → the 15m default;
unlimited is not offered; a negative sentinel is the documented future seam).
Status amendment (2026-07-06, sdk-parity): this design's **phase 1 (sdk/web
SSE primitives) and phase 2 (`sdk/events` + `eventstest`) LANDED early** in
the sdk-parity milestone, built to §2 verbatim; and
`integrations/kvstores/goredis` was built early too, **superseding §9's
v1 deferral** (module name reconciled from `redis`, then subsequently
consolidated into the multi-port `kvstores/goredis` — 2026-07-06
kvstore-consolidation ruling R-KV1). `Mount.Events`, `features/events` (outbox +
SSE gateway), and the durable rail remain deferred — **events-v1 resumes at
phase 3**.
Status amendment (2026-07-06, straddle review): jrazmi challenged the
three-tier events/jobs split (sdk / integration / feature). Re-examined
against the constitution and the live import graph; split **AFFIRMED** —
full reasoning in NOTES.md (2026-07-06 straddle entry). Two additive
requirements folded into §11's plan-cut process (tier-review gate +
wiring-tour deliverable); no ratified decision reopened.
Date: 2026-07-02
Depends on: `.claude/plans/restructure/00-overview.md` (constitution),
`.claude/plans/restructure/capability-map.md` (ratified YOUR CALLs #1, #4;
Jobs & events rows; the SSE gateway row), `features/README.md` (charter, esp.
§6 C3's named event-bus Mount candidate),
`.claude/plans/restructure/auth-feature-design.md` (the fidelity bar; the
`RequireUser`/`CurrentUser` surfaces this design consumes).
Concurrent siblings (cited, not re-decided here):
`roadmap/jobs-feature-design.md` (owns the `sdk/workers` primitive),
`roadmap/datastore-portability.md` (owns the turso+postgres-out-of-the-box
policy and the transaction-seam question this design surfaces).

This is a design document only. Nothing here is built. A future `events-v1`
milestone phases from it, the way `auth-v1` phased from
`auth-feature-design.md`.

**Layout note (2026-07-02, trio re-layout ratified after this doc was
written):** paths in this doc predate the trio layout — when phase files
are cut, apply it: `outbox/` → `logic/outbox/`, the SSE gateway's hub +
HTTP → `internal/inbound/http` (hub internals under `internal/logic/`),
suite at `features/events/storetest` (R4, already amended).

## Context

Ratified YOUR CALL #4 said: the event bus is sdk-shaped (mirrors
cacher/email), do **not** build it for auth v1, build it the day a second
real multi-feature consumer appears — naming the SSE gateway as that
consumer. This document is that consumer's design, written now (before
auth v1 executes) so feature intersections surface early and so the feature
ships multi-datastore (turso AND postgres) from day one, with non-datastore
infrastructure (the bus backend) behind a swappable interface — a bus
consumer must not care whether it's in-memory or Redis.

## Goal

A ratifiable design for `sdk/events` (bus port + memory default +
conformance suite), `features/events` (transactional outbox + SSE gateway,
dialect-blind core), and the `Mount.Events` charter candidate — precise
enough that `events-v1` phases can be cut from it without re-deciding
anything.

## 1. Layer split (the load-bearing call) — confirmed, with one amendment

| layer | module | contents | when built |
|---|---|---|---|
| kernel | `sdk/events` | `Bus` port + `Emitter` narrow port, `Event`/`Handler`/`Subscription` vocabulary, `TypedHandler[T]`, `Record` (outbox/wire envelope), `Memory` default (at-most-once, in-process), `Noop`, `eventstest` conformance suite | `events-v1` phase 2 |
| kernel | `sdk/web` SSE primitives | `SSEEvent`, `SSEStream` (channel-fed, heartbeat, per-write deadline extension) ported from the original's `sdk/web/{sse,stream}.go` | `events-v1` phase 1 — see the **finding** below |
| contract | `sdk/feature` | `Mount.Events` field (emit-only) — the §6 C3 candidate, cashed | `events-v1` phase 3 |
| feature | `features/events` | outbox domain (`outbox/` entities + ports, public), poller (exported, host-driven), SSE gateway (internal hub + HTTP), `Register`/`Repositories`/`Config` | `events-v1` phase 4 |
| stores | `features/events/stores/turso`, `features/events/stores/postgres` | outbox SQL + canonical migrations (source `"events"`) + the dialect-typed transactional appender | `events-v1` phase 6 |
| integration | `integrations/events/redis` | Redis Streams `Bus` + `Broadcaster` (multi-process fan-out) | **NOT v1.** Built when a real multi-instance host exists (non-goal §9) |

**Finding (corrects the record): the new repo's `sdk/web` has NO SSE
primitives.** `capability-map.md`'s SSE-gateway row says "`sdk/web` already
carries the generic SSE primitives (`sse.go`/`stream.go`)" — that is true of
the *original*'s `sdk/web`, not this repo's (verified: no
`text/event-stream`/`Flusher` anywhere under `sdk/`). Porting them
(stdlib-only, well-tested in the original, including the
`http.ResponseController` write-deadline-extension subtlety that keeps
long-lived streams alive past the server's `WriteTimeout`) is a real,
S-sized prerequisite phase of `events-v1`, not existing capital.

**Why the bus port passes the sdk-vs-logic test** (ARCHITECTURE.md, all five):
multiple adapters honestly implement it (memory, Redis Streams, the
original had both); sdk can define observable behavior
(subscribe-receives-emitted, close-stops-delivery, wildcard matching); a
conformance suite exists as salvage (`infrastructure/events/eventstest`);
most apps benefit from the vocabulary; it knows zero CMS/auth concepts.
Stdlib-only (the original's `events.go` imports nothing third-party), so
per constitution rule 2 the default ships **inside sdk**, and the Redis
backend is an integration. Confirmed as ratified — no amendment to the
split itself.

**The build trigger, precisely.** The bus is built in the `events-v1`
milestone and not before, because that milestone delivers the second real
consumer chain ratified call #4 demanded: **`features/cms` is the first
real emitter** (`content.published`/`updated`/`deleted` from `entrysvc`)
and **the `features/events` SSE gateway is the second, multi-feature
consumer** (it subscribes to everything and fans out to browsers), with a
host-side cache-invalidation subscriber (the host calls
`cache.DeletePattern` on `content.*`) as a concrete third. Sequencing per
`capability-map.md` W4: after `auth-v1` (the gateway's connect-time
identity comes from auth) and after `sdk/workers` lands (the jobs design
owns it; see §7). If jobs slips, `events-v1` may land `sdk/workers` itself
— whichever milestone executes first builds it once (coordinate in the
respective milestone overviews).

## 2. `sdk/events` — the bus port

Salvaged from `gopernicus-original/infrastructure/events/events.go` with
deliberate trims. The shape:

```go
package events // sdk/events

// Event is what flows through a bus in-process: typed values.
type Event interface {
    Type() string          // "content.published" — lowercase, dot-separated domain.action
    OccurredAt() time.Time
    CorrelationID() string
}

// Metadata is an optional Event capability carrying routing metadata the
// SSE gateway filters on. BaseEvent satisfies it.
type Metadata interface {
    AggregateType() *string
    AggregateID() *string
    TenantID() *string // optional vocabulary only — tenancy the feature is deferred (auth v2+)
}

// BaseEvent — embeddable defaults (Type/Occurred/Correlation + optional
// aggregate/tenant metadata, JSON tags as in the original). Correlation
// IDs come from sdk/id; the original's package-level `var GenerateID`
// override is NOT ported (package-level mutable state, constitution rule 5's
// spirit).

type Handler func(ctx context.Context, e Event) error

type Subscription interface{ Unsubscribe() error }

// Emitter is the narrow emit-only port — what Mount.Events carries (§4).
type Emitter interface {
    Emit(ctx context.Context, e Event, opts ...EmitOption) error
}

// Bus is the full port a bus backend satisfies and the events feature consumes.
type Bus interface {
    Emitter
    Subscribe(topic string, h Handler) (Subscription, error) // exact topic or "*"
    Close(ctx context.Context) error
}

// Broadcaster is an optional Bus capability: fan-out delivery to EVERY
// process (required for SSE across instances). Memory trivially satisfies
// it (one process); Redis Streams distinguishes consumer-group Subscribe
// from broadcast. Salvaged verbatim in intent from the original.
type Broadcaster interface {
    SubscribeBroadcast(topic string, h Handler) (Subscription, error)
}

// TypedHandler — salvaged as-is: direct type assertion fast path (memory
// bus), Unmarshaler fallback slow path (events rehydrated from the outbox
// or a remote backend). This is the piece that lets one handler serve both
// in-process typed events and re-encoded ones.
func TypedHandler[T Event](fn func(ctx context.Context, e T) error) Handler

// Record is the durable/wire envelope — the outbox row's shape and the
// only event form that crosses a datastore or process boundary.
// EventID (sdk/id) is the at-least-once de-duplication key: it is the
// outbox primary key AND the SSE `id:` field.
type Record struct {
    EventID       string
    Type          string
    OccurredAt    time.Time
    CorrelationID string
    Payload       []byte  // EncodeEvent output (EventEncoder override, JSON fallback)
    AggregateType *string
    AggregateID   *string
    TenantID      *string
}

// NewRecord builds a Record from a typed Event: assigns EventID, extracts
// Metadata, encodes the payload. Serialization is owned HERE (sdk
// vocabulary), never by a domain service hand-rolling json.Marshal.
func NewRecord(e Event) (Record, error)
```

**Defaults shipped next to the port** (the cacher/email slog-style shape):

- `Memory` — in-process pub/sub. **Delivery guarantee: at-most-once,
  in-process, no persistence, no replay.** Async dispatch by default
  (`Emit` returns immediately; handler errors and panics are recovered and
  logged, never returned); `WithSync()` forces synchronous delivery for
  deterministic tests and same-request flows. `Close(ctx)` drains in-flight
  async handlers up to the context deadline. Satisfies `Broadcaster`
  trivially.
- `Noop` — the disabled default (salvaged), for hosts that don't wire
  events.

**Trims from the original, with reasons:** `WithPriority` dropped
(speculative — no consumer ever used priority ordering; fails admission
criterion 1); `EventRegistry` (prefix-pattern handler router) dropped from
v1 — exact + `"*"` topic matching covers the gateway and the poller;
prefix matching returns if a real consumer needs `"content.*"` routing
(open decision O6). `WakeChannel` (bus → coalesced wake signal for a
polling worker) is kept — it is the low-latency bridge between `Emit` and
the outbox poller/`sdk/workers.WithWakeChannel`, four lines of salvage.

**`eventstest` — scoped honestly.** `sdk/events/eventstest` mirrors
`cachertest`'s `Run(t, newBus)` runner, but unlike cacher the
implementations do NOT share one delivery-count contract (Memory is
at-most-once; the outbox rail is at-least-once; Redis consumer groups are
different again). The suite therefore asserts only the **common observable
contract**: subscribe-then-emit delivers; wildcard `"*"` matches;
unsubscribe stops delivery; no delivery after `Close`; `Close` is
idempotent; `WithSync` completes handlers before returning; `TypedHandler`
handles both the direct-assertion and `Unmarshaler` paths. Delivery-count
guarantees are documented per backend, not asserted centrally. (This
scoping is what keeps the "conformance suite exists" sdk-admission claim
true rather than aspirational.)

## 3. Two emit paths — the guarantee table (load-bearing)

There are two ways an event leaves a feature, and they carry **different
guarantees**. Every feature author must pick deliberately; the biggest
coherence risk in this design is someone assuming `Emit` is transactional.

| path | API | guarantee | when to use |
|---|---|---|---|
| **best-effort** | `mount.Events.Emit(ctx, evt)` after the domain write returns | at-most-once; lost on crash between commit and emit; lost if no subscriber | wake-up signals: SSE pushes, cache invalidation — anything where the client/consumer re-fetches authoritative state anyway |
| **durable (outbox)** | `[]events.Record` attached to the repository write input; the store adapter persists them **in the same transaction** as the domain rows; the poller publishes them onto the bus later | at-least-once (publish-then-mark; duplicates possible on poller crash — consumers de-dupe on `Record.EventID`) | side effects that must not be lost: auth v2 security-event logging, future webhooks/email reactions |

Corollaries this design commits to:

- The durable path **never touches `Mount.Events`** — it rides
  `Repositories`. `Mount.Events` carries only the weaker path, and its doc
  comment says so explicitly.
- **Consumers are re-fetch triggers, not command executors, in v1.** The
  SSE projection is metadata-only (§6); duplicates and drops are both
  harmless by construction. The day a consumer treats an event as a
  command (executes a side effect), it must be on the durable path and
  de-dupe on `EventID` — the contract is written into `Handler`'s docs
  ("implementations must be idempotent").
- **cms v1 uses the best-effort path only.** `content.*` events exist to
  wake SSE clients and invalidate caches; losing one on a crash costs a
  stale page until the next fetch. The outbox is designed now (§5), wired
  the day a durable consumer exists.

## 4. `Mount.Events` — the charter candidate, cashed

```go
// sdk/feature/feature.go — the one new field
type Mount struct {
    Router     RouteRegistrar
    Migrations MigrationRegistrar
    Logger     *slog.Logger
    Events     events.Emitter // nil → the feature emits nothing (silent no-op guard, like cms's nil Cache)
}
```

- **One field, one capability** (C3): the emit side only, an sdk interface,
  no concrete type. `sdk/feature` already imports `sdk/web`; importing
  `sdk/events` is the same sdk-internal edge.
- **Why Mount, not a per-feature Config field** (the cms
  `Cache`/`Mailer`/`Blobs` precedent): those are *per-feature-shaped
  collaborators* — cms's cache policy and mail templates are cms's own
  business. The bus is a *uniform cross-cutting platform capability* whose
  entire value is that every emitter and consumer shares **one instance**
  the host wires once — the same one-shared-instance property as `Logger`
  and the migrations ledger. Per-feature Config fields would work
  mechanically but would re-state the identical field N times and invite N
  subtly different nil semantics.
- **When added:** `events-v1` phase 3, immediately before cms's first emit
  call — exactly C3's "the day a real feature needs them." Pre-v1, a new
  named field is a compatible change (hosts construct `Mount` with named
  fields).
- **Rule 6 interaction:** events between features are **not** imports. cms
  emits `content.published` (a cms-internal typed event); the gateway
  subscribes with `"*"` and projects metadata only; no shared struct
  crosses feature boundaries. Typed cross-feature *subscription* would need
  a shared payload type — that is the C2-corollary vocabulary-graduation
  question, deliberately not needed in v1 because the projection is
  metadata-only and payloads stay opaque. If it ever arises, the shared
  type graduates to sdk only under the admission policy's three tests.
- **Nil semantics:** a feature guards `if m.Events != nil` (or wraps in
  `events.Noop` internally, implementer's choice — behavior identical).
  Hosts that don't care wire nothing and nothing changes.

## 5. Transactional outbox — `features/events` domain + the dialect seam

### Entities and ports (public, `features/events/outbox`)

```go
// outbox.Entry — the persisted row; shape = events.Record + bookkeeping.
type Entry struct {
    events.Record            // EventID is the primary key / de-dupe key
    CreatedAt   time.Time
    PublishedAt *time.Time   // nil = unpublished
}

// EntryRepository is the poller's port (constitution rule 3: it lives with
// its consumer, the events feature).
type EntryRepository interface {
    Append(ctx context.Context, recs ...events.Record) error // non-transactional convenience (own tx)
    ListUnpublished(ctx context.Context, limit int) ([]Entry, error) // ordered by CreatedAt
    MarkPublished(ctx context.Context, eventID string) error
    PurgePublished(ctx context.Context, before time.Time) (int, error) // retention
}
```

SQL (canonical migrations in each `stores/<dialect>`, source `"events"`):
the original's `0004_event_outbox.sql` shape — `event_id` PK, `event_type`,
`payload`, `created_at`, `published` flag with a partial index on
unpublished — adapted per dialect (`JSONB`/`TIMESTAMPTZ` on postgres,
`TEXT`/`INTEGER` on turso), plus the metadata columns `Record` carries.

### The poller — exported, host-driven

Salvages `infrastructure/events/poller` nearly verbatim, generalized:
`Poll(ctx) error` reads a batch of unpublished entries, `Emit`s each as an
`Unmarshaler`-capable event (so `TypedHandler` subscribers rehydrate), then
`MarkPublished` — publish-then-mark = at-least-once. Returns
`workers.ErrNoWork` when idle.

**What this design needs from `sdk/workers`** (owned by
`roadmap/jobs-feature-design.md` — stated as requirements, not designed
here): (1) a pool that calls a `func(ctx) error` iterate function in a
loop; (2) the `ErrNoWork` sentinel triggering an idle interval; (3)
`WithWakeChannel(<-chan struct{})` so `events.WakeChannel(bus, "*")` gives
the poller sub-interval latency; (4) panic recovery and graceful,
context-bounded stop. Nothing more.

**The poller is NOT a `features/jobs` job.** It needs no queue row, no
schedule entity, no CAS claim — and making it one would be a
feature→feature dependency (rule 6). It is a plain loop on the sdk
primitive. Consequence: single poller per outbox is the v1 operating
assumption; `ListUnpublished` does no row claiming (`FOR UPDATE SKIP
LOCKED` is a postgres-only multi-poller upgrade, deferred with the Redis
era — non-goal §9). The host constructs it (`events.NewPoller(repos.Outbox,
bus)`) and owns start/stop via the workers pool — features never own
background-goroutine lifecycles, mirroring D4's host-drives-execution
philosophy (`Register` has no shutdown hook, deliberately).

### The multi-datastore transaction seam (the hard part)

**Current reality (verified in code):** `sdk/repository` has **zero
transaction vocabulary**. `integrations/datastores/turso` exposes
`DB.InTx(func(*turso.Tx) error)`; `features/cms/stores/turso` uses it
privately *inside* single repository methods (entry + EAV fields
atomically). No mechanism exists for two store modules to share one
transaction.

**v1 default: the dialect-typed appender.** The insight that keeps feature
cores clean: both the emitting feature's store and the outbox store for a
given dialect already import the **same integration module**, so the
integration's `Tx` type is shared vocabulary *at the store level* without
any feature core ever seeing a driver type.

- The emitting feature's core attaches events **as data** to its write
  input — `events.Record` values (an sdk type, so no feature→feature
  import), built via `events.NewRecord` so serialization stays sdk-owned,
  never hand-rolled in a domain service.
- The emitting feature's **store adapter** declares (consumer-declares —
  direction matters) an optional appender port in its own package:

  ```go
  // features/cms/stores/turso — declared by the EMITTING store, satisfied
  // structurally by features/events/stores/turso.Store.AppendTx. Zero
  // import edge between the two store modules; the only shared type is
  // *turso.Tx from the integration both already require.
  type OutboxAppender interface {
      AppendTx(ctx context.Context, tx *turso.Tx, recs ...events.Record) error
  }
  ```

  Constructor-injected, nil = drop records (best-effort mode). Inside the
  existing `InTx` block: domain rows, then `appender.AppendTx(ctx, tx,
  recs...)` — one commit, true outbox atomicity. The postgres pair is the
  same shape over the postgres integration's `Tx`.
- The **host** wires it: builds the events turso store, passes it into the
  cms turso store's constructor. Wiring stays in `main` (rule 5).

**Costs, stated plainly (this is why the sdk question stays open, not
closed):**

1. **Unguarded, per-feature, per-dialect glue.** Every emitting feature ×
   every dialect hand-rolls the same optional-appender pattern, and none of
   the four `make guard` targets covers it. Two emitters × two dialects is
   tolerable; a third emitter makes this the top candidate for a real
   abstraction.
2. **Cross-source migration ordering.** The outbox table belongs to
   migration source `"events"`; the appender writes to it inside a
   transaction owned by source `"cms"`'s store. The shared ledger keyed
   `(source, version)` deliberately expresses **no ordering between
   sources** — a host that scaffolds cms's migrations but not events'
   fails at runtime, not boot. Mitigations, both required: (a) documented
   prerequisite in the events store README + host checklist ("wiring an
   appender requires the `events` source applied"); (b) **boot-time
   probe** — `features/events/stores/<dialect>.New(db)` verifies the
   outbox table exists and errors before the host serves traffic.
3. **Port-contract blast radius.** Attaching records to write inputs
   touches `content.EntryRepository`'s contract, which has **three**
   implementers today: the turso store, `examples/minimal`'s memstore, and
   `entrysvc`'s test fake. Design commitments: records ride an **input
   struct field** (e.g. a new optional `Events []events.Record` field on
   the existing create/update input structs — never a widened method
   signature, never a field on the `content.Entry` entity, which would put
   transport in the domain); and the port's doc contract says
   implementations **MAY** persist records atomically and MAY drop them —
   memstore (no transactions, no outbox) legitimately drops, and the port
   never promises what the canonical zero-infra implementation cannot
   honor. Since cms v1 is best-effort-only (§3), this port change lands
   only in the phase that actually wires cms's outbox mode — possibly
   never, if no durable cms consumer appears.

**FINDING → `roadmap/datastore-portability.md`:** `sdk/repository`'s lack
of any transaction vocabulary is a genuine gap. A generic seam (a
`Transactor` port, or a context-carried transaction handle the way
`sdk/logging` carries request IDs) would replace the per-dialect appender
boilerplate — but it is a cross-cutting datastore-policy decision with
consequences for every store module, so this design **flags it and
defaults to the appender** rather than deciding unilaterally. Urgency
marker: revisit the moment a **third** emitting feature wants the outbox,
not open-endedly.

## 6. SSE gateway — `features/events`' inbound surface

Salvages the original hub's design (`bridge/events/ssebridge/hub.go`)
wholesale — it is mature, tested code with the right instincts:

- **Hub** (internal): one per process, subscribes to the bus at `Register`
  (`SubscribeBroadcast` when the bus satisfies `Broadcaster`, else plain
  `Subscribe` with a logged single-instance warning); fans events into
  per-connection buffered channels (default 64); **drop-on-full** for slow
  clients with a sampled warning counter — SSE is a wake-up channel, not a
  durable feed; per-subject concurrent-connection cap (default 10);
  heartbeat comment frames (default 25s) via the ported `web.SSEStream`.
- **Projection**: metadata-only by default — `{type, occurred_at,
  aggregate_type, aggregate_id, tenant_id}` — clients re-fetch state
  through the normal (authorized) API. Raw payloads are **never** forwarded
  by default (auth events will carry verification codes one day);
  `Config.Projector` is the audited opt-in. SSE `id:` = `Record.EventID` /
  `CorrelationID`, giving clients a de-dupe key for free.
- **Route surface** (claimed namespace `/events/*`, documented per C1,
  prefixable — JSON/SSE bodies carry no HTML links, so C1's known
  limitation doesn't apply):
  - `GET /events` — the authenticated subject's stream (all events the
    connection's filter allows; `?types=a,b` allow-list).
  - `GET /events/{resource_type}/{resource_id}` — a resource-scoped stream,
    gated by the host's coarse `Authorize` check.

**Connect-time auth — without importing `features/auth` (rule 6), without
ReBAC (ratified #1).** The consuming feature declares its ports; the host
wires auth in:

```go
// features/events/events.go (public surface)

// CurrentUser matches auth.Service.CurrentUser structurally — the exact
// C2 shape features/README.md §5 illustrates. Neither feature imports the other.
type CurrentUser interface {
    CurrentUser(ctx context.Context) (userID string, ok bool)
}

// AuthorizeStream is the host-supplied coarse ownership check for
// resource-scoped streams. No ReBAC: v1's whole authorization model is
// "valid session" + whatever ownership rule the host encodes here.
type AuthorizeStream func(ctx context.Context, userID, resourceType, resourceID string) (bool, error)

type Repositories struct {
    Outbox outbox.EntryRepository // nil → direct-emit mode: no poller, no durable rail
}

type Config struct {
    Bus              events.Bus         // REQUIRED — the gateway is a bus consumer; Register errors on nil
    Identity         CurrentUser        // REQUIRED for streams — host passes authSvc; Register errors on nil
    StreamMiddleware []web.Middleware   // host passes authSvc.RequireUser (the A3/AdminMiddleware pattern)
    Authorize        AuthorizeStream    // nil → resource-scoped routes are NOT registered (deny by absence)
    Projector        Projector          // nil → metadata-only projection
    Heartbeat        time.Duration      // 0 → 25s
    BufferSize       int                // 0 → 64
    MaxConnAge       time.Duration      // 0 → 15m — see revocation note
    MaxConnsPerSubject int              // 0 → 10
}
```

Auth-integration mechanics: `RequireUser` (middleware, sets identity in
context) rides `StreamMiddleware`; the handler then calls
`cfg.Identity.CurrentUser(ctx)` for the subject string (per-subject caps,
stream attribution) and 401s if absent. **Revocation latency:**
authorization is connect-time only, so a revoked session keeps its live
stream until reconnect — `MaxConnAge` defaults ON (~15m) rather than
unlimited, deliberately inverting the original's default (0 = unlimited),
because the metadata-only projection makes forced reconnects cheap and the
security posture better. Nil `Bus` and nil `Identity` are hard `Register`
errors, mirroring auth's no-silent-default rule for `Hasher`/`Mailer`: a
gateway with no bus or no identity is a misconfiguration, not a degraded
mode.

**Config vs Mount for the bus, addressed head-on:** the gateway takes
`Bus` via `Config`, not via `Mount.Events` — it needs `Subscribe`, and
`Mount.Events` is deliberately emit-only. This is consistent, not
contradictory: `Mount` carries the uniform cross-feature capability
(emit); the one feature whose *domain* is the bus consumes the full port
as an explicit dependency, like any consumer-declared port. Host wiring:
one `bus := events.NewMemory(...)` instance flows to both `Mount.Events`
and `eventsfeature.Config.Bus`.

**Package-name collision, flagged:** `features/events` (package `events`)
collides with `sdk/events` at import sites — hosts and the feature's own
files alias (`sdkevents "gopernicus/sdk/events"`). Precedent exists (the
cms turso store and the turso integration are both package `turso`;
`examples/cms` aliases `tursodb`). Open decision O5 offers a rename if
jrazmi prefers; default is keep-and-alias for capability-map naming
continuity.

## 7. Intersections (explicit, load-bearing)

| seam | what crosses it | decision |
|---|---|---|
| **events ↔ jobs** | `sdk/workers` (pool, `ErrNoWork`, `WithWakeChannel`, panic recovery, graceful stop) is the poller's engine — `roadmap/jobs-feature-design.md` owns its design; this doc states requirements only (§5). The poller is **not** a jobs-feature job (no queue row, no CAS, no rule-6 edge). | Build `sdk/workers` once, in whichever milestone executes first (W4 sequences jobs first); `events-v1` declares it a phase-0 precondition. |
| **events ↔ auth** | Connect-time identity: `CurrentUser` port + `RequireUser` middleware, host-wired (§6) — auth never knows the gateway exists. Auth as *emitter*: v1 auth emits nothing (ratified #4 — direct `Config.Mailer`, no pub/sub); auth **v2** security events (`user.registered`, `session.revoked`, login-failure audit) are the first natural **durable-path** consumer — they're audit records, not wake-ups, so they ride the outbox, and they are what finally forces the cms-style write-input Record change onto auth's stores. `session.revoked` could someday actively close live SSE connections; v1 relies on `MaxConnAge` instead. | Auth v2 is the outbox's first real durable emitter; design accounted for, nothing built now. |
| **events ↔ cms** | `content.published`/`updated`/`deleted` emitted from `entrysvc` post-commit via `Mount.Events` (best-effort, §3). Consumers: the SSE gateway (admin live-update/preview) and a host-side cache-invalidation subscriber (`cache.DeletePattern("public:*")` on `content.*` — replacing/augmenting time-based TTL for the public page cache). cms core change: emit calls in `entrysvc` behind a nil guard — S-sized, no port changes in best-effort mode. | cms = first emitter; the two consumers above = the ratified "second real multi-feature consumer" made concrete. |
| **events ↔ host** | Wiring order in `main`: build bus → build stores (+ appender wiring if durable) → `Mount{..., Events: bus}` → `Register` features (gateway subscribes here) → host starts poller pool (if outbox) → serve. **Shutdown order matters and is documented**: stop HTTP server (closes SSE connections via request contexts) → stop poller pool (finish in-flight batch) → `bus.Close(ctx)` (drain async handlers). Migration ordering: `"events"` source must be applied before any appender-wired store boots (§5 mitigation: boot-time probe). | Host README + proof-host `main.go` are the executable documentation. |
| **events ↔ datastore-portability** | The stores/turso + stores/postgres pair, the outbox conformance suite, and the flagged `sdk/repository` transaction gap all conform to `roadmap/datastore-portability.md`'s policy (cited, not re-decided). `integrations/datastores/postgres` must exist before phase 6 (it doesn't today; auth-v1 A2 deferred it). | Precondition, owned by the portability plan. |

## 8. Multi-datastore out of the box

- **`features/events/stores/turso`** and **`features/events/stores/postgres`**
  ship in `events-v1` (jrazmi's standing requirement: features support both
  from day one). Each: own module, outbox SQL + canonical migrations
  (source `"events"`), `EntryRepository` implementation, the dialect-typed
  `AppendTx`, and the boot-time table probe.
- **Conformance suite**: `features/events/storetest` (naming per ratified
  R4 — one `storetest` package per feature, port-set sub-runners) —
  `Run(t, func(t) outbox.EntryRepository)` asserting append/list-order
  (CreatedAt ascending), unpublished-only listing, mark-published
  idempotence, purge-published retention, and EventID-uniqueness
  (duplicate `Append` of the same EventID → `errs.ErrAlreadyExists`). All
  three stores (turso, postgres, in-memory) run it. The *transactional*
  appender can't be conformance-tested dialect-neutrally (it takes a
  dialect Tx); each store module tests it against its own integration.
- **In-memory outbox store + zero-infra proof**: an example-local in-memory
  `EntryRepository` (memstore-honest: enforces EventID uniqueness and its
  tests assert it — the phase-2-W7 lesson) proves the feature core is
  datastore-free end to end: memory bus + in-memory outbox + poller +
  SSE over `go run`, no driver in the module graph (charter §3
  "provable with a zero-infra host").

## 9. Non-goals (v1)

- **No `integrations/events/redis`.** Memory bus only; single-process SSE
  is the v1 deployment shape (the hub logs the single-instance warning
  path anyway, so the seam is proven). Redis Streams + `Broadcaster`
  arrives with the first real multi-instance host — recommended
  explicitly, per the as-needed integrations ruling.
- No ReBAC / fine-grained stream authorization (ratified #1) — valid
  session + host-supplied coarse `Authorize` only.
- No tenancy behavior (metadata vocabulary fields exist on
  `Record`/`BaseEvent`; nothing filters by tenant until tenancy exists,
  auth v2+).
- No prefix topic routing / `EventRegistry` port; no `WithPriority`.
- No durable subscriptions, replay, or event-sourcing ambitions — the
  outbox is a delivery rail, not an event store.
- No webhooks (a future durable-path consumer, not v1).
- No `web.StreamWriter` port (the original's LLM-style
  respond-or-upgrade writer) unless an implementer finds it free while
  porting `sse.go`; `SSEStream` is what the gateway needs.
- No multi-poller claiming (`FOR UPDATE SKIP LOCKED`) — single poller per
  outbox is the documented v1 assumption.

## 10. Open decisions — ALL RATIFIED to their proposed defaults, 2026-07-02 (R9; O8 via R5)

| # | decision | proposed default | notes |
|---|---|---|---|
| O1 | `Mount.Events` vs per-feature Config field for the emitter | **Mount.Events** | §4's uniform-capability argument; C3 pre-names it. Config-per-feature works mechanically if jrazmi prefers Mount frozen |
| O2 | Ship the outbox in `events-v1`, or defer the durable rail until auth v2 needs it? | **Design + core ports + stores ship in v1; no v1 feature wires it** (cms stays best-effort; `Repositories.Outbox` nil in both proof hosts' default config, exercised by the proof host's second variant + store tests) | Keeps v1 honest (SSE needs no durability) while making "multi-datastore out of the box" real, not paper. The cheaper cut — ship ports, defer stores to auth v2 — is defensible if v1 scope must shrink |
| O3 | Memory bus default dispatch: async (original) vs sync | **Async default + `WithSync` option** | Matches the original's proven semantics + eventstest salvage; sync-only would be simpler but makes every emitter's latency hostage to its slowest subscriber |
| O4 | Keep optional tenant metadata on `Record`/`BaseEvent` despite tenancy being deferred | **Keep** | Optional pointers, pure vocabulary, the SSE filter shape needs aggregate scoping anyway; removing-then-re-adding churns the wire format |
| O5 | `features/events` package-name collision with `sdk/events` | **Keep name, alias at import sites** | Precedent: turso/turso. Alternatives if the aliasing grates: `features/relay`, `features/live` |
| O6 | Topic matching: exact + `"*"` only, or prefix patterns (`content.*`) | **Exact + `"*"` in v1** | The gateway subscribes `"*"` and filters per-connection; prefix matching returns with a real subscriber that needs it |
| O7 | `MaxConnAge` default: 15m (this design) vs unlimited (original) | **15m** | Revocation-latency posture beats the original's convenience default; hosts can set 0 explicitly |
| O8 | Transaction seam: dialect-typed appender vs forcing the `sdk/repository` Transactor question now | **Appender; Transactor flagged to `roadmap/datastore-portability.md` as urgent-at-third-emitter** | §5's costs table is the honest price list |

## 11. Rough phase breakdown (for the future `events-v1` milestone)

Preconditions (owned elsewhere): `auth-v1` executed (RequireUser/Service
exist); `sdk/workers` landed (jobs plan, or built here first —
coordinate); `integrations/datastores/postgres` landed (portability plan)
— required by phase 6 only, so phases 1–5 + 7 can proceed without it.

| phase | what | size |
|---|---|---|
| 1 | Port SSE primitives into `sdk/web` (`sse.go` + tests, incl. heartbeat + write-deadline handling) | S |
| 2 | `sdk/events`: port, `Memory`, `Noop`, `WakeChannel`, `Record`/`NewRecord`, `eventstest` | M |
| 3 | `Mount.Events` field + `sdk/feature` tests + charter §6 update (candidate → cashed) | S |
| 4 | `features/events` core: `outbox/` ports+entities, exported `Poller`, gateway hub + HTTP, `Register`/`Repositories`/`Config`, in-module tests | L |
| 5 | cms emitter: `entrysvc` emits `content.*` via `Mount.Events` (nil-guarded); host cache-invalidation subscriber in the proof host | S–M |
| 6 | `stores/turso` + `stores/postgres`: SQL/migrations (source `"events"`), `EntryRepository`, `AppendTx`, boot-time probe, `storetest` (R4) + per-dialect appender tests | L |
| 7 | Proof host (extend `examples/auth-cms` or a new example): memory bus, in-memory outbox variant, SSE end-to-end — real-interaction check is `curl -N` on `/events` while editing a cms entry in another session and watching the `content.updated` frame arrive; green tests alone do not close this | M |
| 8 | Docs sync: feature README (route surface `/events/*`, Config, ports), the capability wiring page (plan-cut requirement 2 below), guards (G2 module list + note the unguarded appender seam), ARCHITECTURE/charter touch-ups | S |

Sequencing: 1→2→3 strictly ordered; 4 needs 2 (+1 for the gateway); 5
needs 3+4; 6 needs 4 (+postgres integration); 7 needs 4+5; 8 last. Guards:
phases 3–6 verify with `make check` + `make guard`; phase 7 ends with the
run-and-look check above.

**Plan-cut requirements (added 2026-07-06, straddle review):** when
`.claude/plans/events-v1/plan.md` is cut from this section:

1. **Tier-review gate.** Before jrazmi ratifies the drafted plan,
   `architecture-steward` and `lead-backend-engineer` critique it with
   exactly this prompt: *"is any piece in the wrong tier, and is the host
   wiring tour acceptable?"* If the three-tier straddle is wrong anywhere,
   it must surface there as a concrete misplacement, not a vibe. The one
   placement flagged genuinely debatable is the SSE gateway's routes living
   in `features/events` rather than the host — that is ratified (R9, §6),
   so the gate confirms it consciously; any change is a deliberate
   reopening, not drift.
2. **Wiring-tour deliverable.** The adopter tour for "live updates
   end-to-end" spans five stops — the `sdk/events` bus, `Mount.Events`,
   `features/events` (gateway + poller), a store module, and an
   `sdk/workers` pool driving the poller. Each placement is right; the tour
   is long; the cost is comprehension-priced, so it is settled with docs,
   not code motion. Phase 8 MUST ship a per-capability wiring page — one
   diagram + one complete `main.go` — and phase 7's proof host is that
   page's executable twin.

## 12. Checklist trace (`features/README.md` §8, for `features/events`)

1. Standalone module compile — yes; own `go.mod`.
2. `go.mod` = stdlib + sdk only — yes; no view deps (JSON/SSE surface, no
   templ — leaner than cms, same as auth v1), no drivers (stores are
   sibling modules), no cron/redis (poller is sdk/workers-driven,
   host-started).
3. Never imports integrations/examples/own stores — by construction; G2's
   module list gains `features/events` (flagged for phase 8, and A4's
   generalized guard covers it if auth-v1 lands first).
4. `Register(mount, repos, cfg) error` conforms; touches only
   `mount.Router`/`mount.Migrations`/`mount.Logger` — **plus the new
   `mount.Events` this design itself adds via C3's sanctioned process**.
   `NewPoller` is additional exported surface in `events.go`, following
   auth's `NewService` precedent (host-facing constructors live in
   `<name>.go`).
5. Migration source `"events"` — unique vs `"cms"`/`"auth"`.
6. Zero-infra proof — §8's memory-bus + in-memory-outbox host.
7. README documents `/events/*` surface, Config, ports — phase 8; this doc
   is the source content.
8. No `init()`, no service locator, no package-level registry — the
   original's `var GenerateID` deliberately not ported (§2); hub state
   lives in an explicitly constructed value.
9. No feature→feature imports — `CurrentUser` is consumer-declared,
   structurally satisfied by `auth.Service`; the cms→events event flow is
   data over the bus, not imports (§4).

## 13. Risks

1. **The two-emit-paths asymmetry** (§3) — a feature author assuming
   `Mount.Events.Emit` is transactional ships a silent durability bug.
   Mitigation: the guarantee table, doc comments on `Emitter`, and the
   charter update naming both paths.
2. **Cross-source migration ordering** (§5) — appender-wired hosts fail at
   runtime if the `"events"` source isn't applied. Mitigation: boot-time
   probe + documented prerequisite; residual risk is hosts that skip both.
3. **Unguarded appender boilerplate** (§5, O8) — per-feature × per-dialect
   glue with no `make guard` coverage; contained at two emitters, urgent
   at three. Mitigation: the flagged Transactor finding with an explicit
   revisit trigger.

## Consultation notes

`lead-backend-engineer` reviewed the sketch (single hop). Verdict:
ship-with-edits — layer split, redis deferral, and the flag-don't-decide
Transactor call confirmed. Material findings incorporated: the
cross-source migration-ordering failure mode (§5 mitigation pair, risk 2);
the two-emit-paths asymmetry elevated to its own section (§3) with the
guarantee table; the Record-on-write-inputs blast radius (three
`EntryRepository` implementers; input-struct-field shape; MAY-drop port
contract so memstore stays honest); `eventstest` scoped to the common
observable contract rather than a false uniform delivery-count claim; the
appender declared by the *emitting* store (consumer-declares) so no
store→store import edge exists; serialization pinned to
`events.NewRecord` (sdk-owned wire format, not domain services);
`Record.EventID` as the explicit at-least-once de-dupe key; `sdk/workers`
called out as a not-yet-existing hard precondition to sequence.

## Open questions

Beyond the O1–O8 table: none. The Transactor question is deliberately
routed to `roadmap/datastore-portability.md`, not left open here.

## Recommended reviews

- **product-manager** — scope discipline: is O2's ship-stores-wire-nothing
  cut the right v1 value line; is the proof-host deliverable (live SSE on
  a cms edit) the demo that earns the milestone.
- **lead-backend-engineer** — already consulted pre-write; re-review the
  full doc, esp. §5's seam and §2's port trims.
- **architecture-steward** — `Mount.Events` (C3), the sdk admission
  argument (§1), the appender's store-level coupling vs the one rule.
- **data-integration-reviewer** — outbox SQL shape across dialects,
  `outboxtest` coverage, boot-time probe, in-memory store parity.
- **platform-sre** — shutdown ordering (§7 host row), migration phasing
  (§5), single-poller operating assumption, `MaxConnAge` revocation
  posture.
- **lead-frontend-engineer** — only if phase 7's proof host grows an
  admin-view live-update surface; the v1 API surface is JSON/SSE-only.

## Notes

- Reference-only salvage sources (design ported, code re-typed fresh):
  `gopernicus-original/infrastructure/events/{events,memorybus,poller,wake}.go`
  + `eventstest/suite.go`; `bridge/events/ssebridge/{bridge,hub}.go`;
  `sdk/web/{sse,stream}.go`; `workshop/migrations/primary/0004_event_outbox.sql`;
  `core/repositories/events/eventoutbox/`. Not salvaged: `registry.go`
  (prefix routing, O6), `goredisbus` (non-goal), `WithPriority`,
  `var GenerateID`.
- This doc + `roadmap/jobs-feature-design.md` + `roadmap/datastore-portability.md`
  are deliberately concurrent: jobs owns `sdk/workers`, portability owns
  the postgres integration + transaction policy, events consumes both.
  Cross-references are one-directional requirements statements to avoid
  circular ratification.
