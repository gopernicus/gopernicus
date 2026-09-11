# Implement the SDK events audit

Status: COMPLETE — 2026-09-10. Owner approved updates following
[framework-audit-events.md](framework-audit-events.md).
Parent: [framework-audit.md](framework-audit.md).

## Preconditions

Branch/base firestore-authentication / 6807ed06; 602 prior dirty paths. Snapshot:
/tmp/gopernicus-events-implementation-baseline.json. Preserve prior audits and
concurrent changes; compare to this snapshot, not the entire HEAD diff.
Go 1.26.1, 42-module workspace with no root go.mod.
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; goimports at
/Users/jrazmi/go/bin/goimports. Owned disposable Redis 8.4.0 and HTTP probes are
authorized. No external consumer/original edits, pins/dependencies/releases,
deployed state migrations, commits or generated manual edits.

## Frozen contracts and scope

- SDK Emitter.Emit(ctx,event) error is bounded asynchronous notification. Remove
  EmitOption/EmitConfig/WithSync/ApplyOptions. Memory.Dispatch(ctx,event) error
  performs checked local handler completion. Redis Publish(ctx,event) error waits
  for XADD acceptance and best-effort broadcast, with no forced local invocation.
  Noop supports only weak notification, never a checked discard default.
- Bus.Subscribe is notification fanout (exact topic or *); add the narrow SDK
  Subscriber interface for WakeChannel, with Bus embedding Emitter+Subscriber.
  Broadcaster remains a supported optional port. Redis Subscribe delegates to
  SubscribeBroadcast. Its former work subscription becomes the explicit
  SubscribeWork(ctx,topic,handler), accepting exact topics only. This deliberately
  resolves differing SDK/Streams wildcard and delivery semantics. Hosts sharing
  a work group must deploy identical handler responsibilities for each topic.
- Export events.ErrClosed and ErrCapacity matching sdk.ErrUnavailable, and
  ErrHandlerPanic for recovered callback panics. ValidateEvent rejects nil, empty
  type and reserved type '*'; ValidateSubscription rejects empty topic/nil handler.
  No reflection for typed-nil/custom-method misuse. Context checked before
  admission; asynchronous work detaches cancellation only after admission. Caller
  owns immutable event graphs and concurrent/idempotent handlers; no total ordering.
- Memory keeps current constructor options/defaults (4 workers/1000 queue),
  normalizes nil logger, returns closed/capacity errors, recovers per callback,
  reports each async handler failure once, and returns first error from Dispatch.
  Dispatch checks context between callbacks and before success. Close refuses new
  work/subscriptions, drains queued work and admitted Dispatch calls, and every
  caller waits on one done channel with its own context. Timeout returns ctx.Err;
  no forced callback termination. Unsubscribe clears vacated slots/empty topics;
  callbacks already selected may finish. Host owns Close, not handler callbacks.
- Record is the canonical JSON envelope: event_id, type, occurred_at,
  correlation_id, payload ([]byte, base64 in JSON), optional aggregate_type,
  aggregate_id, tenant_id. NewRecord validates, preserves nonempty EventID from
  optional Identified, otherwise generates it, and copies payload/metadata.
  RemoteEvent embeds Record; methods expose Event/Metadata/Identified/Unmarshaler/
  EventEncoder. Record.Event() returns a copied RemoteEvent. Record.Validate()
  requires valid type and nonempty ID. Time/correlation remain caller vocabulary.
  RemoteEvent.Unmarshal is explicitly JSON-only; opaque payload transport does
  not require a new codec registry. Remove DecodeRemoteMetadata. New outbox replay
  uses Record.Event(), removing the pocket's duplicate wrapper. Existing stored
  Record fields/payloads require no outbox schema migration.
- Pocket Poller takes consumer-declared DeliverFunc(context.Context,Event) error,
  not Bus. Hosts wire memory.Dispatch or redis.Publish. Nil callback returns
  sdk.ErrInvalidInput. Check cancellation before each delivery and marking; never
  mark after failure/cancellation. Preserve Segovia's synchronous outbox-to-job
  enqueue boundary in an in-repository regression (no consumer edits).
- Redis keeps New(client,logger,Options), caller-owned client. Add bounded async
  QueueSize (1000), keep Workers (4; bounded publisher/consumer worker pools).
  Lifecycle admission precedes encoding/network effects; Close drains admitted
  publications and waits for active callbacks, with shared completion for repeats.
  Reject closed Subscribe/SubscribeWork/Emit/Publish. Each callback is isolated;
  broadcast readiness waits for server subscription acknowledgment with a bounded
  setup context. Underlying client timeout/context configuration governs prompt
  network interruption; no shared client config changes in this slice. Failed
  setup is an error, not inert success. Do not hold lifecycle
  locks over network calls or callbacks. Publish success is remote acceptance,
  not handler completion; committed publication is not undone by cancellation.
- Redis wire/state version: append internal v2: to StreamPrefix for all streams
  and broadcast. New Redis entries carry one serialized Record envelope, preserving
  ID/metadata and opaque bytes on both paths. No automatic old-message conversion,
  deletion or replay. Hosts drain old streams with old code / stop old writers and
  coordinate upgrade; document disjoint prefixes, duplicate-safe replay, old pending
  retention, no mixed-version assumptions. No MaxLen auto-trimming on reliable work:
  remove that option; host explicitly owns archival/trimming after acknowledgments.
  Remove BatchSize too: each sequential worker fetches/claims only one record from
  one stream, so waiting batch tails cannot exhaust their lease before execution.
- Work recovery: default RetryAfter=1 minute, HandlerTimeout=30 seconds, both
  positive whole-millisecond durations; RetryAfter must exceed HandlerTimeout.
  HandlerTimeout covers the entire selected-handler attempt, with context checks
  between callbacks and immediately before ACK. Validate work configuration at
  SubscribeWork. XAUTOCLAIM (Redis >=6.2) reclaims
  idle pending work, alternating with new reads so failures do not block fresh
  work. Retain each stream's returned claim cursor, including empty scans with a
  nonzero continuation. Handler errors/panics, malformed or stream/type-mismatched records, cancellation and no current
  matching handler leave entries pending; ACK only after every selected handler
  succeeds. No automatic poison discard/max-attempt/DLQ abstraction. Permanent
  poison stays pending and observable until host-owned repair or explicit terminal
  disposition using its Redis client. This is the chosen terminal policy. Register a single composed handler where several
  required responsibilities must be ready together during startup.
- Exact-topic work registrations create/reconcile groups before success. Workers
  read only active topics; unsubscribe stops new reads and already-claimed work
  without handlers stays pending for a peer. NOGROUP triggers recreation at 0
  (duplicates possible after group loss), never an endless stale-cache read.
  Reclaim can redeliver a callback that ignores its timeout; idempotency is required.
  No distributed exactly-once promise or hidden serialization guarantee.

## Tasks and ownership

1. Parent: SDK vocabulary/record/Memory/Noop/tests/shared conformance, pocket/caller
   migration and regression, examples, docs/AUDIT-012/RELEASING/master handoff.
2. Named implementer: Redis bus/broadcast/work implementation, bus-related tests
   and bus-only portions of conformance_test.go and README/doc.go. No limiter,
   cache/client/hooks changes. Own new bus/work-specific files if needed.
3. Named backend/platform: read-only review of concrete contracts, lifecycle and
   recovery at useful checkpoints. No new approval boundary.
4. Parent: format, targeted/race/live Redis regressions, real HTTP/host behavior,
   full 42-module make check and docs-build. Record exact task-relative inventory,
   historical setup failures and unverified scope separately.

## Results and verification

Implemented SDK interfaces/admission/errors, Memory lifecycle and callback isolation,
Noop validation, complete snapshotting Record/RemoteEvent, and the explicit Poller
DeliverFunc boundary. All in-repo emitter wrappers and the auth-cms poller compile
against the new API. Redis notification/work separation, bounded publishing,
versioned envelopes, pending reclaim and conditional ACK are implemented.

Named backend/platform reviews found the lifecycle and snapshot design sound.
Their concrete corrections are included: preserve earlier Dispatch failure when
cancellation follows; use one whole work-attempt deadline; claim one record from
one stream per available worker; retain reclaim cursors; validate stream/type
agreement; install subscriptions only after acknowledged setup and closure recheck.
Close/unsubscribe release retained handler references. No additional abstraction
or approval boundary was needed.

Verification (2026-09-10):

- `/Users/jrazmi/go/bin/goimports -w` on task-changed Go files.
- SDK events/pocket `go test -race -count=3`, including callback panic isolation,
  concurrent admission/drain, repeated/timed Close, cancellation and reference
  release; full SDK `go build`, `go test`, `go vet` after final cleanup.
- `pockets/events`: `go test -race -count=3 ./...`, including actual loopback
  HTTP/SSE streaming. The first sandbox run failed to bind httptest listeners;
  the authorized loopback run passed. Poller regressions cover closed delivery,
  cancellation before marking/next delivery, failure retry and invalid records.
- Auth-cms `TestOutboxDispatchToJobsPreservesIdempotentHandoff` passed three race
  runs. This uses the actual Poller → Memory.Dispatch → jobs.Service composition
  with memory repository stand-ins, proving enqueue before mark and stable-ID
  replay after a failed mark. It does not claim SQL durability. The migrated
  `livedelivery` test wrapper also passed compile/vet.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check` passed: 42 modules'
  build/test/vet, generation/scaffold checks, integration/live-tag compile checks
  and all 23 layering guards. Log: /tmp/gopernicus-events-make-check.log.
  Final SDK cleanup and later Redis test additions passed their own targeted
  build/test/vet/race checks after this workspace run.
- `make docs-build` passed the site typecheck and production build after all
  site-page edits. A non-fatal update-config warning did not affect the build.
  Later README edits do not supply pages to that build. Migration is AUDIT-012;
  RELEASING includes the unreleased entry. The Redis README also corrects its
  stale limiter Close description from the preceding audit; limiter source is
  unchanged. No dependency/pin/generated changes.
- Initial live Redis 8.4.0 race count 3 passed envelope/binary/ID/metadata round
  trips, ACK/retry after error/panic/timeout, fresh-work progress, malformed pending
  retention, other-consumer reclaim, NOGROUP recreation, notification panic
  isolation, closed admissions, shared conformance and cross-instance fanout.
  One test setup did not produce its intended failure: a wrong password against
  the disposable no-password server still allowed subscription. Replace that
  faulty assumption with injected connection failure and verify successful retry
  and setup/Close races before accepting the final live run. Initial log:
  /tmp/gopernicus-events-live-implementation.log. Owned Redis stopped in finally.

- Final Redis command, on a fresh disposable Redis 8.4.0:
  `go test -race -run '^(TestBus|TestConformance_Bus|TestBroadcastFansOutAcrossInstances)' -count=3 -timeout=180s -v .`
  passed (7.143s). Added connection failure → successful subscription retry,
  setup/Close race with a provisional connection, unsubscribed claimed-work
  recovery by a peer, and repeated Close waiting for an active notification.
  Log: /tmp/gopernicus-events-live-implementation-final.log; runner:
  /tmp/gopernicus-events-live-implementation.py. Both owned Redis runs stopped
  their own servers in finally; no existing Redis state was used.
- Final Redis module `go build ./...`, `go test ./...`, `go vet ./...` passed.
  The ordinary module run retains env-gated skips; the explicit live command
  above verifies the changed events behavior against Redis.
- Task-relative `git diff --check`, formatter and removed-API scans passed.
  Current source has no EmitOption/EmitConfig/WithSync/DecodeRemoteMetadata callers;
  migration/historical documents retain those names intentionally.

No unresolved implementation/test/setup failure remains. Full pocket audit,
external consumer upgrades, live SQL/cloud stores and Redis cluster/failover/load
validation remain outside this slice. The host must perform the AUDIT-012 Redis
cutover and choose pending disposition/retention before upgrading deployed apps.
Old review probes use removed APIs and are historical evidence, not commands to
rerun unchanged. The prior pgxdb pool-default follow-up remains in
ratelimiter-implementation.md for the later connector audit.

Next: S9 capabilities/filestorage, followed by email/notify/oauth/tracing and S10
pocket wiring. Full pocket reviews still follow the SDK pass. Preserve the approved
root SDK shape, cryptids naming, host password policy and generic worker middleware.

## Task-relative file inventory

See final list below. Baseline excludes ignored files; derive the inventory with
`git ls-files --cached --others --exclude-standard`, then compare SHA256 to
/tmp/gopernicus-events-implementation-baseline.json. Do not infer this slice from
the full dirty HEAD diff. No generated artifacts, dependencies, pins, external
consumer checkouts, releases or deployed state were changed.

47 changed/added files:

- AUDIT.md
- RELEASING.md
- examples/auth-cms/cmd/server/delivery_health_test.go
- examples/auth-cms/cmd/server/jobs_delivery_live_test.go
- examples/auth-cms/cmd/server/jobs_delivery_retry_test.go
- examples/auth-cms/cmd/server/main.go
- examples/auth-cms/cmd/server/outbox_job_handoff_test.go
- examples/auth-cms/internal/deliveryhealth/deliveryhealth.go
- examples/auth-cms/internal/deliveryhealth/deliveryhealth_test.go
- integrations/kvstores/goredis/README.md
- integrations/kvstores/goredis/broadcast.go
- integrations/kvstores/goredis/bus.go
- integrations/kvstores/goredis/bus_lifecycle_live_test.go
- integrations/kvstores/goredis/bus_live_test.go
- integrations/kvstores/goredis/bus_test.go
- integrations/kvstores/goredis/conformance_test.go
- integrations/kvstores/goredis/doc.go
- integrations/kvstores/goredis/work.go
- plans/events-implementation.md
- plans/framework-audit-events.md
- plans/framework-audit.md
- pockets/README.md
- pockets/authentication/internal/logic/delivery/observer_test.go
- pockets/cms/internal/logic/entrysvc/service_test.go
- pockets/events/README.md
- pockets/events/internal/logic/hub/hub_test.go
- pockets/events/poller.go
- pockets/events/poller_test.go
- pockets/events/poller_worker_test.go
- sdk/README.md
- sdk/capabilities/events/events.go
- sdk/capabilities/events/events_test.go
- sdk/capabilities/events/eventstest/eventstest.go
- sdk/capabilities/events/lifecycle_test.go
- sdk/capabilities/events/memory.go
- sdk/capabilities/events/memory_conformance_test.go
- sdk/capabilities/events/memory_test.go
- sdk/capabilities/events/noop.go
- sdk/capabilities/events/record.go
- sdk/capabilities/events/retention_test.go
- sdk/capabilities/events/wake.go
- sdk/capabilities/events/wake_test.go
- sdk/pocket/pocket.go
- sdk/pocket/pocket_test.go
- workshop/documentation/docs/integrations/catalog.md
- workshop/documentation/docs/pockets/events.md
- workshop/documentation/docs/sdk/capabilities.md
