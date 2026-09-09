# SCHEMA — the authorization pocket's Firestore document layout

This file is to this module what `migrations/0001`–`0005` are to
`pockets/authorization/stores/turso`: the tracked, reviewable statement of what
is stored, under which document id, and which SQL constraint each document
shape reproduces. Firestore has no DDL and no unique constraints, so the schema
IS this document plus `firestore.indexes.json` (the access paths) — nothing in
the datastore records it.

Audited at task A1 of the [firestore-stores milestone](../../../../.claude/plans/firestore-stores/authorization.md)
against `pockets/authorization` **v0.12.0** and the turso store's migrations
`0001`–`0005`. Rulings R1–R5 of the milestone README govern.

## 1. Baseline

| Thing | Value |
|---|---|
| Pocket core | `pockets/authorization v0.12.0` (tag is an ancestor of this branch) |
| Turso store pin | `pockets/authorization v0.12.0` (`stores/turso/go.mod`) |
| Connector | `integrations/datastores/firestore v0.1.0` (untagged during this train; workspace-resolved) |
| sdk | `v0.7.0` (the pocket core's pin; the connector pins `v0.4.0`, so the store takes the higher) |

## 2. Port inventory — 32 methods across four interfaces

Every method a store adapter must fill, from `pockets/authorization/domain/` at
v0.12.0. The Firestore column names the read/write primitive each one lands on.

### `relationship.Storer` — 18 methods

| # | Method | Firestore shape |
|---|---|---|
| 1 | `CheckRelationWithGroupExpansion` | snapshot-bound expansion (A-D2) + chunked `resource_key ==`, `relation ==`, `subject_key in` |
| 2 | `GetRelationTargets` | query `resource_key ==`, `relation ==` |
| 3 | `FilterRelation` | one expansion + candidate chunks by `resource_type`, `relation`, `resource_id in` |
| 4 | `RelationTargetsFor` | candidate chunks by `resource_type`, `relation`, `resource_id in` |
| 5 | `CheckRelationExists` | `Get` on the tuple doc id (`subject_relation == ""`) |
| 6 | `CheckBatchDirect` | one expansion + `resource_type ==`, `relation ==`, `resource_id in` chunks |
| 7 | `CreateRelationships` | one `Transact`; tuple + subject claim + id claim per row (§5) |
| 8 | `SetRelationTargets` | one `Transact`; read-reconcile-write (A-D3) |
| 9 | `DeleteRelationshipTarget` | `Transact`; delete tuple + owned claims |
| 10 | `DeleteResourceRelationships` | `Transact`; query `resource_key ==`, delete rows + claims |
| 11 | `DeleteRelationship` | `Transact`; concrete subject (`subject_relation == ""`) |
| 12 | `DeleteByResourceAndSubject` | `Transact`; query `resource_key ==`, `subject_type ==`, `subject_id ==` |
| 13 | `CountByResourceAndRelation` | `Count` aggregation on `resource_key ==`, `relation ==` (DIRECT tuples only) |
| 14 | `ListRelationshipsBySubject` | `List[T]`, PK `relationship_id`, order `created_at` |
| 15 | `ListRelationshipsByResource` | `List[T]`, PK `relationship_id`, order `created_at` |
| 16 | `LookupResourceIDs` | expansion + keyset merge on `resource_id` |
| 17 | `LookupResourceIDsByRelationTarget` | keyset merge on `resource_id`, concrete targets only |
| 18 | `LookupDescendantResourceIDs` | BFS over `subject_key in` frontier chunks, then sort/after/limit |

### `role.Storer` — 7 methods

| # | Method | Firestore shape |
|---|---|---|
| 1 | `Assign` | `Create` on the 5-tuple doc id; `AlreadyExists` is an idempotent no-op preserving `created_at` |
| 2 | `Unassign` | `Delete` on the 5-tuple doc id; absent is nil |
| 3 | `HasExactRole` | `Get` on the 5-tuple doc id |
| 4 | `ListBySubject` | `List[T]`, PK `role_key`, order `created_at` |
| 5 | `ListByResource` | `List[T]`, PK `role_key`, order `created_at` |
| 6 | `ListEffectiveByResource` | dedicated two-stream merge/group reader on `grant_key` (A-D4), NOT a PostFilter |
| 7 | `LookupResourceIDsBySubjectAndRoles` | global-role `GetAll` short-circuit, then keyset merge on `resource_id` |

### `mutation.MutationRepository` — 2 methods

`Apply`, `ApplyGuarded` — each ONE `Transact` with the strictly ordered phases of A-D5.

### `mutation.StoreDecisionView` — 5 methods

`CheckRelation`, `CheckRelationBounded`, `RelationTargets`, `HasRole`, `Dependencies`.
Implemented over the mutation transaction's `Reader`, recording a scope's revision
BEFORE that scope's rows (the ordering rule is part of the port contract).
`mutation.DecisionView.CheckPermission` is composed by the pocket core over these
primitives and is not a store method.

**Total: 32 store-implemented methods** (18 + 7 + 2 + 5).

## 3. SQL schema of record → document layout

Every table, column, primary key, unique index, secondary index, and CHECK
constraint from `stores/turso/migrations/0001`–`0005`, and where it goes.

### 3.1 `iam_relationships` (migration 0001)

| SQL column | Firestore field | Notes |
|---|---|---|
| `relationship_id TEXT PK DEFAULT lower(hex(randomblob(16)))` | `relationship_id` (string) | store-minted with `firestoredb.NewID()` for an all-empty batch; verbatim for an all-populated one; a MIXED batch is a loud `sdk.ErrInvalidInput` (turso parity) |
| `resource_type TEXT NOT NULL` | `resource_type` | |
| `resource_id TEXT NOT NULL` | `resource_id` | also the keyset sort field of the three lookups |
| `relation TEXT NOT NULL` | `relation` | |
| `subject_type TEXT NOT NULL` | `subject_type` | |
| `subject_id TEXT NOT NULL` | `subject_id` | |
| `subject_relation TEXT NOT NULL DEFAULT ''` | `subject_relation` | `""` = concrete subject; always WRITTEN (never absent), so equality filters see it |
| `created_at TEXT NOT NULL` | `created_at` (timestamp) | `firestoredb.TruncateTime`; one stamp for a whole batch |
| — | `resource_key` | `KeyHash(resource_type, resource_id)` |
| — | `subject_key` | `KeyHash(subject_type, subject_id, subject_relation)` |

Derived-only fields (`resource_key`, `subject_key`) exist to collapse two/three
equality filters into one indexed clause and to keep the DNF disjunction budget
usable at expansion hops. They are identities, never projections and never sort
keys.

| SQL constraint | Firestore enforcement |
|---|---|
| PK `relationship_id` | **id claim** `iam_relationship_ids` (§5.3) — the deterministic tuple id cannot carry it |
| UNIQUE `(resource_type, resource_id, relation, subject_type, subject_id, subject_relation)` | the DOCUMENT ID: `KeyHash` of those six parts, in that order (R3a) |
| UNIQUE `(resource_type, resource_id, subject_type, subject_id, subject_relation)` | **subject claim** `iam_relationship_subjects` (§5.2) |
| `idx_..._resource (resource_type, resource_id)` | `resource_key ==` |
| `idx_..._subject (subject_type, subject_id)` | `subject_type ==` + `subject_id ==` (no `subject_relation`, so NOT `subject_key`) |
| `idx_..._type_relation (resource_type, relation)` | composite in `firestore.indexes.json` |
| `idx_..._type_relation_resource (resource_type, relation, resource_id)` (0005) | composite in `firestore.indexes.json` |
| CHECK `ck_iam_relationships_nonempty` | the domain's `ValidateRefField` (defense in depth is a SQL affordance; Firestore has no CHECK). The store rejects an empty structural component with `sdk.ErrInvalidInput` before writing rather than dropping the constraint silently. |

### 3.2 `iam_roles` (migration 0002)

| SQL column | Firestore field | Notes |
|---|---|---|
| `subject_type` | `subject_type` | |
| `subject_id` | `subject_id` | |
| `role` | `role` | |
| `resource_type NOT NULL DEFAULT ''` | `resource_type` | `""` with `resource_id == ""` ⇒ GLOBAL — empty string, never null/absent |
| `resource_id NOT NULL DEFAULT ''` | `resource_id` | keyset sort field of the roles lookup |
| `created_at` | `created_at` (timestamp) | store-stamped; a duplicate `Assign` RETAINS the original |
| — | `subject_key` | `KeyHash(subject_type, subject_id)` — TWO parts (roles have no subject relation) |
| — | `resource_key` | `KeyHash(resource_type, resource_id)` — global scope hashes the empty pair, so `KeyHash("", "")` is a real, matchable value |
| — | `role_key` | the SORT key; raw, see §4 |
| — | `grant_key` | the effective-listing sort key; raw, see §4 |

| SQL constraint | Firestore enforcement |
|---|---|
| UNIQUE `(subject_type, subject_id, role, resource_type, resource_id)` | the DOCUMENT ID: `KeyHash` of those five parts in that order (R3a) |
| `idx_iam_roles_subject (subject_type, subject_id)` | `subject_key ==` |
| `idx_iam_roles_resource (resource_type, resource_id, created_at)` | `resource_key ==` + `created_at` order (composite) |
| `idx_iam_roles_subject_resource_lookup (subject_type, subject_id, resource_type, resource_id, role)` (0005) | composite `(subject_key, resource_type, resource_id, role)` — `resource_id` carries the keyset range |
| CHECK `ck_iam_roles_nonempty`, `ck_iam_roles_scope_pair` | store-side validation before write (no CHECK analogue) |

### 3.3 `iam_scopes` (migration 0003)

| SQL | Firestore |
|---|---|
| PK `(scope_kind, scope_type, scope_id)` | document id `KeyHash(scope_kind, scope_type, scope_id)` |
| `revision INTEGER NOT NULL DEFAULT 0` | `revision` (int64). An ABSENT document reads as revision 0 — the same contract as the absent SQL row. A bare revision-0 anchor is never written just to exist. |
| CHECK kind/nonempty/revision≥0 | store-side validation |

No index needed: every access is by document id.

### 3.4 `iam_mutations` (migration 0004)

| SQL column | Firestore field |
|---|---|
| `mutation_id TEXT PK` | `mutation_id` (field) + document id `KeyHash(mutation_id)` |
| `scope_kind`, `scope_type`, `scope_id` | same names |
| `operation` | `operation` |
| `payload_encoding` | `payload_encoding` |
| `payload_digest` | `payload_digest` |
| `outcome` | `outcome` |
| `revision INTEGER` | `revision` (int64) |
| `schema_digest` | `schema_digest` |
| `created_at TEXT` | `created_at` (timestamp, truncated) |
| `expires_at TEXT NULL` | `expires_at` written through `firestoredb.NullTime` (an explicit null, never an absent field) and never ordered by |

`Receipt.Replayed` and `Receipt.SameRoleGrantRemains` are computed, non-persisted
annotations in SQL and stay non-persisted here. The CHECK on `outcome`
(`applied`/`no_change`/`not_found`) is the store's write-side rule: a receipt is
created ONLY when `outcome.Persisted()`.

No index needed: every access is by document id (`KeyHash(mutation_id)`).

## 4. Derived keys — construction pinned byte-for-byte

`h(...)` is `firestoredb.KeyHash`: lowercase hex SHA-256 over, for each part in
order, eight bytes of the part's byte LENGTH big-endian followed by the part's
raw bytes. Length-prefixing makes the tuple unambiguous, so `h("ab","c")`,
`h("a","bc")`, `h("a","b","")` and `h("a","b")` are four different ids. Part
ORDER is semantic and is never rearranged.

| Key | Construction | Purpose |
|---|---|---|
| `relationshipDocID` | `h(resource_type, resource_id, relation, subject_type, subject_id, subject_relation)` | unique tuple |
| `subjectClaimDocID` | `h(resource_type, resource_id, subject_type, subject_id, subject_relation)` | one relation per subject per resource |
| `idClaimDocID` | `h(relationship_id)` | `relationship_id` PK uniqueness (§5.3) |
| `resourceKey` | `h(resource_type, resource_id)` | equality filter (both collections) |
| `subjectKey` (relationships) | `h(subject_type, subject_id, subject_relation)` | expansion frontier + equality filter |
| `subjectKey` (roles) | `h(subject_type, subject_id)` | roles carry no subject relation |
| `roleDocID` | `h(subject_type, subject_id, role, resource_type, resource_id)` | unique 5-tuple |
| `scopeDocID` | `h(scope_kind, scope_type, scope_id)` | the anchor |
| `mutationDocID` | `h(mutation_id)` | the receipt |

The two `subjectKey` shapes deliberately differ in ARITY (3 vs 2). Because
`KeyHash` is length-prefixed, `h("user","u1","")` ≠ `h("user","u1")`, so the two
can never alias even though both fields are named `subject_key` — but they live
in different collections and are never compared across them.

### 4.1 The two SORT keys — raw, not hashed (turso parity)

A hash is never a sort key. `role_key` and `grant_key` are the SQL adapters'
derived ordering/keyset columns and are reproduced BYTE-FOR-BYTE:

| Key | turso expression (`stores/turso/roles.go`) | Firestore value |
|---|---|---|
| `role_key` | `subject_type \|\| char(1) \|\| subject_id \|\| char(1) \|\| role \|\| char(1) \|\| resource_type \|\| char(1) \|\| resource_id` | the same five components joined by `"\x01"` |
| `grant_key` | `subject_type \|\| char(1) \|\| subject_id \|\| char(1) \|\| role` | the same three components joined by `"\x01"` |

`char(1)` (SQLite) and `chr(1)` (postgres, `stores/pgx/roles.go`) both emit the
single byte `U+0001`, so the three families produce identical bytes; `keys_test.go`
asserts equality against a transcription of each SQL expression plus golden
literals. Ordering parity follows: SQLite's default BINARY collation, postgres's
`COLLATE "C"`, and Firestore's UTF-8 byte order are the same order, and
`crud.OrderField.CastLower` is used by no store (the connector refuses it).

The keys are also the cursor PK the two SQL stores echo back from the database
(`PKOf` returns the scanned column, never a Go recomputation). Firestore has no
computed column, so the store computes the value on WRITE, stores it as a field,
and `PKOf` echoes the STORED value — the same invariant reached from the other
side.

The remaining contractual orders need no derived key:

| List | Order field | PK / tiebreak |
|---|---|---|
| `ListRelationshipsBySubject` / `ByResource` | `created_at` (default DESC) | `relationship_id` |
| `ListBySubject` / `ListByResource` (roles) | `created_at` (default DESC) | `role_key` |
| `ListEffectiveByResource` | `grant_key` (default ASC) | `grant_key` |
| the four keyset lookups | `resource_id` ASC, raw bytes, `after` exclusive | document name |

### 4.2 Indexed-value length audit (C0 notes N1–N3)

`relationship.MaxRefFieldLen = 256` bytes per component and `ValidateRefField`
accepts `/`, so the six-component tuple reaches 1536 bytes — past Firestore's
1500-byte document-id limit — which is why every natural id is a `KeyHash`
(N1). The sortable fields are audited separately, since indexed values over 1500
bytes TRUNCATE:

| Sortable / range field | Max size | Verdict |
|---|---|---|
| `created_at` | timestamp | safe |
| `relationship_id` | engine-minted id (cryptids) or `firestoredb.NewID()` (20 chars) | safe |
| `resource_id` | ≤256 bytes via `ValidateRefField` on every relationship write path | safe |
| `grant_key` | 3 role components + 2 separators | **unbounded at the port** (see below) |
| `role_key` | 5 role components + 4 separators | **unbounded at the port** (see below) |

FINDING (A1): the ROLES kind does not bound its component lengths. `rolesvc`'s
`validateAssignment` rejects only empty values (`internal/logic/rolesvc/service.go`),
so a host may store a role name or subject id of any length; only the MUTATION
path bounds them (`mutation.RoleRow.Validate` → `ValidateRefField`, 256 bytes).
With 256-byte components `role_key` is at most 1284 bytes and `grant_key` at most
770 — both inside the limit — but a host assigning a >700-byte role name through
the raw port could produce a `role_key` over 1500 bytes, whose index entry
truncates and whose keyset page could then repeat or skip a row.

DECISION: keep the keys RAW (byte-parity with the SQL families is contractual and
a hash would destroy the order). The ceiling is documented in the module README
(A6) as a family difference: keep role/subject components within
`relationship.MaxRefFieldLen`, which every mutation-path write already enforces.
The store does not silently truncate and does not reject at write time (that
would be a stricter port than the SQL stores'). Revisit only if a host reports it.

## 5. Uniqueness — which claim enforces what (R3)

### 5.1 The tuple id itself

`iam_relationships` and `iam_roles` documents are keyed by the `KeyHash` of their
SQL UNIQUE tuple, so a duplicate `Create` fails `AlreadyExists` at the server —
the direct analogue of the unique index. `Create` (never `Set`) is therefore the
only write verb for a new row.

### 5.2 `iam_relationship_subjects` — the one-relation-per-subject claim

| | |
|---|---|
| Document id | `h(resource_type, resource_id, subject_type, subject_id, subject_relation)` |
| Fields | `relation`, `tuple_id` (the owning relationship document id), `relationship_id` |
| Enforces | `idx_iam_relationships_unique_subject` |

Read in the SAME transaction as the tuple, before any write. An existing claim
under ANY relation makes `CreateRelationships` a silent first-write-wins NO-OP
(nil error, existing row unchanged — the bare `ON CONFLICT DO NOTHING`
semantics), while the v3 guarded mutation path reports it as
`OutcomeSemanticConflict`. Written and deleted only by the two private helpers
that own a tuple's whole document set (`putTuple`/`dropTuple`, A2c).

### 5.3 `iam_relationship_ids` — the relationship_id claim (A1 DECISION)

The A-D1 table left this open: "preserve relationship_id uniqueness as well as
tuple uniqueness … if deterministic tuple IDs cannot enforce it, add an atomic ID
claim with SQL-equivalent behavior."

They cannot. The document id is the hash of the SIX-part tuple; `relationship_id`
is an independent surrogate that both SQL dialects pin as the PRIMARY KEY, and
two different tuples carrying the same `relationship_id` would sit in two
different documents. That is not cosmetic: `relationship_id` is the LIST cursor's
PK (`created_at`, `relationship_id`), so a duplicate makes a keyset page able to
repeat or skip a row, and the DB-generated-id conformance case reads it as an
identity.

A query (`Where("relationship_id","==",…)`) inside the transaction is NOT
equivalent: Firestore's read set is built from the documents a transaction READ,
so two concurrent creates could each observe "no match" and both commit — the
phantom R3 exists to prevent. A claim DOCUMENT read by id is in the read set and
conflicts.

| | |
|---|---|
| Document id | `h(relationship_id)` |
| Fields | `relationship_id`, `tuple_id` |
| Enforces | the `relationship_id` PRIMARY KEY |

SQL-equivalent behavior: both SQL adapters insert with a BARE
`ON CONFLICT DO NOTHING` (no conflict target), which covers the PK as well as the
two unique indexes — so a create whose `relationship_id` is already taken is a
SILENT NO-OP there. The Firestore store reproduces exactly that: an id claim held
by a different tuple skips the row, nil error, nothing written.

The claim is written for EVERY row, store-minted ids included, so the invariant
is uniform rather than "enforced only when the host supplies ids", and it is
dropped with its tuple by the same `dropTuple` helper (the drift risk the plan's
Risks section names).

Cost: one extra document read and write per created tuple, inside a transaction
that already reads two. Both claims and the tuple are read in the transaction's
read phase; no claim is ever first discovered during the write phase.

### 5.4 Roles, scopes, receipts

| Constraint | Mechanism |
|---|---|
| `idx_iam_roles_unique` (5-tuple) | deterministic document id; `AlreadyExists` on duplicate `Assign` is an idempotent no-op that preserves `created_at` |
| `iam_scopes` PK | deterministic document id; absent = revision 0 |
| `iam_mutations` PK `mutation_id` | deterministic document id; the receipt is `Create`d, never `Set`, so a concurrent double-apply loses at the server rather than overwriting a receipt |

## 6. Collections

| Collection | Purpose | SQL analogue |
|---|---|---|
| `iam_relationships` | tuples | table |
| `iam_relationship_subjects` | one-relation-per-subject claim | unique index |
| `iam_relationship_ids` | `relationship_id` uniqueness claim | primary key |
| `iam_roles` | role assignments | table |
| `iam_scopes` | revision anchors | table |
| `iam_mutations` | receipts | table |

All are TOP-LEVEL collections (no subcollections), so every index in the manifest
is `COLLECTION` scope and no collection-group index is required. Names mirror the
SQL table names so the two documentation trees and operator vocabulary stay
shared (milestone convention).

## 7. Index manifest

`firestore.indexes.json` ships the A-D7 expected composites. It is
**provisional**: A5 derives the definitive set from the complete query matrix
(every optional-filter subset, both directions, the reverse HasPrev probe, and
the chunk shapes) and proves it against a live database. The emulator enforces
no composite index, so nothing here is proven by an emulator-green run.

## 8. Known family differences (R1)

The store returns no `crud.Transactor` and refuses a context carrying a
connector transaction with `ErrAmbientTransactionUnsupported` (the mutation
methods also wrap `mutation.ErrGuardedInsideTransaction`, the sentinel the pocket
already defines for that refusal). `storetest.RunTransactional` skips loudly.
