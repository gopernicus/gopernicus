# integrations/datastores/firestore

The datastore connector for Google Cloud Firestore (**Native mode**). It wraps
the `cloud.google.com/go/firestore` client behind the same connector shape as
`integrations/datastores/pgxdb` and `integrations/datastores/turso`: `Config` /
`Open` / `DB` / `Close` / `StatusCheck` / `MapError` / `RetryPolicy` /
`Redacted`, plus the store-facing surface (`Reader`, `Writer`, `Transact`,
`ReadSnapshot`, `List`, the time/id/key helpers) — with documents and queries
where those have statements and placeholders, an **index manifest** where those
have migrations, and a transaction whose **callback may run more than once**
where those have `BEGIN`/`COMMIT`.

It owns "how to talk to Firestore," never any app's or pocket's queries.
App/pocket repositories consume this package's `*DB` (a pocket's
`stores/firestore` module imports it as `firestoredb`).

What it does **not** own: any pocket's collections, documents, or index
fragment (those ship with each store); security rules (server-side clients
bypass them); query serialization, vector search, real-time listeners, and
BulkWriter. Datastore mode is a different API and Firestore Enterprise
(MongoDB-compatible) is a different product — both are out of scope.

The vendor import is aliased `gcfs` throughout, so this package and the library
it wraps never read alike. The module's direct requires are `sdk`,
`cloud.google.com/go/firestore` (whose module also carries the Admin API package
the index probe needs), and that client's own required surface —
`google.golang.org/api` for credential options and `google.golang.org/grpc` for
the status codes every Firestore error is expressed in (the posture
`integrations/filestorage/gcs` already takes).

## Config, Open, StatusCheck

```go
db, err := firestoredb.Open(ctx, firestoredb.Config{
    ProjectID:  os.Getenv("FIRESTORE_PROJECT_ID"),   // required
    DatabaseID: os.Getenv("FIRESTORE_DATABASE_ID"),  // "" → "(default)"
    Retry:      firestoredb.RetryPolicy{Attempts: 3, MinBackoff: 250 * time.Millisecond},
})
```

`CredentialsJSON` is optional; absent, the client uses Application Default
Credentials (the normal posture on Cloud Run, GKE, or any workload with an
attached service account). `Config.Redacted()` prints the connection target —
`projects/<p>/databases/<d>` — and never a credential; `DB.Target()` is the same
string for an open `DB`. `Config` also implements `String()` as `Redacted()`, so
`%v`, `%+v`, and a `slog` attribute all print the target and never
`CredentialsJSON`'s bytes — a redaction helper only protects the caller who
remembers to call it.

Client construction issues no RPC and receives the **caller's** context
unchanged: the Google Cloud clients keep the construction context for the life
of their connection pool, so a context `Open` cancelled on return would poison
every later request (the posture `integrations/filestorage/gcs` takes).
`Config.ConnectTimeout` therefore bounds the **boot check only**.

`Config.Retry.Attempts > 1` opts into **eager boot validation**, exactly the
turso connector's posture: `Open` runs a real round-trip (`StatusCheck`) retried
under a full-jitter exponential backoff, targeting the orchestration race where
the database is not yet reachable at startup. `Config.Retry` governs the boot
check and nothing else — no read or write is ever auto-retried by the connector.
A **permanent** failure short-circuits the loop: an error carrying
`sdk.ErrForbidden`, `sdk.ErrUnauthorized`, or `sdk.ErrInvalidInput` is a
credential or a request the database will never accept, and sleeping five
backoffs over it only delays the boot failure and buries its cause.

`StatusCheck` reads ONE document at the reserved path
`gopernicus_status/status`, which is never written: **`sdk.ErrNotFound` is the
healthy answer** — it proves the round trip completed and the caller may read —
while transport, permission, and quota failures surface as themselves, already
classified by `MapError` (an unreachable database is `sdk.ErrUnavailable`, a
denied credential `sdk.ErrForbidden`), so a health endpoint can branch on them.
The read goes through `ReaderFrom` like every other read here. The path uses
ordinary identifiers on purpose: Firestore rejects any collection or document id
matching `__.*__` with `InvalidArgument`, so the tempting `__gopernicus__`
spelling is illegal.

`DB.Emulated()` reports whether this client talks to the Firestore emulator. It
reads the vendor's own `Client.UsesEmulator` (set from `FIRESTORE_EMULATOR_HOST`
at construction) rather than re-reading the environment, so it cannot drift from
what the client actually did. The index probe and the test factories branch on
it **loudly**, never silently.

## Reads and writes go through `Reader`/`Writer` — a reference is not permission

`DB.Collection(name)`, `DB.Doc(collection, id)`, and `DB.CollectionGroup(name)`
hand out the vendor's own `*CollectionRef` / `*DocumentRef` /
`*CollectionGroupRef`, and `gcfs.Query` values are built from them.
`CollectionGroup` is the scope for reading a subcollection across every owner
("all sessions", "all relationships"); building it performs no I/O and the same
mediation rules apply to the query it produces. A collection-group query needs
its indexes declared at `COLLECTION_GROUP` query scope in the manifest — the
collection-scoped index of the same fields does not serve it.
Those types carry their own I/O methods. **Holding one is not permission to use
them.** There is deliberately no `Underlying()` and no `Client()` accessor
(guard G9). Every read and write goes through the tx-aware seams:

```go
type Reader interface {
    Get(ctx context.Context, ref *gcfs.DocumentRef) (*gcfs.DocumentSnapshot, error)
    GetAll(ctx context.Context, refs []*gcfs.DocumentRef) ([]*gcfs.DocumentSnapshot, error)
    Documents(ctx context.Context, q gcfs.Query) *gcfs.DocumentIterator
    Count(ctx context.Context, q gcfs.Query) (int64, error)
}
type Writer interface {
    Create(ctx context.Context, ref *gcfs.DocumentRef, data any) error
    Set(ctx context.Context, ref *gcfs.DocumentRef, data any, opts ...gcfs.SetOption) error
    Update(ctx context.Context, ref *gcfs.DocumentRef, updates []gcfs.Update, pre ...gcfs.Precondition) error
    Delete(ctx context.Context, ref *gcfs.DocumentRef, pre ...gcfs.Precondition) error
}

r := db.ReaderFrom(ctx)   // the ambient transaction's reader when ctx carries one, else the client's
w := db.WriterFrom(ctx)
```

The reason is not tidiness: a direct `ref.Get` inside a `Transact` callback runs
on the client, OUTSIDE the transaction, and silently splits an atomic unit. No
import guard can see that — G9 only proves no accessor exists — so the
discipline is stated here and reviewed at the store: **references and queries
are values to BUILD, never to execute.**

Two obligations follow the seam out to the caller, because the vendor's iterator
cannot be wrapped without losing its cursor:

- `Stop` every iterator `Documents` returns (defer it at the call site).
- At the iteration boundary treat `iterator.Done` as the loop terminator and
  pass every other `Next` error through `MapError`. Errors the transactional
  reader defers to `Next` — the read-after-write refusal among them — arrive
  nowhere else.

Two per-method shapes are preserved from the vendor deliberately: `Get` returns
the snapshot ALONGSIDE its `sdk.ErrNotFound` error (so "absent" can be a value,
not a second read), and `GetAll` never errors for a missing document — callers
check `Exists()` per entry.

## `Transact` — **the callback may run more than once**

This is the one contract difference from the SQL connectors, and it is not a
detail. Firestore commits optimistically: when the commit loses a race the
server answers `Aborted` and the vendor **re-runs the callback from the top**,
up to `Config.MaxAttempts` times (`DefaultMaxAttempts` = the vendor's 5). Three
obligations follow:

- The callback must be **idempotent in every side effect OUTSIDE Firestore**.
  Sending a mail, charging a card, or appending to a log from inside it happens
  once per attempt. Do those after `Transact` returns nil.
- Any state the callback writes into its enclosing scope must be **reset at the
  top of the callback**, not initialized once outside it. A result recorded on a
  losing attempt is not the result of the transaction that committed.
- Writes queued by a losing attempt are discarded; nothing read on a losing
  attempt is still known to be true.

`(*DB).Transact` implements `sdk/foundation/crud.Transactor`. It commits when
the callback returns nil, rolls back and returns the callback's error
**unwrapped** when it does not — with one narrowing: an error carrying a bare
gRPC status and no sdk sentinel goes through `MapError`, so a vendor status a
callback forwarded cannot escape unclassified. A domain sentinel, a plain error
and an already-mapped value all come back byte-identical, so `==` still holds.
And — when the callback panics — it recovers,
lets the vendor roll back, then **re-panics with the original value** (the
vendor has no deferred rollback, so an escaping panic would leave the
transaction open until the server released its locks; the panic value survives,
the original stack does not).

**Reads before writes.** Firestore refuses any read issued after the first write
in the same transaction, and a transaction never observes its own pending
writes. Read everything, decide, then write. A read after a write is
`ErrReadAfterWrite` (wraps `sdk.ErrInvalidInput`) and the vendor re-checks it
even if the callback swallows the error. `Count` is unavailable inside a
transaction (`ErrCountInTransaction`) — count by iterating the transactional
`Reader`.

**Nesting fails loud.** A `Transact` inside a `Transact` (or inside a
`ReadSnapshot`) returns `ErrNestedTransact`, before the vendor is called.

**Exhaustion is a conflict.** When the retries run out on a losing COMMIT the
server's `Aborted` goes through `MapError` and surfaces as `sdk.ErrConflict`:
the caller is the contention loser and may retry the whole workflow. Lower
`Config.MaxAttempts` to 1 to get that immediately; raise it for a hot document
(each retry re-runs the callback, so the cost is real reads).

**A contended READ inside the callback is retried too.** The vendor decides
whether to retry by asking whether the error IS or WRAPS a gRPC `Aborted`
status — **including an error the callback returned** — and `MapError` PRESERVES
that status in the chain. So a transactional read that loses a race, mapped to
`sdk.ErrConflict` the way a store maps every read, is still recognized as
contention and re-runs the callback (up to `Config.MaxAttempts`). That is what
makes contention on transactional READS recoverable: without it, only a losing
COMMIT would ever be retried, and the same condition would get two different
answers depending on which RPC noticed it first.

The consequence to design for is the one already stated: the callback may run
more than once, so reset its attempt-local state at the top. If a callback must
end the transaction on an error that happens to carry an `Aborted` status,
return a value that does not — a domain sentinel of its own.

### A committed outcome is not the callback's error

The callback's return value decides COMMIT or ROLLBACK and nothing else. It is
not the operation's domain answer, and conflating the two silently discards
work. Both shapes are legitimate; choose per operation.

```go
// Commit, THEN report. Consuming an expired single-use token still has to
// delete it: the callback returns nil so the delete commits, and the caller
// reports the domain outcome afterwards.
var expired bool
if err := db.Transact(ctx, func(ctx context.Context) error {
    snap, err := db.ReaderFrom(ctx).Get(ctx, ref)
    if err != nil {
        return err
    }
    expired = isExpired(snap)                  // reset on every attempt
    return db.WriterFrom(ctx).Delete(ctx, ref)
}); err != nil {
    return err                                 // nothing committed
}
if expired {
    return sdk.ErrExpired                      // committed, then reported
}
```

```go
// Reject and roll back. A stable rejection that must leave the database
// untouched returns the domain error from the callback; Transact hands it back
// byte-identical and every queued write is discarded.
return db.Transact(ctx, func(ctx context.Context) error {
    ...
    return ErrPasswordlessRejected
})
```

## `ReadSnapshot` — one snapshot across an operation

```go
err := db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error { ... })
```

Every read the callback issues — through the `Reader` it is handed or through
`ReaderFrom` on the context it is handed — observes the SAME instant, however
many queries, chunks, graph hops, or reverse/count probes the operation takes.
It is a vendor read-only transaction, so: no write is possible (`WriterFrom`
returns a `Writer` that refuses with `ErrWriteInReadOnlyTransaction` at the
seam, before the wire); it is never retried, so the callback runs exactly ONCE;
and `Count` is unavailable, as in any transaction.

Called from inside an existing `Transact`, it REUSES that transaction's snapshot
and `Reader` instead of nesting — the one nesting case the connector resolves
rather than refuses, because a read-write transaction already reads one
snapshot. The context it hands the callback is re-stashed **read-only** even
then, so `WriterFrom` refuses inside the snapshot exactly as it would in a
standalone one; the enclosing `Transact` keeps writing on its OWN context. That
matters because a helper written against `ReadSnapshot` must not start queueing
writes into someone else's transaction the day it is wrapped in a `Transact`.
`Transact` inside a `ReadSnapshot` is still `ErrNestedTransact`: a snapshot
cannot grow a write.

`TxFromContext(ctx) (*gcfs.Transaction, bool)` reports either kind. It is the
seam a store uses to REFUSE to run inside a host's transaction — see the known
family difference below.

## `MapError`

`MapError` is the only place vendor error shapes are interpreted; every
`Reader`/`Writer` method returns through it and a store maps its own iterator
errors with it at the iteration boundary.

| Firestore error | Result |
|---|---|
| `NotFound` | `sdk.ErrNotFound` (`Get` also returns the snapshot, `Exists() == false`) |
| `AlreadyExists` | `sdk.ErrAlreadyExists` |
| `Aborted` (transaction retries exhausted) | `sdk.ErrConflict`, with the `Aborted` status still recoverable through `status.Code` |
| `FailedPrecondition`, missing index | `*MissingIndexError` → `ErrMissingIndex` → `sdk.ErrUnavailable`, carrying the server's message and its index-creation URL |
| `FailedPrecondition`, index still BUILDING (different wording, same link) | the same `*MissingIndexError` |
| `FailedPrecondition`, otherwise | `sdk.ErrConflict` |
| `InvalidArgument` | `sdk.ErrInvalidInput` |
| `DeadlineExceeded`, `Unavailable`, `ResourceExhausted` | `sdk.ErrUnavailable` |
| a bare `context.DeadlineExceeded` (the vendor's own retry outlived the caller's deadline, no status attached) | `sdk.ErrUnavailable` |
| `PermissionDenied` | `sdk.ErrForbidden` |
| `Unauthenticated` | `sdk.ErrUnauthorized` |
| read after write in a transaction | `ErrReadAfterWrite` (`sdk.ErrInvalidInput`) |
| write in a read-only transaction | `ErrWriteInReadOnlyTransaction` (`sdk.ErrInvalidInput`) |
| nested transaction / invalid read time (vendor) | `sdk.ErrInvalidInput` |
| `nil`, `iterator.Done`, anything already carrying an sdk sentinel | returned byte-identical |

Three properties worth knowing.

**It wraps rather than replaces**: the result is
`firestore: <the vendor error>: %w<sentinel>`, where the SQL connectors return a
bare sentinel and drop the driver message. Firestore's message carries the
document path, the failed precondition, and the index-creation URL; discarding
it makes production failures unreadable. `errors.Is` is unaffected.

**The gRPC status survives the mapping.** Every status row wraps BOTH the
original error and the sentinel (multi-`%w`), so the mapped value answers
`errors.Is(err, sdk.ErrConflict)` AND `status.Code(err) == codes.Aborted`.
Recover it with `status.Code(err)` or `status.FromError(err)`; both walk the
chain through `errors.As`. This is load-bearing rather than decorative: the
vendor's transaction retry gate asks `status.FromError` about the error a
callback returned, so an `Aborted` flattened into a bare sentinel would tell the
vendor "settled" when the truth is contention, and a store that maps its
transactional reads — the only honest thing to do with them — would lose the
retry. `*MissingIndexError` carries the server status the same way.

And it is **idempotent** — mapping an already-mapped error changes nothing, so a
store may map an error a helper already mapped without flattening a domain
outcome into a generic sentinel. Anything unrecognized comes back wrapped with a
`firestore:` prefix and NO sentinel, so an unknown failure surfaces as a 500
instead of a plausible-looking domain answer.

The two missing-index recognizers are **host-agnostic**: the canonical "requires
an index" phrasing, and `index` plus a `https://console.` link. The console HOST
varies with how the database was provisioned (`console.firebase.google.com` for
one created through Firebase, `console.cloud.google.com` for one created through
Google Cloud) and the still-building variant never says "requires an index", so
matching either host by name would map half of production's missing-index
failures to a bare `sdk.ErrConflict` with no URL. `MissingIndexError.URL` is the
first `https://` token in the message, verbatim.

The connector maps INFRASTRUCTURE errors only. Store-level CAS, no-op, and
idempotency semantics stay with the owning adapter.

## `List[T]` — the crud grammar over documents

`List[T]`/`ListQuery[T]` implement the `sdk/foundation/crud` list standards with
observable semantics identical to `pgxdb.List` and `turso.List`: ordering
against a per-aggregate allow-list, bidirectional keyset cursors with a reverse
probe for `HasPrev`/`PreviousCursor`, an explicit offset strategy, and opt-in
counts. The per-pocket `storetest` conformance suites are the parity proof.

- **Cursor strategy.** Every query orders by (order field, PK) in the requested
  direction, emitted once when the two are the same field, and the cursor is the
  matching exclusive `StartAfter` tuple; the helper over-fetches `limit+1` and
  trims. An empty `PK` means the document id, and a document-id cursor value
  travels as the id STRING — the vendor resolves it to a reference itself. A
  stale order-field cursor decodes to the first page, exactly as turso does.
- **Reverse probe.** The prev window flips the sort, bounds it at the incoming
  cursor, fetches up to `limit` rows and restores forward order. Page two's
  window is always one row short — page one is the first page, which no cursor
  addresses — so it reports `HasPrev` with an EMPTY `PreviousCursor`, which is
  `crud.MarkPrevPage`'s specified behavior and the SQL connectors'.
- **Offset strategy.** `Offset(n).Limit(limit+1)`, `HasMore` from the
  over-fetch, no cursors emitted. O(offset) documents are billed. Under a
  `PostFilter` the offset counts MATCHES, not scanned documents.
- **`WithCount`.** The server-side count aggregation over the **ordered** query
  — the whole population the page can traverse, never the page itself. Ordered,
  not base, because Firestore's `OrderBy` EXCLUDES documents that lack the
  ordered field (there is no NULLS LAST: an absent field is an absent index
  entry), so a base-query count would promise rows no cursor can reach. Inside a
  `Transact` or a `ReadSnapshot` the aggregation is unavailable
  (`ErrCountInTransaction`), so `List` falls back to counting by iterating the
  same population: a snapshot-bound count stays consistent with the page it
  accompanies, at O(population) reads. With a `PostFilter` the count is always
  the iterate-and-filter one — the server does not know the predicate.
- **`PostFilter`** makes every path a page-fill loop: successive underlying
  pulls of `max(limit+1, 50)` documents are decoded and filtered until enough
  matches are collected or the population is exhausted. Each pull resumes with a
  `StartAfter` on the last **snapshot** scanned, not on the decoded row's order
  value — the document the server already positioned, so resuming never depends
  on a value round-tripping. That is load-bearing where the order field can be
  NULL: an absent model decodes to a Go zero (`time.Time{}`), which Firestore
  compares as a timestamp and sorts AFTER every null, so a value-based resume
  would jump the rest of a null run and silently drop those documents.
  `NextCursor` is still encoded from the last RETURNED match after trimming
  (through `OrderValueOf`/`PKOf`), never from the extra match or the last
  scanned document.

**Search (ruling R4).** `List` itself never interprets `req.Search`: the STORE
composes a `PostFilter` from its `SearchFields`, which is how the `storetest`
search group passes without a server-side text operator. Both store trains use
ONE composition of it so they cannot drift from each other or from turso:

```go
q.PostFilter = firestoredb.SearchFilter(apikey.SearchFields, func(row apikey.APIKey, field string) string {
    if field == "name" { return row.Name }
    return ""
}, req.Search)
```

`SearchFilter` matches with `crud.MatchesSearch` (literal substring, ASCII-only
case folding, so `%`, `_` and `\` are ordinary characters) and ORs across the
declared fields — a field the store did not declare is not searched. It returns
`nil` for a blank term (no search is not a filter) and for a non-blank term over
an empty field list; pass that `nil` straight through, because a non-blank
search against a `ListQuery` with a NIL `PostFilter` is REFUSED with
`sdk.ErrInvalidInput` rather than answered with an unfiltered page — the same
answer turso's `AddSearchClause` gives. The rule pinned for the future: **a
Firestore list honors `Search` only under a parent scope**; a top-level
searchable list is a plan-level decision, never an accidental full scan.

`crud.OrderField.CastLower` is likewise REFUSED, not ignored: Firestore cannot
case-fold in an index, and no store in this repository sets the flag. Firestore
orders strings by UTF-8 bytes — the same raw byte order pgx pins with
`COLLATE "C"` and turso gets from SQLite's BINARY collation — so keyset parity
across the three families needs no collation work.

Ordering by one field while filtering on another needs a **composite index** in
production. The emulator enforces none, so an emulator-green list proves nothing
about production's `FAILED_PRECONDITION`; every direction a store serves — plus
the reversed direction the HasPrev probe issues, which needs the same index with
all directions flipped — belongs in that store's manifest.

Order by a field **every document in the population has**. A document missing
the order field is invisible to every List path — page, cursor, reverse probe,
and total — and a document whose field is NULL is traversed (null sorts first)
but cannot be addressed by a `crud.Cursor`, which carries the decoded value.
Write optional timestamps through `NullTime`, and do not order by them.

## The index manifest — shipped, exported, probed

Migrations have no Firestore analogue; indexes do. The scaffold model
(`MigrationsFS` + `ExportMigrations`) becomes the `firestore.indexes.json`
schema:

```go
type IndexManifest struct {
    Indexes        []CompositeIndex `json:"indexes"`
    FieldOverrides []FieldOverride  `json:"fieldOverrides,omitempty"`
}

func ParseIndexManifest(fsys fs.FS, name string) (IndexManifest, error)
func (m IndexManifest) Merge(other IndexManifest) (IndexManifest, error)
func ExportIndexes(m IndexManifest, dst string) error
func ProbeIndexes(ctx context.Context, db *DB, m IndexManifest) error
func ProbeIndexesFS(ctx context.Context, db *DB, fsys fs.FS, name string) error
```

A store embeds its fragment (`//go:embed firestore.indexes.json`) exactly as it
embeds `migrations/*.sql` and re-exports these two calls.

**Parsing is strict**: unknown JSON keys are rejected, so a misspelled
`queryscope` fails at wiring time instead of shipping an index nobody deployed.
A composite index needs at least two fields (one field is a single-field index —
it belongs in `fieldOverrides`, and the Admin API refuses it as a composite).

**Export MERGES.** A migration stream was append-only files; a manifest is ONE
shared document a host commonly already has. `ExportIndexes` parses an existing
destination, unions it with the fragment by index identity, and writes
byte-stable output (canonical order, two-space indent, trailing newline) — so
the host's unrelated indexes and field overrides survive and re-exporting an
unchanged fragment is an empty diff. A destination that CONTRADICTS the fragment
(a field override with different index sets, or a conflicting `ttl` value) fails
with `ErrConflictingFieldOverride` and is left untouched rather than silently
resolved. The write itself is ATOMIC: the merged document goes to `dst.tmp` and
is renamed over `dst`, so an interrupted export cannot truncate the manifest it
was merging into, and a refused export leaves no `.tmp` behind for someone to
mistake for the real file months later.

**The probe is the boot check.** `ProbeIndexes` asks the Firestore Admin API
whether every composite index in the manifest exists and is `READY`, and whether
every declared field override is reflected in that field's live single-field
configuration — the same "fail at wiring time, name the missing thing" posture
as the SQL stores' table probes. It answers `nil`; a `*MissingIndexError` (or an
`errors.Join` of them) naming each gap and the database's console index page; or
an `sdk.ErrForbidden`-wrapped error naming the **`datastore.indexes.list`**
permission the credential lacks — one permission covers BOTH Admin calls, since
the IAM tables map `collectionGroups.indexes.list` and
`collectionGroups.fields.get` to it (there is no `fields.get` permission to
grant). An empty manifest issues no RPC at all. A caller's deadline is honored
as given; a context without one is bounded by **`ProbeTimeout`** (30s), so an
Admin API that never answers fails the boot instead of hanging it.

**A probe failure is a wiring error, never a retry.** `ErrMissingIndex` wraps
`sdk.ErrUnavailable` — the sentinel a generic infrastructure retry watches for —
but no amount of waiting deploys an index. A caller that retries
`sdk.ErrUnavailable` must branch first:

```go
if err := firestoredb.ProbeIndexesFS(ctx, db, IndexesFS, IndexesFile); err != nil {
    if errors.Is(err, firestoredb.ErrMissingIndex) {
        return err // deploy the manifest; retrying cannot help
    }
    // only now is a transport failure worth another attempt
}
```

A store's constructor runs the probe unless the host passes **`WithoutIndexProbe()`**
— the store-side escape for a runtime service account that cannot be granted
`datastore.indexes.list`; the host then owns index deployment itself. Against
the emulator the probe returns `ErrProbeUnavailableOnEmulator`, checked FIRST,
before any Admin client is constructed: the emulator keeps no index registry,
`FIRESTORE_EMULATOR_HOST` is honored only by the DATA client, and an Admin call
from an emulator run would either fail or, worse, reach real GCP with ambient
credentials. Conformance harnesses pass `WithoutIndexProbe()`.

What is NOT probed, stated plainly because a probe that implies more than it
checks is worse than none: a field override the manifest does not declare (a
store whose query depends on a field's default single-field indexing DECLARES
that dependency and only then is it checked); a DECLARED field override whose
index set is EMPTY (it asks for nothing, so nothing is compared and no RPC is
issued for it — such an entry documents intent, usually a TTL setting, and must
not be read as "this field was checked"); TTL configuration, index density, and
multikey/vector/search modes; and whether an index is wide enough for a query
the manifest never described. The probe proves the manifest was deployed.
The store's query matrix — run live — proves the manifest is right.

A manifest spends **two** per-database budgets, and every store a host mounts
shares both: Firestore allows **200 composite indexes** per database without
billing enabled (1,000 with) and, separately, **200 single-field
configurations** (1,000 with) — the budget every `fieldOverrides` entry spends,
shared with TTL policies (an indexing exemption and a TTL policy on the *same*
field count as one configuration). Deploying a manifest therefore takes two
commands, not one: `gcloud firestore indexes composite create` per composite and
`gcloud firestore indexes fields update <fieldPath> --collection-group=<cg>
--index=order=…` (or `--disable-indexes` for an override with an empty `indexes`
array) per field override. `ProbeIndexes` checks both halves, so a run that
deployed only the composites fails at construction on a field the manifest
declares. Both commands accept `--async`, and readiness is read back separately:
`indexes composite list` for the composites, `indexes fields list` for the field
configuration — the former never returns single-field indexes.

## Helpers — time, ids, keys

- `TimePrecision` / `TruncateTime(t)` — Firestore stores timestamps at
  MICROSECOND precision. Every store writes through `TruncateTime` (UTC,
  truncated, monotonic stripped) so a read-back compares equal. `NullTime(t)` /
  `NullTimePtr(t)` are the absent-model writers and `ParseTime` /
  `ParseNullTime` / `ParseNullTimePtr` their read twins over a decoded document
  value; a mistyped field is `sdk.ErrInvalidInput`, never a silent zero
  timestamp that would read as "not set".

  `NullTime` writes an explicit **null value**, not an absent field, and the
  difference is not cosmetic: `OrderBy` excludes documents that do not HAVE the
  field, so a document missing its order field is invisible to every List path —
  page, cursor, reverse probe, and total. A null is present, so the document is
  still traversed (null sorts first ascending). Inequality filters exclude null
  either way, so a store that means "unbounded" filters for it explicitly rather
  than expecting a range to include it. Write optional timestamps through these
  helpers; never omit the field.
- `NewID()` — a vendor-shaped auto id (20 characters of `[A-Za-z0-9]`,
  `crypto/rand`), for the ports where the datastore generates the id.
- `KeyHash(parts...)` — the document id for a natural key: lowercase hex
  SHA-256 (64 characters) of a length-prefixed encoding — for each part in
  order, eight bytes of its byte LENGTH big-endian, then the part's bytes,
  nothing else. Length-prefixing is what makes the tuple unambiguous
  (`["ab","c"]` ≠ `["a","bc"]`, and `KeyHash()` ≠ `KeyHash("")` ≠
  `KeyHash("","")`).

  `KeyHash` is mandatory for natural/claim ids, not a style choice. Document ids
  must be valid UTF-8, ≤ 1500 bytes, contain no `/`, and must not be `.`, `..`,
  or match `__.*__` — and port-legal input in both pockets violates all three of
  the interesting rules (authorization's six-component relationship tuple alone
  reaches 1536 bytes and any component may contain a slash; authentication
  bounds no identifier length at all). A hash is fixed-width, slash-free, and
  never reserved. The original components stay in document FIELDS and in
  returned values: a `KeyHash` is an identity, never a projection **and never a
  sort key** — ordering follows the port's own order fields, which are
  timestamps and short ids and need no hashing.

  Related hazard for stores over unbounded text: indexed values longer than 1500
  bytes are TRUNCATED, so an equality filter on raw unbounded user text can
  match two different values. Filter on the hash field and keep the raw value
  for projection, or re-verify the raw value in Go after the read.

## `firestoretest`

The test-support subpackage compiles without a build tag (the
`net/http/httptest` posture: the tagged tests that call it live in other
packages and modules). It has two factories, deliberately separate, and neither
can turn into the other.

**Emulator.** `Open(t)` / `OpenDatabase(t, id)` require `FIRESTORE_EMULATOR_HOST`
and **skip loudly** without it; the project is `FIRESTORE_PROJECT_ID` or
`gopernicus-test`. `Open(t)` stays on the database named by
`FIRESTORE_DATABASE_ID` — the default database when that is unset — so a CI job
can point a whole run at one named database without editing a test.
`OpenDatabase(t, id)` names a database explicitly and ignores that variable.
`Reset(t, db)` clears the whole database through the emulator's own endpoint
(`DELETE /emulator/v1/projects/{p}/databases/{d}/documents`), SCOPED to the
database that `db` is bound to, and REFUSES — loudly — if `db` is not an
emulator client. That refusal is the whole safety story: the same call shape
against a real project would mean "empty the database". Emulator-only tests
build under `integration && !live`.

**The isolation contract.** `Reset` clears the WHOLE selected database — not a
collection, not a fixture — so two suites sharing a database delete each other's
rows, and the failure arrives as a flake rather than as an error. Two rules
follow, and neither is optional:

1. Tests that call `Open` or `Reset` must NOT run in parallel with each other
   across packages (`go test` runs different PACKAGES concurrently by default;
   either give the package its own database, or run those packages with `-p 1`).
2. **Every pocket store train opens its own database** —
   `OpenDatabase(t, "authorization")` in `pockets/authorization/stores/firestore`,
   `OpenDatabase(t, "authentication")` in its authentication twin. The emulator
   serves named databases side by side and the clear endpoint is scoped to one,
   so the two store suites and this connector's own suite cannot clobber one
   another even when they run at the same time.

**Live.** `OpenLive(t)` uses `FIRESTORE_LIVE_PROJECT_ID` +
`FIRESTORE_LIVE_DATABASE_ID` with Application Default Credentials, and refuses
every unsafe target rather than degrading: an emulator endpoint in the
environment, an emulator client after `Open`, or the project's `(default)`
database are errors, never fallbacks. `ResetLive(t, db, collections...)` deletes
through the connector (query a page, delete it, repeat), touches ONLY the
collections it is named, and treats naming none as a fatal error — a reset that
clears nothing before a conformance run is a false green waiting to happen.
Missing live configuration SKIPS an ordinary run loudly and **FAILS it when
`FIRESTORE_LIVE_REQUIRED=1`**, naming exactly what is missing. That is the
release gate: a train cannot pass because the only leg proving production
behavior quietly did not run. Live tests build under `integration && live` and
are named `Test…Live`.

## Known family difference — the pocket stores do NOT join an ambient transaction

Ruling R1 of the firestore-stores milestone, stated here because a host choosing
a datastore family is choosing this property.

A Firestore transaction requires every read to precede every write and never
observes its own pending writes. The pockets' `storetest.RunTransactional`
family proves the join from both sides ("the ambient read sees the uncommitted
change"), which a native Firestore adapter cannot satisfy. So:

- A pocket's `stores/firestore` returns **no** `crud.Transactor` to the
  conformance harness, and that family **skips loudly** (the harness's existing
  nil-transactor path, which the in-memory stores also take).
- A store method whose context carries a connector transaction
  (`firestoredb.TxFromContext`) **fails loud** with a store-typed sentinel
  wrapping `sdk.ErrInvalidInput` — it never quietly runs on the client beside
  the host's transaction. A silent split is the failure; an error is honest.
- This connector still implements `crud.Transactor` (`(*DB).Transact`) for a
  host's own multi-document atomicity. That seam is sdk-level and host-facing,
  independent of whether the pocket stores join it.

This is a family difference, not a defect: Redis (`MULTI`/`EXEC` has no reads),
DynamoDB (write-only transactions), and Firestore all fail the same spec. The
ambient-join guarantee is a SQL-family property. Opening READS to an ambient
transaction is a possible later minor change; it is not the shipped behavior.

## Live-database provisioning

The live leg needs a **disposable, run-owned named database** — never
`(default)` — in a dedicated test project, created and deleted per run:

```sh
# create (Native mode, never the default database). No --delete-protection flag:
# it is a value-less boolean that defaults to FALSE, so a new database is already
# deletable. `--delete-protection=DISABLED` is not a valid value and fails.
gcloud firestore databases create \
  --database="ci-$GITHUB_RUN_ID" \
  --location="$FIRESTORE_LIVE_LOCATION" \
  --type=firestore-native \
  --project="$FIRESTORE_LIVE_PROJECT_ID"

# deploy the exported manifest: one `indexes composite create` per composite in
# firestore.indexes.json. CI does this with a jq loop (the Firebase CLI is not on
# the runner); a host that already uses the Firebase CLI can equally run
# `firebase deploy --only firestore:indexes` against its own firebase.json.
# --query-scope is omitted because its default is `collection`; pass
# `--query-scope=collection-group` for a collection-group index.
gcloud firestore indexes composite create \
  --collection-group=iam_relationships \
  --database="ci-$GITHUB_RUN_ID" --project="$FIRESTORE_LIVE_PROJECT_ID" \
  --field-config=field-path=resource_key,order=ascending \
  --field-config=field-path=created_at,order=descending

# wait: every expected index must be READY and none CREATING before any query
# runs (a query against a building index is a FAILED_PRECONDITION that reads
# like a connector bug). Count them — "no CREATING" alone is also true of a
# database where nothing was created.
gcloud firestore indexes composite list \
  --database="ci-$GITHUB_RUN_ID" --project="$FIRESTORE_LIVE_PROJECT_ID" \
  --format=json | jq '[.[] | select(.state == "READY")] | length'

# teardown — ONLY the run-owned database, never "(default)", never the project
gcloud firestore databases delete \
  --database="ci-$GITHUB_RUN_ID" --project="$FIRESTORE_LIVE_PROJECT_ID" --quiet
```

CI names the pre-provisioned-database secret **`FIRESTORE_LIVE_DATABASE_PIN`**,
not `FIRESTORE_LIVE_DATABASE_ID`: the workflow resolves the id (the pin, or
`ci-<run id>`) and exports `FIRESTORE_LIVE_DATABASE_ID` for the tests, and a
job-level secret of that name would win over the resolved value. The credential
secret is `FIRESTORE_LIVE_CREDENTIALS_B64` (base64 of the key JSON, mapped on
one step and shredded afterwards). The test PROCESS still reads
`FIRESTORE_LIVE_DATABASE_ID` — only the secret is named differently.

**Sweep for survivors.** A cancelled run, a revoked permission, or a failed
`databases delete` can leave a `ci-*` database behind, and it keeps costing.
Periodically (the owner's habit: before each release train, and monthly
otherwise):

```sh
gcloud firestore databases list --project="$FIRESTORE_LIVE_PROJECT_ID" \
  --format='table(name, createTime)' | grep '/ci-'
```

Anything older than the run that created it is an orphan; delete it with the
teardown command above. The workflow records ownership BEFORE it creates the
database, so a normal failure is already covered — this sweep is for the cases
where the cleanup step itself never got to run.

IAM on the CI service account, project-scoped to the test project:
`roles/datastore.owner` covers it, or the least-privilege split
`roles/datastore.user` (documents) + `roles/datastore.indexAdmin`
(`datastore.indexes.create/list/get`) plus the database admin permissions
`datastore.databases.create` / `datastore.databases.delete`, which today are
bundled only in `roles/datastore.owner`. Add
`roles/serviceusage.serviceUsageConsumer` when the credential belongs to a
different project. A HOST's runtime service account needs far less: the probe's
minimum is `datastore.indexes.list`, and a host that cannot grant even that
passes `WithoutIndexProbe()`.

The test process needs `FIRESTORE_LIVE_PROJECT_ID`, `FIRESTORE_LIVE_DATABASE_ID`,
Application Default Credentials, an EMPTY `FIRESTORE_EMULATOR_HOST`, and — on a
release train — `FIRESTORE_LIVE_REQUIRED=1`. Cleanup deletes only the database
that run created; setup and cleanup errors fail the run. No project-wide or
shared-database cleanup, ever.

## Before the first live dispatch

The `firestore-live` job in `.github/workflows/live-stores.yml` has never run —
no GCP project exists for this repository yet. Walk this list once, in order,
the first time it does:

1. **A dedicated project, with billing enabled.** Named (non-`(default)`)
   Firestore databases require a billing-enabled project, and the whole
   disposable-database protocol depends on named databases. A free/no-billing
   project cannot run this leg at all.
2. **Firestore API enabled** in that project:
   `gcloud services enable firestore.googleapis.com --project=<test-project>`.
   The Admin API the index probe uses is part of it.
3. **IAM bound** on the CI service account, project-scoped, exactly as in
   "Live-database provisioning" above: `roles/datastore.owner`, or the
   least-privilege split plus the two database-admin permissions.
4. **Secrets uploaded**: `FIRESTORE_LIVE_PROJECT_ID`,
   `FIRESTORE_LIVE_CREDENTIALS_B64` (`base64 -w0 key.json`), and optionally
   `FIRESTORE_LIVE_DATABASE_PIN` / `FIRESTORE_LIVE_LOCATION`. A project id is
   **not** a credential — it can equally live in a repository `vars.` entry
   (`vars.FIRESTORE_LIVE_PROJECT_ID`), which keeps it readable in logs and PRs
   from forks are still unaffected because the workflow is dispatch-only. Secret
   vs var is an owner call; switching means editing one line of the job's `env:`.
   The credential itself is always a secret.
5. **The runner has gcloud.** The job asserts this with a `gcloud --version`
   gate step before it creates anything; if a future runner image drops the
   Cloud SDK, that step names it instead of a half-provisioned database.
6. **Dispatch once with `firestore_live_required: false`.** The first run is the
   job's own first proof — every gcloud invocation in it is written from the
   verified CLI reference but has never executed. A skip is acceptable here; a
   failure tells you which step is wrong before a release depends on it.
7. **Confirm the evidence**: the index step ends with "every expected composite
   index is READY", and the audit table shows every derived `Test*Live` root as
   `pass`. Both are in the run's uploaded `firestore-live-evidence-*` artifact.
8. **Confirm no survivors**: run the `ci-*` sweep above; the run-owned database
   must be gone.
9. **Only then dispatch with `firestore_live_required: true`** — that is the run
   whose artifact and run id the release gate cites.

## Testing

```sh
# hermetic — no emulator, no network
go test ./...

# emulator
docker run --rm -d -p 8080:8080 \
  gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators \
  gcloud emulators firestore start --host-port=0.0.0.0:8080 --project=gopernicus-test
FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 FIRESTORE_PROJECT_ID=gopernicus-test \
  go test -tags=integration -count=1 -timeout 15m ./...

# live (a run-owned disposable database with indexes READY; see above)
FIRESTORE_EMULATOR_HOST= FIRESTORE_LIVE_REQUIRED=1 \
  FIRESTORE_LIVE_PROJECT_ID='<test-project>' FIRESTORE_LIVE_DATABASE_ID='<run-owned-db>' \
  GOOGLE_APPLICATION_CREDENTIALS='<sa.json>' \
  go test -json -count=1 -tags='integration,live' -timeout 30m -run 'Live$' ./...
```

If port 8080 is already taken, publish another one —
`docker run --rm -d -p 8081:8080 …` with
`FIRESTORE_EMULATOR_HOST=127.0.0.1:8081`, or, running the emulator directly,
`gcloud emulators firestore start --host-port=0.0.0.0:8081`. The tests read the
endpoint from the environment; nothing here assumes 8080.

The `:emulators` tag above is deliberately floating for local runs. CI pins that
image **by digest** (see `.github/workflows/live-stores.yml`), because the
emulator's transaction and lock timing is exactly what the contention tests
measure.

From the repository root, `make test-stores` runs the emulator leg (skipping
loudly without `FIRESTORE_EMULATOR_HOST`) and `make check` vets the
`integration` and `integration,live` files compile-only. Both legs also run in
`.github/workflows/live-stores.yml`.

**What the emulator cannot prove**, so no green here may be read as production
evidence:

- It enforces **no composite indexes** and keeps no index registry: every
  ordering runs index-free, and the probe refuses outright.
- Its transaction behavior is not identical, and documented locks "may take up
  to 30 seconds to be released."
- It **cannot produce commit-contention exhaustion** (measured, not assumed): a
  read-write transaction locks the document it read, so the conflicting outside
  write blocks and times out while the transaction commits. The strict
  exhaustion → `sdk.ErrConflict` assertion is a live test.
- It does not enforce all limits.
