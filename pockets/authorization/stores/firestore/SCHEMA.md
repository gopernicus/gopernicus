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

## 7. The query matrix

Every query the three ports issue, in the vocabulary Firestore's index rules are
written in. It is the manifest's SPECIFICATION (ruling R5): §9's
`firestore.indexes.json` is DERIVED from this matrix, not from the queries a test
happened to run.

The matrix is **executable**. `queryMatrix()` in `indexes_test.go` carries the
same rows, `requiredIndex` applies the derivation rules of §9.1 to each, and
`TestIndexManifestMatchesTheQueryMatrix` asserts the correspondence in BOTH
directions — a query with no index fails the test, and an index no query needs
fails it too. The tables below and that table are kept in step by review; the
test is what a build enforces.

Conventions: `~` marks a range/inequality filter, `in*` an `in` filter that is
chunked, and **Composite** names the §9 entry the shape requires (`—` means
Firestore serves it from automatic single-field indexes). Reads addressed by
DOCUMENT ID use no index at all and are listed for completeness, never as index
requirements.

### 7.1 Query shapes the relationship port issues (A2a–A2d)

| Method | Equality | Range / order | Chunked by | Composite |
|---|---|---|---|---|
| expansion hop (`expandScoped`) | `subject_key in*` | — | 30 | — (one `in`, one field) |
| expanded check (`anyTupleWithSubject`) | `resource_key`, `relation`, `subject_key in*` | `Limit(1)` | 30 | `(resource_key, relation, subject_key)` |
| relation targets (`relationTargets`) | `resource_key`, `relation` | — | — | — (equality only) |
| direct count (`CountByResourceAndRelation`) | `resource_key`, `relation` | count aggregation | — | — (the query underneath needs none) |
| reconciliation read (`setRelationTargets`) | `resource_key`, `relation` | — | — | — |
| delete by relation (`DeleteRelationship`) | `resource_key`, `relation` | — | — | — |
| delete by resource / resource rows (`dropMatching`, `resourceRows`) | `resource_key` | — | — | — (one field) |
| candidate scan (`scanCandidates`: batch check, `FilterRelation`, `RelationTargetsFor`) | `resource_type`, `relation`, `resource_id in*` | — | 30 | `(resource_type, relation, resource_id)` |
| descendant hop (`descendantClosure`) | `resource_type`, `relation in*`, `subject_key in*` | — | 30 (product) | `(resource_type, relation, subject_key)` |
| lookup by relations (`LookupResourceIDs`) | `resource_type`, `relation in*`, `subject_key in*` | `resource_id ~`, order `resource_id`, `__name__` | 24 (product) | `(resource_type, relation, subject_key, resource_id)` |
| lookup by relation target (`LookupResourceIDsByRelationTarget`) | `resource_type`, `relation`, `subject_key in*` | same | 24 | the same entry |
| list by subject (`ListRelationshipsBySubject`) | `subject_type`, `subject_id` (+ optional `resource_type`, `relation`) | order `created_at`, `relationship_id`, BOTH directions | — | four subsets × two directions = 8 entries |
| list by resource (`ListRelationshipsByResource`) | `resource_key` (+ optional `subject_type`, `relation`) | same | — | four subsets × two directions = 8 entries |
| tuple / claim / id-claim reads and writes | document id | — | — | — (no query) |

The two `in`-chunk sizes come from vendor caps, not taste: at most **30
disjunctions** after DNF expansion (an `in` of N values contributes N, and two
`in` filters contribute their PRODUCT), AND at most **100 filters plus sort
orders** counted across the expanded disjunctions. A lookup stream carries four
filters and two orders per disjunct, so its budget is `(100 - 2) / 4 = 24`, not
30. `maxChunk` in `reads.go` resolves both caps; `chunking_test.go` pins the
arithmetic hermetically. Chunk SIZE never changes which index a shape needs: a
one-value `in` is written as `==` (`whereAnyOf`) and both use the same index.

**Why every optional-filter subset is its own index.** A composite index serves a
query only when its equality prefix is the query's WHOLE equality set, so
`(subject_type, subject_id, created_at, relationship_id)` does not serve the same
listing with a `relation` filter added. Firestore MAY merge single-field indexes
for equality-only queries and MAY merge composites that share a sort suffix
("Use index merging"), but merging is a documented optimization, not a
guarantee — and an under-declared manifest surfaces as a production
`FAILED_PRECONDITION`, which is the failure this manifest exists to prevent. The
live matrix leg (§9.4) is what may later justify pruning entries; nothing else
may.

### 7.2 Query shapes the roles port issues (A3a–A3b)

Three of the seven `role.Storer` methods issue NO query at all: the unique
5-tuple is the document id, so an exact read, an assign, and an unassign address
ONE document, and the unrestricted short-circuit is a `GetAll` over the computed
global-role ids.

| Method | Equality | Range / order | Chunked by | Composite |
|---|---|---|---|---|
| `Assign` / `Unassign` / `HasExactRole` | document id | — | — | — (no query) |
| unrestricted probe (`anyGlobalRole`) | `GetAll` on computed ids | — | — | — (no query) |
| teardown role sweep (`scopedRolesQuery`) | `resource_key` | — | — | — (one field) |
| `ListBySubject` | `subject_key` | order `created_at`, `role_key`, BOTH directions | — | `(subject_key, created_at, role_key)` × 2 |
| `ListByResource` | `resource_key` | same | — | `(resource_key, created_at, role_key)` × 2 |
| `LookupResourceIDsBySubjectAndRoles` | `subject_key`, `resource_type`, `role in*` | `resource_id ~`, order `resource_id`, `__name__` | 24 | `(subject_key, resource_type, role, resource_id)` |
| `ListEffectiveByResource` (per stream) | `resource_key` | `grant_key ~`, order `grant_key`, BOTH directions | — | `(resource_key, grant_key)` × 2 |

The roles lookup carries the same four filters and two sort orders per disjunct
as the relationship lookups, so it shares `lookupChunkBudget` — 24, not 30. The
effective listing issues TWO of its stream shape per call for a scoped request
(the requested `resource_key` and the global one) and exactly ONE for a global
request; the shape is identical, so one index per direction serves both.

### 7.3 Query shapes the mutation path issues (A4a–A4c)

The guarded mutation path is dominated by DOCUMENT reads rather than queries, on
purpose: a Firestore transaction locks everything it reads until it commits, so
every fact that can be addressed by id is read by id, and the anchors and the
receipt are read in ONE `GetAll`.

| Phase | Shape | Composite |
|---|---|---|
| guard — `CheckRelation(Bounded)` | the expansion hop and the expanded check of §7.1 | those entries |
| guard — `RelationTargets` | `resource_key`, `relation` | — |
| guard — `HasRole` | document `Get` (exact scope, then the global fallback) | — |
| guard — dependency anchors | document `Get` per newly recorded scope | — |
| anchors + receipt | ONE `GetAll` over the lock set plus `iam_mutations/h(id)` | — |
| grant / revoke / replace / purge | `resource_key` (`resourceRows`) | — |
| teardown role sweep | `resource_key` on `iam_roles` | — |
| role assign / unassign | `GetAll` on the exact 5-tuple ids | — |
| create pre-check | `GetAll` on each new tuple's three documents | — |

The mutation path therefore adds NO index of its own: every query it issues is
either a document address or a shape §7.1/§7.2 already declares. That is a
property worth keeping — a new guarded query that needs a new index must add a
matrix row, or `TestIndexManifestMatchesTheQueryMatrix` fails.

## 8. Known family differences (R1)

The store returns no `crud.Transactor` and refuses a context carrying a
connector transaction with `ErrAmbientTransactionUnsupported` (the mutation
methods also wrap `mutation.ErrGuardedInsideTransaction`, the sentinel the pocket
already defines for that refusal). `storetest.RunTransactional` skips loudly.

### 8.1 The request-size ceiling is the SERVER's (A2c; corrected at A7)

**There is no per-transaction write COUNT limit.** The Firestore quotas page
bounds a commit by the **10 MiB maximum API request size** and by **500 field
transformations per document** — neither of which counts writes — so this store
enforces no ceiling of its own on `CreateRelationships`, `SetRelationTargets`, or
the delete family. A tuple owns three documents (row + two claims), and a batch
of any size commits as one transaction.

An oversized request fails at the SERVER, atomically: the commit is rejected,
nothing is written, and the error arrives through `firestoredb.MapError` like any
other. There is no half-applied state to repair and no remediation step beyond
"send less in one call".

The A2c implementation shipped a client-side refusal of more than 166 tuples,
derived from a "500 writes per transaction" limit the quotas page does not
state. The A7 data review checked the page and found only the two bounds above,
so the constants, `ErrTupleWriteLimit`, and every pre-write count check were
REMOVED — they rejected batches Firestore accepts. `TestLargeBatchesCommitInOneTransaction`
commits 600 documents in one transaction on the emulator and
`TestLargeBatchCommitsInOneTransactionLive` does the same against a real
database.

### 8.2 The effective listing's count is O(population) (A3b)

`ListEffectiveByResource` de-duplicates by `(subject_type, subject_id, role)`
ACROSS the requested scope and the global scope, so the unit a page, a cursor, an
offset, and a count all count is a GROUP. Firestore's count aggregation counts
DOCUMENTS and cannot group, so a `WithCount` request reads and groups the whole
population of both scopes — O(population) document reads, under the page's own
snapshot so the two always agree. The SQL families answer the same count with one
`GROUP BY` the database evaluates. The RAW listings (`ListBySubject`,
`ListByResource`) count documents and stay on the connector's server-side
aggregation, so this cost is confined to the effective listing.

A group that spans BOTH scopes is read as two documents and returned as one row;
a page limit therefore bounds the ROWS returned, not the documents read.

### 8.3 What one command writes (A4b; ceiling removed at A7)

§8.1's correction applies here too: a `Command` is bounded by the server's
10 MiB request size and by nothing this store checks. The document COUNTS are
still worth stating, because they are what a large command's request size is made
of:

| Staged change | Documents |
|---|---|
| a created relationship row | 3 (row + subject claim + id claim) |
| a removed relationship row | 3 |
| a REPLACED relationship row | 4 (the old row's delete, the new row's create, and one `Set` on each claim — neither claim id carries the relation, so neither moves) |
| a role grant assigned or unassigned | 1 |
| the scope anchor, when the outcome changes rows | 1 |
| the receipt, when the outcome is persisted | 1 |

A command whose staged writes exceed the request size is refused by the server
with nothing written and no receipt minted, so a caller that hits it splits the
work and re-applies with a fresh MutationID. The mutation path never splits a
command itself: a split is a partially applied command, and atomicity is exactly
what `mutation.MutationRepository` promises. `ErrMutationWriteLimit` and its
pre-write count check were removed at A7 for the reason §8.1 gives.

### 8.4 Contention surfaces as waiting, not as an error (A4b; widened at A7)

Every command on one scope reads that scope's anchor and its resource's rows, so
N concurrent commands on ONE resource genuinely serialize. The vendor re-runs the
transaction callback up to `Config.MaxAttempts` times (5) with its own backoff;
past that the store re-runs the WHOLE operation a bounded number of times with a
JITTERED backoff (`contention.go`).

**The retry is store-wide, not mutation-only.** Every raw write path —
`CreateRelationships`, `SetRelationTargets`, the four deletes, and
`Assign` — runs its transaction under the same loop (`retryTransact`). Each of
those callbacks re-READS before it decides, so re-running one is safe; `Apply` is
additionally idempotent by MutationID, so its re-run either replays a
now-committed receipt or re-evaluates against current state.

**Retryability is decided by the error's IDENTITY, never by where it surfaced.**
The A4b implementation retried only a failure the CALLBACK did not return, which
made a mapped `Aborted` raised by a transactional READ terminal while the
identical condition reported by the COMMIT was retried — one condition, two
answers. The rule now (`retryableConflict`): retry an `sdk.ErrConflict` unless it
is one of the stable domain refusals — `mutation.ErrStaleRevision`,
`mutation.ErrPayloadMismatch`, `mutation.ErrInvalidCommand`, the reconciliation's
own relation conflict — or the guard returned it (a denial is an answer, however
it is spelled). Anything that is not `sdk.ErrConflict` is not contention.

The connector half of the same fix: `firestoredb.MapError` PRESERVES the gRPC
status in the wrap chain, so a mapped `Aborted` still drives the VENDOR's own
retry gate. Without it, only a losing commit was ever re-run.

**The two loops do not nest.** `detachVendorRetry` re-roots a retryable conflict
this store's callback can SEE on `sdk.ErrConflict` alone, dropping the status, so
the vendor's loop ends and THIS loop re-runs the operation. That is not a
walk-back of the connector fix — the condition is still retried, and a host's own
`Transact` callback still gets the vendor's retry, which is what the connector
change is for. It is about WHOSE backoff runs: the vendor's is not jittered, and
§8.4's measurement is precisely that an un-jittered backoff starves a twenty-way
storm (186.7 s and a failure) where a jittered one drains it (6.7 s). A conflict
the callback cannot see — a losing COMMIT — is still the vendor's to retry, then
falls through to this loop.

The jitter is load-bearing rather than decorative: without it every contender
waits the same interval and collides again in the same order, and the shared
suite's twenty-way `ConcurrentReceiptRevisionForensics` storm starved one writer
past every retry budget on the emulator (measured: 186 s and a failure, versus
6.7 s and a pass with jitter). When the budget IS exhausted the caller gets
`sdk.ErrConflict` — an infrastructure conflict it may retry — never a committed
outcome and never a minted receipt.

## 9. Index manifest (A5; field overrides added at A7)

`firestore.indexes.json` declares **28 composite indexes** — 21 on
`iam_relationships`, 7 on `iam_roles` — all at `COLLECTION` scope (§6: every
collection is top-level), plus **17 field overrides** (§9.5). It is no longer
provisional: every entry is derived from §7 by the rules below, and
`indexes_test.go` fails if the two disagree in either direction. A database
allows 200 composite indexes without billing enabled and 1,000 with it, shared
across every store a host mounts; this store's 28 are its share of that budget,
and `TestIndexManifestParses` states the number so a change has to move it
deliberately.

### 9.1 The derivation rules, with sources

From [index-overview](https://firebase.google.com/docs/firestore/query-data/index-overview)
and [queries](https://firebase.google.com/docs/firestore/query-data/queries):

1. **Equality-only needs nothing.** "You can combine constraints with a logical
   AND by chaining multiple equality operators (`==` or `array-contains`).
   However, you must create a composite index to combine equality operators with
   the inequality operators, `<`, `<=`, `>`, and `!=`." A single `in` likewise:
   "You can also create `in` and compound equality (`==`) queries" appears under
   *Queries supported by single-field indexes*.
2. **`in` is an equality for index selection** — "Since the query uses an
   equality (`==` or `in`) for the `country` field…", "`in` and `==` clauses use
   the same index". Rule 1 is therefore narrowed to a SINGLE filter field: this
   store declares a composite for **any query with two or more filter fields**,
   `in` or not. The server may serve multi-equality queries by MERGING automatic
   single-field indexes, but merging is an optimization the docs recommend, not a
   guarantee, and the cost asymmetry is the whole argument (a surplus index costs
   storage; a missing one is a production `FAILED_PRECONDITION`).

   The rule was previously conditional on an `in` being present, which is what
   dropped `(resource_key, relation)` — an equality PAIR — from the A5 manifest
   while keeping composites for otherwise identical shapes that happened to carry
   an `in`. Two spellings of one rule. A7 made it uniform and restored that
   entry; it is the single index the count moved by (27 → 28).
3. **Filter + sort on another field, or any two-field sort, needs one.** "If you
   need to run a compound query that uses a range comparison … or if you need to
   sort by a different field, you must create a manual index for that query."
4. **Field order.** "The start position is prefixed with the query's equality
   filters and ends with the range and inequality filters on the first `orderBy`
   field" — equality-class fields first, then the range/order fields in the
   query's direction. Firestore accepts any order WITHIN the equality prefix, so
   the manifest pins one (`equalityPrecedence` in `indexes_test.go`, which is the
   order the store's query builders apply their filters in) and every shape obeys
   it, or two spellings of one index would both have to be deployed.
5. **`__name__` is implicit.** "By default, the `__name__` field is sorted in the
   same direction of the last sorted field in the index definition … To sort
   results by the non-default `__name__` direction, you need to create that
   index." Every ordered shape here sorts `__name__` in the last order field's
   direction (the keyset streams state it explicitly, `.OrderBy(DocumentID, Asc)`
   after `resource_id ASC`), so no entry lists it.
6. **An aggregation uses the index of the query underneath it.**
   `CountByResourceAndRelation` and the connector List's `WithCount` add no
   entry.

### 9.2 Both directions are listed — Firestore has no automatic reverse index

Every list in §7 is served in both directions: the port's order is
caller-supplied, and the connector List's HasPrev probe re-issues the page query
with EVERY direction flipped (`ListQuery.ordered(…, reverse: true)`). The vendor
does not derive one from the other:

> To run the same queries but with a descending sort order, you need an
> additional index in the descending direction for `population`.
> — [index-overview](https://firebase.google.com/docs/firestore/query-data/index-overview),
> *Queries supported by manual indexes*

So each list shape contributes an all-ASCENDING entry and an all-DESCENDING one
(the equality prefix stays `ASCENDING` in both — the docs state the prefix's mode
is free for equality fields, so pinning it keeps the two entries comparable).
This mirrors the connector's own C4/C5 live leg, which deploys four indexes for
two order fields × two directions. The keyset lookups are the exception that
proves the rule: `idStream` only ever traverses `resource_id ASC`, so they
contribute one direction, not two.

### 9.3 The entries

| Collection | Fields (all `ASCENDING` unless marked) | Serves |
|---|---|---|
| `iam_relationships` | `resource_key`, `relation` | relation targets, the direct count, the reconciliation read, `DeleteRelationship`'s sweep |
| `iam_relationships` | `resource_key`, `relation`, `subject_key` | expanded check |
| `iam_relationships` | `resource_type`, `relation`, `resource_id` | candidate scan |
| `iam_relationships` | `resource_type`, `relation`, `subject_key` | descendant hop |
| `iam_relationships` | `resource_type`, `relation`, `subject_key`, `resource_id` | both keyset lookups |
| `iam_relationships` | `subject_type`, `subject_id`[, `resource_type`][, `relation`], `created_at`, `relationship_id` — 4 subsets × 2 directions | `ListRelationshipsBySubject` + its HasPrev probe |
| `iam_relationships` | `resource_key`[, `subject_type`][, `relation`], `created_at`, `relationship_id` — 4 subsets × 2 directions | `ListRelationshipsByResource` + its HasPrev probe |
| `iam_roles` | `subject_key`, `created_at`, `role_key` — 2 directions | `ListBySubject` |
| `iam_roles` | `resource_key`, `created_at`, `role_key` — 2 directions | `ListByResource` |
| `iam_roles` | `subject_key`, `resource_type`, `role`, `resource_id` | `LookupResourceIDsBySubjectAndRoles` |
| `iam_roles` | `resource_key`, `grant_key` — 2 directions | `ListEffectiveByResource`'s streams |

Changes from the A1 provisional manifest (16 entries, A-D7's expectations):
`(subject_type, subject_key, relation, resource_id)` was REMOVED — a shape no
query issues (the target lookup filters `resource_type`, not `subject_type`).
`(resource_type, subject_key, relation, resource_id)` was RESPELLED as
`(resource_type, relation, subject_key, resource_id)` to obey the pinned equality
order (rule 4). Thirteen entries were ADDED: the descendant hop's
`(resource_type, relation, subject_key)` and the twelve optional-filter subsets
of the two relationship listings. `(resource_key, relation)` was removed at A5
and RESTORED at A7 when rule 2 became uniform.

### 9.5 Field overrides — the single-field indexes this store depends on

A composite index is not the only index the store needs. Several §7 shapes derive
none at all — the expansion hop (one `in` on `subject_key`), the resource-rows
read and the delete family (one equality on `resource_key`), the role teardown
sweep — and each is served by Firestore's AUTOMATIC single-field indexing.

Automatic is not the same as guaranteed. A host, or a later manifest of this
store's own, can disable a field's single-field indexes with a `fieldOverride`,
and those queries would then fail with `FAILED_PRECONDITION` in production while
every emulator run stayed green (the emulator enforces no index at all). The
manifest is the specification and `ProbeIndexes` checks only what the manifest
DECLARES (connector C5), so an undeclared dependency is an unchecked one. A5's
"no field overrides, deliberately" reasoned that the default configuration
supplies what those shapes need — true, and exactly the assumption that goes
unnoticed when it stops holding.

So the manifest declares them. The rule is uniform rather than minimal: **every
field any matrix row filters or orders on**, on the collection that queries it —
nine on `iam_relationships` (`resource_key`, `subject_key`, `relation`,
`resource_type`, `resource_id`, `subject_type`, `subject_id`, `created_at`,
`relationship_id`) and eight on `iam_roles` (`subject_key`, `resource_key`,
`resource_type`, `role`, `resource_id`, `grant_key`, `role_key`, `created_at`).
`TestCompositeFreeShapesDeclareTheirSingleFieldIndexes` enforces both directions:
a composite-free shape whose field is undeclared fails, and a declared override
no query uses fails.

Each entry asks for ONE index — `ASCENDING` at `COLLECTION` scope. **Declaring an
override REPLACES a field's default set** (ascending + descending +
array-contains), which is deliberate: these collections carry no array field
(usersets are separate documents, never an array), no query is collection-group
scoped, and no query sorts a single field descending without an equality prefix —
every descending order in §7 belongs to a composite. The effect on a deployed
database is fewer single-field indexes, not fewer served queries. A host that
adds its own queries against these collections must extend the manifest rather
than rely on the defaults.

### 9.4 Export, probe, and what live still owes

`ExportIndexes(dst)` merges this fragment into the host's own manifest (the
connector's `ExportIndexes`: union by index identity, byte-stable, atomic
rename), which is why a host's unrelated indexes survive and a re-export is an
empty diff. The checked-in file is byte-identical to that output
(`TestIndexManifestIsSortedAsMergeSorts`), so it is also directly deployable by
the connector README's `gcloud firestore indexes composite create` loop.

`Repositories` / `RelationshipRepository` probe the manifest at construction
(`firestoredb.ProbeIndexesFS`) unless `WithoutIndexProbe()` is passed. The
constructors take no `context.Context` — the SQL siblings' table probes do not
either — so the probe runs on `context.Background()` and is bounded by the
connector's `ProbeTimeout` (30 s), which is what an Admin API that never answers
hits instead of hanging a host's boot. Against the emulator the probe is
`ErrProbeUnavailableOnEmulator` rather than a silent skip
(`indexes_integration_test.go` asserts both sides).

**Owed, and not satisfiable on an emulator:** `indexes_live_test.go` deploys
nothing but requires this manifest deployed and READY on the target database
(CI: `FIRESTORE_LIVE_INDEXES=pockets/authorization/stores/firestore/firestore.indexes.json`,
wired by A6). It then proves (a) the probe accepts the deployment and both
constructors succeed with the probe ENABLED, and (b) every matrix row executes
without a `*firestoredb.MissingIndexError`. As of A5 it has NOT run — no live GCP
project existed in that session — so the manifest is derived, reviewed, and
hermetically consistent with the code, but the index set itself is UNPROVEN until
that leg runs. Anything rule 2's conservatism over-declared can only be pruned by
that run.
