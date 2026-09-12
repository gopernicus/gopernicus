# Gopernicus framework audit

Active follow-up: [firestore-release.md](firestore-release.md). The owner requested
the three first Firestore tags. The newer authentication branch is being merged
with the published audit contracts on `firestore-release-20260911`. All three
independent release candidates, emulator checks, the final workspace check and
review pass. The required real-Firestore workflow gate awaits test configuration.
No Firestore tag has been published. This plan owns the continuation state.

Latest completed follow-up: [startup-release-segovia.md](startup-release-segovia.md).
Startup cancellation/construction errors (AUDIT-032) and SQLite result completion
(AUDIT-033) are fixed. All 34 coordinated module tags are published from
`c3f8b4ad453021ba1e40c91572d8e4618c2d8382` on `audit-release-20260911`; public
checksums/builds and released-pin scaffold generation pass without replacements.
[AUDIT.md](../AUDIT.md) and the release manifest record exact published versions.

Segovia v2 is upgraded on local branch `chore/gopernicus-audit-upgrade`, commit
`9db497fd703601cf574bffc1c4371b9e70679cba`. The original checkout is on that branch, with
owner plans preserved. Its full check, 320 live race tests, fresh/baseline SQL
upgrade and real HTTP authentication/dashboard/SSE smoke pass. No consumer
publication or deployment was performed. The plan above owns exact evidence.

Next work is a separately scoped rollout: Segovia's shared production limiter
remains unwired and production correctly refuses to boot; verify provider posture
and plan the breaking SQL cutover. Firestore release work is tracked separately
above; its source branches remain preserved. CMS behavior remains deferred.
Do not repeat the completed audit or constructor phases.

Previous completed follow-up: [pocket-constructor-options.md](pocket-constructor-options.md),
AUDIT-031. Explicit required inputs and grouped policy options replaced pocket
construction bags. Current APIs and consumer usage are recorded in the later
completed adoption above.

Previous completed follow-up: [constructor-options.md](constructor-options.md),
AUDIT-030. Constructor-wide review and selective functional options are implemented,
with required inputs explicit and coherent Config records retained. Existing
options now configure private state; delivery's unused clock was removed.
The 42-module check, module/race/scaffold/docs checks and live disposable Redis
passed. Separate startup-context and constructor error-policy items are recorded
in that plan; CMS remains deferred.

Previous completed phase: [integration-sweep.md](integration-sweep.md), task-3.
All 14 integrations were reviewed; twelve findings covering configuration,
cancellation, ownership, errors, migrations and dependency guards are implemented.
The final 42-module make check, actual-repository guards, documentation checks,
adapter race suites and disposable PostgreSQL/Redis/local SQLite checks passed.
Migration guidance is AUDIT-029. Provider-specific live gaps remain explicit.
The requested SDK, non-CMS pockets and integration audit pass is complete.
Next: consumer adoption and host-specific live verification; CMS stays deferred.

Previous completed phase: [pocket-structure.md](pocket-structure.md). Public focused
logic services and public inbound/http adapters are implemented for authentication,
authorization, jobs and events; CMS remains deferred. Consumer migrations,
scaffold/guards, architecture docs and AUDIT-028 are complete. The 42-module
build/test/vet gate, core race suites, docs checks and HTTP/jobs behavior checks
passed. This replaces AUDIT-027's root-heavy layout.

Previous implementation: [authorization-audit-log-implementation.md](authorization-audit-log-implementation.md).
The owner approved removing scope revisions and the durable request receipt ledger,
and adding optional atomic authorization change history. Implementation and
verification are complete; consumer migration is AUDIT-026. Tuple composite
identity and metadata removal (AUDIT-025) are the preceding completed phase.

Previous completed phase: authorization second-review implementation, 2026-09-11, tracked in
[authorization-followup-implementation.md](authorization-followup-implementation.md).
The owner authorized the [independent review](authorization-deep-review.md)'s fixes
and API simplifications. Migration guidance is AUDIT-024, following AUDIT-023.
Verification and cleanup are recorded in that implementation plan.

Status: COMPLETE for the requested audit pass — 2026-09-11; CMS remains deferred.
The historical implementation record follows. S1 reviewed with documentation follow-ups completed;
S2 validation/utilities and S3 environment/logging implemented and verified.
S4 identity/ID/cryptography and host password policy implemented and verified.
S5 listing and transaction fixes/cleanup implemented and verified in
[listing-transaction-cleanup.md](listing-transaction-cleanup.md).
S6 web fixes and API cleanup are implemented and verified in
[web-cleanup.md](web-cleanup.md). S7 async/workers/work is implemented in
[workers-cleanup.md](workers-cleanup.md), following the revised
[framework-audit-work.md](framework-audit-work.md) design. Owner naming update:
[cryptids-name-and-sdk-shape.md](cryptids-name-and-sdk-shape.md) restores the crypto
namespace to cryptids. Approved root SDK consolidation is implemented in
[sdk-root-promotion.md](sdk-root-promotion.md). S8a cacher is implemented and verified in
[cacher-implementation.md](cacher-implementation.md), following the findings in
[framework-audit-cacher.md](framework-audit-cacher.md) and the owner-approved
[cacher-design.md](cacher-design.md). S8b ratelimiter is reviewed in
[framework-audit-ratelimiter.md](framework-audit-ratelimiter.md); implementation is in
[ratelimiter-implementation.md](ratelimiter-implementation.md) with final verification complete.
SDK events is reviewed in [framework-audit-events.md](framework-audit-events.md)
and implemented/verified in [events-implementation.md](events-implementation.md).
S9a filestorage is implemented and verified in
[filestorage-implementation.md](filestorage-implementation.md), following
[framework-audit-filestorage.md](framework-audit-filestorage.md).
S9b email/notify is reviewed in [framework-audit-email-notify.md](framework-audit-email-notify.md).
Email/notify implementation is in
[email-notify-implementation.md](email-notify-implementation.md), including the
owner's explicit per-call selection of one or several typed deliveries. All
41-module build/test/vet checks, 23 guards, focused race tests and docs-build
pass. Migration is recorded in AUDIT-014. S9c OAuth/tracing review is complete in
[framework-audit-oauth-tracing.md](framework-audit-oauth-tracing.md). Its approved fixes are implemented in
[oauth-tracing-implementation.md](oauth-tracing-implementation.md). S10 wiring/composition
is reviewed in [framework-audit-pocket.md](framework-audit-pocket.md), completing
the first SDK review pass. The shared contract has since moved into the independent
`pockets` module in [pockets-module-move.md](pockets-module-move.md) (AUDIT-016).
The requested `sdk/foundation` → `sdk/pkg` directory rename is implemented and
verified in [sdk-pkg-rename.md](sdk-pkg-rename.md) (AUDIT-017). The bounded jobs
runtime/scheduler snapshot, validation and optional-registration fixes are
implemented in [jobs-runtime-implementation.md](jobs-runtime-implementation.md)
(AUDIT-018). Continuing jobs, SQL generation ordering is implemented and
live-verified in [jobs-persistence-audit.md](jobs-persistence-audit.md) (AUDIT-019).
The remaining jobs and events pocket audits are now implemented and verified in
[jobs-events-audit-implementation.md](jobs-events-audit-implementation.md), with
migration entries AUDIT-020/021. Central coverage:
[framework-audit-jobs.md](framework-audit-jobs.md) and
[framework-audit-events-pocket.md](framework-audit-events-pocket.md).
The deep authentication/authorization audit is complete in
[authentication-authorization-audit.md](authentication-authorization-audit.md).
The approved authentication implementation is complete and verified in
[authentication-audit-implementation.md](authentication-audit-implementation.md).
The [authentication findings](framework-audit-authentication.md) are implemented,
with migration in AUDIT-022. The first authorization implementation and its three
listing scenarios are complete and verified in
[authorization-audit-implementation.md](authorization-audit-implementation.md),
with migration in AUDIT-023. The independent second review's implemented follow-ups
are recorded in [authorization-followup-implementation.md](authorization-followup-implementation.md)
and AUDIT-024.

## Context

Gopernicus packages recurring web application work as a stdlib-only SDK,
opt-in pockets, and replaceable integrations. This audit asks whether the
current implementation is correct, understandable to people and coding agents,
and simpler than its alternatives. Existing architecture documents explain
intent; they and their detailed rules are also reviewable evidence, not proof
that every present abstraction is necessary.

This file is the shared audit brief, progress index, and session handoff.
Use tracked `plans/` for durable context; `.claude/plans/` is ignored and contains
local drafts alongside plans whose executed copies live in `plans/`.

The owner also wants a standalone consumer migration guide in root
[`AUDIT.md`](../AUDIT.md). Update it whenever audit implementation introduces a
breaking change. Each entry must stand alone when copied into a consumer app's
context: affected module/release status, old/new behavior, migration examples,
known callers, and verification. Record implemented changes there; keep proposals
in these audit plans. `RELEASING.md` links to the corresponding migration entry.

## Goal

Establish evidence-backed findings and agree on small, independently reviewable
changes that improve correctness and clarity without losing useful modularity.

## Shared brief and decisions

- Preserve the stated essentials: SDK imports only stdlib/SDK; pockets are
  opt-in; capability interfaces point inward; hosts choose and wire adapters.
- Prefer explicit control flow, clear names, and visible dependencies. More
  lines can be simpler; fewer types or files are not automatically better.
- An abstraction earns its place through real shared behavior, a meaningful
  boundary, or a useful consumer seam. Trace actual callers before proposing
  deletion; examples do not represent every external consumer.
- An explicitly intended extension point may precede production adoption.
  Absence of callers alone does not justify removing it. Describe the intended
  use and prove a small example; distinguish that seam from overlapping or
  speculative convenience APIs. The owner's S7 examples are custom queues using
  generic SDK runners and composable worker/job-processing middleware.
- Package existence, boundaries, and names are review questions too. The owner
  explicitly asks whether conversion/slug need separate packages or belong in
  SDK at all, and whether names such as CRUD accurately describe their scope.
  Use the current inventory to organize discovery, not to freeze the structure.
- Separate confirmed defects, documentation drift, design tradeoffs, and missing
  features. Incomplete CMS scope is not itself a correctness defect.
- Compare each proposed simplification with keeping the current design. Name
  the benefit, behavior preserved or changed, compatibility cost, and evidence.
  Keep complexity that protects a demonstrated invariant.
- Breaking API changes may be recommended with a good reason. Explain the
  concrete improvement, affected consumers, and migration cost; a cleaner-looking
  signature alone is not enough. Preserve persisted behavior unless its change
  is part of a justified finding.
- Detailed architecture rules are open to challenge, including SDK import
  restrictions and package placement. Judge them against the user's essentials
  and actual behavior; historical rulings do not settle the audit by themselves.
- Review and implementation remain distinct. Record evidence and proposed
  changes before fixing or restructuring code; this is not a blanket rewrite.

Start a new context with this file, the applicable AGENTS instructions, and the
current slice's code/tests. Consult `ARCHITECTURE.md`, `sdk/README.md`,
`pockets/README.md`, and package docs for relevant contracts; avoid repeatedly
loading historical plans. Record accepted decisions below with their rationale
and links; update canonical docs when a decision is implemented.

Owner decisions (2026-09-09): breaking changes are acceptable with a good reason;
detailed architectural rules may be questioned freely; representative consumers
are Segovia v2, coordination-hub, and gps-360-go. The original Gopernicus is a
source of useful comparisons. The owner prefers the present foundation; the
original is not a target architecture to restore.

Owner clarification (2026-09-10, S7): retain generic SDK workers/runners and
small store contracts so consumers can bring their own jobs/work without adopting
the jobs pocket. Retain middleware as an intentional extension point, including
the intended ability to gate execution. Specify polling versus job-processing
middleware and explicit deferral/rejection semantics; an unused extension seam
is not automatically deletion material. Simplify unused requirements and
overlapping mechanisms while preserving that goal. This supersedes the initial
S7 proposal to move all runners into jobs and remove worker middleware.

Owner clarification (2026-09-10, S8a): incomplete APIs may be intended features
that have not been ported yet. Compare original functionality and framework use
cases before recommending deletion. The cacher comparison supports completing
Cache with typed data and load-on-miss helpers; concrete behavior earns the
service, and lack of current adoption alone does not disqualify the design.
[cacher-design.md](cacher-design.md) records the revised proposal and boundaries.

Accepted validation decision (2026-09-09): one root `sdk.ValidationError`
collector for DTOs and domains, with optional `*sdk.Violation` helper results.
Remove the duplicate collectors and generic password policy; fix Unicode
length, optional pointer behavior, and pointer JSON validation. The owner
authorized implementation; rationale, API migration, and verification live in
[validation-consolidation.md](validation-consolidation.md).

Accepted utility decisions: S2 removed conversion while preserving pointer reads
and slug behavior. On 2026-09-10 the owner approved moving those primitives,
ID generation and identity vocabulary into root SDK with explicit names. Their
former packages are removed; validation/environment/cryptids retain coherent
namespaces. See [sdk-root-promotion.md](sdk-root-promotion.md) and AUDIT-009 for
final APIs; [utility-package-cleanup.md](utility-package-cleanup.md) preserves the
original S2 evidence. Root admission now includes common vocabulary and small
clearly named primitives, without requiring two existing foundation consumers.

Accepted environment/logging decision (2026-09-09): retain both packages;
fix dotenv and tagged parsing, return value-free diagnostics, clone log records,
and include available context IDs automatically. Remove logging's one-boolean
functional-option layer and redundant constructors/lookups; rename its handler
ContextHandler. Preserve existing default/empty/required and grouping policies.
Examples/scaffold check loading and use explicit configured logger ownership.
Implemented and verified in [environment-logging-cleanup.md](environment-logging-cleanup.md);
consumer migration: AUDIT-003.

Local reference paths (read-only for this audit):

- Segovia v2: `/Users/jrazmi/code/segovia/segovia/v2`.
- Coordination Hub: `/Users/jrazmi/code/gps/coordination-hub`.
- GPS 360: `/Users/jrazmi/code/gps/three-sixty/gps-360-go`.
- Original: `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original`.

Check each consumer's instructions, current module pins, and relevant call sites
before drawing conclusions. Exclude vendored dependencies from usage searches.

## Out of scope

- Completing unfinished pockets, adding new integrations, or redesigning host
  applications merely to conform to a preferred pattern.
- A standalone audit of `ui/goth`, scaffolding, or every example; inspect those
  consumers where needed to validate framework behavior and usability.
- Releases, deployment, production mutations, and changes to active user work.

## Risks

- A simplification can weaken cancellation, concurrency, security, transaction,
  or adapter-parity guarantees. Trace those guarantees through real behavior.
- Green suites may share a mistaken assumption with their implementations or
  silently skip datastore tests. Record independent evidence and every skip.
- Repository docs and module inventories have drifted. Read current source and
  `go.work`/`Makefile`; do not infer current behavior from historical rulings.

## Tasks

### task-1: Establish the baseline and audit the SDK in bounded slices

- **depends_on:** []
- **files:** `sdk/`, `ARCHITECTURE.md`, `sdk/README.md`, `Makefile`, `go.work`,
  `plans/framework-audit.md`; inspect named consumers as each slice requires.
- **verify:** After environment preflight, run `go build ./...`, `go test ./...`,
  and `go vet ./...` from `sdk/`; run `make guard` from the repository root.
- **description:** Confirm the intended contract from a caller's perspective,
  trace its implementation and failure paths, then assess tests and complexity.
  Start with S1 to calibrate the review together; take larger slices across
  multiple contexts when needed. Do not queue mechanical cleanup before judging
  whether the public behavior and abstraction should exist in their current form.

The inventory below comes from the current source tree, not the old planner
module map. Every listed area includes its tests, examples, and exported
conformance helpers where present.

| Slice | SDK paths | Initial focus | Status |
|---|---|---|---|
| S1 | root `errors.go`, `context.go`, `faults.go` | Error/context vocabulary, semantics, overlaps | [Reviewed; follow-ups complete](framework-audit-sdk-root.md) |
| S2 | root `pointer.go`, `slug.go`; `pkg/validation` | Predictable utilities; surprising inputs; utility value | [Validation S2a implemented](framework-audit-validation.md); [utility S2b implemented](framework-audit-conversion-slug.md) |
| S3 | `pkg/{environment,logging}` | Defaults, parsing, process state, secret/log handling | [Implemented and verified](framework-audit-environment-logging.md) |
| S4 | root `identity.go`, `id.go`; `pkg/cryptids` | Identity and cryptographic contracts, safe defaults | [Implemented and verified](identity-cryptography-cleanup.md) |
| S5 | `pkg/list`, `capabilities/transaction` | Generic API cost, pagination, ordering/search, writes and transactions | [Implemented and verified](listing-transaction-cleanup.md) |
| S6 | `pkg/web` | Request/response flow, errors, middleware, lifecycle, streaming and rendering | [Implemented and verified](web-cleanup.md) |
| S7 | `pkg/{async,workers}`, `capabilities/work` | Ownership, shutdown, retries, fencing, protocol versus runtime | [Implemented](workers-cleanup.md) |
| S8 | `capabilities/{cacher,ratelimiter,events}` | Concurrent defaults, expiry, delivery and outage semantics | [Cacher implemented](cacher-implementation.md); [ratelimiter implemented and verified](ratelimiter-implementation.md); [events implemented and verified](events-implementation.md) |
| S9 | `capabilities/{filestorage,notify,oauth,tracing}` | Narrow replaceable contracts, policy and wrapper value | [Filestorage implemented and verified](filestorage-implementation.md). [Email/notify implemented and verified](email-notify-implementation.md). [OAuth/tracing implemented and verified](oauth-tracing-implementation.md) |
| S10 | former `sdk/pocket`, now shared `pockets` | Minimal host wiring, route mounting and composition | [Reviewed; remaining ownership fixes proposed](framework-audit-pocket.md); [module move implemented](pockets-module-move.md); [jobs ownership fixes implemented](jobs-runtime-implementation.md) |

S6 and S9 are groupings, not a requirement to finish all their concerns in one
session. Record a narrower boundary in the handoff before starting.

### task-2: Audit one pocket and its exercised adapters at a time

- **depends_on:** [task-1]
- **files:** `pockets/events/`, `pockets/jobs/`, `pockets/authentication/`,
  `pockets/authorization/`, `pockets/cms/` in separate slices; their `stores/`,
  `views/`, relevant `integrations/`, and named consumer paths per slice.
- **verify:** Build/test/vet each touched module; exercise a real use case with
  the relevant example or representative consumer. Run datastore conformance
  against disposable services, and browser checks for HTTP/HTML behavior.
- **description:** Trace construction → use case → domain rule → port → adapter
  → observable result, including errors and disabled subsystems. Review the
  service, HTTP surface, tests, documentation, and optionality as one experience.
  Follow integration issues exposed by that path and record exactly which adapter
  behaviors were covered so they are not needlessly re-audited later.

Provisional order: events → jobs → authentication → authorization → CMS, adjustable
for consumer pain or risk. Jobs/events and their exercised adapters are now reviewed
and fixed; authentication and both authorization implementation passes are also
complete and verified (AUDIT-022/023/024). CMS remains deferred.

The central jobs checklist is [framework-audit-jobs.md](framework-audit-jobs.md).
It consolidates completed runtime fixes, known carry-forward findings and the
remaining queue/fenced/scheduler/store/host-ergonomics review. Keep its statuses
current as the full jobs audit progresses; individual implementation plans own
their detailed evidence and execution records.

Owner-authorized implementation followed each completed review. Current records
are [authentication-audit-implementation.md](authentication-audit-implementation.md)
and [authorization-followup-implementation.md](authorization-followup-implementation.md).
No auth implementation restriction from the earlier planning phase remains active.

Authorization's three listing recipes are implemented and documented in
[authorization-listing-design.md](authorization-listing-design.md): different
stores, the same SQL database without joins, and the same SQL database with joins.
The host owns business filtering/order and cursor binding. LookupAllResourceIDs
returns a complete bounded ResourceSet; LookupResourceIDPage returns a lexical
ID page; FilterPage filters host-ordered candidates. Keep these distinct and in
the authorization pocket. A future SDK extraction needs an independent consumer
and a simpler contract. The baseline-writer decision is resolved by AUDIT-025/026:
tuple-based writes with optional atomic change history, without revisions or
request receipts. No generic query planner was introduced.
CMS feature work remains deferred.

### task-3: Close remaining integration and cross-layer gaps

- **status:** COMPLETE — [integration-sweep.md](integration-sweep.md), AUDIT-029.
- **depends_on:** [task-2]
- **files:** Remaining modules under `integrations/`; affected SDK/pocket
  contracts and representative examples; `plans/framework-audit.md`.
- **verify:** Per-module build/test/vet, applicable conformance and live adapter
  checks, then `make check` at a cross-module milestone after preflight.
- **description:** Review uncovered adapter behavior, vendor error mapping,
  resource ownership, cancellation, and optional capability claims. Validate
  swaps against observable contracts, including missing-data behavior, ordering,
  and limits that cannot be made equivalent; interface satisfaction alone is
  insufficient. Close the audit with accepted findings, deferred items, and
  verification gaps rather than declaring all integrations interchangeable.

S4 reviewed `cryptids/{bcrypt,golang-jwt,google-uuid}` wrapper code/tests and
observable SDK compatibility; see its findings and limits. This does not close
authentication's end-to-end flows or constitute a dependency security audit.
The final integration sweep covered `datastores/{firestore,pgxdb,turso}` and
`kvstores/goredis` beyond the already audited capability seams, plus
`scheduling/robfig-cron`. Email/SendGrid and GCS/S3 filestorage are implemented and
verified in S9a/b; the notify/mailer bridge was removed. OAuth GitHub/Google and
tracing/otel are implemented and verified in S9c and reviewed again in the sweep.
The separate pocket/store audits are complete except CMS. This does not constitute
a dependency security audit or full production-provider verification.

## Review record

Use one compact record per completed slice or substantial finding here. If a
slice outgrows this file, link a focused record under existing `plans/` and retain
only its status and accepted decisions here.

```text
Slice / status / reviewed commit:
Intended behavior and representative consumer:
Evidence: paths/symbols, traced cases, observed results; unknowns:
Finding and consequence: defect | doc drift | design tradeoff | missing feature:
Recommendation: keep | simplify | fix | defer; alternative and reason:
Compatibility: callers, API, wire/persisted data, migration/release impact:
Verification: exact commands and behavior exercised; passed/skipped/blocked:
Decision / changed files / next step:
```

Seed documentation finding resolved: `sdk/README.md` now agrees with
`ARCHITECTURE.md` and `Makefile` that G12 includes tests.

[S1 root review](framework-audit-sdk-root.md): keep the current root APIs; no
runtime defect established in that slice. Misleading `IsExpected` documentation
was corrected during S6 without changing domain classification. Validation collector consolidation and its contract-comment fixes
were authorized and implemented during S2a.

[S2a validation review](framework-audit-validation.md): reproduced pointer-form
`DecodeJSON` skipping validation, byte-based length checks presented as character
checks, and minimum-length pointer/scalar empty-string disagreement. These are
fixed with regressions. One `sdk.ValidationError` now collects typed helper
results; duplicate collectors and the SDK's fixed password policy are removed.
The migration note covers breaking API and acceptance changes. S6's broader
web scope is covered in the S6 review below.

[S2b conversion/slug review](framework-audit-conversion-slug.md): reproduced
invalid UTF-8 and lost word boundaries in case conversion, JSON object-guarantee
violations, and CMS edit-time slug regeneration omitted by compatibility docs.
No conversion imports were found in the framework or sampled apps, but nil
pointer readers repeat across all three apps and CMS/Segovia demonstrate slug
needs. The owner selected and implemented keeping slug output and moving
`Deref`/`DerefOr` into a small `pointer` package; conversion's other APIs are
removed, with `Ptr` replaced by Go 1.26's `new(value)`. Casing/JSON defective
APIs were removed and the slug compatibility documentation corrected. Carry CMS
URL lifecycle, derived route-base validation, and display-label casing into
its later review. Full workspace/docs checks and the migration probe passed;
AUDIT-002 records the breaking changes.

[S3 environment/logging review](framework-audit-environment-logging.md):
fixed quoted dotenv/comment corruption, swallowed writes, named-string-slice
reflection panic, destination-width overflow, and value-bearing diagnostics.
Unsupported tagged types now fail even when absent. Logging clones records
and includes available context IDs automatically, closing the default host's
request-correlation gap. The selected duplicate helpers and functional options
are removed; ContextHandler names the remaining wrapper. Examples/scaffold
check dotenv errors and report returned startup errors with their configured
logger. All targeted/race, SDK, workspace, docs, generated-host, and HTTP/migration
checks passed. AUDIT-003 documents the breaking changes; S3 is complete.

[S4 identity/cryptids review](framework-audit-identity-cryptids.md): implemented
one JWT integration behind the SDK port, strict claims/time/algorithm/key checks,
bcrypt's symmetric byte limit, valid ASCII ID alphabets, and complete-principal
context lookup. Separated ID generation from crypto (the final crypto name is
cryptids after the owner's 2026-09-10 naming correction); removed unused ResolveAll,
SDK HS256, and the stateless hasher object. AES now uses stdlib random-nonce
handling with the same envelope; ID shape and stored digests remain unchanged.
The owner clarified host password policy ownership: Config.ValidatePassword
replaces pocket defaults when supplied; hashing/cost and breach checks remain
independent. Real HTTP registration/token/protected-route checks, legacy-format
regressions, full workspace/docs gates, and scoped jobs race tests passed. The
gate also exposed a pre-existing jobs memstore ordering defect, now corrected
with deterministic regressions. Delivery eligibility, token profiles, and durable
jobs ordering carry forward. Implementation: [identity-cryptography-cleanup.md](identity-cryptography-cleanup.md);
consumer migration: AUDIT-004.

[S5 CRUD review](framework-audit-crud.md) led to the authorized
[listing/transaction implementation](listing-transaction-cleanup.md). Read helpers
now live in foundation/list; Transactor lives in capabilities/transaction.
Unused generic repositories, sparse-write helpers and ErrNotFound alias are
removed. Corrections cover SQL projection/filter composition, folded PK ordering,
inclusive previous probes, malformed/type/precision cursor handling, strategy
validation, empty offset pages and SQL transaction cleanup. Final review also
reproduced a float32 boundary loop and transformed-projection ordering mismatch;
both are included. Consumer instructions are standalone AUDIT-005.

[S6 web review](framework-audit-web.md) led to the authorized
[web implementation](web-cleanup.md). W1–W13 are resolved: owned middleware
slices, combined proxy fields, shared status/error/flush/hijack handling,
failed-render cache refusal, correct abort/recovery and timeout shutdown,
complete SSE framing, strict trailing/body-limit classification and predictable
static/SPA behavior. JSON response failures reach the logger. Removed dead
router logging options, Decode alias, duplicate streaming, generic responder
conveniences and the unadopted reflection OpenAPI builder. ReadBody remains with
its presence contract and corrected EOF check. Decoder review found real null
policy differences, so no third configurable reader API was added. Authentication's
older reader was replaced at twelve calls with the existing bounded strict reader;
new trailing400/oversize413 behavior is covered by registration no-mutation tests.
SDK/pocket tests, full 42-module gate, scoped race checks, docs build and real HTTP
migration/composition proof pass. AUDIT-006 records standalone consumer changes.
S1 IsExpected documentation is now corrected; no source semantics changed there.

[S7 async/workers/work](workers-cleanup.md) is implemented. Generic SDK runners
and worker middleware remain intentional extension points; job middleware wraps
error-only processing with explicit defer/reject outcomes. Async admission/drain,
Pool fatal/error/timer handling and runner persistence reporting are corrected.
Jobs memory/pgx/Turso support optional atomic deferral, and fatal queue/scheduler
errors cancel the sibling. Work conformance covers concurrency, payload ownership
and real Service lifecycle; empty consumer-protocol keys are rejected. AUDIT-007
records breaking API/behavior changes. The implementation record owns final gates.

## Verification and implementation discipline

- Preflight branch/dirty files, Go version, module path, service endpoints, and
  generated-file state. `go.work` requests Go 1.26.1; there is no root module,
  so `go ... ./...` belongs inside the relevant module.
- Read the tests as specifications to challenge, not proof of intended behavior.
  Add focused regressions for demonstrated defects; use race checks for changed
  concurrent behavior and other targeted checks only where justified.
- For approved code work, use the documented `goimports` formatter on changed Go
  files and keep diffs surgical. Never hand-edit generated `*_templ.go`.
- `make check` regenerates templates, warms a module cache, checks every module,
  and runs guards. Use it for cross-module completion after checking prerequisites;
  it is broader than an SDK-only baseline and does not prove live store behavior.
- Follow `Makefile`'s datastore targets/build tags and each module's instructions.
  Identify unavailable services and skipped suites explicitly. Use disposable
  test data; never treat production credentials as a test setup.
- When an implementation changes a published contract, inspect `RELEASING.md`
  and consumers for migration/version impact; release execution is separate.

## Open questions

S5 fixes and cleanup are authorized and implemented; verification is recorded in
[listing-transaction-cleanup.md](listing-transaction-cleanup.md). No package-choice
decision remains open for S2–S5. The owner's permission to question package
existence, names and abstractions remains active for later slices.

S6 choices are implemented: retain ReadBody, remove the unused OpenAPI builder,
keep generic DecodeJSON's permissive fields and host-selected limits, and keep
strict pocket decoding separate. No S6 implementation decision remains open.
A future schema generator needs a real consumer and separate opt-in scope.

S7's revised design is implemented: keep generic SDK runner/store interfaces and
worker middleware; add JobMiddleware with explicit gate outcomes and optional
atomic deferral ports. Remove overlapping hooks, within-claim retry, attempt-only
fenced retry, async presets/shutdown options and the lossy Errors stream. Current
GPS360 worker code was reviewed after the owner refreshed main; it requires no
reversal of those removals. Full jobs-pocket and consumer upgrades remain separate.

The 2026-09-10 owner interlude restored cryptids ("cryptography tidbits") and
promoted pointer/slug/ID/identity to root SDK. Both are implemented, with final
migration guidance in AUDIT-008/AUDIT-009 and revised earlier entries. Validation
and environment keep their namespaces. No package-shape decision remains open;
[sdk-root-promotion.md](sdk-root-promotion.md) owns verification and the inventory.

S8a cacher is implemented in [cacher-implementation.md](cacher-implementation.md).
The original/framework comparison justified completing Cache with namespaced raw
operations, an error hook and typed JSON/load-on-miss helpers. Memory is bounded
with copied bytes and a simple LRU; PrefixDeleter is optional and literal; resource
lifecycle belongs to the host. Redis namespace/TTL/cancellation defects are fixed.
Pages has bounded versioned records, host/scope isolation and conservative public
HTML/header policy; CMS exposes PageCache configuration. AUDIT-010 is the consumer
migration. The minimal host demonstrates data caching at GET /catalog.json.
Full workspace/docs, race, live Redis and running-host checks pass. No implementation
decision remains open in this slice. Strong publication invalidation, concurrency
coalescing and original auth's broader invalidation needs remain separate work.

S8b ratelimiter implementation follows the completed review in
[framework-audit-ratelimiter.md](framework-audit-ratelimiter.md). The approved
contract and results live in [ratelimiter-implementation.md](ratelimiter-implementation.md).
Keep Allower admission, Limiter reset, HTTP middleware and generic worker Acquire.
Memory, Redis and Postgres share anchored millisecond two-window counters with
Burst, validated bounds, retained quota on ceiling changes and immutable live
windows. Memory is bounded and never evicts active budgets. SDK HTTP defaults
closed with an error hook; authentication explicitly retains and logs selected
open paths. Remove no-op Close, the resolver/wrapper/subject DTOs and unused defaults;
host policy remains fallible and is demonstrated by a runnable minimal-host example.

Postgres requires the host-owned window_ms migration; both adapters use v2 keys.
Require disjoint physical namespaces for old/new writers. Preexisting incompatible
records are preserved, including Reset. Every PG quota decision uses the post-lock
clock; missing keys initialize without quota then repeat once. No backend-error
retry. AUDIT-011 contains standalone API/behavior/schema/rollout migration.
SDK/auth/minimal race, the expanded live Redis/PG suites (including post-lock and
rolled-back-insert timing), full workspace/docs and running-host checks passed.
All disposable services stopped. No limiter product or verification failure remains.
A separate pgxdb pool-lifetime-default issue exposed by test setup is recorded in
the implementation plan for the later connector audit.
S8c events is implemented in [events-implementation.md](events-implementation.md)
following [framework-audit-events.md](framework-audit-events.md). Generic SDK
notifications, Memory/Noop, subscriptions, envelopes and the tiny wake bridge stay.
Ambiguous WithSync is replaced by checked local Memory.Dispatch and remote
Redis.Publish; Poller takes the host's chosen delivery function. Emit is bounded
asynchronous admission; full/closed/canceled work cannot silently report acceptance.
One Record/RemoteEvent envelope preserves stable IDs, opaque payloads and metadata.
Memory drains consistently, isolates each callback panic and releases subscriptions.

Redis Subscribe now means notification fanout; explicit exact-topic SubscribeWork
owns competing group processing. Bounded workers reclaim pending entries with one
whole-attempt deadline, retained scan cursors and ACK only after every selected
handler succeeds. Errors, panic, cancellation, malformed input and missing handlers
remain pending. MaxLen and BatchSize are removed; hosts own poison disposition and
retention. Versioned v2 streams/broadcasts require the coordinated cutover documented
in AUDIT-012; existing outbox records need no schema migration.

SDK/pocket race tests, actual HTTP/SSE, real outbox→Dispatch→jobs.Service composition
with memory stores, expanded live Redis race count 3, workspace build/test/vet/23
guards and docs-build passed. Final setup failure/retry, setup/Close, unsubscribe
recovery and active-handler shutdown tests passed. Owned Redis stopped. External
consumer upgrades, production cutover, live SQL/cloud stores and full pocket review
remain separate.

S9a filestorage is implemented in
[filestorage-implementation.md](filestorage-implementation.md), following the
owner-approved [review](framework-audit-filestorage.md). Keep the package, seven
core operations, streaming io.Reader input, deliberate overwrite and optional
signed-read/session intent. Remove FileStore/New/Option/WithLogger, operation-only
and unsupported sentinels; consumers use adapters/narrow ports directly and own
logging/resources. ErrObjectNotFound/ErrInvalidPath compose with root SDK kinds.

Canonical keys are validated without rewriting, listing uses literal prefixes,
and stored-byte ranges share explicit zero/EOF behavior. Disk uses os.Root,
private atomic staging and concrete Close; it rejects symlinks/non-regular objects,
reserves its top-level .gopernicus-tmp case-insensitively, and publishes 0600 files.
GCS aborts source failures, retains configured authenticated HTTP for direct JSON
session initiation, forwards host Origin and uses explicit context-aware IAM/local
signing. S3 uses bounded vendor uploads, preserves explicit bucket errors, corrects
multipart naming and handles MinIO dot-prefix queries. All preserve simultaneous
source/cancellation causes. Signed expiry is whole seconds from 1s through 7d.

Full 42-module build/test/vet/23 guards and docs-build passed; SDK/provider race,
real Disk HTTP200/404/400, expanded MinIO multipart/failure cleanup and full GCS
emulator storage/session PUT checks passed. Named platform source review corrected
staging aliases/panic cleanup; final provider review is recorded in the plan.
AUDIT-013 owns standalone consumer migration. No external consumers, deployed
keys, versions/tags or generated artifacts were changed. S3 adds only compatible
transfermanager v0.1.6; GCS promotes an existing auth pin for tests. Real cloud IAM,
AWS/GCS production behavior and browser CORS remain unverified. Later CMS review
owns blob/metadata reconciliation and the intended total 32 MiB upload ceiling.
S9b email/notify is implemented in
[email-notify-implementation.md](email-notify-implementation.md), following the
[review](framework-audit-email-notify.md) and the owner's explicit per-call selection
requirement. notify.Send takes prepared Delivery values; email.NewDelivery preserves
rich email and DeliveryFunc adapts host channel calls. An outage can select email
and Slack; a reset selects only email. Indexed partial failures preserve causes,
ordinary failure permits later attempts, and caller cancellation records skipped
positions. Hosts retain provider configuration, eligibility, queues and retry policy.

Email moved under notify/email and the bridge module was removed. Shared posture
lives in notify. Authentication now has copied Config.BodySenders keyed by non-email
kind, and one rich Mailer route; queued envelope schemas/checkpoint rules remain.
SendGrid no longer shares mutable requests or follows redirects, and provider
response bodies no longer become error strings. SMTP honors cancellation, bounds
attempts and MIME-encodes messages; stricter validation closes the CMS header path.

Renderer is render-only and immutable after construction. Explicit render requests,
separate HTML/text engines, deliberate .txt content, exact executable template roots,
strict configuration and branding snapshots replace silent fallback/mutation paths.
Final named backend review findings were fixed and confirmed. Platform review fixes
and owned SMTP/HTTP/concurrency regressions are recorded in the implementation plan.
SDK/provider and authentication race suites, documentation build and the final
41-module/23-guard workspace gate pass. AUDIT-014 is the standalone consumer guide.
S9c OAuth/tracing is implemented in
[oauth-tracing-implementation.md](oauth-tracing-implementation.md), following the
[review](framework-audit-oauth-tracing.md). OAuth's core port is smaller, with
optional OIDC/refresh and explicit provider configuration. Host email trust is
separate from verified/authoritative evidence. Browser and native authorization
flows bind a separate initiating-client proof, mode and exact provider callback;
wrong callbacks cannot consume the valid transaction. Native JSON start,
completion and pending-link verification are opt-in and cookie-free. The separate
device grant and automatic ephemeral loopback ports remain unimplemented.

Provider response validation, safe error/cancellation handling, redirect refusal
and 1 MiB bounds include Google discovery/JWKS. Existing linked-ID login requires
no email; new email-based registration/adoption requires explicit host trust.
Old in-flight authorization flows restart; repository/pending-link formats stay.

Tracing reports partial response errors and escaping panics, keeps known committed
status and Logger's cause, and finishes once. OTel HTTP middleware starts server
spans with typed metadata and opt-in W3C propagation. Literal zero disables OTLP
root sampling; parent decisions take precedence. No SDK vendor dependencies or
broad tracing abstraction were added. AUDIT-015 is the standalone migration guide.

Full workspace checks, focused race tests and documentation build passed; the
implementation plan owns exact final command results and file inventory. Named
backend/platform source reviews were resolved with regression tests. Real
provider login, delivery/collector traffic, mobile OS dispatch and live external
store legs were not exercised. Consumer applications remain unchanged.
S10 `sdk/pocket` wiring and composition has since been reviewed below.

### S10 — pocket wiring and composition review

[framework-audit-pocket.md](framework-audit-pocket.md) records the completed review,
reproductions, real consumer checkpoints and proposed cleanup. Keep the small
RouteRegistrar/Mount contract and both generic registrar wrappers. Keep Mount.Events
until CMS gains its public Service facade; CMS currently consumes that emitter.

The jobs snapshot/validation and no-route Register findings are now fixed in
[jobs-runtime-implementation.md](jobs-runtime-implementation.md), AUDIT-018.
NewRuntime validates and copies staged handlers once, deriving both pool filters
from that snapshot; each scheduler work function owns its copied kinds. Register
is optional logging and accepts enqueue-only/fenced-only configurations.

Remaining findings: events exposes no cleanup for its acquired subscription,
retains the caller's middleware slice, and operational logger wiring is inconsistent.
Authorization's SystemMutator bypasses the mount logger. Nil-router startup errors and
documentation of optional registration need alignment. CMS's unfinished facade
and generated URLs stay for its full pocket audit.

Preserve Segovia's deliberate staged handler map and the NewRuntime snapshot;
do not clone it at NewService or make Register required finalization. Next expose
events.Close, copy its middleware and wire a construction-time logger. Continue
the plan's remaining small per-pocket slices, then
continue full pockets events → jobs → authentication → authorization → CMS.

SDK and five pocket core build/test/vet, focused SDK-pocket/events/jobs race suites,
all 23 guards and public behavior probes passed. The probes independently confirmed
foreign schedule enqueue and logger routing; green existing tests do not negate
those defects. Full workspace/live adapters/browser checks were not rerun. Only
the review plan and this index changed during that review. The subsequent approved
module relocation is recorded in pockets-module-move.md and AUDIT-016; it preserves
all previous AUDIT entries and does not implement the ownership fixes.

## Recommended reviews

Use the repository's named `product-manager` for developer-experience tradeoffs
and `lead-backend-engineer` for cross-layer contracts; involve `platform-sre` or
the frontend lead only when the finding calls for it. This is guidance for
bounded review work, not a mandatory approval chain.

## Next-session handoff

- **Current follow-up handoff:** [startup-release-segovia.md](startup-release-segovia.md),
  COMPLETE. Published 34 framework tags; upgraded Segovia v2 locally at
  9db497fd703601cf574bffc1c4371b9e70679cba. All public release and local consumer acceptance
  gates pass. Remaining work: separate production limiter/cutover and deferred
  Firestore reconciliation/live proof; CMS stays deferred.

- **Previous follow-up handoff:** [pocket-constructor-options.md](pocket-constructor-options.md),
  COMPLETE, AUDIT-031. All four non-CMS pocket roots and configurable component
  constructors use explicit required inputs plus typed options and grouped policy
  records. Group replacement, capture/reuse, mode/model authority and lifecycle are
  verified. Constructor filenames are consistent. Exact-source 42-module checks,
  actual-workspace guards, core race, host HTTP/jobs and docs checks passed; no
  remaining implementation blocker. The plan contains a copyable handoff prompt.
  Its then-next startup/release/Segovia sequence is now complete, as recorded above.
  CMS remains deferred; do not repeat the completed options work.

- **Previous follow-up handoff:** [constructor-options.md](constructor-options.md),
  COMPLETE, AUDIT-030. All constructor families were reviewed; selected option APIs,
  private construction state, callers, scaffolds and documentation are migrated.
  Exact changed files, baseline, commands, race/live evidence, independent reviews
  and retained Config exceptions are recorded there. Next: a separately planned
  host-startup-context/constructor-error pass or consumer adoption. Do not repeat
  the completed constructor options work. CMS and external consumer edits remain
  outside the completed scope.

- **Previous completed handoff:** [integration-sweep.md](integration-sweep.md), COMPLETE,
  AUDIT-029. The SDK/non-CMS pocket/integration audit pass is complete; all accepted
  findings are implemented and verified. This plan records the 90 changed paths,
  fresh baseline, final source snapshot, exact commands, live evidence, initial
  failures and retained provider limits. Branch remains firestore-authentication;
  extensive pre-existing changes are preserved. No release or external consumer
  edit was performed. **Next:** use AUDIT.md for consumer adoption and run the
  live checks for that host's providers. CMS remains deferred. Historical handoffs
  below are superseded where they describe revisions/receipts as open decisions
  or an earlier phase as the next step.

- **Previous completed:** [pocket-structure.md](pocket-structure.md), AUDIT-028.
  The compact-root follow-up is complete: public focused logic services, public
  HTTP adapters, grouped stores and aligned scaffolds/guards/docs. It replaced
  AUDIT-027's root-heavy first pass before the integration sweep.

- **Latest completed:** plans/authorization-followup-implementation.md, AUDIT-024.
  The independent second review's correctness fixes and host API simplifications
  are implemented and verified: predicate/dependency protection, copied guard
  proposals, explicit guardian policy, refusal errors, public CheckRequest guard
  API, optional bulk readers, role budgets and fixed FilterPage pulls.
  Full 42-module make check, root guards and live store gates passed; the plan
  records exact commands, skipped live-cloud/non-C checks and fixture cleanup.
  **Next:** a new bounded review or consumer adoption; baseline receipt-free
  revision-aware writing remains a separate design decision (review D6).
  Historical handoffs below preserve prior work and are not active restrictions.
- **Latest completed:** plans/jobs-events-audit-implementation.md, AUDIT-020/021.
  Scheduler pending dispatch/recovery, queue ownership/retry/terminal fixes and
  events visibility/lifecycle/opaque payloads implemented. Full live SQL race
  conformance, migration upgrades, actual process restart/HTTP behavior, make
  check/guards/docs passed. Core/SDK remain opt-in and boundary-safe. No auth pocket
  implementation, external consumer edits or releases. That plan owns exact
  changed paths, logs, limits and disposable-service cleanup evidence.

- **Previous implementation (completed):** plans/jobs-runtime-implementation.md,
  COMPLETE. Jobs P1/P3 fixes are implemented: final validated handlers and matching
  queue/scheduler kinds are captured at NewRuntime; scheduler closures are
  independent; Register is optional logging. Staged wiring and generic SDK
  workers/job middleware remain intact. Baseline:
  /tmp/gopernicus-jobs-runtime-baseline.json (2019 files, 992 prior dirty entries
  using --short -uall; firestore-authentication / 6807ed06). The implementation
  plan owns the exact changed paths, before/after regressions and verification.
  Six-module build/test/vet, jobs race, 23 guards, docs build and actual
  jobs-minimal HTTP/scheduling/drain behavior passed. AUDIT-018 preserves earlier
  entries; no module requirements, SDK implementation or external consumer changed.
  **Next:** events cleanup, middleware ownership and construction logger per
  framework-audit-pocket.md, then remaining logger/router findings and full
  pocket audits. The full jobs domain/store audit is not complete. CMS feature
  work stays deferred. Use plans/framework-audit-jobs.md as the central jobs
  checklist for completed work, unresolved findings and remaining slices.
  plans/authorization-listing-audit.md remains queued for
  authorization, explicitly covering all three store/join arrangements.

- **Previous implementation (completed):** plans/sdk-pkg-rename.md, COMPLETE.
  Eight mechanism packages moved from sdk/foundation to sdk/pkg; package names,
  APIs and all 42 module requirements are unchanged. Baseline:
  /tmp/gopernicus-sdk-pkg-baseline.json (2017 files, 799 prior dirty entries,
  firestore-authentication / 6807ed06). The implementation plan owns exact task
  inventory and verification: full make check, focused race suites, docs build,
  docs typecheck and standalone HTTP behavior passed. AUDIT-017 records the import
  migration and preserves all prior entry bytes. No release or consumer updates.
  **Queued owner request:** plans/authorization-listing-audit.md captures the
  three store/join arrangements, current helpers and host-ergonomics questions.
  Revisit it during the authorization-pocket audit; no auth implementation changed.
  **Next:** the bounded jobs snapshot/validation fix in framework-audit-pocket.md,
  then remaining ownership fixes and full pocket audits. CMS feature work stays
  deferred. Neither this rename nor the preceding module move fixes those bugs.

- **Previous implementation (completed):** plans/pockets-module-move.md,
  COMPLETE. Shared contract now lives in the independent pockets module, package
  pockets; concrete module paths are unchanged. Baseline:
  /tmp/gopernicus-pockets-module-baseline.json (2015 files, 747 prior dirty entries,
  firestore-authentication / 6807ed06). The implementation plan owns exact final
  inventory and verification. AUDIT-016 covers import/module migration; all prior
  AUDIT bytes remain intact. No releases or external consumer updates. CMS changes
  were limited to import/module migration; its feature audit remains deferred.
  **Next:** the bounded jobs snapshot/validation fix in framework-audit-pocket.md,
  followed by the remaining ownership fixes and full pocket audits. This package
  move did not fix jobs scheduling, events cleanup or logger wiring.

- **Latest review:** plans/framework-audit-pocket.md (S10), REVIEW
  COMPLETE. It owns findings, compatible fix scope, verification and consumer
  evidence. Baseline: /tmp/gopernicus-pocket-review-baseline.json, 2014 files,
  746 prior dirty entries on firestore-authentication / 6807ed06. Exact task changes:
  plans/framework-audit-pocket.md and plans/framework-audit.md only; source and
  AUDIT remain unchanged. **Next concrete step:** implement the bounded ownership
  fixes in its sequence, starting with jobs runtime/scheduler snapshots, then full
  pocket audits in the established order. Record implemented behavior changes in
  AUDIT; do not treat these proposals as shipped. Keep staged host wiring,
  Mount.Events until CMS migration, generic worker/job middleware and optional HTTP.
  **Owner follow-up:** root SDK placement would cycle through web/events. The
  owner instead approved the shared top-level pockets module, now implemented
  in the active handoff above. CMS remains excluded from feature/ownership cleanup;
  only the import/module migration was part of the approved relocation.

- **Previous implementation (completed):** plans/oauth-tracing-implementation.md
  (S9c). It owns the task-relative inventory, final checks and migration notes.
  Baseline: firestore-authentication / 6807ed06; 723 prior dirty entries, 2005
  files in /tmp/gopernicus-oauth-tracing-implementation-baseline.json. Prior
  review evidence stays in framework-audit-oauth-tracing.md. AUDIT-001..014 are
  byte-preserved; AUDIT-015 records implemented OAuth/tracing breaks.
  Its following S10 review is now complete; use the active handoff above before
  proceeding through full pockets and exercised adapters.
  Preserve host policy, root promotion, cryptids and generic worker/job middleware.
  Device authorization is a separate future grant; do not imply it was implemented.
- **Completed choices:** S2 validation/utilities, S3 environment/logging, S4
  identity/ID/cryptography, S5 listing/transactions, S6 web and S7 async/workers/work. Keep list vocabulary,
  Maps/Items bridges and an independent Transactor capability. Remove unused
  generic repository/write APIs; no new generic patch package or default/no-op
  transactor. Host authentication password policy remains Config.ValidatePassword;
  nil preserves previous defaults. SDK remains stdlib-only.
- **Email/notify implementation verification/changed files:**
  plans/email-notify-implementation.md owns the exact task-relative inventory and
  final commands/results. Baseline /tmp/gopernicus-email-notify-implementation-baseline.json
  covers 1990 visible files / 662 prior dirty entries on firestore-authentication /
  6807ed06. SDK notify/email, shared metadata, SendGrid, authentication, affected CMS
  imports/regression, examples, guards/module list and canonical docs are migrated.
  The bridge removal leaves 41 modules. Focused SDK/provider and authentication
  race suites, CMS header regression, docs-build and final make check pass. The
  implementation owns 132 task-relative paths (including package moves); generated
  files and prior AUDIT entries are preserved. Exact inventory is in its plan.
  The historical SendGrid race/SMTP/template failures are corrected, not pending.
  No real delivery, external consumer changes, module releases or persistent service.
  Review-only baseline/evidence remains in plans/framework-audit-email-notify.md.
- **Filestorage implementation verification/changed files:**
  plans/filestorage-implementation.md owns final APIs, 37 task-relative paths and
  verification against /tmp/gopernicus-filestorage-implementation-baseline.json
  (633 prior dirty entries; 1977 files). Full 42-module check, 23 guards, docs-build,
  SDK/adapter race, real Disk HTTP, live MinIO and GCS emulator storage/client-session
  checks passed. No unresolved product or verification failure; final source reviews
  passed and both owned containers stopped/removed. External
  consumer upgrades/cloud IAM/browser CORS remain host checks. AUDIT-013 contains
  the standalone migration; previous entries remain unchanged.
- **Previous events implementation verification/changed files:**
  plans/events-implementation.md owns the final contracts, exact task-relative
  inventory and verification against /tmp/gopernicus-events-implementation-baseline.json
  (602 prior dirty paths; 1969 files). Full 42-module check, all 23 guards,
  docs-build, SDK/pocket race, real HTTP/SSE, outbox→local dispatch→jobs composition,
  and live Redis race count 3 passed. Final SDK/Redis changes also passed targeted
  build/test/vet. One bad failure-injection assumption was corrected and rerun;
  there is no unresolved failure. Owned services stopped. Named backend/platform
  reviews covered admission/shutdown, snapshot ownership, claim recovery and ACK.
  Original SDK/Redis probes in the review use removed APIs; do not rerun unchanged.
  Preserve earlier/concurrent changes; the whole HEAD diff is not this task.
  Earlier limiter/cacher evidence and the deferred pgxdb pool-default issue stay
  in their respective implementation plans.
- **Filestorage review baseline (before implementation):**
  plans/framework-audit-filestorage.md owns S9a findings, commands, consumer
  snapshots and limits. Baseline /tmp/gopernicus-filestorage-review-baseline.json
  has 1976 files / 632 prior dirty entries; only that review plan and this master
  changed. Focused build/test/vet, SDK race, real temporary-file and fake HTTP/IAM
  probes with race, and 23 guards passed. Live GCS/S3 tests skipped; no cloud,
  consumer or full-workspace runtime claim. No source/pin/generated changes or
  unresolved verification failures. Preserve the baseline, not just the HEAD diff.
- **Migration guide:** AUDIT.md contains AUDIT-001 through AUDIT-013. Earlier
  entries now point to final root destinations for direct consumer migration.
  Coordinate published module requirements at release; no versions/tags or
  consumer upgrades were made during these implementations.
- **Branch/base:** firestore-authentication / 6807ed06. Preserve prior dirty and
  untracked changes. No audit commits, production mutations, credential reads,
  or manual generated-file edits. plans/ is the durable workflow location.
- **Environment:** Go 1.26.1, no root go.mod; run ./... inside modules or use
  workspace-qualified paths. GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache;
  formatter /Users/jrazmi/go/bin/goimports. Datastore env vars are scoped to owned
  disposable runs; existing HTTP/live tests need approved loopback access.
  Redis and PostgreSQL 17 binaries are available locally; never target existing
  app data merely because a service or connection string is available.
- **Prior records:** identity-cryptography-cleanup.md contains S4's 164 paths and
  passing 42-module/docs/scaffold/HTTP checks; earlier inventories remain in
  validation-consolidation.md, utility-package-cleanup.md and
  environment-logging-cleanup.md. Historical probes importing removed packages
  describe old behavior and are not current verification commands.
- **S5 usage evidence:** Framework/Segovia/Coordination Hub/GPS360 have real list
  and transaction consumers but no active generic CRUD/Field usage. GPS360's
  directory-write helper was an unused orphan. Read-only consumer review found
  current projected bare column names compatible; consumer upgrades still need
  their own build/runtime checks, especially custom SQL and reverse probes.
- **S6 usage evidence:** WithLogging has ten production calls but no behavior;
  DecodeJSON has 48 direct consumer references. Param/QueryParam are trivial but
  widely used. OpenAPI, ReadBody, StreamWriter/AcceptsStream and several generic
  responder conveniences have no inspected production callers. SSEStream serves
  events, static serving has four example callers, and all hosts use Run/config.
  Method counts are partial; no claim of zero unknown external consumers.
- **S7 usage evidence:** async has no inspected external Go consumers; only jobs
  constructs generic runners. Hooks/within-claim retry/attempt-only fenced retry
  have no production callers. Pool independently drives event outboxes; heartbeat
  is configured through jobs by GPS360. work is shared by jobs/authentication and
  delivery bridges. Segovia SDK/jobs pins: v0.8.0/v0.4.2; Hub v0.7.0/v0.3.0;
  GPS360 was initially v0.7.1/v0.4.1 with local replacements. The owner refreshed
  main during S7: clean e1ab3f0 now pins SDK v0.7.1 and jobs/stores v0.5.0 with
  no replacements. Echo/delivery/comps workers plus jobs UI use ordinary jobs
  Runtime and host timeout wrappers; no removed SDK hook/retry API calls were
  found. This newer inspection supersedes the original GPS snapshot. Details are in
  plans/framework-audit-work.md and /tmp/gopernicus-s7-usage.md.
  This adoption snapshot does not override the owner's explicit custom-worker
  and middleware extension goals recorded on 2026-09-10.
- **Open follow-ups:** GPS360 comps may swallow final-race cancellation into a
  failure summary and then return job success; fix at the consumer workflow
  boundary while preserving deliberate partial success. Source-reviewed only;
  no consumer edit/probe in S7. CMS slug lifecycle/route bases/casing, media metadata
  reconciliation and total upload-size limit; tracing defaults/sampling; identity projection
  versus notification eligibility, token profiles, digest privacy wording;
  durable jobs generation ordering; strong CMS cache invalidation and
  tracing failure/abort semantics. Transaction participation and same-instance
  ownership must be checked in later pocket/consumer workflows. Do not globally
  filter resolver addresses; GPS360's legacy ownership depends on them.
- **Backend limits:** SQLite and real application router behavior were exercised
  in prior slices. S8b additionally exercised Redis/Postgres limiter behavior on
  disposable services; this does not establish other PostgreSQL contracts.
  S9a additionally exercised isolated MinIO and fake-gcs-server storage/session
  behavior; real AWS/GCS, browser CORS, Firestore emulator/GCP and hosted Turso
  remain unverified. Tagged compilation is
  not runtime conformance. Rollback deadlines depend on driver cooperation;
  cancellation racing commit cannot guarantee undo.

Keep the active plan path, changed-file list, commands, unresolved failures and
next concrete step current before handing off to another context window.
