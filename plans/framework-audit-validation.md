# SDK audit S2a: validation

Status: IMPLEMENTED AND VERIFIED — 2026-09-09.
Parent: [framework-audit.md](framework-audit.md). Follows
[S1 root review](framework-audit-sdk-root.md). Implementation and verification:
[validation-consolidation.md](validation-consolidation.md).
The original review used HEAD `bbf4cc86`; concurrent Firestore work advanced
HEAD to `9339c574` without changing the reviewed SDK source. The findings below
record that pre-change behavior, followed by the implementation resolution.

## Assessment and scope

Keep explicit validator functions and ordinary `Validate() error` methods.
The main simplification is one structured field-error contract across DTO and
domain validation, using the existing `sdk.ValidationError`/`sdk.Violation`.
Different places can enforce different rules without needing different
accumulators for the resulting field problems.

Reviewed all source/tests in `sdk/foundation/validation`, root fault types,
`web.FieldErrors`, `ErrValidation`, `ErrFromDomain`, and `DecodeJSON`. Inspected
`ReadBody` as a consumer of the shared fault vocabulary; this is not a complete
body-reader or web audit. Conversion/slug remain unreviewed.

The named `lead-backend-engineer` supplied a read-only design critique. Its
recommendation also favors one structured carrier and warns against flattening
arbitrary error messages or substituting `errors.Join` without preserving all
field violations.

## Findings

### V1: Pointer-form decoding silently skips validation

**Confirmed defect, medium priority.** `sdk/foundation/web/request.go:54` checks
`any(&v)` for `Validate() error`. With `DecodeJSON[*Request]`, that value is a
`**Request`, which does not have the request's validation method. No public
type constraint or documented prohibition excludes pointer type arguments.

Reproduced with a request whose `Validate` rejects an empty name:

| JSON | `DecodeJSON[Request]` | `DecodeJSON[*Request]` |
|---|---|---|
| `{}` | validation error | succeeds; empty name |
| `{"name":"valid"}` | succeeds | succeeds |
| `null` | validation error on zero value | succeeds; nil pointer |

No pointer-form decode calls were found in the sampled framework/examples or
three named consumers. This is an exposed API defect, not a demonstrated live
incident. Fix independently of collector consolidation: explicitly support or
reject pointer type arguments, address `null` deliberately, and prove that
validation runs exactly once. Carry this finding into S6.

### V2: Length validators count bytes while claiming characters

**Confirmed defect, medium priority.** `validate.go:95,106,115,126,353` use
`len(string)` for `MinLength`, `MaxLength`, pointer counterparts, and
`PasswordStrength`'s minimum. The documented/messages' unit is characters.

Reproduced: `MinLength("name", "é", 2)` passes and
`MaxLength("name", "é", 1)` fails. `PasswordStrength("password", "Äa1€!")`
passes its eight-character minimum with five runes/eight UTF-8 bytes.

Recommend defining text length in Unicode code points and using
`utf8.RuneCountInString`; document that unit precisely, including that combining
sequences can contain several code points. Byte/storage limits should be
explicitly named. This changes acceptance for non-ASCII inputs, so record it
as behavior compatibility work. Existing tests cover ASCII length cases only.

### V3: Scalar and pointer minimum-length behavior diverges

**Confirmed inconsistency, medium priority.** The package says empty strings
pass non-required string validators. For `empty := ""` and minimum 3:

- `MinLength("name", empty, 3)` passes.
- `MinLengthPtr("name", &empty, 3)` fails.
- `IfSet(&empty, func(v string) error { return MinLength("name", v, 3) })` passes.

Recommend preserving presence as a separate check and making pointer helpers
skip nil, then delegate to their scalar counterpart. Retaining these short
convenience functions is reasonable; removing them would force callbacks at
call sites without a demonstrated clarity benefit. Numeric zero and empty
collections need their own explicit rules; the package's blanket empty/nil
claim must be scoped to optional string validators.

### V4: Three collectors create lossy and inconsistent boundaries

**Confirmed behavior/design problem; consolidation recommended.**

| Current representation | Information retained by returned error | Web behavior |
|---|---|---|
| `validation.Errors` | Joined text; loses original `errors.Is`/`errors.As`, fields and codes | Generic domain mapper returns 500 |
| `web.FieldErrors` | Fields and messages, no SDK error category | Requires validation-specific mapper for 400 |
| `sdk.ValidationError` | Fields, messages, optional codes, `sdk.ErrInvalidInput` | Both mappers preserve field detail and return 400 |

`validation/errors.go:30` uses `errors.New(strings.Join(...))`. Passing an SDK
validation error through this collector demonstrably loses its classification
and structured data. That behavior is not promised to preserve wrapping today,
so treat migration as a semantic change, not an invisible internal refactor.

The documented placement rule in `web/errors.go:68` is already contradicted by
`ReadBody`: its transport field checks produce `sdk.ValidationError`.
`web.FieldError` can remain the wire representation; its accumulator duplicates
the shared collector rather than protecting a transport boundary.

Do not fix this with `errors.Join` alone: joining two `sdk.Refuse` results and
passing them to `ErrFromDomain` renders only the first violation because the
mapper uses one `errors.As` match. Also do not add an unrestricted `AddErr(error)`
that copies arbitrary errors into public messages. Validation data and unexpected
dependency errors must remain distinguishable.

### V5: Generic format helpers need narrower documented meaning

**Design/documentation follow-up, low priority.** `Email` accepts display-name
syntax (`Alice <alice@example.com>`), while authentication's
`domain/identifier/normalize.go` explicitly rejects that syntax. Both behaviors
can be legitimate; the generic helper should say which syntax it checks and
must not be presented as equivalent to authentication identifier validation.

`PasswordStrength` embeds an eight-character composition policy. Authentication
already owns a different policy (`internal/logic/authsvc/service.go`: 15–64 code
points plus its other checks). Recommend retiring the generic strength-policy
helper in a planned breaking cleanup rather than introducing another configurable
policy engine. This is a placement/consistency recommendation, not a full review
of authentication's policy or cryptography.

`custom.go` is an empty package file telling consumers to add validators to the
SDK source. Move that example into documentation describing app-local functions.
Its removal would change no exported API.

## Consumer evidence

Paths are relative to the consumer roots in the parent plan. Vendor and generated
view files were excluded. No Go import of `sdk/foundation/validation` was found
in framework runtime/example/scaffolder code or in Segovia v2, coordination-hub,
or gps-360-go. This does not establish absence of other external consumers.

- GPS 360's `internal/inbound/domains/echo/notes.go:NoteRequestBody.Validate`
  uses `web.FieldErrors` for required/max-length note checks. Its domain
  `internal/logic/domains/echo/note.go` performs equivalent checks with
  `sdk.ValidationError`/`sdk.Refuse`, including for durable worker input.
  These entry points need validation, but do not need different error carriers.
- GPS 360's directory domain embeds `sdk.ValidationError`; its HTTP write
  helper already relies on the shared structured response. Preserve its field
  names, optional machine codes, and ordinary contextual wrapping.
- Coordination Hub's `internal/inbound/domains/coordination/items.go` documents
  a deferred move from first-error responses to field lists. Changing the
  collector alone must not silently change that user-facing form behavior.
- No production `validation.Errors` consumer was found in these searches.
  Within the framework, `web.FieldErrors` examples/tests are the main migration
  sites; authentication's similarly named view-model field is a separate type.

## Original proposed direction and migration

1. Fix V1–V3 with focused regressions independently of a public API redesign.
   Agree explicit pointer/null and text-length behavior in the implementation
   plan. Preserve ordinary supported decode use and existing response envelopes.
2. Make `sdk.ValidationError` the single collector for public field problems.
   DTOs and domains both return it through `Validate() error` or domain methods.
   Keep HTTP rendering in `web`; keep parsing versus domain rule ownership.
   Optional codes allow existing sentence-only fields to keep their JSON shape.
3. Retire the separate `web.FieldErrors` accumulator and `validation.Errors`
   contract through a deliberate API migration. Preserve expected 400 response
   bodies, generic handling of unexpected failures, and wrapped field detail.
4. Decide helper signatures before implementing their migration. A small,
   typed direction is for helpers to return optional `*sdk.Violation` data,
   collected by one `AddViolation` method on `sdk.ValidationError`. This adds
   no error type, interface, reflection, or rule DSL. Existing direct error
   returns, `IfSet` callbacks, and
   custom helpers would need changes. Keeping helper `error` signatures during
   the initial collector migration is the less disruptive alternative; avoid
   building a generic error-tree framework just to preserve every call shape.

The useful invariant is: callers decide which rules to run, then return one
structured result. Rule ownership stays explicit, and custom domain refusals
continue to use `Add` or `sdk.Refuse`. These were recommendations at the review
stage; the owner subsequently authorized implementation.

## Original audit verification

- Fresh pass from `sdk/`:
  `go test -count=1 ./foundation/validation ./foundation/web -run 'Test(Required|Min|Max|OneOf|Email|UUID|URL|Matches|Range|Positive|NotEmpty|Slug|Password|IfSet|Errors|Decode|ErrValidation|ValidationError|ErrFromDomain|EmptyValidationError)'`.
- Executed `go run /tmp/gopernicus-validation-audit.bmtsfe/probe.go` from `sdk/`.
  It reproduced V1–V4 and the email syntax example using current code, including
  `httptest.NewRequest` for actual decode behavior. Both positive and negative
  pointer/value inputs were tried; the table above records the results.
- Existing tests pass despite the defects because they do not cover pointer
  generic inputs, non-ASCII length boundaries, or present-empty pointer parity.
- SDK build/test/vet and 23 guards passed in S1. They were not repeated in this
  documentation-only pass. No consumer apps, browser, datastore, or live services
  were run; no claim of end-to-end consumer impact. No environment blocker.
- This pass changed only this record and `plans/framework-audit.md`.
  Existing S1 documents remain part of the audit; active Firestore work is
  unrelated and preserved.

## Implementation resolution

The owner's request to make these updates authorized the bounded
[implementation plan](validation-consolidation.md).

- **V1 fixed:** `DecodeJSON` supports value and pointer targets, validates once,
  and rejects top-level JSON `null` before validation. Ordinary supported JSON
  shapes and nested nulls remain covered. S6 still needs its broader web review.
- **V2 fixed:** string bounds count Unicode code points; combining sequences
  are explicitly documented and tested. The fixed strength-policy helper is
  removed instead of keeping another password policy in the SDK.
- **V3 fixed:** optional pointer checks delegate to the scalar rules after the
  nil check, including present-empty minimum-length behavior.
- **V4 simplified:** helpers return optional `*sdk.Violation` data and
  `sdk.ValidationError.AddViolation` collects it. Removed `validation.Errors`
  and `web.FieldErrors`; retained `web.FieldError` for JSON responses. Tests
  preserve ordinary wrapping, field order, optional codes, and both mappers.
- **V5 addressed in the SDK:** documented email syntax, removed
  `PasswordStrength` and empty `custom.go`, and documented app-local custom
  checks. Authentication's own policy remains unaudited.

Live documentation, compiled examples, and the standalone
[`AUDIT.md` migration entry](../AUDIT.md#audit-001-validation-consolidation)
describe the new contract; `RELEASING.md` links there. GPS 360's known old-collector call site
still needs migration before adopting a release; no consumer repository or pin
was changed. See the implementation plan for exact checks and remaining gaps.

Next: continue S2 conversion/slug. The broader SDK,
web, authentication, and consumer apps are not marked audited by this change.
