# SDK audit S8c: Events

Status: REVIEW COMPLETE; corrections IMPLEMENTED — 2026-09-10. Parent: [framework-audit.md](framework-audit.md).
This document preserves the pre-fix review and evidence. Approved corrections and
final verification are in [events-implementation.md](events-implementation.md);
[AUDIT-012](../AUDIT.md#audit-012-event-delivery-contracts-and-redis-recovery) records
the implemented consumer migration. Findings below describe the reviewed baseline.

## Preconditions and scope

Branch/base: firestore-authentication / 6807ed06; 601 existing dirty paths.
Snapshot: /tmp/gopernicus-events-review-baseline.json (1968 files). Preserve previous audits
and concurrent changes. Go 1.26.1; no root go.mod; workspace has 42 modules.
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter
/Users/jrazmi/go/bin/goimports. No production-source, dependency/pin, generated,
consumer, release or deployed-service changes in this review. Owned disposable
local Redis and local HTTP probes are permitted; report cleanup and limitations.

Keep the SDK stdlib-only, generic and usable without pockets. Inspect core event
vocabulary, Memory/Noop, handler/subscription/close contracts, broadcast vs delivery,
record/wake helpers and shared conformance. Trace Redis adapter behavior and
pocket/host use to distinguish unfinished architectural purpose from dead API.
The complete outbox/SSE pocket and store audit remains a later slice; include
enough of it here to expose SDK contract mismatches and keep real uses intact.

## Plan and ownership

1. Parent: inventory SDK surface/tests/callers and original/consumer precedent;
   assess naming, policy placement and simplest viable API; independently
   reproduce concrete SDK/adapter findings with disposable probes as useful.
2. Named lead-backend-engineer: read-only Redis event bus/broadcast and pocket
   boundary review, including durable delivery/ack/retry/group claims.
3. Named platform-sre: read-only SDK Memory/Noop lifecycle, concurrency,
   cancellation, observable errors and resource bounds review.
4. Parent: targeted build/test/vet, race and owned live Redis suites/probes;
   distinguish green baseline tests from uncovered defects. No generated edits.
5. Record ranked evidence, proposed contract choices, real-use justification,
   compatibility costs and an implementation sequence. Update master handoff
   and exact task-relative inventory. Do not add speculative AUDIT entries.

## Conclusion and real-use evidence

Keep events as an SDK capability. Typed events, notifications, transport envelopes,
subscription lifecycle and bounded local dispatch are generic mechanisms with real
users; they should not move into the root package or the events pocket. The major
simplification is to separate three promises that currently share WithSync/Bus:
local handler completion, remote publication acceptance, and durable processing.
Existing tests pass while missing important failures in those boundaries.

Production caller sample (read-only, excluding vendor and generated/test code):

- Segovia v2 (`/Users/jrazmi/code/segovia/segovia/v2`) constructs Memory in
  cmd/server/main.go:136, drives the outbox poller at :260, creates Record in
  internal/outbound/domains/timelines/notices.go:120, and subscribes its notice
  drain in cmd/server/jobs.go:189. That handler durably enqueues a job using
  EventID as its job/deduplication ID and returns enqueue errors. Local synchronous
  completion is load-bearing here. Do not replace this path with an async Emit.
- Coordination Hub (`/Users/jrazmi/code/gps/coordination-hub`) constructs Memory
  in cmd/server/main.go:181. No direct production event helper/subscriber calls
  beyond wiring found in the sample.
- GPS360 (`/Users/jrazmi/code/gps/three-sixty/gps-360-go`) has no direct production
  SDK events imports/calls in this sample; vendor copies do not count as consumers.
- Framework CMS emits content events; authentication emits delivery observations;
  pockets/events rehydrates the outbox and subscribes broadcast for SSE. The
  auth-cms example demonstrates poller/SSE and explicit append-then-wake wiring.
- WakeChannel has tests and a clear tiny event-to-worker adapter purpose, but no
  production use was found in these checkouts (workers.WithWakeChannel is a
  different API). Retain it as an intentional convenience, not as evidence that
  the outbox needs a bus just to wake. Its Bus parameter is broader than necessary.
- Original gopernicus infrastructure/events contains the same typed/metadata/
  encoding/broadcast concepts and analogous async bus machinery. It confirms
  architectural intent, not correctness. No restoration of its larger registry
  or original authentication workflow machinery is justified here.

## Ranked findings

### E1 — P1: successful emission can mean durable handoff was discarded

Memory.Emit after Close returns nil (memory.go:163–169,183–185), including WithSync.
Redis Emit does likewise for Streams (bus.go:180–198). The outbox poller marks a
row published whenever this call succeeds (pockets/events/poller.go:94–109).
Independent SDK/pocket probe: closed Memory, one unpublished record, Poll ->
`marked_published=true error=<nil>`. Shutdown-order documentation acknowledges this
hole; it does not make a successful checked handoff truthful. Reject closed
checked delivery with a stable error and retain the row for retry. Keep Noop for
explicitly disabled weak notifications; it must not silently satisfy a checked
outbox delivery operation.

Related policy, not a newly discovered race: async Memory queue overflow returns
nil with a warning (memory.go:191–196). Existing tests explicitly expect loss.
Returning a capacity error would improve admission observability but is a behavior
change requiring migration. Outbox correctly avoids that queue today with WithSync.

### E2 — P1: remote transport discards durable identity and routing metadata

Record owns EventID and metadata (record.go:30–38). The pocket adds EventID through
its own outboxEvent wrapper because RemoteEvent lacks it (poller.go:112–156).
Redis stream fields and broadcastEnvelope omit the ID and explicit metadata
(bus.go:173–178; broadcast.go:29–34). Receivers instead infer routing fields from
payload JSON, although SDK Metadata does not require those JSON fields.

Independent real Redis probe sent a record-like event with ID, tenant and aggregate
in its envelope and ordinary JSON payload: the received RemoteEvent had no EventID,
nil tenant and nil aggregate ID. Hub uses correlation as an SSE ID fallback and
suppresses resource-filtered events lacking metadata (hub.go:111–131,275–295).
Segovia's job drain rejects missing durable IDs rather than risk duplicate jobs.
A Redis substitution therefore breaks a real host pattern, not just a hypothetical
future feature.

Recommendation: one complete transport envelope (use Record as the canonical
shape), a straightforward Record-to-RemoteEvent conversion preserving its ID, and
explicit metadata fields on both Redis transports. Remove the pocket-local wrapper
and payload metadata inference for new messages. Preserve legacy decoding only as
an explicit migration choice. Replaying/republishing a record must preserve its
EventID; correlation is not an event identity.

### E3 — P1: advertised Redis at-least-once recovery does not exist

consumeBatch uses XREADGROUP with only `>` (bus.go:362–380). There is no pending
read, XCLAIM or XAUTOCLAIM path. Seed a pending claim owned by a dead consumer,
then start a new bus and add a fresh event: the fresh event is handled while the
abandoned entry remains pending. Probe: `handled=1 abandoned_pending=1`.
The README explicitly claims unacknowledged messages are redelivered; that is
false for this implementation.

Redis documents that `>` without claiming returns new entries, and pending work
needs explicit recovery. See [XREADGROUP](https://redis.io/docs/latest/commands/xreadgroup/)
and [XAUTOCLAIM](https://redis.io/docs/latest/commands/xautoclaim/). The local
Redis 8.4.0 probe confirms the implementation consequence; no Redis upgrade is
needed to explain the defect.

Separately, processMessage always ACKs parse errors, handler errors and panics
(bus.go:403–435). This is the documented poison-message policy, not accidental
missing error handling. It is incompatible with describing successful processing
as guaranteed. Preserve the intentional Streams capability, but complete explicit
reclaim/retry/terminal-disposition behavior before claiming at-least-once processing.
Keep that backend lifecycle in the integration, not a new generic SDK broker engine.
MaxLen trimming/retention must be documented alongside pending recovery; persistence
alone does not make handlers reliable.

### E4 — P1/P2: subscription state is not kept consistent with consumption

- Unsubscribe removes handlers but leaves streams in groups (bus.go:506–515).
  A live instance continues consuming and ACKing with no matching handler.
  Real probe: unsubscribe the only handler, publish, then observe group lag=0 and
  pending=0 with zero handler calls. This can also steal work from an active peer.
  Remove streams that no active subscriber needs and define already-claimed work
  handling. A consumer group also needs consistent handler responsibilities across
  its instances; one group cannot promise delivery to every distinct subscriber.
- Subscribe reports success when group creation failed; ensureGroup only logs and
  does not retain a retry target (bus.go:251–256,293–315). A receive-only process
  can remain inert after startup dependency failure. Source-confirmed, not fault-
  injected in this slice. Return setup failure or track explicit retryable state.
- A server-side missing group leaves the cached groups set stale, so NOGROUP can
  block the multi-stream read indefinitely (bus.go:293–315,374–393). Source-
  confirmed; no destructive live group deletion probe was needed here.
- Streams Subscribe("*") discovers only topics emitted by that same instance.
  Real remote-only probe: delivered=0, stored=1. This limit is documented in the
  Redis README but conflicts with the SDK's unconditional wildcard wording.
  Make the consumer/broadcast distinction explicit; do not claim a shared wildcard
  contract while implementing only a local-topic subset. See design choices below.

### E5 — P1/P2: lifecycle completion and post-close behavior are misleading

Memory.Close returns nil on timeout, and a later Close immediately returns nil
while handlers still run (memory.go:273–295). Probe: deadline expired,
`error=<nil> second_error=<nil> handler_still_running=true`. Use one shared done
channel; every Close caller waits on real completion with its own context and
receives its context error if still incomplete. Redis already returns the first
caller's deadline error, but later calls also skip the unfinished wait.

Redis performs broadcast before its closed check (bus.go:163 vs180–198). Real
probe: an already-closed publisher still delivered `closed` to a remote broadcast
subscriber. Admission/lifecycle checks must happen before encoding/network work;
track every admitted transport operation.

Sync dispatch is currently excluded from tracked work in both implementations.
The SDK Close comment specifically promises async drain; expanding it to all
admitted dispatches is a deliberate contract improvement, not a claim that an
existing async-only promise was broken. Recommend host-owned Close cover the new
explicit Dispatch operation too. Handlers still must cooperate; no API can forcibly
stop arbitrary Go callbacks. A callback must not synchronously wait for its own
bus to finish shutdown.

### E6 — P1/P2: handler isolation and local resource cleanup have concrete defects

- Redis broadcast invokes handlers without recovery (broadcast.go:156–161).
  An isolated real Redis subprocess terminated with exit 2 and
  `panic: audit broadcast handler`. A subscriber should not terminate the host.
- Memory async recovery wraps the entire event, so a panic skips later unrelated
  subscribers (memory.go:136–155,209–233). Probe: later subscriber calls=0 while
  a subsequent barrier event proves the worker survived. Streams has the same
  per-message recovery issue before unconditional ACK. Recover per invocation,
  continue other subscribers, and expose failure to the chosen checked path.
- Memory deletion retains empty topic entries and backing-array subscription
  pointers (memory.go:258–268). Internal overlay probe after one unsubscribe:
  retained_topics=1, retained_handler_slot=true. Delete empty map entries and
  clear the vacated slice slot so closures can be collected.
- WithLogger(nil) remains nil (memory.go:82–85,113–116); a drop/error panics while
  trying to log, and async recovery uses that same nil logger. Probe confirms
  closed Emit panics. Apply the normal default logger for nil.
- An ordinary async error is logged by dispatch and then again as firstErr by
  dispatchRecovered. Use one reporting owner for each handler failure.

### E7 — P2: encoding and ownership promises are wider than their implementations

EventEncoder advertises arbitrary encodings (record.go:8–12), but Redis broadcast
puts those bytes in json.RawMessage. Binary payloads fail envelope marshaling;
Emit can still succeed after Streams XADD. Probe: binary Emit nil error, one
stream entry, broadcast encoding rejected. Carry opaque bytes in the envelope
(or explicitly narrow the public contract); do not add a codec registry. Keep
TypedHandler's RemoteEvent decoding explicitly JSON-only. A custom binary consumer
can decode payload itself; arbitrary transport bytes do not imply generic codec
support in every helper.

NewRecord aliases encoder-owned payload bytes and metadata pointers (record.go:45–60).
Probe: changing the source after NewRecord changed the record's tenant and payload.
Recommend a bounded snapshot copy at record construction/conversion. This does not
justify trying to deep-copy arbitrary typed events on every Memory Emit: document
those values as immutable after submission and safe for concurrent handlers.

TypedHandler has real utility for typed/replayed events. Its silent ignore of a
nonmatching, non-Unmarshaler event is documented and tested; it is a policy worth
making explicit, not grounds for removing generic handlers. Do not invent runtime
reflection just to detect every typed-nil or dynamic event misuse.

### E8 — P2: validation and cancellation policy need a small explicit contract

Emitting an event of type "*" dispatches the wildcard list twice in Memory and
Redis local dispatch (memory.go:212–216; bus.go:480–485). SDK probe: deliveries=2.
Reserve "*" for subscription patterns and reject it as an emitted type, or select
that list once; rejecting empty/reserved event types consistently is clearer.
Reject nil events and nil handlers as invalid input at public boundaries, and
reject Subscribe after Close. Memory currently accepts a useless late subscription.

A pre-canceled sync Memory Emit still invokes handlers and returns nil; Noop
also returns nil. Async detaches caller cancellation by documented design.
Recommend checking cancellation before accepting new work, then detaching only
accepted asynchronous work while retaining context values. Treat this as an
explicit compatibility change. Document that handlers may run concurrently,
events are not globally ordered, subscriptions are selected at dispatch, and
Unsubscribe does not wait for callbacks already selected. Avoid adding ordering,
deep copying or transactional delivery as accidental SDK guarantees.

## Recommended simplification and implementation sequence

Keep the useful mechanisms: Emitter, typed events/BaseEvent, optional metadata,
Record, RemoteEvent, TypedHandler/Unmarshaler, bounded Memory, Noop, subscriptions,
and the tiny WakeChannel adapter. Close owns real goroutines here and must remain;
this differs from the removed no-op cacher/limiter lifecycles. Logger configuration
also has real behavior and actual consumers.

Replace ambiguous WithSync with explicitly named operations. Recommended shape
(names and signatures are a proposal, not a frozen implementation):

```go
bus.Emit(ctx, event)                 // weak, bounded asynchronous notification
memory.Dispatch(ctx, event)         // run local handlers, return their failures
redisStreams.Publish(ctx, event)    // await remote publication acceptance

// Consumer-declared function: the host picks the outbox's required handoff.
NewPoller(repo, memory.Dispatch)
NewPoller(repo, redisStreams.Publish)
```

The outbox only marks published after that function returns success. This retains
Segovia's local event-to-job transaction boundary and removes its unnecessary
requirement for a full Bus (subscribe/close). It also removes forced Redis local
handler execution merely to pass a misleading shared WithSync test. A remote
publication acknowledgment does not mean a remote handler finished; recovery and
processing acknowledgment remain the Streams consumer's responsibility. Noop must
not supply a checked discard function as a default. Required subscriber wiring
remains host-owned; an empty local dispatch cannot invent a missing job consumer.

### Step 1: SDK and the minimum affected caller/transport corrections

1. Freeze the three operation contracts above, accepted-input/cancellation rules,
   closed/queue-full errors, panic reporting, Dispatch/Close accounting and wire
   format migration. Retain bounded weak notification; do not replace it with an
   unbounded goroutine per Emit (Redis currently does that for async XADD).
2. Fix Memory lifecycle/retention/panic/nil-logger/wildcard defects and expand shared
   conformance around truthful admission and subscription behavior. Separate local
   Dispatch tests from remote publication tests; do not force remote local delivery.
3. Make Record the complete envelope and preserve EventID/metadata/opaque payload
   through RemoteEvent and Redis. Replace the pocket-local rehydration wrapper.
   Decide backward decoding/versioned broadcast envelope deliberately; stored
   outbox payloads need not change just to preserve their existing envelope.
4. Migrate Poller to its consumer-owned delivery function and update in-repository
   examples/tests. Preserve actual Segovia behavior in a framework test; record
   its later consumer migration without editing that checkout.
5. Record implemented changes in AUDIT-012/RELEASING only after implementation;
   run full workspace/docs/race/HTTP/live adapter gates then.

### Step 2: complete the intended Redis Streams contract

Implement pending recovery and explicit failure disposition, subscription/group
reconciliation, bounded publication work, and tests with multiple processes and
startup/outage/restart boundaries. Do not rename an incomplete durable path as
reliable or delete it merely because implementation is unfinished.

Two decisions need explicit resolution in its concrete plan:

- Shared subscription semantics: a Streams consumer group processes work once per
  group; a notification/broadcast subscription receives its own copy. Prefer
  exposing Streams consumption explicitly at the integration boundary, with exact
  topics and documented homogeneous group responsibilities, while retaining wildcard
  notification/broadcast. If Redis must continue to satisfy Bus.Subscribe's full
  wildcard contract, implement real remote topic discovery and test it; the current
  local-only shortcut is not acceptable. Avoid a generic SDK broker framework.
- Failure disposition: choose retry/reclaim timing and terminal handling (including
  malformed messages and shutdown) deliberately. The default must not silently ACK
  a failed required handler while claiming reliable processing. Retention/trimming
  policy belongs to the host/integration and must not silently discard pending work.

Both steps address this review's findings; Step 1 alone does not complete the
Redis durability findings. Full pockets/events domain/store/SSE authorization
review remains a later pocket slice, after the SDK capability pass. Once the
SDK events correction is verified, next SDK audit is S9 filestorage, followed by
email/notify/oauth/tracing, then S10 pocket wiring.

## Verification and handoff

Passed (review baseline; production code intentionally unchanged):

- `go build ./...`, `go test ./...`, `go vet ./...` in sdk, pockets/events and
  integrations/kvstores/goredis, using GOCACHE above. Log:
  /tmp/gopernicus-events-review-build-test-vet.log.
- `go test -race ./sdk/capabilities/events/... ./pockets/events/... -count=3`.
  Existing SSE/HTTP tests needed approved local listeners. Initial sandbox-only
  attempt failed on bind; rerun passed in
  /tmp/gopernicus-events-review-race-verified.log. Original failure preserved in
  /tmp/gopernicus-events-review-race.log.
- All 23 `make guard` checks passed; /tmp/gopernicus-events-review-guard.log.
- Redis 8.4.0 disposable live bus conformance + broadcast fanout race suites,
  count=3, passed in 8.471s. The independent probes still reproduced E2–E7;
  green existing tests are not evidence those findings are fixed.

Independent proof artifacts:

- /tmp/gopernicus-events-sdk-probe.go + .log: closed-bus outbox data loss,
  record aliases, literal-star duplicate, late Subscribe, pre-canceled sync
  dispatch, Noop/closed return policy, false Close completion, panic fanout.
- /tmp/gopernicus-events-internal-probe_test.go +
  /tmp/gopernicus-events-internal-overlay.json: virtual Go test file; no source
  added to the repository. `go test -race -overlay=<overlay> ./sdk/capabilities/events
  -run '^TestAuditRetentionAndNilLogger$' -count=1 -v` confirms retained closures
  and nil logger panic. Log /tmp/gopernicus-events-internal-probe.log.
- /tmp/gopernicus-events-live-review.py runs owned loopback Redis with persistence
  disabled, `go test -race -run '^(TestConformance_Bus|TestBroadcastFansOutAcrossInstances)$'
  -count=3 -v .`, then a separately built race-enabled probe binary from
  /tmp/gopernicus-events-live-probe.go. It simulates an abandoned pending claim,
  unsubscribed ACK, remote wildcard, lost envelope, post-close broadcast,
  non-JSON payload and forced sync duplicate. Expected-crash subprocess confirms
  broadcast panic. Full output: /tmp/gopernicus-events-live-review.log.
  Crash log: /var/folders/qm/bjn1lmt54tlf9fc8vblm1hc80000gp/T/gopernicus-events-review-obtlbdjj/broadcast-panic.log.
  Owned Redis stopped in finally; no existing database/service touched.

Pub/sub is intentionally ephemeral, so ordinary disconnect loss is not a defect;
[Redis Pub/Sub documentation](https://redis.io/docs/latest/develop/pubsub/) supports
that boundary. References above are primary Redis docs checked during review.
The implementation-specific failures are supported independently by local source
and probes; no external source supplies the repository conclusions.

Source-only/unverified edges: startup group failure and NOGROUP recovery were
reviewed, not fault-injected; no distributed load benchmark, Redis cluster/failover,
real consumer application run, live outbox store, production/cloud service or full
pocket audit is claimed. Existing unrelated live-store tests retain their normal
skips. Full make check/docs-build were not repeated for two review Markdown files;
the prior limiter implementation's full gates remain historical evidence only.

At review completion there was no unresolved setup/approval failure. Confirmed
defects were left for the implementation, now completed in events-implementation.md. Named backend/platform reviews were read-only; parent
ran the independent tests/probes and consumer/original checks. Consumer checkouts
were not changed. Initial caller search included vendor copies; it was rerun with
vendor exclusions and only real production calls inform the sample above.

Exact changed files against the 1968-file review baseline:

- plans/framework-audit-events.md (this review).
- plans/framework-audit.md (progress and next-session handoff).

At review completion AUDIT.md remained through AUDIT-011, without speculative
migration instructions. AUDIT-012 now records the implemented changes. Continue S9
filestorage after the completed events-implementation.md slice. Preserve all prior
changes and the pgxdb pool-default follow-up recorded in the limiter plan.
