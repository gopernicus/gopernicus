# Host-controlled authorization denial responses and decision logging

Status: COMPLETED — implemented and verified; uncommitted and unreleased. Starting main f1dbd30d9049f3835bf3aaecbafcaa9e4e32cacc.

## Goal and scope

Hosts reuse the framework's Require predicates and coherent evaluation while
choosing denied HTTP presentation and enabling DEBUG decision logging. Remove
the reasons to duplicate the predicate compiler or wrap every read just to log.
Preserve owner planning edits, existing denial defaults, snapshot/budget/cache
semantics and error behavior. No dependency/schema/store changes, host edits,
commit, push or release in this task.

## Design

### H1 — HTTP denial presentation

Add Require(predicate, opts ...RequireOption) and
WithDeniedHandler(http.Handler), owned by inbound/http. Default remains the
existing JSON 403. A host can provide http.NotFoundHandler or its own JSON/HTML
handler per mounted policy. Invalid nil options or nil/typed-nil handlers panic
at mount with the existing configuration-error posture. Options are resolved
once at mount and never mutate Adapter or shared option state.

Invoke the custom handler only for a final nil-error denied result, after the
request cancellation check. It receives no next handler. Allowed requests,
missing principal (401), budget exhaustion (503), and other errors (500) retain
their existing behavior. This is response adaptation; the hook performs no
framework authorization reads. Keep the narrow two-method DecisionService.

A separate host clarification is pending: whether one route needs 404 for failed
visibility but 403 for denied action. A route-local override alone does not
classify those cases. Do not infer classification from CheckResult.Reason or
stack independent middleware to claim a shared snapshot. Any such extension
requires a separately explicit design after clarification.

### H2 — Passive decision logging

Use the existing slog seam instead of adding an observer/event API solely for
logging. Add decisions.WithLogger(*slog.Logger); nil captures slog.Default at
construction. Root forwards its existing borrowed logger to the decision service.
The host owns level, formatting, filtering, contextual correlation and redaction.
The specialized DiagnosticObserver is unchanged.

Emit one DEBUG record per completed public decision-service operation, including
Check, CheckBatch, FilterAuthorized, Evaluate/EvaluateResolved, Explain, lookup
entry points and caller-bound With variants. Validation/no-model/empty returns
and failures must be observed too. Private unlogged implementations prevent
public aliases from doubling events. No request-context suppression flag, recursive
logging, cache-attempt records, extra tuple reads or automatic explanations.

Check Enabled(LevelDebug) before timing/attribute construction. Record operation,
final outcome, elapsed duration and useful bounded request/result metadata.
Errors are an error outcome, never a denied zero value. Aggregate batches and
lookups; do not dump expressions, raw tuple collections, result IDs, traces or
unbounded error strings. Bound views report evaluation, not transaction commit.
Schema inspection/validation-only APIs and the separate roles/relationship
listing services are outside the decision-read logging surface.

### H3 — Documentation and review

Document host 404/custom response usage and DEBUG slog configuration, behavior
boundaries, and logging separate from durable mutation audit. Update current
release/audit notes as unreleased. Named architecture review supports the two
small seams; complete implementation review with the named backend reviewer.

### H4 — Verification

Tests exercise real middleware with the memory authority: default403/custom404,
custom bodies, allow/deny/authentication/error/cancellation separation, one
snapshot, option isolation, mount validation and concurrent requests. Logging
tests cover one record across delegation/cache fallback/lookup retries, final
errors, disabled DEBUG, aggregate output and no extra authority reads. Root
logger wiring is exercised through a real request.

Run goimports, authorization go build/test/vet and focused race tests, make guard
and sanitized 42-module make check. Use env -i PATH/HOME/TMPDIR, GOENV=off,
GOTOOLCHAIN=local, writable /tmp GOCACHE; remove inherited datastore settings.
Public Go dependency downloads may be used when workspace caches lack the
released pins. No live datastore suite or full benchmark rerun is required for
presentation/logging-only changes; measure disabled/enabled logging locally if
implementation introduces material hot-path work. Preserve durable concise
verification evidence and copy the completed plan to plans/.

## Tasks

- [x] H1 denial response hook and HTTP behavior tests.
- [x] H2 decision logging and root wiring/tests.
- [x] H3 current docs and independent review.
- [x] H4 final verification and execution record.

## Execution record

- H1 adds one per-mounted-policy handler option, preserving the narrow service
  interface and one expression evaluation. Real memory-backed middleware tests,
  concurrency/race tests and an external-package example cover the response seam.
- H2 uses the existing slog setting and instruments 17 public decision operations
  through private unlogged implementations. Nine focused test functions cover
  retries, fallback, delegation, independent nested checks, metadata bounds,
  disabled logging, final failures and root logger capture through real HTTP.
- Named architecture review supports the HTTP/domain boundaries and native slog
  seam. Named backend review found no correctness blockers. Its ambient-transaction
  wording correction was applied: evaluation completion does not necessarily
  close a caller-owned transaction.
- Core build/vet and focused logging race passed; the complete authorization
  core race suite and HTTP race suite passed. Architecture guards passed.
- Initial verification setup issues were corrected: one HTTP test assumed JSON
  field order/charset, the first guard command omitted the writable GOCACHE,
  and a real HTTP test needed sandbox allowance for an owned loopback listener.
  Corrected checks passed; no authority/service configuration was changed.
- Final 42-module make check passed, including build/test/vet, generated-file
  checks and guards. The full core race suite passed. Nineteen changed Go files
  are goimports-clean; diff check passes.
- Focused benchmarks passed: nine workloads, three one-second samples each.
  Exact-role evaluation median: unlogged control 670.6 ns, DEBUG disabled
  705.2 ns, DEBUG enabled 1299 ns with JSON discarded. Disabled DEBUG adds
  no allocations. HTTP medians: global 808.5 ns, two roles 1416 ns, mixed 2329 ns.
- Benchmark source was identical to the final verified Go inventory. The full
  owned cache/store matrix was not rerun; its explicit workload selection is
  unchanged and does not include the standalone logging benchmark.
- Durable evidence is plans/authorization-host-policy-verification.json; temporary
  full logs are under /tmp/gopernicus-authorization-host-policy.
- No optional clarification arrived about same-route visibility/action status
  selection during implementation. The completed scope provides a per-policy
  response hook and explicitly leaves that additional distinction unimplemented.
  Existing default403 policies preserve their behavior.
- Owner planning edits and pre-existing Python cache files remain untouched.
  No dependency, schema, external host, commit, push or release changed.
