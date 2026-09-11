# Firestore authorization storage

This adapter stores natural relationship tuples and role assignments. The
collections below, `firestore.indexes.json`, and the executable query matrix in
`indexes_test.go` define its storage contract. Existing installations must follow
[UPGRADE.md](UPGRADE.md); constructors never migrate records or deploy indexes.

## Collections and identity

| Collection | Document identity | Purpose |
|---|---|---|
| `iam_relationships` | hash of the complete six-field tuple | relationship facts |
| `iam_relationship_subjects` | hash of resource type/ID and exact subject type/ID/relation | one relation per exact subject and resource |
| `iam_roles` | hash of subject type/ID, role, resource type/ID | role facts |
| `iam_audit` (optional) | generated event ID plus change ordinal | committed fact changes |

All collections are top-level; composite indexes use `COLLECTION` scope.
`iam_relationship_ids` is retired and is deleted by the explicit upgrade.

Document hashes use the connector's length-prefixed `KeyHash`, not a delimiter
join. A complete tuple can exceed Firestore's document-ID limit or contain `/`,
so its hash is a private addressing mechanism. Hashes never determine list order
and are never substituted for domain identity. Their golden values are tested.

## Relationship documents

`iam_relationships` stores:

| Field | Value |
|---|---|
| `resource_type`, `resource_id` | resource identity |
| `relation` | relation on that resource |
| `subject_type`, `subject_id`, `subject_relation` | exact subject identity; empty subject relation denotes a concrete subject |
| `resource_key` | hash of resource type/ID; equality filter key |
| `subject_key` | hash of subject type/ID/relation; equality/expansion key |
| `tuple_key_prefix` | first five tuple components joined by U+0001 |
| `tuple_key_suffix` | U+0001 followed by subject relation |

The natural tuple order is resource type, resource ID, relation, subject type,
subject ID, subject relation. No surrogate relationship ID or tuple timestamp is
stored. Both listing projections retain `SubjectRelation` so concrete subjects
and distinct usersets cannot collapse into the same projected identity.

Every field is written, including empty subject relation. Firestore ordering
excludes a document missing an ordered field; a zero Go value computed while
reading cannot repair an absent index entry. This is why old records need a
backfill before the new reader starts.

### Natural ordering without oversized index values

The public listing order is `tuple_key ASC`, with descending order available.
Joining six 256-byte components and five separators can produce 1541 bytes,
which exceeds Firestore's 1500-byte indexed-value limit. The adapter therefore
orders by the private prefix and suffix pair instead of indexing the full key.
The prefix is at most 1284 bytes and the suffix at most 257 bytes. Their
concatenation is exactly the canonical full key.

U+0001 sorts before every permitted component character and is forbidden inside
components. Comparing the pair is therefore equivalent to comparing the full
joined key, including a shorter subject ID that prefixes another ID. The suffix
always contains its leading separator, so concrete-subject cursor positions are
nonempty. The pair uniquely identifies a tuple even though the suffix alone is
not unique. `List` uses the pair for forward cursors and reverses both fields
for previous-page probes; offset and count use the same ordered population.

Tests cover UTF-8 byte order, prefix collisions, maximum-length values,
distinctions beyond byte 1500 of a full key, both directions, counts, offset
pages, and forward/reverse cursor round-trips. Opaque cursors contain private
query positions; hosts must not construct or interpret them.

### Subject claims and writes

`iam_relationship_subjects` stores only `relation` and `tuple_id`. Its document
hash excludes the resource relation and includes the exact subject relation.
It enforces the independently retained one-relation-per-exact-subject rule.

Each tuple owns two documents: its row and its subject claim. `putTuple` and
`dropTuple` maintain them together, and `replaceTuple` atomically moves the tuple
row to its new relation while updating the existing claim. All reads precede
writes inside the Firestore transaction. Runtime write paths use these helpers;
the explicit upgrade is the only separate maintenance writer.

Raw batch create validates all tuple components before any write. An exact
duplicate or occupied subject claim is a no-op; the first input claiming the
subject wins. Reconciliation (`SetRelationTargets`) rejects a desired subject
already holding a different relation and leaves the prior state intact. It
re-reads the winner after a concurrent conflict, so two requested sets converge
to one whole set. Deletes remove the row and its claim together. An absent exact
tuple cannot delete another relation's claim.

## Role documents

`iam_roles` stores the five natural fields `subject_type`, `subject_id`, `role`,
`resource_type`, and `resource_id`; global grants have both resource fields empty.
It stores no timestamp. Derived fields are:

| Field | Construction |
|---|---|
| `subject_key` | hash of subject type/ID |
| `resource_key` | hash of resource type/ID, including the empty global pair |
| `role_key` | subject type/ID, role, resource type/ID joined by U+0001 |
| `grant_key` | subject type/ID and role joined by U+0001 |

Raw listings use `role_key ASC`; `ListEffectiveByResource` uses `grant_key` and
groups scoped/global provenance for the same subject and role. These keys match
the SQL adapters' byte order. A validated role key is at most 1284 bytes and a
grant key at most 770 bytes. Assign validates components and paired resource
fields, then transactionally creates an absent grant; duplicate Assign does not
rewrite it. Exact role probes use document IDs and issue no query.

## Atomic mutations and optional audit

There are no mutation IDs, durable receipts, revisions or dependency anchors.
`Command.Target` identifies the resource or global-role subject being changed.
Every application runs its guard and current semantic validator, including a
natural no-op. `Result` contains only the outcome and the scoped-role removal
annotation. Semantic conflicts and guardian blocks return stable errors and a
nil result without committing writes.

Guard reads use the native transaction's Reader. Firestore provides serializable
isolation by commit time for documents and queries, including negative predicates.
No replacement counter or lock document is introduced. All reads precede the
staged fact writes. Controlled races cover empty target queries against raw
reconciliation, missing global-role reads against raw assignment, and inherited
allows against raw membership removal. The latter checks compare native document
commit timestamps rather than callback or goroutine completion times.

Only definite `Aborted` transaction contention retries. A private marker preserves
that classification when status information is detached from a callback-visible
read failure. Guard and pure validator refusals retain their exact identity and
remain terminal, including a policy-created Aborted status. Unavailable, deadline,
unknown transport and other ambiguous commit failures never trigger an operation
replay. Exhausted definite contention matches `ErrConcurrentMutation`. Each attempt
resets its result, write plan and audit records.

`WithAudit()` enables recording for all raw and guarded write methods. It is off
by default. Enabled writers validate `audit.SourceFromContext` before work,
including no-op attempts. A source contains either an actor type/ID pair or an
explicit system name, plus an optional bounded reason. Guarded service methods
supply the validated actor; direct trusted/raw callers attach their own source.
Attribution never grants authority.

`iam_audit` stores one document per actual added or removed tuple or role. Fields
are `id`, `event_id`, `occurred_at`, `actor_type`, `actor_id`, `system`, `reason`,
`action`, `kind`, the resource/subject pairs, `relation`, `subject_relation`, `role`,
and private `resource_key`, `subject_key`, `actor_key` equality hashes. Inapplicable
fields are empty strings. `kind` is `relationship` or `role`; `action` is `added`
or `removed`. Full userset identity is retained. IDs join a freshly generated
event ID, a colon, and a one-based 20-digit change ordinal. Event IDs group one
operation's actual changes and have no replay or caller-supplied identity role.

The shared `audit.NewRecords` validates, owns and canonicalizes changes, cancels
opposing deltas, and normalizes timestamps to UTC microseconds. Replacements
record old-fact removal and new-fact addition. No-ops, refusals and failed attempts
record nothing. Claims and derived keys are implementation details, never audit
facts. An audit failure rolls back the fact writes in the same transaction; the
adapter never splits a transaction to fit audit data. Separate per-fact documents
avoid a single event's 1 MiB document-size ceiling. The complete native request
size limit still applies.

`Repositories.Audit` remains readable when recording is off. Listing supports every
combination of optional resource, subject and actor pairs, ordered by `occurred_at`
and `id` in the requested direction (default DESC), with cursor/offset/count.
Timestamps record event construction time inside the committed attempt; they are
not global revision counters or Firestore commit timestamps. Hosts own access,
retention, export and presentation. Old receipts cannot reconstruct this history.

Guardian policy remains explicit and empty by default; stores own its snapshot.

## Queries and indexes

The baseline manifest contains 28 composite indexes: 21 relationship indexes and 7 role
indexes. It declares 16 single-field dependencies (9 relationship, 7 role). The
hermetic matrix checks missing and surplus entries, canonical ordering, equality
prefixes, ranges, directions, and export parity.

| Query family | Equality/filter fields | Ordered fields |
|---|---|---|
| relationship subject listing | subject type/ID; optional resource type and relation | tuple prefix, tuple suffix; both directions |
| relationship resource listing | resource key; optional subject type and relation | tuple prefix, tuple suffix; both directions |
| expansion hop | subject key `in` | none |
| expanded relation check | resource key, relation, subject key `in` | none |
| relation targets/direct count/reconciliation | resource key, relation | none |
| whole-resource rows/deletion | resource key | none |
| candidate scan | resource type, relation, resource ID `in` | none |
| relation resource lookup | resource type, relation and subject-key sets | resource ID, document ID |
| descendant lookup | resource type and relation | resource ID |
| raw role subject/resource lists | subject key or resource key | role key; both directions |
| effective role stream | resource key | grant key; both directions |
| role resource lookup | subject key, resource type, roles | resource ID, document ID |
| tuple, role and claim exact reads | document IDs | no query index |
| audit listing | optional resource key, subject key, actor key | occurred_at, id; both directions |
| explicit maintenance scan | none | document ID |

The separate `firestore.audit.indexes.json` adds 16 composite indexes for all
8 audit filter combinations in both directions. Its field overrides disable
unneeded single-field indexing, including payload and reason fields. Export it
with `ExportAuditIndexes` before recording or listing history. Constructors probe
it only with `WithAudit()`; baseline-only hosts need no audit index deployment.
A recording-disabled history reader still needs those indexes deployed by its host.

Every optional-filter combination is represented separately in the manifest;
index merging is not assumed. Relationship listing contributes eight subject
and eight resource indexes. Both directions are required because previous-page
probes reverse the query order. An index on a removed timestamp is not a
substitute for the new natural-order indexes.

The connector chunks equality disjunctions according to Firestore's disjunctive
normal form limits. The adapter uses tested budgets from `chunks.go`; callers
never observe duplicate IDs or per-chunk page boundaries. Current-model readers
filter retired tuples before they influence permission facts or lookup results.

`ExportIndexes` merges the manifest into the host's file, preserving unrelated
entries. The host deploys it and waits for readiness. Constructors probe the
manifest unless the host explicitly passes `WithoutIndexProbe`. An emulator has
no index registry and cannot verify index coverage; live matrix tests are needed
for that proof. Field overrides preserve the single-field indexes used by scans
and queries, including when a host disables collection-wide automatic indexing.

## Firestore-specific behavior and costs

The store refuses every ambient connector transaction, including read snapshots,
with `ErrAmbientTransactionUnsupported`. Mutation calls additionally match
`ErrGuardedInsideTransaction`. Firestore requires reads before writes and does
not observe its pending writes; it cannot satisfy the shared SQL ambient-join
contract. The transaction conformance family explicitly skips, and a separate
test drives every database method to prove the refusal.

The store does not split a write command. Oversized requests fail atomically at
the server; the request-size bound, rather than an invented write-count budget,
controls capacity. A tuple create/delete now affects two documents; a replacement
affects three (old-row delete, new-row create, subject-claim update). A role
assignment affects one fact document. Enabled audit adds one document per actual
added or removed fact. No-op attempts append no history.

Effective-role counts require grouping all matching scoped/global rows, so
`WithCount` costs O(population) reads. Ordinary effective pages merge ordered
streams only as far as needed. Raw list counts use server aggregation when
available; snapshot-bound counts iterate the same ordered population. A failed
or truncated graph expansion is an error, never a partial allow or deny.
