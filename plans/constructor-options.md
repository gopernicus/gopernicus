# Constructor and options audit

Status: COMPLETE — 2026-09-11. The owner asked to review all constructors and
adopt functional options (`WithFoo(...)`) where they improve construction APIs.
This follows the completed integration sweep; CMS remains deferred.
Selected changes, consumer/scaffold migrations and verification are complete.
Consumer migration: [AUDIT-030](../AUDIT.md#audit-030-constructor-options).

Owner follow-up: [pocket-constructor-options.md](pocket-constructor-options.md)
supersedes this pass's retained-pocket-Config decisions with consistent options
and meaningful grouped policy records. The decisions below describe this earlier
completed pass, not the final pocket API after that follow-up.

## Scope and intent

Inventory public construction in SDK, non-CMS pockets (including stores/views),
integrations and UI. Include existing option APIs and scaffold output. Separate
service/resource construction from ordinary value builders, context helpers and
operation-specific options. Required dependencies stay visible in the signature.
Prefer understandable local implementations over universal option infrastructure.
No domain-policy change, provider feature, schema migration or module change is
part of this work.

Functional options are a common Go idiom, not a universal language/stdlib rule.
gRPC uses typed option lists; slog uses a coherent HandlerOptions struct. The
choice should reduce ambiguity at actual host call sites rather than maximize
the number of functions beginning with With.

The owner was asked whether large connection/policy Config values should remain
alongside options (recommended), or nearly all optional settings should move to
options. After allowing time for an answer, proceed with the stated selective
retention assumption. No contrary preference has been received.

## Established convention

- Context first when construction performs I/O; required ports, keys, resource
  identities and other prerequisites follow as ordinary typed arguments.
- Optional independent settings/dependencies use trailing `opts ...Option`;
  use a target-specific name when a package has multiple construction families.
- Keep coherent policy/model/connection records when they express one meaningful
  value. Never hide a universally required dependency in a With function.
- Use package-local configuration, defaults, option application, then final
  validation and resource acquisition. Do not mutate a running instance via an
  exported option or allocate resources while applying an option.
- Preserve intentional existing defaults/validation. Specify replacement versus
  append semantics; avoid silent acceptance of invalid security policy. Copy
  mutable configuration retained by constructed objects, while borrowing the
  host's clients/services with their documented ownership.
- A nil option is invalid programming input, distinct from a valid option whose
  argument is nil with documented default/disabled meaning. Constructors already
  returning errors reject nil options as invalid input; constructors without an
  error return panic with a specific diagnostic. Do not silently skip policy.
  Use the package's existing error vocabulary; UI does not acquire an SDK
  dependency merely to classify an option error. Vendor-owned options retain
  their vendor contract.
- Do not add an options abstraction to zero-config constructors or simple value
  builders, or duplicate every field just for naming uniformity.
- Record each accepted conversion and any required compatibility change before
  implementation. Keep a single clear construction path; avoid variadic any,
  runtime type-switch overloads, shared generic options and unnecessary aliases.

## Completed tasks

1. Inventory actual declarations/call sites and classify constructor families.
   Read-only named backend/steward reviews cover pockets and integrations/UI;
   root covers SDK and reconciles the whole surface.
2. Settle the convention and record an explicit keep/convert/fix decision for
   every family. Show concrete host examples and scope justified conversions.
3. Implement accepted changes, update in-repository consumers/scaffolds/docs and
   record breaking migrations in AUDIT.md. Preserve existing domain behavior.
4. Verify changed modules and meaningful construction/ownership behavior; run
   cross-module checks at completion. Record exact results and deferred work.

## Preconditions and workflow

- Branch firestore-authentication with extensive prior uncommitted audit work.
  Preserve it. Fresh source baseline:
  `/tmp/gopernicus-constructor-options-baseline` and matching `.json` manifest;
  status snapshot: `/tmp/gopernicus-constructor-options-status.txt`.
- Use `/Users/jrazmi/go/bin/goimports` and
  `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache` for every Go/make command.
- Go commands run within modules; there is no root go.mod. The workspace has
  42 modules. No dependency edits, external consumer edits, release or live
  service mutation are authorized by this construction-API task.
- Applicable named project review roles are used. Their legacy model names are
  unavailable in the current runtime, so the agents inherit the active model.
- Significant implementation follows the recorded plan. Build/test/vet affected
  modules, test behavior where option ordering/ownership could change it, and run
  actual-repository guards plus final make check. Prior dirty generated files may
  require the established isolated-source-snapshot verification method.

## Decisions and changes

AST inventory: `/tmp/gopernicus-constructor-inventory.json`, generated by the
stdlib-only `/tmp/gopernicus-constructor-inventory.go`. It includes 168 public
non-CMS construction candidates (29 SDK, 22 integrations, 115 pockets, 2 UI),
31 deferred CMS candidates, and 7 private/test helpers. Ordinary value builders
are classified separately in the family review; names alone are not evidence
that an options API belongs there. Raw declarations include multiline signatures.

### SDK decisions (approved for implementation within this request)

| Construction family | Decision and rationale |
|---|---|
| cacher.New | Convert to `New(storer Storer, opts ...Option)` with WithNamespace and WithOnError. Both settings are independent and optional; preserve nil-store Noop, framing and callback behavior. |
| cacher.NewMemory | Convert to `NewMemory(opts ...MemoryOption)` and WithMaxEntries; preserve nonpositive -> 10,000. Remove the one-field MemoryConfig wrapper. |
| ratelimiter.NewMemory | Same MemoryOption/WithMaxEntries shape; preserve existing capacity semantics and defaults. |
| workers.NewRunner / NewFencedRunner | Keep required store/process positional; move the optional logger to WithRunnerLogger / WithFencedLogger in the existing target-specific option families. Preserve processing/retry configuration and return signatures. |
| web.NewStaticFileServer / NewSSEStream | Keep current constructor signatures and With functions, but make options target private construction settings instead of an already usable HTTP handler. |
| workers.NewPool, async.NewPool, events.NewMemory | Keep existing private configuration options and defaults. Clarify nil-option diagnostics; preserve middleware order and ownership. |
| notify/email New and NewRenderer | Keep shared template/branding options, already encapsulated and validated. Their parsing during construction is intentional; no resource acquisition or outbound sending occurs. |
| logging.New(Options), email.NewSMTP(SMTPConfig) | Keep coherent tagged environment/connection records; no parallel setters that duplicate the same source of truth. |
| notify and email NewConsole | Keep the simple one-logger convenience signature; another option family adds no useful choice. |
| NewIDGenerator, NewAESGCM, NewDisk, NewContextHandler, NewStatusRecorder | Keep explicit single prerequisites. |
| NewBaseEvent, NewBaseEventWithCorrelation, NewRecord, NewDelivery, NewOrder, NewError, NewSafeDomainError | Keep domain/protocol values and ordinary arguments; they are not configurable runtime services. |
| NewWebHandler | Keep zero-configuration constructor. |

Existing option setters retain their documented default/ignore behavior for
invalid scalar values; this naming pass is not permission to change policies.
Options cannot configure live objects after construction. A custom option that
previously accepted an exported runtime pointer will require migration; ordinary
WithFoo call sites retain their shape unless listed as converted above.

### Integration and UI decisions

- Convert Redis bus construction to `New(rdb *redis.Client, opts ...BusOption)`.
  WithLogger, WithStreamPrefix, WithConsumerGroup, WithWorkers, WithQueueSize,
  WithBlockTimeout, WithRetryAfter and WithHandlerTimeout replace its all-default
  Options record. Preserve timing, defaults and lifecycle; settings resolve before
  workers start. Required client remains explicit. ClientOption and WithLogging
  still belong to connection instrumentation, not the bus.
- Convert `goth.New(opts ...Option)` using WithAssetBasePath, WithProfile and
  WithThemeStylesheetPath. Preserve defaults and validation. Render/document
  values and generated component functions stay ordinary values.
- Keep bcrypt/JWT/Redis cache/Redis limiter/PostgreSQL limiter constructor shapes
  and With names, but use private construction records. A live JWT signer must
  not accept an option that bypasses key-strength validation, and limiter prefix
  options must not bypass the internal versioned namespace. Make bcrypt options
  reusable without writing captured values; snapshot mutable option inputs where
  retained. Apply the stated nil-option contract, including nested hook options.
- Keep GCS and Redis Open's existing config-plus-options seam. Snapshot captured
  option slices and distinguish additive hook/vendor ordering from scalar
  replacement. Provider behavior and ownership remain unchanged.
- Keep connection/credential/config records in PostgreSQL, Turso, Firestore, S3,
  SendGrid, GitHub/Google OAuth and OTel. No parallel setter for every field and
  no new vendor override seam. Keep zero-config UUID/cron constructors, schema
  parsing, direct borrowed S3 construction and tracer helper signatures.
- Keep PostgreSQL migration options, adding explicit nil-option error handling.
  This is operation construction, reviewed alongside constructors because it
  already exposes the same functional-options API.
- Missing host contexts on PostgreSQL/Turso Open and Firestore pocket index
  probes are recorded follow-ups, not implicitly folded into the options change.
  Fixing them needs its own context/lifecycle plan and consumer migration.

The complete read-only matrix and call-site evidence are in
`/tmp/gopernicus-constructor-adapters-review.md`. Nil required-client validation,
public MultiQueryTracer mutability, and constructor-vs-Send error policy were
observed but are not changed for naming consistency. The requested options
convention introduces neither new validation policies nor callback ownership.

### Pocket decisions

Five focused constructor changes are justified; four use options and one removes
an unused setting rather than creating an inert option:

| Constructor | Required arguments / optional settings |
|---|---|
| queue.NewService | `repos Repositories, opts ...Option`; WithMaxAttempts, WithClock. Keep queue.Config as the root host's tagged admission-policy record; root maps it into options. |
| schedules.NewService | `repo Repository, opts ...Option`; WithEnqueuer, WithCronParser, WithBatchSize, WithClock. Nil enqueuer still means management-only; fixed intervals need no cron parser. |
| relationships.NewService | `store Storer, schema Schema, opts ...Option`; WithLimits(model.EvaluationLimits). Limits remain one validated coherent value; preserve final normalization/snapshot behavior. |
| delivery.NewService | `dispatcher Dispatcher, encrypter cryptids.Encrypter`; no options. Review during implementation proved the old ServiceDeps.Now was retained but never read. Remove that field and the dead stored clock instead of adding a meaningless WithClock. |
| delivery/command.NewProcessor | `encrypter cryptids.Encrypter, deliverer Deliverer, opts ...Option`; WithInitializer, WithClock, WithPolicy(Config). Keep the retry/timeout policy record intact and preserve typed-nil/negative-bound validation and opaque-command behavior. |

Remove superseded schedules.Config, relationships.Config, delivery.ServiceDeps
and command.ProcessorDeps construction bags; migrate owned calls and examples.
Do not add a catch-all WithConfig parallel construction path.

Keep all four root composition constructors, authentication's large Deps and
inbound Config/AuthenticatorConfig, invitation and delivery router/jobs-processor
Deps, in-process and jobs runtime configs, authorization decision/mutation configs,
events hub/stream/HTTP configs. These express coupled capabilities/policies or
meaningful dependency assemblies; adding one setter per field obscures them.
Keep roles service/writer, gate prerequisites, schema/read-model builders,
HTML policy directives, cryptographic protectors and all ordinary entity/protocol
value factories explicit. Keep store constructor argument lists and already
focused schema/audit/guardian/lease options, plus zero-config memory factories.

Move jobs memory/Turso queue and authentication Goth view options from live
objects to private construction state. Apply explicit nil-option diagnostics
across existing constructor families, including stores and outbox pollers, while
preserving nil option-argument meanings and all return signatures. The backend
review preferred ignoring nil options; root chooses invalid-input errors / clear
programmer-error panics because silent omission of configuration is less explicit
and differs from the existing panic behavior. This is not approval to tighten
other nil-dependency or scalar policies.

The exhaustive family review is at
`/tmp/gopernicus-constructor-pockets-review.md`. Its 114 factory count excludes
the `delivery/command.Open` decoder included in the mechanical 115-name pocket
inventory; that decoder remains unchanged. No constructor family is skipped
merely because its signature already uses Config or options.

The delivery clock decision supersedes the review's original WithClock proposal.
Command processor clocks remain configurable because they control retry timing.

The old pockets charter's blanket prohibition on functional options (FS6) is
superseded by this owner request and replaced with the selective rule.

### Scaffold decision

The generated one-service pocket has one optional ID generator. Generate a
private config, `Option`, and `WithIDs(sdk.IDGenerator)` on the public logic
service; root forwards the same typed options with the explicit repositories.
Remove its one-field Config construction bag. Keep entity/value constructors and
store schema/migration configuration explicit. Exercise configured IDs through
Create in generated tests, plus nil-option rejection before using the store.

Primary references: [gRPC construction](https://pkg.go.dev/google.golang.org/grpc#NewClient)
and [slog handler options](https://pkg.go.dev/log/slog#HandlerOptions).

## Final verification and handoff

All accepted changes are implemented. The SDK/pocket/adapter family decisions
above are final, including the retained Config exceptions and removal of the
unused delivery admission clock. No implementation or approval blocker remains.

### Checks and observed behavior

All Go/make commands used `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.

- SDK, six modified integration/UI modules and thirteen modified pocket modules
  each passed `go build ./...`, `go test ./...` (fresh `-count=1` in module
  sweeps), and `go vet ./...`.
- Final 42-module `make check` passed in the established isolated-source-snapshot
  workflow, including module build/test/vet, integration/live-tag checks, templ
  generation, scaffold checks and guards. Snapshot:
  `/tmp/gopernicus-constructor-options-check`; log and initial source manifest:
  `/tmp/gopernicus-constructor-options-check.log` and
  `/tmp/gopernicus-constructor-options-check-manifest.json`.
- Nine pocket source/test files received final formatting/comment corrections
  while that check ran. A token-by-token comparison excluding comments proved
  every Go source has the same code as the checked snapshot. There was no
  behavior or test-assertion change after the snapshot. Evidence:
  `/tmp/gopernicus-constructor-snapshot-tokens.go` and matching `.log`.
- Actual-repository `make guard` passed separately, including Git-dependent
  checks unavailable to the Git-less source copy. Log:
  `/tmp/gopernicus-constructor-options-guard.log`.
- `go test ./internal/commands -run 'TestScaffoldPocket|TestIntegrationBoundar' -count=1`
  passed in workshop/gopernicus. This generated a real pocket, built/tested/vetted
  its core, exercised configured IDs through Create/Get/Delete and checked nil
  option rejection. SQL scaffold modules compiled/vetted without a live database.
  Log: `/tmp/gopernicus-constructor-scaffold.log`.
- Documentation `pnpm typecheck` and `pnpm build` passed using the existing lockfile.
  Logs: `/tmp/gopernicus-constructor-docs-{typecheck,build}.log`.
- All 177 changed Go files are goimports-clean; `git diff --check` passed.

Race checks passed for SDK cacher/ratelimiter/events/async/workers/web, all six
changed integration/UI modules (bcrypt, golang-jwt, goredis, pgxdb, GCS, ui/goth),
and focused pocket delivery, authorization, jobs, outbox and Goth suites. The
events PostgreSQL nil-option pre-probe regression also passed under `-race`.
These exercised namespace replacement and disabled reporting; LRU/active-budget
capacity behavior; actual worker completion/log routing; static/SSE responses;
concurrent bcrypt option reuse; JWT method snapshots and key-strength validation;
limiter namespace finalization; copied vendor/instrumentation option slices;
queue admission defaults, schedule clocks/management-only behavior, relationship
limits/model ownership and delivery retry timing/typed-nil validation.

The full Redis suite passed with `go test -race -count=1 ./...` against disposable
local Redis, including real cache, limiter, event delivery and recovery behavior.
Two independent fixtures overlapped because their launch notifications crossed;
each used its own owned port, cleared ambient credentials and stopped afterward.
Logs: `/tmp/gopernicus-constructor-redis-check.log` and
`/tmp/gopernicus-constructor-redis-live.log`. No external Redis was used.

Initial failures were configuration-migration mistakes or sandbox-only loopback
denials: one SDK variadic option-slice call needed the logger moved into its slice;
local HTTP test listeners needed approved execution. These were corrected or
rerun with local access; no workaround, skip or unresolved product failure remains.

The independent SDK/integration/UI review and final pocket review found no
introduced correctness defect or blocking API ambiguity. Reports:
`/tmp/gopernicus-constructor-options-review.md` and
`/tmp/gopernicus-constructor-pockets-final-review.md`.

### Exact changes and retained workflow state

Compared with the fresh phase baseline: 18 added, 189 modified, 0 removed files;
most modifications are constructor call migrations in existing tests. Exact
relative paths and final hashes:
`/tmp/gopernicus-constructor-options-files.json`. Owned implementation reports and
inventories are under `/tmp/gopernicus-constructor-{sdk,adapters,pockets}-implementation.md`
and the corresponding `sdk-files.json`, `adapter-files.json`, `pockets-files.json`.
The AST inventory preserves all original constructor declarations, and the family
decisions above cover them without requiring the temporary review reports.

Root-owned changes include ARCHITECTURE.md, pockets/README.md, SDK reference docs,
AUDIT-030, RELEASING.md, this plan/index, public documentation, five pocket template
files and twelve example/CMS test caller files. The only CMS source changes are
two dependency-call tests and one view GoDoc example; CMS behavior is unchanged.
Module/workspace manifests, dependencies, SQL, generated templ files and UI assets
are unchanged from this phase's baseline. No external consumer edit, publication,
commit or deployment was performed. The branch remains firestore-authentication
with the prior uncommitted audit work preserved.

### Separate follow-ups and unverified behavior

These are recorded constructor/lifecycle design items, not unfinished options work:

1. PostgreSQL and Turso connector Open still derive startup contexts from
   context.Background plus their configured timeout. A future context-first
   signature should carry the host's startup cancellation through dialing/ping/
   retry without changing lifetime ownership. This needs explicit caller migration.
2. Authentication/authorization Firestore store index probes likewise need a
   separate context-first construction pass; their stale SQL-sibling comments
   were corrected, but no index-probe API changed here. Context belongs in the
   signature, not a WithContext option.
3. Existing required-nil dependency checks/return arities vary in raw store
   wrappers, borrowed S3 construction and outbox pollers. SendGrid still defers
   its stored configuration error to Send. These require deliberate error-policy
   decisions, not incidental tightening during an options migration.
4. Public MultiQueryTracer mutability and long domain-value constructors such as
   invitations.NewInvitation are future API review candidates. Domain facts should
   become named input values if needed, not optional settings.

Live PostgreSQL/Turso/Firestore, GCS/S3/provider/collector services and browser
engine suites were not rerun this phase. Relevant tagged code compiled/vetted;
local transports and existing HTTP/render behavior tests do not prove cloud or
browser conformance. CMS remains deferred. Next: choose one recorded lifecycle
follow-up or adopt AUDIT.md in a selected consumer and verify its actual providers.
