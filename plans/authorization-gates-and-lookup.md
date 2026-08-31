# authorization: RequireAnyPermission + LookupResources caller Limit (#19, #22)

## Context

Two gps-360-go-sourced gaps in `pockets/authorization`, resolved in one additive
release. #19: the middleware surface is conjunction-only, so every route
admitted by *either* of two grants hand-rolls the OR (short-circuit, fail-closed,
403) in the handler — the exact body `authorizersvc.Gates` already owns for the
single-gate case. #22: `LookupResources` materializes every reachable ID and
hosts re-cap after the fact; **owner-ruled to Limit-only v1** — no `After`/cursor
continuation in this release (the deterministic-continuation design a cursor
needs is deferred to a future issue if a host actually needs it). This is
**Plan B of a three-plan batch**: it releases together with Plan C (#20, bundled
role-administration routes, planned separately) as ONE
`pockets/authorization/v0.7.0` train (current tag: `pockets/authorization/v0.6.0`).
Additive only.

## Goal

`RequireAnyPermission(alternatives ...GateSpec)` puts the disjunction on the
route line through the one shared gate body, and `LookupResourcesIn` caps
returned IDs to a caller Limit without weakening the evaluation budget — both
green under the pocket's full test suite and `make guard`, ready to tag with
Plan C as `pockets/authorization/v0.7.0`.

## Out of scope

- **No `After`/cursor continuation** (owner ruling on #22). Priced deferral: a
  real cursor is a store-port change across `memstore`, `stores/pgx`,
  `stores/turso`, and the `storetest` conformance suite — a multi-module train,
  not a core-only additive release. Task-3 leaves the marker in the
  `LookupRequest` doc comment referencing #22.
- No nesting, no policy language, no bypass hooks in `RequireAnyPermission`
  (issue non-goals; an AND of ORs is stacked middleware, as today).
- No "not applicable, try the next alternative" resolver sentinel in v1 — a
  resolver error fails the whole request closed (see Risks and Open questions).
- No `CheckBatch` fast path on the wire path — evaluation is a sequential
  `Check` loop; batching stays a future internal optimization with zero API
  change (the issue makes it an explicit internal concern).
- No signature change to the existing `LookupResources` (it sits on the internal
  `kind` interface and on host-defined ports; changing it is breaking).
- No refactor of the triplicated principal/permission/resourceType validation
  blocks in `lookup.go` / `composite.go` / `roles.go` — the new `LookupRequest`
  path validates through its own `Validate()`; existing paths stay untouched
  (surgical-diff discipline).
- No example-host route adoption; the route-line demonstration lands with Plan
  C's bundled routes and downstream host adoption (gps-360-go).

## Schema / datastore impact

**None.** No SQL, no migrations, no store-port changes. The Limit truncation
happens above the `kind` interface, after the owning engine's budget-bounded
enumeration returns — `memstore`, `stores/pgx`, `stores/turso`, and `storetest`
are untouched, so there is no adapter-parity work.

## Module / API impact

One module, `pockets/authorization`, all additive. **No new imports, no
`go.mod`/`go.work` changes, no new boundary — no new guard is owed** (stated
here so review doesn't go looking).

New exported symbols on the facade (`pockets/authorization`):

- `type GateSpec = authorizersvc.GateSpec` — `{ResourceType, Permission string; Resource ResourceResolver}`.
- `func (s *Service) RequireAnyPermission(alternatives ...GateSpec) web.Middleware`.
- `type LookupRequest = authorizersvc.LookupRequest` — `{Principal PrincipalRef; Permission, ResourceType string; Limit int}`.
- `func (s *Service) LookupResourcesIn(ctx context.Context, req LookupRequest) (LookupResult, error)`.
- `LookupResult` gains `Truncated bool` (additive; every internal construction
  site is keyed-literal, verified: `decisionsvc/roles.go`, `authorizersvc/lookup.go`).

Internal (not API, but named for the implementer): `authorizersvc.Gates` gains
`RequireAnyPermission` and an unexported `maxAlternatives` cap;
`NewGates(checker, declarer, maxAlternatives)` grows a third parameter (two
call sites: `authorizersvc.(*Service).gates()`, `decisionsvc.(*Composite).gates()`);
`decisionsvc.Composite` gains `RequireAnyPermission` and `LookupResourcesIn`.

Release: tagged as `pockets/authorization/v0.7.0` **jointly with Plan C (#20)**
— this plan merges but does NOT tag; the tag lands once both plans are merged
(minor bump, additive-only, per RELEASING.md; poll the proxy `.info` URL before
`go mod tidy` anywhere downstream, per the 2026-08-27 lesson).

## Generated-artifact impact

None. No `.templ` sources touched; no views in this pocket.

## Design decisions (settled here, implementer does not relitigate)

**#19 — RequireAnyPermission semantics** (exactly the issue's proposal, plus
two holes closed on backend-lead review):

1. **One gate body.** The implementation lives on `authorizersvc.Gates`
   (`pockets/authorization/internal/logic/authorizersvc/middleware.go`), so the
   relationship engine's `gates()` and the composite's `gates()` mount the
   identical 401/403/500/503 ladder. Facade and `Composite` methods are thin
   delegations; the facade method panics on a nil decider with the same message
   shape as `RequirePermissionOn` (`pockets/authorization/middleware.go:49-54`).
2. **Registration-time validation, per alternative:** panic on zero
   alternatives; panic via `mustDeclare` for each `(ResourceType, Permission)`
   pair (the composite's `Declarer` spans BOTH compiled models — the
   `RequirePermissionOn` rule); panic on a nil `Resource` resolver; panic when
   `len(alternatives)` exceeds the resolved `EvaluationLimits.MaxBatchSize`
   (the new `maxAlternatives` cap — a sequential N-Check loop multiplies
   per-request store work by N, and an uncapped route line is a DoS seam).
   Every panic message names the alternative index ("alternative 2 of 5") and
   the pair.
3. **Type agreement, fail closed:** `mustDeclare` validates
   `GateSpec.ResourceType`, but `Check` dispatches on the *resolver's*
   `Resource.Type` — a disagreeing resolver would evaluate a pair no model
   declares and 403 forever behind a green mount. The gate therefore compares
   the resolved `Resource.Type` against `GateSpec.ResourceType` at request time
   and fails closed (500) on mismatch. Documented beside the fail-closed
   clause; tested.
4. **Request-time ladder** (all through `web.RespondJSONError`, FS9 bodies):
   no context Principal → 401; alternatives evaluated **strictly in order**;
   per alternative resolve-then-`Check`; **short-circuit `next` on the first
   allow**; a resolver error, type mismatch, or `Check` error fails the WHOLE
   request closed — 503 on `ErrEvaluationLimit`, 500 otherwise — even if a
   later alternative might have allowed; all-deny → 403.
5. **Order is outcome-affecting, not a cost knob.** Under whole-request
   fail-closed, an erroring alternative 1 means alternative 2's allow is never
   reached. The doc comment corrects the issue's "cheapest first is the host's
   choice" accordingly. Duplicate alternatives (same pair, different resolvers)
   are legal; document. No logging on all-deny (`Gates` holds no logger today
   — accepted, unchanged).

**#22 — Limit-only lookup sibling:**

6. **Shape: full struct-input sibling** `LookupResourcesIn(ctx, LookupRequest)`
   — matching the surface's own `CheckRequest` vocabulary and the repo's ruled
   struct-input-siblings convention (`EnqueueOnceIn` precedent); NOT a variadic
   on the existing method (breaks method-value/port compatibility), NOT
   `LookupResourcesPage` (it is not a page and must not promise one).
   `LookupRequest` and its `Validate()` live in `authorizersvc/model.go` next
   to `CheckRequest`/`LookupResult`; `After` becomes a future additive field —
   zero signature churn.
7. **Limit semantics:** `Limit == 0` = **the `MaxLookupResults` budget ceiling,
   today's behavior** (NOT "unbounded" — nothing here is; and deliberately NOT
   `crud.ListRequest` semantics, where 0 means `DefaultLimit` — the doc comment
   names this divergence). Negative `Limit` is a validation error wrapping
   `sdk.ErrInvalidInput` (not `relationship.ErrInvalidRef` — a limit is not a
   reference), so hosts keep mapping it to 400 through `codes.go`.
8. **Budget interplay (owner-ruled):** the composite calls the owning kind's
   existing budget-bounded `LookupResources` untouched, then truncates the
   sorted, deduplicated IDs to the first `Limit` — a deterministic prefix.
   Limit **never weakens or bypasses the budget**: enumeration that overflows
   `MaxLookupResults` is still `ErrEvaluationLimit` even when `Limit` is small,
   and v1 Limit does NOT reduce enumeration cost — it moves the host's re-cap
   into the engine and anchors the future cursor. Both stated honestly in doc
   comment and README.
9. **Truncation is observable:** `LookupResult` gains `Truncated bool`, set
   only when the Limit path drops IDs — otherwise `len(IDs) == Limit` would be
   indistinguishable from "exactly Limit grants", a regression for the very
   hosts #22 names (the /client-hubs "+ more" affordance). `Unrestricted`
   passes through untouched with `Limit` ignored (documented); the non-nil-IDs
   invariant is preserved through truncation. The classic `LookupResources`
   never sets `Truncated`.
10. **Placement:** truncation lives in `decisionsvc.Composite.LookupResourcesIn`
    (one body, above the unchanged `kind` interface); the facade stays pure
    delegation with the `ErrNoDecisionKind` nil-decider check.

## Risks

1. **Heterogeneous-shape disjunctions can't adopt v1 cleanly.** #19's gps-360
   sites mix a path-param alternative with a row-derived resolver; if the row
   resolver errors on "row has no org", the whole request 500s where the host
   wants "try the next alternative". v1 ships strict fail-closed (the existing
   `ResourceResolver` contract); a first-class `ErrAlternativeNotApplicable`
   sentinel is a small additive follow-up if the owner rules it in — see Open
   questions. Do not weaken to silent skip-on-error: that turns a store outage
   into an allow via a later alternative.
2. **N-alternative budget multiplication** — mitigated by the registration cap
   against `MaxBatchSize` and a loud doc note; residual: N budget-bounded
   Checks per request remains real work on hot routes.
3. **Joint-train coupling with Plan C** — v0.7.0 tags only when both plans are
   merged; if Plan C touches `composite.go` or the facade files, merge this
   plan first and rebase Plan C (this plan's surface is the smaller one).

## Tasks

### task-1: GateSpec + Gates.RequireAnyPermission in authorizersvc

- **depends_on:** []
- **model:** opus
- **files:** [pockets/authorization/internal/logic/authorizersvc/middleware.go, pockets/authorization/internal/logic/authorizersvc/middleware_test.go]
- **verify:** `cd pockets/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** Define `GateSpec` (vars/consts top, struct with the three
  fields) and implement `Gates.RequireAnyPermission(alternatives ...GateSpec)`
  per design decisions 1–5: registration panics (zero alternatives, undeclared
  pair via `mustDeclare`, nil resolver, `maxAlternatives` cap — all with the
  alternative index in the message), the request-time ladder (401 / in-order
  sequential Check / short-circuit on first allow / resolver-error, type-mismatch
  and Check-error whole-request fail-closed 500, `ErrEvaluationLimit` 503 /
  all-deny 403). Grow `NewGates(checker, declarer, maxAlternatives)` and update
  its two call sites (`authorizersvc.(*Service).gates()` passes
  `s.limits.MaxBatchSize`; leave `decisionsvc` compiling by updating its
  `gates()` in the same commit). Add the delegating
  `(*authorizersvc.Service).RequireAnyPermission`. Tests (G12, httptest-driven
  through real handlers): each registration panic; 401; first-allow
  short-circuit with a counting Checker proving alternative 2 is never
  consulted; in-order evaluation; all-deny 403; alternative-1 resolver error →
  500 even when alternative 2 would allow; alternative-2 resolver error after
  an alternative-1 deny → 500; resolved-type-mismatch → 500; Check error → 500;
  `ErrEvaluationLimit` → 503; single-alternative degenerate case matches
  `RequirePermission` behavior; duplicate alternatives legal.

### task-2: Composite + facade RequireAnyPermission

- **depends_on:** [task-1]
- **model:** opus
- **files:** [pockets/authorization/internal/logic/decisionsvc/middleware.go, pockets/authorization/internal/logic/decisionsvc/middleware_test.go, pockets/authorization/middleware.go, pockets/authorization/middleware_test.go]
- **verify:** `cd pockets/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** Add `(*Composite).RequireAnyPermission` delegating to
  `c.gates()` (which now carries `c.limits.MaxBatchSize`), the facade
  `type GateSpec = authorizersvc.GateSpec` alias and
  `(*Service).RequireAnyPermission` with the nil-decider panic (same message
  shape as `RequirePermissionOn` — the facade writes NO HTTP). Tests:
  cross-model disjunction through the composite (one relationship-owned pair +
  one roles-owned pair; either alternative admits); registration panic when
  neither model declares a pair; facade nil-decider panic; one happy-path
  request through the public facade API. Note: httptest requests through the
  mounted middleware are the run-and-look for this HTTP surface — no example
  host mounts it yet (route-line demonstration rides Plan C / downstream
  adoption).

### task-3: LookupRequest, LookupResult.Truncated, Composite.LookupResourcesIn

- **depends_on:** []
- **model:** opus
- **files:** [pockets/authorization/internal/logic/authorizersvc/model.go, pockets/authorization/internal/logic/authorizersvc/model_test.go, pockets/authorization/internal/logic/decisionsvc/composite.go, pockets/authorization/internal/logic/decisionsvc/composite_test.go]
- **verify:** `cd pockets/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** In `authorizersvc/model.go`: add `LookupRequest{Principal,
  Permission, ResourceType, Limit}` beside `CheckRequest` with `Validate()`
  (reuses the existing principal/ref validation; negative `Limit` wraps
  `sdk.ErrInvalidInput`), add `Truncated bool` to `LookupResult`, and write the
  doc comments carrying design decisions 7–9 verbatim in spirit: "Limit 0 = the
  MaxLookupResults budget ceiling (today's behavior)", the deliberate `crud`
  divergence, Limit-never-weakens-the-budget, no-cost-reduction honesty,
  `Unrestricted` ignores Limit, and the `After`-deferred marker referencing
  issue #22 with its store-port price. In `decisionsvc/composite.go`: add
  `(*Composite).LookupResourcesIn` — validate, dispatch to
  `c.owner(...).LookupResources` unchanged, then truncate to the sorted prefix
  and set `Truncated`; `kind` interface untouched. Tests: `Validate` rejects a
  negative Limit with `errors.Is(err, sdk.ErrInvalidInput)`; Limit < len →
  sorted prefix + `Truncated` true + non-nil IDs; Limit == len and Limit > len
  → full list, `Truncated` false; Limit 0 → today's behavior; `Unrestricted`
  passes through untouched with Limit set; roles-owned and relationship-owned
  pairs both truncate through the one composite body.

### task-4: Facade LookupResourcesIn + overflow-vs-Limit proof

- **depends_on:** [task-3]
- **model:** opus
- **files:** [pockets/authorization/authorization.go, pockets/authorization/authorization_test.go, pockets/authorization/budget_test.go]
- **verify:** `cd pockets/authorization && go build ./... && go test ./... && go vet ./...`
- **description:** Add the facade `type LookupRequest = authorizersvc.LookupRequest`
  alias (beside the existing `LookupResult` alias) and
  `(*Service).LookupResourcesIn` — nil-decider `ErrNoDecisionKind`, then pure
  delegation, mirroring `LookupResources`' doc-comment style with the decision-7/8/9
  semantics. Facade tests: happy-path truncation through the public API; the
  budget proof — a wiring whose grants exceed a small custom
  `Limits.MaxLookupResults` still returns `ErrEvaluationLimit` when called with
  a tiny `Limit` (Limit must NOT weaken the budget); `ErrNoDecisionKind` on an
  unwired decider; classic `LookupResources` never sets `Truncated`.

### task-5: README — gate docs, lookup docs, CheckExplain recipe

- **depends_on:** [task-2, task-4]
- **model:** opus
- **files:** [pockets/authorization/README.md]
- **verify:** `cd pockets/authorization && go build ./... && go test ./... && go vet ./...` (doc-only; confirms no accidental code drift)
- **description:** Three edits. (1) In "### The `RequirePermission` middleware
  gate": document `RequireAnyPermission` — the GateSpec shape, both-models
  registration validation, the fail-closed ladder including type-agreement,
  short-circuit, the order-is-outcome-affecting correction, the
  `MaxBatchSize` alternatives cap and N-Check budget multiplication, duplicate
  alternatives legal, and the same PURE-Check no-bypass stance. (2) In the
  enumeration docs ("Stop 3 — enumeration" in the wiring page, plus the
  `LookupResources` prose): add `LookupResourcesIn` with Limit semantics per
  decisions 7–9 (budget ceiling wording, `Truncated`, overflow-beats-small-Limit,
  Unrestricted-ignores-Limit) and the `After` deferred marker naming the
  store-port price and issue #22. (3) The #22 secondary docs item: a short
  "explain a decision" recipe over the existing `CheckExplain` in the flagship
  wiring section (near the Stops) — call `CheckExplain`, read
  `result.ReasonCode` and walk the bounded `Explanation` steps, with the
  fail-closed `err` handling shown; today it is discoverable only from source.

### task-6: Full-sweep verification + release-train record

- **depends_on:** [task-5]
- **model:** opus
- **files:** [.claude/plans/authorization-gates-and-lookup.md]
- **verify:** `make check && make guard`
- **description:** Run the full cross-module sweep and the layering guards
  (expected: zero guard changes — no new boundary). Append an execution-status
  note to this plan recording: merged-not-tagged, `pockets/authorization/v0.7.0`
  tags jointly with Plan C (#20) once both are merged (minor, additive-only;
  poll the module proxy `.info` URL before any downstream `go mod tidy`). Do
  not tag in this plan.

## Sequencing

Two independent tracks — the gate track (task-1 → task-2) and the lookup track
(task-3 → task-4) — join at docs (task-5) and the sweep (task-6). task-1 and
task-3 touch different regions of `authorizersvc` (middleware.go vs model.go)
and different regions of `decisionsvc` land in task-2 vs task-3
(middleware.go vs composite.go); run them sequentially anyway (default) —
task-1, task-2, task-3, task-4, task-5, task-6 is the straight line.

## Consultation notes

`lead-backend-engineer` reviewed the sketch: **ship-with-edits**. Adopted from
the review: the GateSpec/resolver type-agreement fail-closed rule (decision 3);
the `MaxBatchSize` alternatives cap and indexed panic messages (decision 2);
order-is-outcome-affecting doc correction (decision 5); "Limit 0 = the budget
ceiling" wording + the `crud.ListRequest` divergence note + the
`sdk.ErrInvalidInput` sentinel (decision 7); `LookupResult.Truncated`
(decision 9); the priced `After` deferral (Out of scope); the full struct-input
`LookupResourcesIn`/`LookupRequest` shape over `LookupResourcesWith` /
`LookupResourcesPage` (decision 6); "no new boundary, no new guard" stated on
the record; the added tests for type mismatch, alternative-2-error-after-deny,
and overflow-beats-small-Limit. Declined (final call, per the owner's
exactly-per-issue framing): the `ErrAlternativeNotApplicable` skip sentinel —
surfaced as an open question instead of shipped; the triplicated-validation
collapse — deliberate non-refactor, surgical diffs.

## Open questions

- **Not-applicable alternatives (owner ruling requested before tagging
  v0.7.0):** #19's own gps-360 sites include a row-derived resolver that may
  legitimately have no resource to name; under v1's strict fail-closed, that
  alternative erroring 500s the request instead of falling through, which may
  push those exact sites back to hand-rolled ORs. If real adoption needs it, an
  additive `ErrAlternativeNotApplicable` sentinel (`errors.Is`-checked in the
  loop; all-skip → 403; any other error still fails closed) fits in this train
  or a fast-follow. Default if unruled: ship v1 as specified, document the
  limitation.

## Recommended reviews

- **product-manager** — scope discipline: two issues + a docs item in one
  additive release; the Limit-only ruling's honest cost note; the joint-train
  coupling with Plan C.
- **lead-backend-engineer** — post-implementation pass on the gate ladder and
  the truncation placement it reviewed at sketch stage.
- **platform-sre** — the joint v0.7.0 tagging procedure, the N-Check budget
  multiplication as an operational seam, and the `MaxBatchSize` cap.

## Notes

- Naming discipline holds: authentication/authorization, never authz/authn.
- `pockets/authorization/go.mod` must show zero diff at the end of the train —
  a cheap tripwire that the release stayed core-only.

## Execution record (2026-08-31)

**EXECUTED, MERGED-NOT-TAGGED.** All six tasks landed on
`authorization-gates-and-lookup`; `make check && make guard` green (21 guards,
zero guard changes — no new boundary). `pockets/authorization/go.mod`/`go.sum`
show zero diff.

**Owner amendment (ruled after the plan was written).** The Open question is
CLOSED IN: `RequireAnyPermission` ships with the `ErrAlternativeNotApplicable`
skip sentinel — an `errors.Is`-matching resolver error skips that alternative
as a deny does; all-denied-or-inapplicable is 403; any other resolver error and
every `Check` error still fail the whole request closed; the
resolved-type-agreement check stays fail-closed. Defined in
`authorizersvc/middleware.go`, re-exported from `codes.go`. This supersedes the
Out-of-scope bullet "No 'not applicable…' resolver sentinel in v1" and risk 1's
"if the owner rules it in".

**Release.** DONE — `pockets/authorization/v0.7.0` @ `e95c82b` tagged jointly with
Plan C (#20) — minor, additive-only. That train pins `sdk v0.7.0`; the sdk pin
was deliberately not touched here. Poll the proxy `.info` URL before any
downstream `go mod tidy`. Do not tag from this plan.
