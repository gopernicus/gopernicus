# Events pocket audit

Status: COMPLETE — 2026-09-10; independent review and documentation checks passed.
Evidence is tracked in [jobs-events-audit-implementation.md](jobs-events-audit-implementation.md).
This is the pocket audit, distinct from the completed SDK
[events audit](framework-audit-events.md). Migration: AUDIT-021.

## Reviewed areas and disposition

| Area | Result |
| --- | --- |
| Construction, optional routes, middleware, logger, ownership | Added explicit operational logger, copied middleware, public idempotent Close and nil-router errors. Mounting remains optional; host owns HTTP, poller and bus lifetime. |
| Identity, visibility, resource scopes | Added required host Visible policy for streams. Authentication alone no longer exposes the whole bus. Resource authorization and aggregate/type filters remain additional checks. No implicit SDK tenancy rule. |
| SSE projection, slow clients, caps, cancellation | Visibility and projection run per connection before serialization, off the shared bus callback. Bounded channels drop slow-client events. Principal is the cap key without delimiter collisions; request deadlines bound supported writes. |
| Outbox identity/payload/ownership | Whole-batch validation prevents malformed new head records; raw bytes including empty/non-UTF8 are preserved. Returned in-memory records are detached. PG bytea conversion has an upgrade regression; matching Turso migration is a no-op. |
| Transactions, delivery, acknowledgement, replay | Append owns its transaction by existing contract; AppendTx couples host mutation and outbox atomically and is rollback-tested. Poll delivers before marking, checks cancellation, retains failures, and reports nil dependencies. EventID supports consumer deduplication. |
| Store parity, migrations and schemas | Expanded shared conformance passed live PostgreSQL bare/named/decoy schemas and local libSQL with race detection. Historical stored JSON text is preserved by PG migration; prior normalization cannot be undone. |
| Host setup and simplicity | Preserve separate gateway and Poller: either can be used independently. No bus registry, background lifecycle manager, cross-pocket import or generic notification policy was added. Host example explicitly chooses a shared authenticated feed. |

## Limits and future features

- SSE is a lossy wake-up stream. No persisted replay, guaranteed ordering across
  concurrent emissions, or durable client acknowledgement is promised.
- Host visibility/projector functions must be concurrency-safe, not mutate
  events, and honor cancellation. A timeout cannot forcibly stop arbitrary Go
  code. Custom ResponseWriters must support deadlines for blocked-write bounds.
- Gateway Close unsubscribes; it does not drain HTTP requests or close the bus.
- One poller per outbox remains the supported topology. There are no row claims
  for competing pollers. Multiple destinations/atomic cross-system effects and
  a durable dead-letter repair workflow would need separate product design.
- A persistently failing delivery or invalid historical first row holds up later
  records. Repair/quarantine is explicit operator policy; records are not silently
  skipped. New Append validation prevents new malformed records through the API.
- Outbox persistence couples the domain commit to an event record, not to remote
  effects. Failed MarkPublished or process loss after delivery can replay events.
- Redis acceptance is a different handoff from local Memory.Dispatch completion;
  the existing SDK/Redis audit covers that distinction. Real hosted Redis/provider
  deployments were not re-exercised in this pocket slice.

## Evidence

The implementation plan owns the exact changed paths and verification commands.
Temporary reports: /tmp/gopernicus-events-review.md (baseline source review),
/tmp/gopernicus-events-implementation-report.md (implementation/checks), and the
final reviewer report recorded by the parent. No authentication/authorization
pocket implementation or external consumer repository was changed.
