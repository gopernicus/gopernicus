# Deferred follow-up — effects and observability

Status: **DEFERRED; not part of the authorization v3 completion gate.**
Depends on: completed authorization v3 mutation/receipt contract plus separate
owner ratification of event cardinality, procedural retry semantics, and store
composition.

## Goal

Compose completed authorization changes with events/jobs without creating a
third queue or pretending a post-commit callback is durable or exactly-once.

## Task AZFX-1 — stable authorization change event envelope

Touch: public/domain event types and tests.

Implement:

- Event types for relationship granted/revoked/replaced/purged and role
  assigned/unassigned. Use lowercase stable names such as
  `authorization.relationship_granted`.
- One mutation command produces one semantic envelope with
  EventID=MutationID. It includes occurred time, actor principal or trusted
  system provenance, operation, scope, schema digest, resulting revision, and
  domain outcome.
- Single-row grant/revoke/replace operations may include exact old/new subject
  state. Bounded batch operations may carry a bounded change list. Purge and
  teardown envelopes carry scope plus bounded aggregate counts, never an
  unbounded tuple dump. If consumers later require per-row events, derive stable
  child IDs such as `MutationID/index` under a separately versioned contract.
- Define JSON compatibility and optional bounded correlation metadata. Do not
  add decorative tenant metadata: if tenancy is later supported, tenant identity
  must first become part of authorization scope keys, MutationID namespace, and
  receipt/event isolation.
- Do not include display names, email/phone, HTTP headers, raw store errors, or an
  unbounded explain trace.
- Encode once through sdk event helpers; procedural and events modes consume the
  same semantic envelope.

Verify:

```sh
cd features/authorization && go test ./... -run 'Event|Envelope|Encode|Redact'
make guard
```

Acceptance: one mutation has one stable de-duplication/event identity in every
mode.

## Task AZFX-2 — procedural post-commit effect mode

Depends on: AZFX-1.
Touch: service effect dispatcher and tests.

Implement:

- In procedural mode, call the configured handler only after Apply returns a
  committed applied receipt. Do not call on denial, stale/conflict, or a
  first-call no_change outcome. An exact replay of an applied mutation may call
  the handler again because the feature persists no external-effect checkpoint;
  the handler's MutationID de-duplication absorbs it.
- Handler may notify synchronously or hand off asynchronously; the feature does
  not start an unbounded goroutine.
- The procedural guarantee is **at-least-once attempt under client retry**, not
  exactly-once delivery. On handler error, return a typed
  committed-post-effect failure carrying the receipt. Retrying the same
  MutationID never remutates state but may call the handler again.
- Require the handler to de-duplicate its externally visible effect by
  MutationID. A handler that cannot do so is incompatible with procedural retry
  and must choose off or durable events mode. Mutation receipt persistence alone
  cannot determine whether a prior external effect succeeded before an
  ambiguous response.
- Log only event type, domain outcome, and coarse error kind.
- Test panic containment if handlers are allowed to be third-party; otherwise
  document panic as host-fatal and do not recover inconsistently.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Procedural|PostCommit|Effect|Replay'
make guard
```

Acceptance: effect failure can never make a committed grant look rolled back,
and documentation makes repeated handler attempts explicit.

## Task AZFX-3 — same-transaction events-outbox mode

Depends on: AZFX-1 and completed AZ3-2.3/AZ3-2.4.
Touch: pgx/turso composed store constructors and tests; no core feature import.

Ratification precondition: the dialect-typed transactional appender port must be
expressible in sdk plus database-driver types only — no `features/events` types
may appear in its signature. If the events store's `AppendTx` cannot satisfy
that shape, the events-side port is revised first;
`guard-store-no-foreign-feature` must pass unmodified.

Implement:

- Store adapters accept a dialect-typed transactional appender interface
  structurally satisfied by the matching events store `AppendTx`.
- In events mode, Apply inserts the mutation receipt/state/revision and event
  outbox row in the same transaction.
- Construction fails when events mode is selected without an appender, missing
  event table boot probe, or explicit host acknowledgment that the events poller
  is run.
- Rollback and duplicate EventID roll back the domain mutation. Mutation replay
  returns the original receipt without appending again.
- Demonstrate a generic events subscriber enqueueing a generic jobs handler for
  a notification, through the settled `sdk/capabilities/work` protocol and the
  `features/jobs` fenced surface as shipped by the delivery refactor.
  Authorization imports neither feature.

Verify:

```sh
cd features/authorization/stores/pgx && go test -race -count=1 ./... -run 'Event|AppendTx|Rollback|Replay'
cd features/authorization/stores/turso && go test -tags=integration -race -count=1 ./... -run 'Event|AppendTx|Rollback|Replay'
make guard
```

Acceptance: killing the process after commit can lose neither the mutation event
nor require a bespoke authorization queue.

## Task AZFX-4 — decision/mutation observers, safe logs, and metrics guidance

Depends on: completed AZ3-1.6 and AZFX-1.
Touch: observer ports, Register logging, docs/tests.

Implement:

- Optional low-overhead decision observer with outcome/reason, duration, query
  count, depth, and limit-exhausted flag. Resource/subject IDs are fields only
  where the host explicitly opts in; they are never metric labels.
- Mutation AuditSink records accepted/denied/failed/committed/effect-failed using
  coarse error classes and MutationID.
- Register logs schema digest, enabled kinds, limits, guard/system availability,
  and effect mode—never model contents or grant data.
- Provide host guidance for cache invalidation through mutation revision/events,
  while keeping a built-in cache out of v3.
- Add failing-observer tests proving decision/mutation behavior is unchanged.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Observer|Audit|Logger|Metrics|Failure'
make guard
```

Acceptance: observability failures do not grant, revoke, or alter a committed
mutation, and default telemetry is bounded-cardinality.

## Task AZFX-5 — construction negatives and real-interaction effects proof

Depends on: AZFX-2 through AZFX-4.

Test the full matrix:

- off + no handlers succeeds; off + orphan handler/appender errors;
- procedural + handler succeeds; procedural + nil handler errors;
- events + atomic appender + acknowledgment succeeds;
- events without appender/table/acknowledgment errors;
- event mode transaction rollback leaves no mutation or event;
- procedural effect failure returns committed receipt;
- exact replay does not duplicate the durable outbox row; procedural replay may
  repeat the handler attempt and the handler de-duplicates MutationID; and
- events → generic jobs handler runs at least once and de-dupes MutationID.

Verify:

```sh
cd features/authorization && go test -race ./... -run 'Construction|Effect|Event'
make check
make guard
```

Acceptance: guarantee differences are observable in tests and documentation,
not hidden behind one “async” boolean.

## Phase acceptance

- Stable event envelope exists.
- Procedural and durable modes both work and state their guarantees.
- Durable mode is transactionally proven on both dialects.
- No authorization-specific job repository or core feature import exists.
- `make check` and `make guard` pass.

## Stop conditions

- Durable mode would emit only after commit.
- Procedural mode would start unmanaged goroutines.
- Effect failure could roll back or misreport a committed mutation.
- A generic job needs authorization core to import jobs/events.

## Execution log

Append only during execution.
