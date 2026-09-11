# Authorization tuple and mutation design reconsideration

Current mutation contract: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md)
(AUDIT-026). The approved change removes request receipts, scope revisions and the
best-effort audit sink, adds optional atomic change history, and protects guarded
reads against supported raw writers. Earlier protocol descriptions below are
historical evidence, not the current API.

Status: DISCUSSION / PROPOSAL — 2026-09-11. The owner is questioning the original
design after AUDIT-024. This record does not authorize schema or API removal.
No runtime or migration code changed during this review.

## Owner clarification

The owner originally intended scopes/policies as a possible authorization mode
alongside ReBAC/RBAC, and has not developed or adopted that feature. The current
`iam_scopes` table implements something different: resource/subject revision
anchors used by guarded writes. It contains no policy definitions, scope grants,
or third decision engine. Keep those ideas separate in subsequent discussions.

## Verified current implementation

- Relationships have a six-component natural identity: resource type/ID,
  relation, subject type/ID, and optional subject relation. A concrete group
  subject and group#member are distinct. The current SQL schema also has a
  generated relationship_id and created_at; raw listings order by timestamp
  with the ID as tiebreaker. Permission evaluation uses the tuple fields.
- Firestore already addresses relationship documents by a hash of their tuple
  fields. A separate ID-claim collection reproduces SQL surrogate-ID uniqueness;
  the public ID requirement therefore adds work beyond the natural tuple key.
- Roles already have no separate assignment ID. Their natural key is subject
  type/ID, role, and optional resource type/ID. CreatedAt supports raw grant
  listing order and the HTTP projection; effective listings use a derived key.
- iam_scopes holds per-resource or per-subject revision counters. It is used for
  concurrency bookkeeping and optional ExpectedRevision preconditions.
- iam_mutations is the durable receipt ledger for relationship AND role commands:
  grant/revoke/replace/purge/teardown and role assign/unassign. Scope fields identify
  the affected resource/subject; this is not a ledger of scope-policy definitions.
  Mutation ID, payload digest, outcome and original revision support durable
  replay and payload-mismatch detection. Removing revision counters and removing
  this ledger are separate semantic choices, although currently coupled in code.
- The current schema additionally permits only one relation per exact subject
  per resource. That is stricter than uniqueness of the full relationship tuple.
  It drives replacement/conflict machinery and deserves an explicit decision if
  the owner chooses ordinary tuple-set semantics.

Sources: domain/relationship/relationship.go, domain/role/role.go,
domain/mutation/{mutation,repository,receipt}.go, stores/pgx/migrations/0001–0004,
stores/firestore/{keys,tuples}.go, relationship_writer.go.

## Other engines: public model versus implementation metadata

- OpenFGA's public TupleKey contains user/relation/object and an optional
  condition; its Tuple read result adds a timestamp. There is no separate public
  relationship ID. [API schema](https://github.com/openfga/api/blob/main/openfga/v1/openfga.proto),
  [read examples](https://openfga.dev/docs/interacting/relationship-queries).
- SpiceDB's public Relationship contains resource/relation/subject plus optional
  caveat and expiration. It has no surrogate relationship ID or creation
  timestamp field. This says nothing about internal datastore bookkeeping.
  [API schema](https://github.com/authzed/api/blob/main/authzed/api/v1/core.proto).
- OpenFGA models role grants through relationships and permits multiple roles.
  This shows a surrogate role-assignment ID is not essential; it does not require
  Gopernicus to merge its intentionally independent RBAC and ReBAC kinds.
  [Roles guide](https://openfga.dev/docs/modeling/roles-and-permissions).
- Tuple-shaped APIs do not mean no consistency mechanisms. SpiceDB exposes
  datastore snapshot/freshness tokens separately from relationships. These are
  not equivalent to Gopernicus's current per-scope mutation counters.
  [Consistency documentation](https://authzed.com/docs/spicedb/concepts/consistency).

## Recommended direction, pending owner decision

1. Prefer natural tuple identity. Remove the public relationship ID requirement;
   bundled stores can use the natural key, with private backend keys where needed.
2. Remove CreatedAt from relationship and role contracts if the owner has no
   chronological grant-listing requirement. Keep audit/history as an explicit
   feature. A surviving row's timestamp is not a durable record of who changed
   access, why, or what was deleted.
3. Keep the alternative scope/policy decision mode deferred. The current
   iam_scopes implementation should not be retained on its behalf.
4. Consider retiring public revisions/ExpectedRevision and permanent mutation
   receipts if hosts do not need those features. Preserve atomic writes, current
   model validation, correct guard-and-write isolation, and intentionally retained
   guardian behavior. Prove equivalent retained guarantees for every adapter;
   dropping the tables alone is not a correct implementation.
5. Decide the one-relation-per-subject rule independently. Ordinary tuple sets
   can hold viewer and billing_admin for the same subject/resource. Exclusive
   access levels are an additional policy, not a consequence of tuple identity.

## Work needed before implementation

- Inventory real consumer uses of IDs, creation order, revisions, durable replay,
  role routes, guards and last-owner protection. Current review establishes local
  framework uses; it does not claim those fields have no external consumers.
- Select replacement list order/cursor contracts using canonical tuple fields,
  preserving exact userset identity. Timestamps and surrogate IDs are not required
  for deterministic keyset pagination, but current cursor/wire contracts change.
- Separate state idempotence from operation replay: repeating an add with no
  intervening writes is a no-op; add -> remove -> retry old add re-adds the tuple
  without an operation ledger. This distinction must be accepted explicitly.
- Design the simple trusted and guarded write surfaces, transaction ownership,
  and all-writer concurrency guarantees before removing revisions. Preserve the
  negative membership and global-role race regressions from AUDIT-024.
- Specify SQL/Firestore upgrade and rollback handling, indexes/claim collection
  changes, API/HTTP/example migrations and meaningful conformance checks.
- Record implemented breaking changes in a new AUDIT entry only after approval
  and implementation. AUDIT-023/024 remain the current implemented contracts.
