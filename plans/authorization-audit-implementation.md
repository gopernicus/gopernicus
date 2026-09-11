# Authorization audit implementation

Current mutation contract: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md)
(AUDIT-026). The approved change removes request receipts, scope revisions and the
best-effort audit sink, adds optional atomic change history, and protects guarded
reads against supported raw writers. Earlier protocol descriptions below are
historical evidence, not the current API.

Status: COMPLETE — 2026-09-11. Owner authorized a pass through the current
[authorization plan](framework-audit-authorization.md) and
[listing plan](authorization-listing-design.md), followed by another review.
The owner explicitly agreed that the host's compiled RelationshipModel/RoleModel
governs every supported permission read, including previously stored tuples.

## Preconditions and scope

- Branch `firestore-authentication`, HEAD `6807ed06`, 42 modules; preserve the
  large pre-existing dirty tree, including completed authentication work.
  `/tmp/gopernicus-authorization-implementation-baseline.json` records all
  pre-phase working-tree hashes; `/tmp/gopernicus-authorization-implementation-AUDIT-before.md`
  preserves the existing migration ledger.
- Use named repository implementer, backend-review and verifier roles for bounded
  parallel slices, as required by the supplied AGENTS preferences. Current
  architecture/Makefile override obsolete paths and module counts in role files.
  Configured opus is unavailable; preserve role behavior with inherited models.
- Formatter `/Users/jrazmi/go/bin/goimports`; all Go commands use
  `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`. No root go.mod.
- Authentication/CMS/SDK feature changes, external consumer edits, publishing,
  live providers and production mutations are out of scope. Use new isolated
  loopback DB/emulator fixtures for destructive conformance; prior fixtures were
  removed. Preserve stdlib SDK, inward ports and separate store modules.

## Decisions and acceptance

1. The current immutable compiled model governs direct subjects, usersets and
   Through edges. Unsupported stored tuples contribute no authority. Check,
   Explain, batches, filtering, lookup and supported guarded permission checks
   agree. No automatic tuple deletion or model-version schema is assumed.
   Mixed-model deployment and custom-reader obligations go into AUDIT.
2. Preserve deterministic OR short-circuit/error behavior, input order/duplicates,
   root-relative Through depth, and explicit indeterminate limit errors. Bound
   repeated traversal as well as distinct states. Reuse only work safe for its
   depth/cycle context. Do not preserve multiple evaluators merely for unproved
   optimization, or replace one with a known inconsistent set evaluator.
3. Snapshot construction policy; reject invalid/typed-nil wiring at boot. Preserve
   valid punctuation, deterministic model errors, and documented guardian defaults.
   Memory cancellation before mutation consumes no operation ID; receipt replay
   never retains nonpersistent annotations. Constructor logger applies headlessly.
4. Keep baseline and guarded writers as explicit capabilities. This pass produces
   the dependency-ownership design and corrected docs for AZ-C5; a revision-aware
   baseline writer is a separate concurrency feature, not silently introduced.
   Complete role-owned guarded permission checks if they can use existing exact/
   global role dependency tracking without changing that ownership contract.
5. Distinguish complete bounded resource-ID sets from ID pages in names/types;
   remove deprecated ambiguous truncation vocabulary in the coordinated break.
   Candidate filtering preserves business order and resumes from the last consumed
   row. Distinguish scan exhaustion, no permission, unrestricted and errors.
6. Deliver an actual portable host listing example with tenant/search/composite
   sort, query-bound cursor and HTTP verification. Exercise complete-set and
   candidate strategies on the same data. Add an explicit constrained same-SQL
   predicate example and differential tests; reject unsupported models rather than
   implementing a partial permission policy. No general SQL compiler/SDK planner.
7. Measure repeated reads/roles/SQL expansion before adopting optimizations.
   Distinguish semantic states, returned rows and physical DB work; context
   deadlines do not become a claimed hard I/O bound. No cross-request cache or
   new automatic strategy selector. Preserve exact/global role semantics.

## Execution packets and ownership

- [x] A — AZ-C0/C1 and model-aware read contracts/adapters: C1/C2/C3/C10,
  evaluator parity/work bounds, maintained stale-tuple matrix, SQL expansion
  investigation from L4. Owner: credential agent in authorization implementer role.
- [x] B — AZ-C3/C5/C6: immutable guardians, memory cancellation/receipt parity,
  baseline/guard dependency design, role-owned guarded checks after A contract.
  Owner: pocket-review agent in implementer role, then independent review.
- [x] D — AZ-C2/C4: compile/boot model validation, typed-nil dependencies, logger
  wiring/optional transport and small constructor usability fixes. Owner:
  validation agent in implementer role, then independent review.
- [x] Root — L2/L3/L5/L6: explicit complete/page APIs, portable host example,
  measured filtering ergonomics and constrained SQL example. Coordinate shared
  root API files/methods with D; keep engine/store reads owned by A.
- [x] Final — cross-slice backend review, meaningful race/live/browser or HTTP
  checks, full 42-module gate and root guards, docs/build, migration AUDIT entry,
  fixture cleanup and exact phase inventory. Final user report distinguishes
  implemented fixes, design-only outcomes and unverified deployment behavior.

## Progress and verification

Implementation is complete. Original probes describe the old defects; maintained
regressions now verify their fixes. The final full-workspace gate passed.
Packet reports below retain exact commands, changed paths and scoped limitations.

- D implementation is in place: shared reference validation and collision-free
  cycle vertices, duplicate grantor refusal, gate boot validation, typed-nil
  constructor checks, and constructor-owned Service/SystemMutator logging.
  Focused compiler/constructor/logger/gate tests pass with `-race`, and the
  authorization core's `go build ./...` and `go vet ./...` pass. Exact commands,
  scoped verification limits and the independent reader review are recorded in
  `/tmp/gopernicus-authorization-D-report.md`.

- D follow-up L4: measure exact role reads for a 20-candidate batch (denied,
  sparse, direct and global grants), then reuse exact/global facts only within
  one CheckBatch. Keep one scope-resolution helper, sorted grantor OR order,
  exact-before-global errors/provenance and cancellation before cache hits.
  The internal role probe may expose the existing exact read; no new store
  method or guarded-view cache. Regressions must show changes between batches
  are visible and record the measured before/after read counts.
  Completed: denied/global reads 40→21, sparse 35→21, direct remains 20,
  repeated denied candidate 40→2. Full core `go test -race ./...`, build and vet
  pass. Report/inventory: `/tmp/gopernicus-authorization-L4-report.md` and
  `/tmp/gopernicus-authorization-L4-files.json`.

### Packet A implementation refinement (2026-09-11)

Root approved mandatory `Storer.ForModel(ReadModel)` and transaction-bound
`StoreDecisionView.ForModel(ReadModel)`. The immutable domain value stores only
allowed tuple shapes; zero denies all. Every expansion edge, final grant and
Through target uses that model. Raw store methods remain management/fact reads.
SQL binds the allowlist as data; no schema migration or model version column.

The inconsistent filter BFS has been replaced by ordinary root-relative DFS,
with call-local read memo and an explicit `MaxEvaluationSteps` ceiling for
repeated frames/rules/targets. Direct homogeneous batches retain grouped reads
and remove resolved candidates before subsequent branches. Enumeration candidates
are checked against ordinary per-root depth/work semantics before return.
Verification includes stale-tuple matrix, counted convergent graph work,
short-circuit failures, canceled reads, all adapters and guarded views.

### Packet B — mutation parity, guard ownership and roles

Implemented immutable guardian option snapshots in all four stores, memory
cancellation after lock/callbacks before writes, and nonpersistent receipt
annotation parity. Maintained regressions failed before and pass afterward.
Guarded CheckPermission now evaluates current RoleModel grantors through existing
transaction-bound exact/global role reads using a shared ordinary/guard evaluator;
removed the obsolete roles-owned refusal sentinel. Shared scope/decision, stale
model, replay, cancellation and scoped/global revoke race tests pass on memory
and isolated PostgreSQL named/libSQL fixtures. Firestore option ownership passes
hermetically; shared emulator role/replay/cancellation tests pass with race after
adding adapter-local cancellation checks around callbacks and before writes.

AZ-C5 design-only outcome is recorded in
[authorization-guard-dependency-design.md](authorization-guard-dependency-design.md),
including actual Segovia MoveSpace baseline topology dependencies and explicit
host consistency choices. No baseline revision-aware feature or consumer change
was introduced. Exact source list/evidence:
`/tmp/gopernicus-authorization-B-inventory.json` and
`/tmp/gopernicus-authorization-B-report.md`.

B additionally owns the shared `storetest/read_model.go` matrix requested by A/root.
Seven stored-model narrowing cases plus all-method zero-model/containment cases
prove reader, public decision/list and guarded-write parity, retain positive
authority and raw inspection, and preserve independent reader model snapshots.
Memory, named PostgreSQL, libSQL and Firestore emulator race conformance pass.
The shared ReadModel matrix passed in A's final emulator reader run (24.781s);
B's final mutation scope passed separately (21.448s). No production Firestore
index/lock claim is inferred. B fixtures and emulator slot are released to root.
See B report for exact logs and files.

A verification status: all four modules build and pass vet, including integration
tags. Full core race, PostgreSQL default/named, and libSQL race suites passed.
Firestore emulator reader/model/lookup race suite passed; its full suite exposed
only two mutation cancellation-classification failures delegated to B and fixed
there. Shared stale-model matrix is B-owned and exercised by the same adapters.
Canonical `(Type, ID, Relation)` Through target ordering now governs Check,
Explain, batch/filter and guarded checks, with reversed-row-order regressions.
Exact files, commands, measured costs, breaks and remaining deployment limits:
`/tmp/gopernicus-authorization-A-report.md` and
`/tmp/gopernicus-authorization-A-inventory.json`.


### Root — host listing and migration

Implemented `lookup.go` with separate complete `ResourceSet` and paged
`ResourceIDPage` APIs; removed old public lookup aliases and Truncated, migrated
repository consumers/fakes, and kept internal evaluator names where changing
them would add unrelated churn. `FilteredPage` now exposes resumable scan-limit
status without unsupported totals/previous fields. Explicit BatchSize and a
remaining-capacity default keep the pull policy visible to the host.

The normal host operation, actual endpoint, SQL and encrypted cursor codec are
in [the worked documents guide](../examples/auth-cms/internal/outbound/domains/documents/README.md).
Memory/PostgreSQL and both portable strategies share the domain port. The
constrained same-DB EXISTS validates the full selected direct-user policy at
construction; usersets, Through, role ownership and extra OR branches are refused.
SQL operational work limits are distinct from permission/data parity. No general
compiler, SDK query planner, automatic strategy selector or batch-function port
was needed by the working example.

HTTP tests verify composite name/ID ordering in both directions, Unicode,
tenant/search, empty/unrestricted/errors, sparse empty continuation, cursor
principal/query binding and privacy, 1,005-grant overflow versus complete paging,
and revocations/moves between pages. Persisted document values are validated so
their own encrypted cursors fit the request bound. The real example route uses
live identity middleware and places fallible construction before starting the
optional outbox worker. Logger ownership is migrated through the host constructor;
its real role-route 404 test now checks informational headless configuration.

Measured source pulls for a ten-row page (remaining capacity versus batch20):
dense 1/10 versus1/20 calls/candidates, half-permitted5/19 versus1/20, and
one-in-100 282/901 versus46/920. All returned the same ordered ten rows. The default
avoids dense overfetch; sparse hosts should explicitly size batches or choose
another strategy. No remote-latency or universal performance claim is inferred.

### Review and verification record

Every Go/make command below used
`GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`; formatter is
`/Users/jrazmi/go/bin/goimports`. Adapter fixtures were isolated and unrelated to
consumer databases. Commands are from the root unless a module is named.

| Scope | Command/evidence | Result |
| --- | --- | --- |
| Core | `go build ./...`, `go test -race ./...`, `go vet ./...` in pockets/authorization | PASS; final canonical-order and role changes included. A/D/L4 reports retain exact logs. |
| PostgreSQL store | `go test -tags=integration -race ./... -count=1` | PASS in default97.544s and named98.932s schemas; canonical-order/model/budget delta47.053s. Build/vet also pass. |
| libSQL store | same integration/race command | PASS34.734s; final delta15.613s. Build/vet also pass. |
| Firestore | hermetic build/vet/race; emulator integration/race | Final reader/model/lookup24.781s and guarded-role/replay/cancellation21.448s PASS. Full initial emulator run found only the two callback cancellation classifications corrected by B and rerun. |
| Listing host | `go test -race ./examples/auth-cms/internal/outbound/domains/documents ./examples/auth-cms/cmd/server -run 'Test(MemoryListing\|Listing\|PostgresListing\|SQLListing\|PostgresLarge\|CandidatePull\|Document)' -count=1 -v` with dedicated AUTHORIZATION_LISTING_TEST_DSN | PASS22.779s/1.402s, real HTTP and PostgreSQL; log `/tmp/gopernicus-authorization-root-listing-final.log`. Later logger migration is covered by the final workspace host suite. |
| Independent review | A readers/evaluator/lookup + D role memo source review; focused race and nil scoped-reader probes | PASS, no remaining runtime blocker; `/tmp/gopernicus-authorization-final-backend-review.md` and `-tests.log`. D separately reviewed host cursor/query/SQL and its two findings were fixed/tested. |
| Root architecture | `make guard` | PASS in the real git workspace; `/tmp/gopernicus-authorization-root-guards-final.log`. |
| Documentation | `make docs-build` (pnpm typecheck/build) | PASS; `/tmp/gopernicus-authorization-docs-build.log`. Nonfatal untracked-date/update-cache warnings remain. New relative links checked. |
| Full workspace | `make check` in a hash-recorded snapshot | PASS, all 42 modules, integration/live compile-only vet and generation hashes; `/tmp/gopernicus-authorization-make-check.log`. |

Full gate uses `/tmp/gopernicus-authorization-check-w2wiimye`, recorded in
`/tmp/gopernicus-authorization-check-snapshot.json`. Its no-.git branch checks
before/after generation hashes; root guards cover the real git-only G20 leg.
The first gate caught the new route's boot-order cancellation leak; fixed by
moving fallible setup earlier. The second caught a legacy expected-WARN host
assertion; migrated with Config.Logger and the preserved real HTTP404 checks.
Neither failure was waived or disabled. The final snapshot includes those fixes and passed. Source hashes matched the
working tree at completion; subsequent changes only finalize the audit records.

### Cleanup, scope and next review

All seven recorded disposable containers (three PostgreSQL/libSQL pairs and one
Firestore emulator) were stopped and confirmed removed by
`/tmp/gopernicus-authorization-cleanup.py`; all four fixture metadata files are
marked cleaned_up. HTTP fixture servers close at test completion. No persistent
consumer/production service or external application was modified.

Phase inventory is `/tmp/gopernicus-authorization-final-inventory.json`, computed
against the pre-phase2097-file hash baseline. It records exact added/modified/
deleted paths, including shared-file edits: 120 paths, 42 added and one removed.
All 107 changed Go files pass goimports. Current source/module bytes match the
passing snapshot; only final Markdown records differ. Prior AUDIT bytes are preserved as an
exact prefix. Changes are restricted to authorization, its worked auth-cms host,
plans, AUDIT and RELEASING; SDK/authentication/CMS/jobs/events source and integration
connectors are unchanged. Generated templates/UI assets are unchanged from the
pre-phase baseline. Root migration is [AUDIT-023](../AUDIT.md#audit-023-authorization-model-authority-evaluation-and-listing-apis).

No required implementation or verification remains open in this pass.
Next is the owner's requested independent in-depth pass. Start with public API
clarity and the dependency-ownership design, then exercise the listing choices
against actual host workloads. Revision-aware baseline writes, a general SQL
permission compiler, cross-store snapshots, remote latency, production Firestore
indexes/IAM/contention and the optional PostgreSQL non-C database leg remain
explicitly outside this implementation. No consumer update or release occurred.


Finding disposition: C1/C2/C3/C10 are implemented by model-scoped reads and the
shared bounded evaluator; C4/C6 by guardian/cancellation/replay fixes; C7/C8/C9 by
compiler/boot/logger validation. C5 is deliberately resolved at the approved
design/documentation level, with host dependency ownership still requiring an
application decision. L1–L6 are complete within the explicit constrained-SQL and
no-cross-request-cache scope above. The next review is not evidence of an
unfinished current implementation.
