# SDK audit S1: root errors, context, and write faults

Status: REVIEWED, follow-ups implemented — 2026-09-09, original framework HEAD `bbf4cc86`.
Parent: [framework-audit.md](framework-audit.md). This record describes the
original read-only review; validation follow-up is recorded below. S6 also
corrected IsExpected's misleading HTTP prediction without changing its behavior;
see [web-cleanup.md](web-cleanup.md).

## Assessment

Keep the root implementation for now. Its sentinel errors, private context keys,
and small validation/stale-write types provide useful shared vocabulary with
direct control flow. No runtime defect was established in the reviewed root
functions. This is a bounded assessment, not a claim that the SDK or its
consumers are fully correct.

Read all three root source files and their tests. Traced relevant paths through
`sdk/foundation/web/{errors,middleware,readbody}.go`,
`sdk/foundation/logging/handler.go`, and `sdk/capabilities/tracing/middleware.go`.
Adjacent packages were sampled, not audited in full.

## Consumer evidence

Reference roots are recorded in the parent plan. Manifest versions below are
declared requirements, not verified effective build resolutions.

| Consumer | SDK requirement | Relevant evidence |
|---|---|---|
| Segovia v2 | v0.8.0 | `internal/outbound/domains/dashboards/dashboards.go:mapError` lifts SDK categories into domain errors; domain/inbound code handles them without depending on driver error types. |
| coordination-hub | v0.7.0 | `cmd/server/main.go` composes request IDs and logging; `internal/inbound/domains/platform/campaigns.go` supplies a specific missing-name response before generic error mapping. |
| gps-360-go | v0.7.1, local SDK replacement declared | Directory domain `write.go` embeds `sdk.ValidationError`; outbound `audit.go` returns `sdk.StaleError`; inbound `writes.go:respondWriteError` renders both. This checkout also has vendor files; effective vendor/workspace resolution was not tested. |

The original's `sdk/web/errors.go` already has `FieldErrors`, while its
`telemetry/context.go` depends directly on OpenTelemetry. Today's root context
accessors keep those IDs available without a vendor dependency. No broader
judgment about the original implementation follows from this sample.

## Findings and recommendations

### S1-1: Correct the error-classification documentation

**Confirmed documentation defect; low priority.** `sdk/errors.go:IsExpected`
correctly reports whether an error wraps one of the SDK's known sentinels. Its
comment additionally promises that false means `web.ErrFromDomain` will return
HTTP 500. A `*http.MaxBytesError` disproves that: `IsExpected` returns false and
the web mapper returns 413. Web-specific public error wrappers are another
reason classification and transport policy must remain separate.

Recommend documenting the narrow classification contract and removing the HTTP
prediction. Preserve the function and behavior: the Firestore connector's
`errors.go:MapError` uses it to preserve known SDK errors. No breaking change is
needed. Do not teach callers to use it as a universal logging or retry policy.

### S1-2: Evaluate the three validation collectors together in S2/S6

**Design question; medium priority, not a confirmed runtime defect.** The
framework asks developers to distinguish `validation.Errors`,
`web.FieldErrors`, and `sdk.ValidationError`. They carry different information
and require different mapping choices. `web/errors.go` documents a three-way
selection rule, but their behavior makes the choice consequential:

| Error created | `IsExpected` | `ErrFromDomain` status | `ErrValidation` status |
|---|---|---|---|
| `validation.Errors` with one error | false | 500 | 400 |
| `web.FieldErrors` with one field | false | 500 | 400 |
| `sdk.ValidationError` with one violation | true | 400 | 400 |
| `http.MaxBytesError` | false | 413 | 413 |

These results were exercised against the current SDK. The first two are not
interchangeable with the third. Investigate whether one transport-neutral
structured field-error type plus transport rendering would reduce decisions
for callers. Also consider simply retaining a general error accumulator and
consolidating only the two structured collectors. Compare both with keeping the
present design after tracing validation and decode consumers.

Any consolidation must account for JSON response shape, field codes, safe
public messages, `errors.Is`/`errors.As`, and existing `Validate()` signatures.
Do not silently promote arbitrary internal error text to public field messages.
An implementation proposal waits for that review; no new abstraction is proposed
as a foregone conclusion.

### S1-3: Separate current contracts from historical explanations

**Documentation simplification; low priority.** Root comments mix useful API
contracts with promotion dates, past plan IDs, and long defenses of placement.
`faults.go` also describes an empty collector as a typed-nil-equivalent value;
the example actually holds a non-nil pointer to an empty collector. Its explicit
nil return is correct, but the explanation obscures the simple rule.

Prefer concise current behavior and examples in Go documentation, with historical
rationale linked from architecture/decision records. Keep meaningful obligations
such as caller-safe messages, pointer error types, and timestamp precision. This
is editing for accuracy and comprehension, not a target to minimize comments.

## Verification

Passed with Go `go1.26.1 darwin/arm64`:

- From `sdk/`: `go build ./...`, `go test ./...`, `go vet ./...`.
  The full SDK test baseline used cached results for 22 packages; five exported
  test-helper packages have no direct test files.
- From the repository root: `make guard`, all 23 configured guards passed.
- From `sdk/`: `go test -count=1 .` freshly exercised all root tests.
- From `sdk/`: `go test -count=1 ./foundation/web ./foundation/logging ./capabilities/tracing -run 'Test(ErrFromDomain|ErrValidation|ValidationError|EmptyValidationError|ErrStale|TracingHandler|RequestID|Middleware)'`
  freshly exercised relevant error mapping and ID/logging integration tests.
- From `sdk/`: `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache GOWORK=off go list -deps -test -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...`.
  Every non-stdlib dependency was inside SDK, including test dependencies, for
  the current platform/default build configuration. The first attempt using
  the default cache hit a sandbox denial; a temporary cache resolved it.
- From `sdk/`: `go run /tmp/gopernicus-audit-s1.aMLwCQ/probe.go` produced the
  table above and exercised an actual `httptest.ResponseRecorder` response:
  a wrapped `sdk.Refuse` yielded HTTP 400 with `validation_failed`, the field
  name/message, and `required` code. The temporary probe can be reconstructed
  by creating one error of each listed type and passing it to both mappers.

Not run: full `make check`, generated-artifact checks, other module test suites,
live datastores, consumer apps, or browser flows. Root response mapping was
exercised in process; no end-to-end consumer behavior is claimed. No unresolved
verification failure within the executed scope.

## Next step

S2a validation subsequently justified and implemented one shared collector:
[validation-consolidation.md](validation-consolidation.md). This resolves S1-2
and the misleading `ValidationError` comments from S1-3. The SDK README's G12
test-exemption mismatch is also corrected. S1-1 (`IsExpected`'s HTTP prediction)
was subsequently corrected during S6; the rest of the root documentation was
not broadly rewritten. Follow the parent index for the current slice (S7 after
S6 implementation), preserving concurrent Firestore work.
