# SDK audit S5: CRUD

Status: IMPLEMENTED AND VERIFIED — 2026-09-09. Reviewed baseline: firestore-authentication / 6807ed06.
Parent: [framework-audit.md](framework-audit.md).

Implementation: [listing-transaction-cleanup.md](listing-transaction-cleanup.md).
The findings below describe the reviewed baseline; the owner authorized both
bug fixes and package/API cleanup. The linked implementation record owns current
API decisions, exact changed files and verification; the review below is historical.

## Scope and work plan

Review all foundation/crud source/tests, trace transport parsing through real
domain/store callers, and inspect exercised datastore adapters. Judge correctness,
readability, package boundaries/name, generic API value, and compatibility.
This slice delivers findings and recommendations; source implementation follows
selection. AUDIT.md continues recording implemented changes only.

1. Parent audits SDK pagination/cursor/order/search/field/mapping contracts and
   tests, counts actual API usage, and compares representative consumer APIs.
2. Named lead-backend-engineer independently reviews pagination/query adapter
   parity and generic repository/transaction boundaries, read-only. Trace real
   pgx/Turso/Firestore callers, distinguish SDK defects from adapter follow-ups.
3. Named platform-sre reviews transaction/no-op guarantees and write/PATCH failure
   behavior with concrete callers, read-only. No live databases or mutations.
4. Run fresh SDK build/test/vet and guards, relevant hermetic adapter tests,
   and independent dummy-data probes for suspected defects. Record prerequisites,
   skips, observed behavior, and source locations. Challenge green tests.
5. Record keep/simplify/fix/remove recommendations, naming alternatives, migration
   cost, and unresolved questions; update the parent handoff. No API/package
   rename or consumer migration in the review itself.

## Preconditions and ownership

Prior S2–S4 audit changes and parallel Firestore work are dirty/untracked; preserve
all of them. CRUD has one prior S2 documentation edit in crud.go; its executable
source is unchanged at this start. No root go.mod. Go 1.26.1; formatter
/Users/jrazmi/go/bin/goimports; GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
POSTGRES_TEST_DSN, POSTGRES_DSN, DATABASE_URL, TURSO_DATABASE_URL,
TURSO_AUTH_TOKEN, FIRESTORE_EMULATOR_HOST, FIRESTORE_PROJECT_ID, and
GOOGLE_CLOUD_PROJECT are unset. Do not load real dotenv files or credentials.
Reference consumers are read-only. No commits, tags, live stores, generated
edits, consumer edits, releases, or production migrations.

## Assessment

The useful center is shared **listing vocabulary**, not generic CRUD machinery.
Keep page/request/limits, cursor and offset modes, ordering/search, and row/DTO
mapping helpers. Keep transaction coordination as a real independent capability.
Remove unused generic repository interfaces, the unused sparse-field layer, and
the duplicate ErrNotFound alias. Prefer foundation/list plus
capabilities/transaction over the current catch-all name; this is a proposed
breaking change, not an implemented decision.

Correctness work should precede mechanical package cleanup. The audit reproduced
bad pagination and transaction behavior despite green existing suites. Concrete
findings below distinguish SDK defects, adapter defects, and contract tradeoffs.

## Actual consumers and abstraction value

An AST scan resolved crud import aliases and counted distinct non-test,
non-generated Go files with selected references; vendor/node_modules/build and
historical .claude files were excluded. These are source counts, not proof of
runtime use; the framework count includes exported storetest harnesses.

| Checkout | CRUD-importing files | Page | ListRequest | Transactor | Reader/Writer/CRUD |
|---|---:|---:|---:|---:|---:|
| Framework | 100 | 71 | 74 | 4 | 0 |
| Segovia v2 | 27 | 20 | 12 | 3 | 0 |
| Coordination Hub | 30 | 24 | 18 | 4 | 0 |
| GPS360 | 136 | 92 | 72 | 0 | 0 |

- CMS's domain-owned EntryRepository has GetBySlug, ListByTerm, SetTerms, and a
  single EntryQuery parameter (pockets/cms/domain/content/entry_repository.go:23).
  Its shape serves the aggregate better than Reader[T,F]/Writer[T,C,U]. Workshop's
  repository template also declares its own narrow methods; none embeds the
  generic interfaces. Generic Go typing in Page and mapping helpers still earns
  its place; this is not a recommendation to remove generics generally.
- GPS360 uses MapItems in 24 files and MapPageErr in five. Those small helpers
  have real row/DTO and empty-list behavior; retain them even though this repo
  alone would make them look unused. MapPage occurs in all sampled apps.
- Limits is meaningful resource policy. GPS360's directory/tables.go:58 declares
  Default:50/Max:200, while Segovia's timelines/mentions.go:83 applies explicit
  mention limits at the HTTP edge. Preserve strict input parsing and defensive
  store clamping as different behaviors, sharing private default/max resolution.
- No framework or sampled host production source calls Some or Overlay. The only
  Field reference is GPS360 directory/write.go:188's uncalled overlay helper.
  The same file:82 explicitly records the owner's choice to retire Field for
  pointer-based per-field write semantics. Do not invent a replacement PATCH
  package or impose a universal null/clear policy. Remove the stale consumer
  helper/import when that app is migrated; no active flow depends on it.
- Transactor is consumed by Segovia tenancy/dashboard/timeline services and
  Coordination Hub's cascade/review/thread services. For example,
  coordination/cascade.go:132 uses it to commit a date change and downstream
  effects together. It is not an unused abstraction.
- crud.ErrNotFound appears in six CMS store source files and one Turso test;
  it aliases the root sentinel and adds no behavior. Use sdk.ErrNotFound.
- Current pins: Segovia SDK v0.8.0; Coordination Hub v0.7.0; GPS360 v0.7.1 with
  local SDK/pgxdb replacements. No consumer builds or edits occurred.
- Original Gopernicus's sdk/fop pagination contains much of the same policy.
  Its infrastructure/database/crud also has a generic dialect/spec store runtime.
  Do not restore that broader machinery: the current narrow domain ports and
  adapter-owned SQL are the better starting point.

## Confirmed correctness findings

### S5-01 — SQL predicate composition can repeat a page indefinitely

**Adapter defect; high priority.** pgxdb/listquery.go:79 and turso/list.go:321
choose WHERE versus AND by searching the entire SQL text for `WHERE`; their
search helpers do likewise. They also append AND without grouping a pre-existing
outer OR predicate.

Through actual turso.List over disposable SQLite:

- Base `SELECT id,n FROM rows WHERE n=1 OR n=2`, limit one: page one e1,
  page two e1, identical NextCursor. The existing OR branch bypasses the added
  cursor predicate, so navigation does not advance.
- Base `SELECT id,n FROM (SELECT id,n FROM rows WHERE n>0) AS r`: first page
  works, second page fails near AND because the only WHERE was nested.
- The actual pgx builder emits the same malformed outer-AND SQL.

This already costs consumers complexity: authorization's pgx/turso roles.go
and Coordination Hub vendor.go:571 explicitly document outer `WHERE TRUE` /
`WHERE 1 = 1` workarounds. No authorization bypass was demonstrated.

**Fix:** make the outer list boundary explicit. Prefer wrapping an authored
SELECT and applying list predicates outside it, with sortable/searchable columns
projected by contract. Inspect qualified columns and FixedOrder before selecting
that implementation; separating the base SELECT and a parenthesized filter is an
alternative. Do not add a SQL parser or broaden this into an ORM. Test nested
queries, OR filters, search+cursor+count, and repeated traversal until termination.
Preserve filter meaning and argument ownership.

### S5-02 — Case-folded PK ordering can skip rows

**Adapter defect; medium priority.** pgxdb/listquery.go:120 and turso/list.go:362
omit the raw PK tiebreaker whenever orderCol == PK, even when CastLower turns the
primary sort into LOWER(PK). The cursor predicate still compares (LOWER(PK), PK).

Actual Turso List with inserted IDs a, A, b, CastLower=true, limit one returns a,
then b, then finishes. A is never seen. The pgx builder likewise emits only
`ORDER BY LOWER("id") ASC`. This is a reproducible supported-configuration bug;
no sampled production use of this exact configuration was established.

**Fix:** keep the raw PK tiebreaker when the primary ordering expression is
transformed. Test equal folded values, both directions, and traversal. No schema
or default ordering change is required.

### S5-03 — A one-row second page loses its previous-page link

**Shared SDK/adapter contract defect; medium priority.** Reverse probes exclude
the incoming boundary record and fetch limit records. At limit one, page two's
cursor points at the first record, leaving zero records strictly before it.
MarkPrevPage (pagination.go:40) therefore sets no HasPrev.

Actual Turso List and the existing examples/minimal CMS memstore both returned
e1 then e2 with HasPrev=false. Matching source exists in pgxdb/list.go:291,
turso/list.go:239, firestore/list.go:347, and the SDK normative package doc.
The existing SDK tests only verify supplied probe slices; they never walk this
edge through the public paging protocol.

**Fix:** coordinate an inclusive reverse probe through the boundary, with
limit+1 records so the extra predecessor can supply the previous cursor. Do not
set HasPrev solely because a token was supplied. Verify first/second/last pages,
partial windows, changed page sizes, both directions, duplicate order values,
and deleted boundary records across implementations. Existing valid cursors can
keep their wire shape; metadata/helper behavior changes must be documented.

### S5-04 — Cursor decoding is permissive enough to produce wrong pages or 500s

**SDK defect and inconsistent adapter validation; medium priority.**
cursor.go:54 decodes one JSON value without checking EOF, object completeness,
or supported untagged values. It checks order-field mismatch before validating
shape. restoreOrderValue:105 accepts nil before checking even an unknown type tag.

Independent SDK probe observed all of these without an error:

- `null` and `{}` become an apparent first page.
- A matching order_field with missing order_value becomes a cursor with nil value.
- An unknown order_type with null value is accepted.
- A complete cursor followed by a second JSON object is accepted.
- An object-valued order_value is passed through.

Actual Turso List accepted the missing-value cursor and returned an empty page;
the object value reached the driver and produced an unsupported-map error without
sdk.ErrInvalidInput. All three connectors correctly classify decoder *errors*,
but cannot classify values the decoder incorrectly accepted. The example CMS
memstores return DecodeCursor errors directly; `cursor=!!!` through the actual
memstore and web responder produced HTTP 500 instead of invalid-input 400.

**Fix:** require one complete cursor object, EOF, required structural fields,
valid type tags, and supported values; preserve intentional JSON null order
values because Firestore supports nullable positions. Validate shape before
considering a well-formed token stale. Return malformed-input errors wrapping
sdk.ErrInvalidInput with causes preserved. Validate backend-required order-value
types at the adapter boundary; SDK cannot infer a column's type. Keep the existing
well-formed stale-order reset behavior unless a separate policy change is chosen.
Do not introduce signing/encryption or embed authorization policy in cursors;
filters/authorization must still be applied independently on every page.

### S5-05 — EncodeCursor silently loses named integer precision

**SDK defect; medium priority.** orderValueType (cursor.go:85) recognizes only
exact built-in numeric types. For `type Sequence int64`, encoding
Sequence(9007199254740993) succeeds without an order_type. Decoding follows the
legacy float fallback and yields float64(9007199254740992), with no error.
This defeats the codec's stated precision-preservation purpose and can move a
pagination boundary. Unsupported structs/maps likewise encode without a clear
supported-value contract.

**Fix:** define accepted cursor value types and either normalize supported named
scalar types deliberately or reject unsupported types during encoding. Prefer a
clear error plus explicit caller conversion over silently lossy fallback or a
large reflective codec. Retain precision for current tagged int/uint/float/time
values and account for legacy untagged tokens before changing decoding. This
needs boundary regressions beyond the current four round-trip cases.

### S5-06 — Strict parsing can return an invalid pagination strategy

**SDK defect; lower priority.** pagination.go:156 returns DefaultStrategy without
validating the completed request. ParseListRequest(DefaultStrategy:"sideways")
returns nil error; the returned request's Validate immediately fails. ParseListQuery
inherits this. Existing tests assert parsed requests validate but only use valid
defaults. SQL/Firestore stores reject the request later, limiting the impact.

**Fix:** validate the resolved result before returning it. Host configuration
validation should still catch unsupported defaults at startup; do not conflate
host misconfiguration with an end user's malformed input.

Related policy ambiguity: parsers use nonempty string values, not query-key
presence. `?cursor=` with an offset default stays in offset mode, despite docs
saying a cursor param selects cursor mode. Keep blank-as-absent if intended and
state that precisely, or add presence information at the query edge; do not add
new flags merely to make an imprecise comment true.

### S5-07 — Empty Turso offset results serialize differently

**Adapter defect; lower priority.** turso/list.go:273 accumulates a nil slice;
listOffset:192 returns it directly. Actual result: `{"items":null}`. Cursor mode
passes through TrimPage and returns []; Firestore allocates a slice and pgx
CollectRows does as well. MapPage masks the issue in many pocket consumers.

**Fix:** normalize successful query results once, and test empty wire results
under both modes. Keep directly constructed Page{} caller-owned semantics.

### S5-08 — Legacy InTx helpers leak their transaction on panic

**Adapter defect; high priority, separate from SDK Transact.**
pgxdb/tx.go:59 and turso/tx.go:96 call the callback without deferred cleanup.
They have substantial pocket/consumer use. The newer Transact implementations
already defer rollback; interface satisfaction does not protect the older entry
point.

A temporary Turso test recovered the callback panic and observed pool InUse=1;
only the probe's captured handle and explicit cleanup released it. pgx has the
same missing-cleanup path; actual PostgreSQL pool behavior was not exercised.
[pgx documents explicit transaction finalization](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool.Begin).

**Fix:** guarantee rollback/release on all abnormal exits while preserving the
panic and callback error identity/classification. Share a small lifecycle path
where appropriate; do not add workflow retries or silently change nesting.
Test panic, returned error, commit failure, and cleanup failure independently.

### S5-09 — Turso can return a still-open failed transaction to the pool

**Adapter defect; high priority, proven for its local SQLite fixture.**
turso/tx.go:43 issues COMMIT and closes the pinned sql.Conn even on failure.
Transact marks itself completed before this call, preventing its defer from
repairing the failure. Closing sql.Conn returns it to the pool; it does not
implicitly roll back manually issued BEGIN/COMMIT SQL.
[Go Conn.Close contract](https://pkg.go.dev/database/sql#Conn.Close).

The probe used the existing modernc SQLite helper with foreign_keys enabled and
a deferred FK violation. Transact returned `commit failed: invalid reference`;
the next pooled query saw the uncommitted row; the next Begin failed with
`cannot start a transaction within a transaction`. This is unresolved connection
state, not a successfully committed invalid row.

**Fix:** finish rollback or discard the unusable physical connection before pool
reuse, retaining the commit error and diagnostic cleanup cause. Test reuse after
both commit and rollback failures. Hosted libsql HTTP resets its stream before
reuse, so this result must not be generalized into a proven hosted-HTTP leak;
remote cleanup/failure behavior still needs adapter-specific verification.

### S5-10 — Transaction cancellation/retry guarantees need an honest shared contract

**Contract gap plus adapter inconsistency.** Turso Commit/Rollback use unbounded
context.Background (tx.go:44/54), whereas pgx uses the Begin context. In the
Turso probe, the callback inserted a row, canceled its context, and returned nil:
Transact returned nil and the row committed. The SDK currently says nothing about
cancellation, so this is not a violation of an explicit SDK cancellation promise.

Firestore already documents callback retries, reads-before-writes, and no reading
its own pending writes (firestore/transact.go:43 onward). The SDK Transactor doc
omits those portability limits and promises callback errors unwrapped even though
SQL cleanup failures wrap them and Firestore classifies raw vendor statuses.
[Firestore RunTransaction contract](https://docs.cloud.google.com/go/docs/reference/cloud.google.com/go/firestore/latest#cloud_google_com_go_firestore_Client_RunTransaction).

**Fix shared docs:** callback may run again; reset captured results per attempt;
perform external effects only after Transact succeeds; participating repositories
must use its callback context and the same datastore instance. Preserve domain
errors through errors.Is/As, documenting vendor classification. Describe nesting
refusal and connector-specific read/isolation restrictions. No no-op default,
automatic retry wrapper, or SDK untyped transaction stash.

**Follow-up implementation policy:** retain caller context for commit attempts;
use bounded cleanup independent of an already canceled context. A canceled request
cannot promise that a commit never happened after a race; do not invent that
stronger guarantee. Test these semantics by connector before promising parity.

Conditional ownership concern: ambient lookup keys distinguish connector types,
not DB instances, so dbB.QuerierFrom(ctxFromA) selects A's transaction. No consumer
incident was established. Document same-instance wiring now; consider a fail-fast
ownership check during the adapter audit. Authentication/CMS SQL stores commonly
start their own InTx rather than joining ambient transactions; authorization's
participating store methods are deliberately different. The generic port alone
does not make every repository composable in a host transaction.

## Simplification and naming recommendation

| Surface | Recommendation | Reason / cost |
|---|---|---|
| Reader, Writer, CRUD | Remove | No callers found; domain ports carry useful semantics these generic signatures omit. External hosts can declare the few methods they actually need. |
| Field, Some, Overlay | Remove | No active caller found; sampled owner explicitly retired the pattern. Keep host/domain-owned update semantics; no replacement package. |
| ErrNotFound alias | Remove | sdk.ErrNotFound is already the authoritative sentinel; six local source files and one test need spelling changes. |
| Page, mapping/bounded-list helpers | Keep | Widely used; preserve metadata and consistent empty JSON. MapPageErr has real fallible conversion users. |
| Limits, request parsing/validation | Keep; share private limit resolution | Real resource defaults/maxima and deliberate strict/clamping distinction. Do not merge caller intent and resource policy. |
| Cursor, offset, count, ordering, literal search | Keep and fix | All have concrete consumers. Preserve allow-lists, precision, counts, filters, and literal wildcard behavior. |
| Transactor | Keep in capabilities/transaction | Real independent capability; keep Transact signature and adapter ownership. No default is necessary. |
| Remaining read helpers | Prefer foundation/list | Accurately names listing/page/order/search work after removals. One focused foundation package, not a proliferation of pagination/order/search packages. |
| SQL query execution | Keep in integrations | Domain ports remain datastore-free; do not import drivers or a SQL builder into SDK. |

The naming recommendation is structural clarity, not a claimed runtime fix.
`list.Page`, `list.Request`, `list.ParseQuery` are clearer than a CRUD package
whose CRUD interfaces nobody uses. Avoid doubling the package word in new names
where practical; exact exported-name mapping belongs in the implementation plan.
`transaction.Transactor` can keep its existing method unchanged. Existing adapter
types continue to satisfy it structurally.

**Lower-cost alternative:** keep the crud path for now, remove unused APIs and
shorten the docs. This saves the broad import migration, but leaves unrelated
transaction/listing responsibilities under a misleading name. Because earlier
audit breaking changes already await a coordinated consumer upgrade, prefer doing
one justified package migration in that same upgrade rather than maintaining a
permanent alias package. No new Go modules or external dependencies are needed.

**Do not bundle:** replacing OrderField.Column/CastLower with a second logical
mapping vocabulary. Domain declarations expose storage names, a genuine coupling
tradeoff; current aliases correctly map through every adapter, and the existing
seam works. A second mapping layer needs demonstrated consumer benefit. Likewise,
retain NewOrder's small normalization and both parsing entry points unless a
specific simplification preserves their current callers and behavior.

## Documentation corrections

- crud.go's 145-line package introduction mixes current contract, future-tense
  claims, release history, and rules for absent consumers. Keep a short package
  purpose plus a compact mode/count contract and examples; history belongs in
  plans. tx.go similarly describes already-shipped helpers as future work.
- The claim that no resource declares limits is stale; sampled hosts and generated
  pocket templates already do. Replace it with current examples.
- foundation.md:127 names nonexistent SDK Tx; its ListParams example:133 sets a
  nonexistent Order field. Use ParseListQuery and a separate ParseOrder example,
  or a compile-checked public example. A green site build does not compile Go
  snippets.
- SDK search comments must say the PostgreSQL connector pins COLLATE "C";
  plain ILIKE is not globally ASCII-only. The actual connector already has the
  right expression, so no matching algorithm change is proposed.
- Shorten the SDK README's enormous crud inventory row into a concise purpose
  and links. Describe Field only as optional while it exists; never imply its
  null semantics bind every host domain.

## Proposed implementation sequence and compatibility

1. Correct SDK request/cursor contracts and shared previous-page behavior with
   deterministic regressions; update every affected helper consumer together.
2. Correct SQL outer predicate composition, transformed-PK ordering, and empty
   offset normalization. Verify public traversal behavior over SQLite and add
   live PostgreSQL/Firestore conformance legs where those services are available.
3. Correct transaction finalization/panic cleanup and settle cancellation policy
   with bounded cleanup. Keep this review's transaction findings distinct from
   the later full datastore audit.
4. Remove unused APIs, move listing and transaction vocabulary, update local
   consumers/templates/docs/guards, and write AUDIT-005 with exact migrations.
   Preserve well-formed cursor wire encoding, JSON page shape, filters, search
   semantics, and transaction method signatures unless a documented fix requires
   a behavior change. Older malformed cursors may be rejected; clients restart
   their listing instead of rewriting stored data.
5. Run full workspace/docs/scaffold gates and real list-navigation/transaction
   proofs. Coordinate new SDK/dependent releases later; no versions selected here.

The owner has authorized this review, not yet selected this newly discovered
implementation scope. None of these proposed breaking changes is in AUDIT.md.
After S5 fixes/selection, S6 foundation/web remains the next unreviewed SDK slice.

## Review-phase verification and handoff (historical)

**Passed existing suites:** SDK and each of integrations/datastores/{pgxdb,turso,
firestore}: `go build ./...`, `go test -count=1 ./...`, `go vet ./...`, from each
module directory with GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Root
`make guard` passed all 23 guards. SDK test loopback access was approved; Turso's
first run failed only on a sandbox bind denial in TestOpen_RetryAgainstUnreachable,
then its approved loopback rerun passed. This is resolved environment friction,
not a product test failure. No unresolved test/approval block remains.

Logs:

- /tmp/gopernicus-s5-sdk-test.log
- /tmp/gopernicus-s5-pgxdb-test.log
- /tmp/gopernicus-s5-turso-test.log; initial -sandbox.log companion
- /tmp/gopernicus-s5-firestore-test.log
- /tmp/gopernicus-s5-guard.log

**Independent behavior probes:** all commands completed successfully while
reproducing the defects above. Probe success means the observation was reproduced,
not that the product behavior is correct.

- From sdk: `go run /tmp/gopernicus-s5-probe.go`, same GOCACHE. Output:
  /tmp/gopernicus-s5-probe.log. Invalid strategy, blank query presence, malformed
  cursors, error classification, and named-int precision.
- From examples/minimal:
  `go test -overlay /tmp/gopernicus-s5-memstore-overlay.json -count=1 -v -run TestAuditS5MemstoreEdges ./internal/memstore`.
  Output: /tmp/gopernicus-s5-memstore-probe.log. Actual CMS repository and SDK web
  responder; one-row previous metadata and malformed-cursor HTTP 500. Recorder
  used; no browser or production HTTP server.
- From integrations/datastores/turso:
  `GOPROXY=off GOSUMDB=off go run /tmp/gopernicus-audit-s5-adapters/main.go`.
  Output: /tmp/gopernicus-s5-adapter-probe.log. Actual Turso list execution with
  in-memory SQLite, actual pgx SQL builders; OR/nested queries, folded PK,
  previous-page metadata, empty offset wire, cursor-to-driver failure.
- From integrations/datastores/turso:
  `go test -overlay=/tmp/gopernicus-s5-transactions.gRj938/overlay.json -run '^TestS5TxnProbe' -count=1 -timeout=30s -v .`,
  with the same GOCACHE; test names TestS5TxnProbeInTxPanic,
  TestS5TxnProbeCanceledCommit, TestS5TxnProbeFailedCommit. Output probe.log;
  existing modernc newMemDB fixture, explicit cleanup after each observation.
  No repository source or fixture changed.
- Usage scan: /tmp/gopernicus-s5-usage.go over framework plus the three reference
  roots, output /tmp/gopernicus-s5-usage.txt. AST findings cross-checked against
  actual domain/handler/store files and original framework references.

**Not exercised:** live PostgreSQL, hosted Turso, Firestore emulator/live project,
consumer builds, browser flows, 32-bit or race execution. Environment-gated live
suites skipped; Firestore integration/live build-tag suites were not rerun for
this review. No full workspace/docs build rerun: S4's passing gate remains the
last one, and S5 changes only review Markdown. This is a bounded cross-layer
review, not a completed datastore/pocket security audit.

**Changed files this slice:** plans/framework-audit-crud.md and
plans/framework-audit.md only. All original source changes, including CRUD's prior
S2 comment edit and concurrent Firestore work, remain untouched. Branch/base at
start and close: firestore-authentication / 6807ed06. Final git diff --check
passed. AUDIT.md/RELEASING.md are unchanged by S5. No commits/tags/consumer
upgrades/migrations. Next action: select S5 correctness and package cleanup scope,
then record an implementation plan before changing source.

## Implementation closure

All authorized fixes and recommendations are implemented. Read helpers are now
foundation/list and Transactor is capabilities/transaction; unused generic
repository/write helpers and the not-found alias are removed. The implementation
also fixes final-review float32 boundary precision and consistent projected SQL
ordering. See listing-transaction-cleanup.md for the 252-path inventory, passed
full workspace/docs/race/behavior checks and live-backend limits. AUDIT-005 is the
standalone consumer migration guide. Next unreviewed SDK slice: S6 foundation/web.
