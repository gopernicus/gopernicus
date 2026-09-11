# Pocket structure and public API

Status: COMPLETE — 2026-09-11. The owner approved public focused services
under logic/, public adapters under inbound/http/, and a small root for
construction/composition for authentication, authorization, jobs and events.
This replaces the first pass's root-heavy implementation (AUDIT-027). CMS
remains deferred. Implementation, consumer migrations and verification are complete;
the integration sweep is next.

## Approved public-package follow-up

- Public packages are supported host extension points; private fields and
  unexported helpers still encapsulate their implementation. Do not mechanically
  publish every old internal helper or trusted write primitive.
- Root owns construction/composition and convenient access to built components.
  Real use cases and their tests belong to cohesive logic packages; avoid a
  second mirror service made of forwarding methods or a root full of aliases.
- Use domain nouns: roles.Service, relationships.Service, decisions.Service,
  queue.Service, schedules.Service, invitations.Service. Exact maps are recorded
  by each implementer before edits; boundaries take precedence over uniformity.
- Co-locate vocabulary and consumed ports with their owning responsibility.
  Remove compulsory domain/ grouping where it only separates related code;
  retain a shared inward model package only for an actual dependency need.
- Publish inbound/http with deliberate validated construction and callable
  middleware/handlers. Hosts can mount bundled routes, use middleware on their
  own routes, or call logic directly. Preserve mount-time validation, resolved
  budgets, browser/route guards, failure responses and lifecycle ownership.
- Logic never imports HTTP, inbound, concrete stores, root composition, or
  another pocket. Inbound consumes narrow logic contracts. Stores implement
  public logic-owned ports. Internal is optional for genuinely private engines,
  test support or helpers; no mandatory internal wrapper over every layer.
- Preserve authorization's separately held guarded Service, RelationshipWriter
  and SystemMutator capabilities, atomic guard/write/audit invariants, and model
  ownership. Separating roles and relationships must not expose actor bypasses
  as ordinary mutation methods or split an atomic mutation across services.
- Move implementations rather than reimplementing behavior. Preserve existing
  regression coverage; add targeted public-construction/extension tests where
  the newly accessible API needs input validation or capability protection.
- Root updates all in-repository consumers, scaffold, boundary guards and
  current architecture docs. Record imports/API migrations in AUDIT-028.
  No SQL migration, persisted format, module count, SDK protocol, external
  consumer or CMS implementation changes are intended.

### Independent public API review

The named architecture steward reviewed the new external construction paths and
independently confirmed these findings are closed with targeted regressions:

- Authentication must clear earlier session-liveness proof on re-authentication
  and bind verified credential context to the service that verified it.
- Low-level challenge issuance/consumption stays private. Delivery initialization
  is a separately held construction capability, never obtainable from an ordinary
  authentication Service reference.
- Direct decision/mutation construction must share the supplied relationship
  engine's resolved limits, or reject contradictory limits.
- Direct mutation construction must reject role/relationship permission ownership
  overlap, including independently compiled role models.
- Direct authentication HTTP construction requires explicit RuntimeMode and
  enforces production secure-cookie and shared-limiter requirements, matching
  root construction.

Root assembly's optional-kind regressions (typed-nil role reader and compiling
an absent role model) were found by the migrated auth-CMS example and fixed.
The full auth-CMS suite then passed, including real HTTP/TLS flows and listing.
All four core race checks and affected adapter-module checks passed. The final
workspace check and independent public API review are complete.

### Final public structure and handoff

| Pocket | Public logic services | Public transport | Root production files |
|---|---|---|---|
| authentication | authentication, invitations, delivery | inbound/http | 7 |
| authorization | decisions, relationships, roles, mutations; shared model/audit vocabulary | inbound/http | 3 |
| jobs | queue, schedules | none required | 1 |
| events | streams, outbox | inbound/http | 1 |

Roots construct named components and own useful assembly/lifecycle behavior.
Direct constructors support independent host composition. Ordinary services do
not expose trusted writers or authentication delivery initialization. Focused
behavior tests moved with services; root tests retained there exercise construction
or several assembled services, including atomic guard/write/audit integration.

All in-repository consumers use the new services and owned vocabulary. Auth-CMS
request helpers receive the specific decision, role, relationship or HTTP service
they need. Scaffolded pockets place a real service beside its aggregate and store
port under logic/<aggregate>; a single-service root returns it directly.
Dependency guards cover both public logic and deferred/private logic layouts.
Current architecture, pocket, example, release and documentation-site guidance
describes the public package rules. Named implementer/steward role documents were
aligned with the owner's decision.

Breaking changes are recorded in AUDIT-028. The persistent
[pocket-api-migrations.json](pocket-api-migrations.json) has 497 checked symbol
destinations, 18 whole-package moves and the component method owners. A symbol
map does not replace the constructor/signature/capability guidance in AUDIT.md.

### Final verification

All Go commands used GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
No unresolved implementation or verification failure remains.

- PASS: full 42-module `make check` in
  /tmp/gopernicus-pocket-public-check. Every module passed `go vet ./...`,
  `go build ./...`, `go test ./...`; integration/live-tag vet compiled the
  relevant suites. Scaffold cache preparation and all layering guards passed.
  Log: /tmp/gopernicus-pocket-public-check.log. Snapshot manifest:
  /tmp/gopernicus-pocket-public-check-manifest.json.
- The snapshot contains the working sources rather than HEAD, preserving earlier
  uncommitted audit work. Its gitless generation check confirmed templ output was
  unchanged. Two integration-only Firestore test import fixes were synchronized
  before the tagged step began. Final executable sources match this checked
  snapshot. Git-dependent guard coverage was additionally run in the real tree.
- PASS: actual-tree `make guard`, recorded in
  /tmp/gopernicus-pocket-public-guard.log; targeted scaffold/core/store/guard fixture
  tests in /tmp/gopernicus-pocket-public-scaffold.log. The final goimports check
  was empty for all 735 changed non-generated Go files.
- PASS: `go test -race ./...` for authentication, authorization, jobs and events.
  Authorization's three stores and jobs/events' four SQL store modules also
  passed race checks. Authentication adapters and bundled views passed
  build/test/vet; tagged fixtures compiled without live infrastructure execution.
- PASS: auth-CMS regression suite, including real local HTTP/TLS cookie,
  CSRF/Origin, invitation, role administration, guarded mutation, delivery and
  list-filter flows. Jobs example lifecycle and events SSE tests passed.
  Logs: /tmp/gopernicus-pocket-public-auth-cms.log and
  /tmp/gopernicus-pocket-public-jobs-example.log; final workspace run repeats them.
- PASS: separately built jobs host smoke: health response, enqueue, actual work
  completion, SIGTERM during a running slow job, completion and graceful drain
  with exit 0. Log: /tmp/gopernicus-pocket-public-jobs-smoke.log. No smoke process
  remains running.
- PASS: documentation `pnpm typecheck` and `pnpm build`; logs are
  /tmp/gopernicus-pocket-public-docs-typecheck.log and
  /tmp/gopernicus-pocket-public-docs-build.log. No interactive browser UI review
  was performed; transport behavior was exercised through HTTP/TLS tests.
- Independent architecture-steward source/assertion review confirmed all five
  direct-construction/proof/capability findings above are closed.

Baseline comparison confirms no CMS implementation, SDK, SQL migration, index
artifact, module-definition or persisted-format change. Live PostgreSQL/Turso and
Firestore emulator/cloud checks were not rerun for these structural changes;
earlier live evidence remains in preceding audit entries. No external consumer
apps, publication or deployment were modified.

Exact phase file inventory: /tmp/gopernicus-pocket-public-final-files.json.
The changed source areas are pockets/{authentication,authorization,jobs,events},
examples/{auth-cms,jobs-minimal}, workshop/gopernicus scaffold/guards, Makefile,
current architecture/readme/release/docs-site guidance, AUDIT.md and these plans.
Detailed owned reports: /tmp/gopernicus-pocket-public-authentication.md,
/tmp/gopernicus-pocket-public-authorization.md and
/tmp/gopernicus-pocket-public-jobs-events.md. Resume the integration sweep from
plans/framework-audit.md; CMS remains excluded.

### Follow-up ownership and baseline

- Authorization implementer owns pockets/authorization (including its stores)
  and /tmp/gopernicus-pocket-public-authorization.md plus an import-map JSON.
- Authentication implementer owns pockets/authentication (including stores/views)
  and /tmp/gopernicus-pocket-public-authentication.md plus an import-map JSON.
- Jobs/events implementer owns those two pockets (including stores) and
  /tmp/gopernicus-pocket-public-jobs-events.md plus an import-map JSON.
- Root owns shared consumers, templates/guards, docs, migration notes and full
  verification. Named project implementer roles are used; this owner-approved
  public-service decision supersedes their older internal-only language.
- Fresh baseline: /tmp/gopernicus-pocket-public-baseline (2217 files), manifest
  /tmp/gopernicus-pocket-public-baseline.json and git status snapshot beside it.
  Branch firestore-authentication; preserve all pre-existing dirty changes.
- Use /Users/jrazmi/go/bin/goimports and
  GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Build/test/vet affected modules,
  race-test core packages, run guards, scaffold tests, docs checks and full
  42-module make check. Exercise auth HTTP and jobs runtime behavior. Live
  external datastore checks are not automatically required for package moves.

The remaining sections describe the completed first pass unless explicitly
updated below; the approved follow-up above supersedes its placement choices.

## Owner feedback after the first pass

Authentication increased from 6 to 18 root production files; authorization
increased from 12 to 16, and jobs from 2 to 6. Splitting large files improved
individual file readability but did not meet the intended root organization.
Root test counts are also substantial (17 authentication, 23 authorization).

The next design must move cohesive implementation responsibilities and their
tests into packages, while keeping a small public construction/configuration/
mounting surface. Avoid creating one package per file or recreating a parallel
service consisting of forwarding methods. Go requires methods to share their
receiver type's defining package, so relocating the real service and preserving
the public API must be considered together. Public aliases or carefully scoped
embedding can expose supported use cases without a method-by-method wrapper;
do not embed an internal engine that exposes extra verifier or trusted methods.
Retain the authorization capability separation and optional host mounting.

The completed verification and first-pass inventory below remain valid for that
implementation. Capture a fresh baseline before further source changes.

## Purpose and decisions

- Pockets should demonstrate the application's inbound / logic / outbound
  responsibilities while supporting external consumers and adapter authors.
- Keep a deliberate, complete public use-case API. A host can provide its own
  handlers, compose workflows, replace declared dependencies and supply policy.
  File count alone is not a quality measure, but a compact, navigable root is an
  explicit owner goal. Group implementation into packages rather than expanding
  the root whenever a source file becomes large.
- Do not require two matching Service types with forwarding methods. A wrapper
  must earn its place through assembly, lifecycle ownership, capability separation,
  or a meaningful public contract. Private fields and unexported helpers can keep
  a public concrete service encapsulated. Use internal packages for coherent
  implementation boundaries; do not mechanically expose existing internal packages.
- Preserve authorization's separately held Service, RelationshipWriter and
  SystemMutator capabilities. Simplifying delegation must not combine them.
- Keep public domain types and port contracts available to hosts and adapters.
  Choose a shared inward home for use-case vocabulary where it removes duplicate
  commands and root/inbound import cycles. Do not add a generic contracts package
  or relocate every type merely for symmetry.
- HTTP handling, response mapping and middleware implementation belong to inbound.
  Rules and workflows should not depend on HTTP or concrete stores. Decide where
  each real service lives before promising a root alias or changing Register;
  aliasing a type from another package does not permit adding methods to it.
- Keep stores as the outbound persistence tier. Move usable memstore packages to
  stores/memory; retain the implementations used by examples and conformance.
  Move shared storetest packages to stores/storetest. The stores directory itself
  remains a grouping directory with no package or go.mod.
- memory and storetest remain packages in their existing pocket core module.
  Driver adapters retain their separate go.mod files. Directory grouping does
  not require additional modules or aggregate imports of every adapter.
- Keep source tests beside the responsibility they exercise and retain public
  contract tests at the API boundary. Do not relocate tests solely to hide files.
- Keep CMS deferred. First establish the shape in authorization, then apply the
  relevant conventions to authentication, jobs and events. Update the pocket
  scaffold and documentation to teach the same rules.

## Observations to resolve

- authorization/mutation_service.go contains guarded write orchestration and
  model validation; filter_page.go contains a complete scanning algorithm.
- authorization/role_routes.go duplicates use-case commands specifically because
  root construction imports inbound and inbound cannot import root back.
- authorization/internal/logic/authorizersvc/middleware.go implements HTTP gates.
- authentication/authentication.go combines almost 2,900 lines of public
  vocabulary, configuration, construction and service methods.
- jobs/fenced.go and events/poller.go implement substantial public runtime/use-case
  behavior. Judge whether extraction simplifies each before adding a wrapper.
- Makefile G2/G6/G7 exclude the whole stores directory. Nested core packages must
  retain dependency coverage, while core tests may use memory and
  storetest. Preserve the prohibition on production logic importing adapters.
- The pocket charter describes removed authorization revisions/receipts and
  inaccurately equates Go internal visibility with module boundaries. Correct
  those claims when updating the charter; internal restrictions follow the parent
  import tree, including eligible descendant module paths.

## Work sequence

1. Map authorization's exported API, implementation ownership and import graph.
   Compare retaining useful root methods with moving cohesive implementations
   inward. Record a concrete before/after map, keeping host customization intact.
2. Implement the authorization structure and stores grouping with matching
   guards, examples, documentation and consumer migration notes in AUDIT.md.
3. Apply the established conventions to the other three in-scope pockets and
   scaffold. Avoid forced package symmetry for small pockets.
4. Verify behavior and package boundaries, record exact changes and remaining
   limitations, then resume the integration sweep.

## Implementation ownership and concrete map

- Root owns stores grouping across all four pockets, import migration, Makefile
  boundary checks, scaffold, shared documentation, AUDIT-027 and final verification.
  stores/memory and stores/storetest remain in each pocket's existing module.
  No migration SQL or module path changes are needed.
- Authorization: keep the real host-facing Service and separately held writers;
  their rule orchestration can remain on those concrete types. Do not add a new
  internal counterpart merely to forward methods. Split root configuration,
  construction and public vocabulary into cohesive files where useful. Move
  HTTP middleware and response mapping from decision logic into inbound; give
  shared role use-case vocabulary an inward home to eliminate the root transport
  adapter's duplicate command structs where possible without changing public calls.
- Authentication: retain the meaningful auth/invitation/delivery components and
  public assembly API, split the large root file by responsibility, and move
  cookie/header/middleware behavior from authsvc into inbound. Transport-independent
  credential verification stays with logic. Preserve public route, cookie, token
  and policy behavior and the optional mounting API.
- Jobs/events: retain useful public Runtime/Poller implementations and small
  public methods rather than adding delegation. Separate jobs configuration,
  construction, protocol methods and worker runtime into readable files. Review
  events ownership; its small existing Service/Poller separation may already be
  appropriate. Preserve all lifecycle and delivery semantics.
- Each implementation owner records their final file map and checks in a report
  under /tmp/gopernicus-pocket-structure-<area>.md. Use the named implementer
  project role; its unavailable configured model is replaced by the inherited
  model. Current owner decisions supersede the role's older mandatory-facade rule.

## Preconditions and verification

- Branch firestore-authentication, HEAD
  6807ed062fa93d71e176ae85d0612e2342dc8694 at proposal time. Preserve the existing
  dirty tree; capture a fresh change baseline before implementation.
- Use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache and the repository formatter
  /Users/jrazmi/go/bin/goimports. There is no root go.mod; use the module inventory.
- Build/test/vet affected modules and existing public behavior tests. Verify
  generated scaffolds and that memory-only consumers acquire no driver dependency.
  Run make guard and the full make check at the cross-module milestone.
- New verification should protect changed dependency rules or behavior, not
  mirror mechanical file moves. Exercise live stores if implementation behavior
  changes; report exactly which live checks run or remain unverified.

## Current handoff

Baseline: /tmp/gopernicus-pocket-structure-baseline.json (2183 source files), with
copies under /tmp/gopernicus-pocket-structure-baseline. Existing tree had 1215
changed paths before this phase. Toolchain Go 1.26.1 and formatter confirmed.
No active database fixture or production mutation is needed for this structure
phase. Do not edit external consumers, publish, deploy or commit. Current phase
changes and verification are recorded as work completes below.

## Completed work and review

- All four conformance packages moved to stores/storetest. Jobs and authorization
  memory implementations moved to stores/memory, with package name memory and
  matching imports. There are still 42 modules; no core/support module was added.
- Authorization keeps real root orchestration and separate trusted writers.
  Configuration, construction, models and service methods occupy cohesive files.
  Shared actor/role commands live in existing domain packages, allowing Service
  to satisfy inbound's port directly. Duplicate command conversions, engine/composite
  HTTP wrappers and the obsolete error-mapper callback are removed. HTTP handling
  and its behavior tests moved to inbound; public methods remain available.
- Authentication retains meaningful auth/invitation/delivery composition, splits
  root declarations by concern, and moves cookies, headers, middleware and their
  tests to inbound.Authenticator. Its shared bearer parser serves both middleware
  and logout. No host signature or policy changed; all 898 baseline test names
  remain. Internal credential verification stays out of the public API.
- Jobs' public Runtime now owns its pools, removing the duplicate internal
  Runtime/Deps/HandlerFunc and forwarding layer. Configuration, work protocol and
  fenced runtime are separate files. Events retains its small Service/Poller split.
- The scaffold emits a direct small Service and stores/memory + stores/storetest.
  The same parsed-source boundary checker protects both repository and scaffold:
  dynamic core discovery, correct nested-module boundaries, covered core store
  support, and transport-independent logic. Fixtures cover incorrect imports and
  hidden modules. Existing views/foreign-pocket coverage is retained.
- Updated ARCHITECTURE.md, pockets/README.md, pocket READMEs, site documentation,
  example imports/docs, and AUDIT-027. No external consumers or CMS source changed.

Named architecture-steward review found two initial checker gaps (hardcoded core
names and unrestricted nested go.mod skipping). Both were fixed and regression
fixtures added. The reviewer also identified the obsolete authorization response
callback; it was removed. Final review found no remaining high-value issue,
including authentication cookie/default/credential behavior and public API privacy.

Detailed implementation reports:
- /tmp/gopernicus-pocket-structure-authorization.md
- /tmp/gopernicus-pocket-structure-authentication.md
- /tmp/gopernicus-pocket-structure-jobs-events.md

Final inventory: /tmp/gopernicus-pocket-structure-inventory.json — 105 added,
139 modified and 71 removed paths relative to this phase's baseline (315 total;
relocations count as additions/removals). This includes prior existing dirty files
only where this phase changed them; the prior work is preserved.

## Final verification

All Go commands used GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.

- PASS: each of the four pocket cores, go build ./..., go test ./...,
  go test -race ./..., go vet ./.... Existing loopback HTTP/TLS/SSE cases ran
  with automatically approved local listener access. Authentication and events
  initial sandbox bind failures were environmental and are resolved.
- PASS: generated pocket core and both driver modules, plus boundary fixtures.
  Command from workshop/gopernicus: go test ./internal/commands -run
  '^(TestPocketBoundar.*|TestScaffoldPocketCoreCompiles|TestScaffoldPocketStoresCompile)$'
  -count=1. Log: /tmp/gopernicus-pocket-structure-scaffold.log.
- PASS: full 42-module make check from /tmp/gopernicus-pocket-structure-check,
  including templ generation drift, build/test/vet, integration/live-tag compile
  checks and guards. Datastore environment variables were explicitly removed.
  Log: /tmp/gopernicus-pocket-structure-make-check.log; snapshot manifest:
  /tmp/gopernicus-pocket-structure-check.json. A gitless copy avoids interpreting
  the owner's pre-existing generated-file edits as new generation drift. The
  actual repository's make guard passed separately, including its git-based rule:
  /tmp/gopernicus-pocket-structure-guard.log.
- PASS: actual rebuilt jobs-minimal process on an ephemeral loopback listener.
  GET /healthz returned 200; POST /enqueue delivered a payload to demo.print;
  SIGTERM during demo.slow allowed completion, runtime drain and exit 0.
  Process was stopped; no service remains. Log:
  /tmp/gopernicus-pocket-structure-jobs-smoke.log.
- PASS: auth-cms's complete host test suite in make check, including real TLS
  cookie/CORS/CSRF flows, role HTTP lifecycle, authorization proof and delivery
  behavior (cmd/server 16.311s).
- PASS: public root dependency graphs contain no stores, testing or integrations;
  memory adapters add only own core/SDK/shared-pocket modules. Historical SQL
  migration hashes are unchanged. No persistent schema or stored format changed.
- PASS: documentation pnpm typecheck and pnpm build, using its existing pnpm
  lockfile/toolchain. Log: /tmp/gopernicus-pocket-structure-docs.log. The optional
  Docusaurus update-check permission notice is nonblocking; no config was changed.
- PASS: goimports, changed-file whitespace review and git diff --check. Source
  matched the full-check snapshot; afterward only two stale source-comment paths
  and audit/plan documentation changed, with no executable-code difference.

Not rerun: live databases, Firestore emulator/cloud and optional locale tests.
This phase reorganizes code and imports without changing driver behavior; prior
live evidence is in preceding audit plans. No unresolved test or implementation
failure. Consumer updates and releases remain host-owned follow-up work.
