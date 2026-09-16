# Composable authorization guards

Status: COMPLETED — implemented and verified; uncommitted and unreleased.

## Context

Hosts need policies such as “global administrator, or organization member who
can edit this document” without implementing their own authorization evaluator.
The accepted vocabulary is `Require`, `All`, `Any`, `HasRole`,
`HasRelationship`, and `Can`. Extend the completed unified tuple implementation
with mixed-resource expressions, lazy HTTP inputs and one coherent operation.

Today `decisions.Evaluate` accepts exact-role expressions only, while
`RequireAnyPermission` performs separate checks. The new surface uses the existing
core expression evaluator and budget; HTTP supplies request inputs and responses.

## Goal

Compose role, relationship and named-permission guards across resources in one
snapshot/cache operation, with ordered short-circuiting and model-free exact roles.

## Scope

- Follow `ARCHITECTURE.md` and the FS2 services/optional-transport contract in
  `pockets/README.md`. Logic never imports HTTP, stores or the root assembler.
- Preserve the uncommitted unified tuple work, its completed plan, and other owner
  files. Check the current worktree before editing; do not reset unrelated work.
- No schema, migration, tuple/cache protocol, production store interface, SDK,
  dependency or module changes. No publication, commit or application DB actions.
- Keep `roles.Service.HasRole`/`HasRoleIn` and existing permission middleware.
  No implicit global fallback, mandatory role catalog, second evaluator, `Not`,
  parallel branch execution or arbitrary host boolean callbacks.
- No generated-artifact changes. These additions join the pending coordinated
  release described in `RELEASING.md`; this task assigns no versions or tags.

## Agreed API and semantics

### HTTP predicates

`inbound/http` owns immutable opaque-pointer `Target` values and `Predicate` trees:

```go
Global() Target
Resource(resourceType string, resolve ResourceResolver) Target
Fixed(resourceType, resourceID string) Target
Path(resourceType, parameter string) Target

HasRole(label string, target Target) Predicate
HasRelationship(label string, resource Target) Predicate
Can(permission string, resource Target) Predicate
All(predicates ...Predicate) Predicate
Any(predicates ...Predicate) Predicate

(Gates).Require(Predicate) web.Middleware
(*Adapter).Require(Predicate) web.Middleware
```

The existing HTTP `ResourceResolver` type remains. Targets retain their declared
resource type; Fixed/Path reuse existing resolution behavior. Reusing one Target
shares one input slot; independently constructed dynamic targets remain separate.

```go
organization := authorizationhttp.Path("organization", "organizationID")
document := authorizationhttp.Path("document", "documentID")
guard := components.HTTP.Require(authorizationhttp.Any(
    authorizationhttp.HasRole("administrator", authorizationhttp.Global()),
    authorizationhttp.All(
        authorizationhttp.HasRole("member", organization),
        authorizationhttp.HasRelationship("editor", document),
        authorizationhttp.Can("publish", document),
    ),
))
```

These are deferred predicates, distinct from immediate roles-service calls.
Scope is the second argument; the predicate names do not add an `In` suffix.

- **HasRole:** exact concrete-subject fact in the explicit scope; no model,
  userset expansion or fallback. Global is valid only for this predicate.
- **HasRelationship:** existing Direct semantics, including model-permitted
  userset membership; requires the declared resource/relation coordinates.
- **Can:** named model expression, including any declared Through rules;
  requires the declared resource/permission coordinates.

### Core resource bindings

Reuse `decisions.Expression`, All/Any and its existing evaluator. Add:

```go
type ResourceSlot struct { Type, Key string }
type ResourceResolver func(context.Context, string) (model.Resource, error)

On(resource model.Resource, leaf Expression) Expression
BindResource(resourceType, key string, leaf Expression) Expression
(*Service).ValidateExpression(Expression) error
(*Service).EvaluateResolved(context.Context, model.PrincipalRef,
    Expression, ResourceResolver) (model.CheckResult, error)
```

Expression gains an optional resource slot; fixed resources use its existing
resource field. Bind only supported leaves, not All/Any subtrees. Helpers clear
implicit CurrentResource but retain conflicting explicit selectors for rejection.
Validate empty/invalid keys and types, conflicting selectors, the same key declared
with different types, and missing resources/resolvers before opening a snapshot.
Evaluate continues to accept existing exact expressions and additionally supports
fixed mixed-resource leaves through On.

Export `MaxExpressionDepth` and `MaxExpressionNodes` so bounded HTTP lowering and
core validation agree. Validate the entire tree, including branches that later
short-circuit, before resolver or tuple I/O. Reject empty All/Any, ambiguous
operations, invalid declarations and excessive depth/nodes. Copy/validation must
also be bounded. Ad hoc Through leaves retain concrete resource-only target
constraints, even when a relation is not navigational in the named model.

Slots are runtime-only: Compile rejects them and new fixed selectors on
Direct/Through/Permission; existing named fixed RoleIn expressions remain legal.
Callbacks never enter Expression, and existing named-model semantics remain
unchanged. `compiler.go` hashes JSON
expressions: omit the absent slot field from JSON and prove an existing model's
pre-extension digest remains identical. No model encoding bump is planned.

### One operation and lazy inputs

EvaluateResolved calls `withOperation` once. Each attempt shares one bound view,
exact/graph read memoization, budget and recursion stack. A permission leaf calls
internal `checkPermission` with that budget, never public Check. Resource binding
is leaf-local; named state keys retain type, ID and permission. Ad hoc bound
Direct/Through roots charge distinct graph-state keys disjoint from named
permission keys, without changing ordinary named-model counts. Existing Through,
closure and per-hop limit semantics remain; do not invent SQL query accounting.

All stops on false; Any stops on true. Encountered real errors abort the whole
expression. Check cancellation before/after callbacks and before reads. Discard
provisional allows on any resolver/read/budget/cancellation/completion failure.
Missing coherent snapshot support is an error, never permission to run independent
reads. Suitable ambient snapshots and cache bypass retain their existing contract.

Resolve a slot only when reached, then validate its returned resource and declared
type. Core memoization retains values/errors for the entire top-level call across
cache retries; authorization results and evaluation budgets are fresh per attempt.
Resolvers receive the parent operation context, not a cache attempt's shorter
deadline, so an expired cache read cannot poison the memo for durable fallback.
Reusing a target must not repeat host input reads during durable fallback.
Map HTTP resolver `ErrAlternativeNotApplicable` to core
`ErrResourceNotApplicable`: a false leaf, so Any continues and All denies. Interpret
that sentinel only at the resolver boundary; tuple-reader errors never become
inapplicability. Resolvers must be read-only and respect request cancellation.
Their external reads are pinned inputs, not part of the tuple snapshot; hosts own
any wider domain transaction requirements.

### Thin HTTP adaptation and existing gates

At mount, Require snapshots/boundedly lowers the complete predicate tree into a
core expression plus resolver table and calls pure core validation. Invalid
wiring panics before traffic. Gates.Require asserts a narrow ExpressionEvaluator
capability on its checker; Adapter.Require forwards to it. Preserve Checker-only
construction and existing gates; no new standalone constructor is needed.

Request handling obtains the principal before resolution and invokes the core
once. Keep existing SDK JSON response semantics: absent principal 401; false 403;
evaluation limit 503; other resolver/evaluation error 500; successful completion
calls next once with the original request, after the snapshot completes.

Existing RequirePermission/On/Fixed, RequireAnyPermission and GateSpec remain
unchanged, including their error/sentinel behavior. Document that legacy
alternatives/stacked middleware can perform independent operations, while
Require(expr) supplies one coherent compound decision. New predicates must not
be implemented through those older per-leaf middleware calls.

## Risks

1. Per-leaf public checks, eager inputs or retry result reuse can combine grants
   from incompatible states. Prove coherent operations and input-only retry memo.
2. Resource bindings can leak across leaves or change named-model digests. Prove
   selector isolation, declaration/type checks and unchanged digest encoding.
3. Unbounded lowering or reset budgets can multiply work. Bound configuration
   before traffic and prove aggregate runtime exhaustion across permission leaves.

## Tasks

### CG1: Extend the core evaluator with runtime resource bindings

- **depends_on:** []
- **files:** `pockets/authorization/logic/decisions/model.go`,
  `pockets/authorization/logic/decisions/expression.go`,
  `pockets/authorization/logic/decisions/compiler.go`,
  `pockets/authorization/logic/decisions/check_operation.go`,
  `pockets/authorization/logic/decisions/evaluate.go`,
  `pockets/authorization/logic/decisions/budget.go`,
  `pockets/authorization/logic/decisions/expression_test.go`,
  `pockets/authorization/logic/decisions/tuple_cache_test.go`, and new
  `pockets/authorization/logic/decisions/resource_binding_test.go`.
- **verify:** In `pockets/authorization`, run
  `go test -race ./logic/decisions ./stores/memory`, `go build ./...`,
  `go test ./...`, and `go vet ./...`.
- **description:** Implement the agreed slot/helpers/validation/resolver APIs
  through the existing evaluator. Cover mixed fixed/dynamic resources, model-free
  roles, exact versus userset semantics, deep/wide/malformed/skipped branches,
  named-model slot rejection and digest stability. Test shared step/state budgets,
  lazy ordered inputs, cancellation, type mismatch, unsupported snapshots,
  callback-completion failure, atomic state-swap coherence and cache fallback
  reusing inputs while discarding provisional authorization results. Include a
  live parent context with an expired cache attempt: durable fallback must reuse
  one successful target resolution. Reader errors carrying the sentinel must
  remain errors.

### CG2: Add composable HTTP predicates and Require

- **depends_on:** [CG1]
- **files:** new `pockets/authorization/inbound/http/predicates.go`,
  `pockets/authorization/inbound/http/composable_middleware.go` and their tests;
  `pockets/authorization/inbound/http/adapter_gates.go`,
  `pockets/authorization/inbound/http/routes.go`,
  `pockets/authorization/inbound/http/construction_test.go`,
  `pockets/authorization/inbound/http/middleware_test.go`;
  `pockets/authorization/middleware_test.go`.
- **verify:** In `pockets/authorization`, run `go test -race ./inbound/http ./...`;
  from repository root run `make guard`.
- **description:** Implement immutable targets/predicates, bounded lowering,
  evaluator capability checks and the one-call middleware. Test mount rejection,
  lazy/reused targets, nested inapplicability, exact roles with no model, type
  mismatches, 401/403/500/503 outcomes and next called only after successful
  completion. Retain legacy middleware tests and Checker-only construction.

### CG3: Prove real behavior, update docs and finish verification

- **depends_on:** [CG1, CG2]
- **files:** `examples/auth-cms/README.md`, `pockets/authorization/README.md`,
  `workshop/documentation/docs/pockets/authorization.md`, and this plan.
- **verify:** In `examples/auth-cms`, run `go build ./...`, `go test ./...`,
  `go vet ./...`, and `go test -race ./cmd/server -run 'DemoAudit|RoleRoutes'`.
  From root run the owned fixture command
  `python3 .github/scripts/authorization_cache_verify.py --mode all --report /tmp/authorization-composable-guards.json`,
  then `make guard` and `make check`. Format changed Go files with the repository's
  goimports workflow.
- **description:** Reuse existing SQL/cache conformance and owned fixture suites;
  the new core tests own mixed-expression snapshot/cache regressions. Do not add
  broad adapter suites or change host routes for this extension. Run a standalone
  disposable HTTP fixture outside the repository using public
  `Components.HTTP.Require`, a real ServeMux/listener and authenticated request
  contexts. Make actual requests for anonymous, allowed and denied principals,
  including a skipped failing resolver; record request/status outcomes. This
  exercises the public middleware without changing a host or requiring a full
  authentication flow. Document role/relationship semantics, explicit global
  scope, lazy inputs, coherent operation guarantees and legacy gate distinctions.
  Record durable command results, skips and limitations here; no tag/publication.

## Sequencing and completion

CG1 → CG2 → CG3. Store production/API changes or digest changes require scope
review. No new host layout is planned; preserve H0–H10 in `examples/README.md`.

Complete when the accepted API compiles, model-free roles and mixed guards work
through one evaluator, snapshot/retry/error/budget regressions pass, existing
model digests/store contracts remain unchanged, and build/test/vet/race/guard,
owned SQL/Redis integration and real HTTP outcomes are recorded. A skipped
backend is unverified, not passed. Repeat/broaden checks only for new changes or
unresolved concerns; no full benchmark rerun is required without a performance
question introduced by this change.

## Consultation notes

Root and core implementer coordinated slots, immutable targets, the narrow
ExpressionEvaluator seam, helper conflict semantics, unchanged legacy gates and
resolver-only sentinel handling. The planner identified the JSON model-digest
hazard. No additional agents were spawned for planning.

## Open questions

None blocking. Private representation/file splits may vary without changing the
agreed contracts. Production store changes and legacy gate retirement are outside
this plan.

## Recommended reviews

- Product manager: vocabulary, role-only posture and route examples.
- Backend engineer / architecture steward: one evaluator, bindings, snapshots,
  shared budgets and transport boundaries.
- Platform SRE: fail-closed outcomes, retry consistency and fixture safety.


## Execution record (2026-09-15)

CG1, CG2 and CG3 are complete. The accepted `All`/`Any` surface is implemented
through the existing evaluator. Scope is explicit in the new HTTP predicates;
existing immediate roles-service and legacy permission gate APIs remain intact.
No store interface, migration, cache protocol or model digest changed.

### Verification

- `go build ./...`, `go test ./... -count=1`, and `go vet ./...` passed in
  `pockets/authorization`.
- Core decisions/memory race tests and HTTP race tests passed. There are 21 new
  regression test functions, a compiled public example, and three benchmark
  shapes sampled five times each. Cases cover mixed resources, exact/userset
  distinction, shared budgets, immutable inputs, concurrent request isolation,
  lazy resolution, whole-operation cache retry, and completion/cancellation
  errors discarding provisional allows.
- All 42 modules passed `make check`, including generated drift, build/test/vet,
  compile-only integration/live-tag checks and architecture guards. The command
  used an explicit environment allowlist, with no inherited datastore/provider
  settings and Go downloads disabled. Automatic review rejected the initial
  inherited-environment command; the inspected, bounded alternative passed.
- The owned fixture runner passed connector, authorization core, PostgreSQL in
  public and named schemas, SQLite, actual SQL-to-Redis, and CMS HTTP race suites.
  Every SQL/Redis behavioral test ran. The memory `TestTransactional` skip is
  intentional (no host transaction.Transactor); packages without tests also
  report non-behavioral skip records. Fixture cleanup passed.
- A standalone public-API fixture bound only to loopback served actual HTTP:
  anonymous 401; outsider 403; userset editor plus organization member 204;
  another document 403; explicit global admin 204; skipped failing resolver 204;
  reached failing resolver 500. The failing resolver ran exactly once. Its
  source and outcomes are retained in the verification JSON.
- Documentation `pnpm run build` passed. Docusaurus could not write its optional
  update-check config; this did not affect the site build.
- Changed Go files are goimports-clean and `git diff --check` passed. The three
  owner-maintained plans match their pre-task SHA-256 hashes.

The backend review found one selector-construction bug: binding helpers could
clear an invalid non-role `CurrentResource` marker. The helpers now preserve
that invalid shape, with a regression test. No review blockers remain.

Benchmark medians: global exact role 1.371 µs; two scoped exact roles 2.460 µs;
mixed exact/relationship/permission guard 2.866 µs. These include an in-memory
snapshot and HTTP recorder, ran alongside other verification, and are diagnostic
samples rather than SQL/cache or throughput claims. Raw samples and durable
command/HTTP evidence are in
`plans/authorization-composable-guards-verification.json`.

### Limits and next step

Remote Turso, production load and isolated release archives were not exercised.
Custom input reads do not automatically join the tuple snapshot; legacy flat OR
and stacked gates retain independent operations. Review/commit and the pending
coordinated release remain separate actions. No version, tag or publication was
performed. The only datastore mutations in this task were inside owned fixtures.

### Changed files

- `pockets/authorization/logic/decisions/model.go`
- `pockets/authorization/logic/decisions/resource_binding.go`
- `pockets/authorization/logic/decisions/expression_validation.go`
- `pockets/authorization/logic/decisions/expression.go`
- `pockets/authorization/logic/decisions/budget.go`
- `pockets/authorization/logic/decisions/evaluate.go`
- `pockets/authorization/logic/decisions/resource_binding_test.go`
- `pockets/authorization/logic/decisions/tuple_cache_test.go`
- `pockets/authorization/inbound/http/predicates.go`
- `pockets/authorization/inbound/http/composable_middleware.go`
- `pockets/authorization/inbound/http/composable_middleware_test.go`
- `pockets/authorization/inbound/http/composable_example_test.go`
- `pockets/authorization/inbound/http/composable_benchmark_test.go`
- `pockets/authorization/inbound/http/adapter_gates.go`
- `pockets/authorization/README.md`
- `pockets/authorization/BENCHMARKS.md`
- `workshop/documentation/docs/pockets/authorization.md`
- `ARCHITECTURE.md`
- `RELEASING.md`
- `plans/authorization-composable-guards.md`
- `plans/authorization-composable-guards-verification.json`
