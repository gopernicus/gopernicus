# Validation: one collector and predictable checks

Status: COMPLETE — 2026-09-09. Authorized by the owner's request to make the
updates from [the validation audit](framework-audit-validation.md).

## Context

Field validation currently has three error collectors with inconsistent HTTP
handling. The audit also reproduced byte/character length disagreement,
pointer/scalar empty-value disagreement, and skipped validation for pointer JSON
targets. Consolidate the field-error contract and fix those observable defects.

## Goal

Explicit validators produce structured field problems; one SDK collector returns
them through ordinary Go errors, and both web mapping paths retain every field.

## Decisions

- `sdk.ValidationError` remains the sole field-error collector. Add
  `AddViolation(*sdk.Violation)`, skipping nil and copying the supplied value.
  Keep `Add(field, code, message)`, `Err`, and `sdk.Refuse` for custom rules.
- Validators return optional `*sdk.Violation` data, not errors. Keep existing
  field/value arguments except `PasswordsMatch`, which gains an explicit first
  `field` argument so the caller owns the confirmation field name. Update
  `IfSet` callbacks to return that same typed result. No `Violation.Error`
  method and no arbitrary-error-to-public-message conversion.
- Existing codes: required/presence rules use `CodeRequired`; format, length,
  range, and choice checks use `CodeInvalidFormat`. Preserve existing sentences.
  Callers needing more specific codes can author their own violations.
- Remove `validation.Errors` and `web.FieldErrors`, including their duplicate
  accumulator tests. Keep `web.FieldError` as the response representation.
  Preserve sentence-only field JSON when a violation's code is empty.
- String lengths count Unicode code points. Optional pointer helpers skip nil
  then delegate to scalar checks; `RequiredPtr` still rejects nil/blank.
- Remove the fixed-policy `PasswordStrength` helper as recommended in the
  audit; authentication already owns password policy. Keep `PasswordsMatch`
  as a field-named equality check. Remove empty `custom.go`; document app-local
  custom checks alongside typed validator usage.
- `DecodeJSON` validates value or pointer targets exactly once, checking the
  address first (preserves pointer receiver mutations for value targets), then
  the value. Reject a top-level JSON `null` before either validation path; do
  not add reflection or restrict otherwise valid JSON arrays/scalars/maps.
- SDK remains stdlib-only with existing inward imports. Preserve default web
  error envelope and unexpected-error redaction. Do not broaden the existing
  ErrValidation plain-error behavior or build error-tree aggregation machinery.

## Out of scope

Changes to consumer repositories, authentication policy, unrelated SDK audit
findings, Firestore work, generated views, dependency pins, tags and publishing.

## Module / API impact

Breaking SDK API migration: old collectors removed, validator result/callback
types changed, `PasswordsMatch` gains a field argument, `PasswordStrength`
removed. `Validate() error`, root fault types, and wire envelope stay available.
Document the migration before release; pre-v1 breaking changes need a deliberate
SDK release per `RELEASING.md`. No tag or version number is selected here.

## Tasks

1. Root and validator implementation: `sdk/faults.go`, `sdk/faults_test.go`,
   `sdk/foundation/validation/{validate.go,validate_test.go}`; remove
   `{errors.go,errors_test.go,custom.go}`. Test Unicode boundaries, nil/empty
   scalar/pointer parity, result field/code/message, collector copy/nil
   semantics, and multi-field typed errors through an ordinary Validate method.
2. Web implementation: `sdk/foundation/web/{request.go,request_test.go,
   errors.go,errors_test.go,web_test.go}`. Remove duplicate collector paths;
   regress value/pointer validation exactly once, receiver mutation, null,
   malformed JSON, ordinary supported JSON targets, and old sentence-only wire
   shape. Exercise JSON requests/responses with `httptest`.
3. Migrate live documentation: `sdk/README.md`, stale comment in
   `sdk/foundation/crud/crud.go`, `workshop/documentation/docs/sdk/{web,foundation}.md`,
   and a surgical upgrade note in `RELEASING.md`. Add compiled SDK examples for
   the new helper/collector pattern. Preserve concurrent edits to shared docs.
4. Verify and close audit records: `goimports` changed Go files; from `sdk/`,
   `go build ./...`, `go test ./...`, `go vet ./...`; root `make guard`, then
   `make check` after checking generated files and dependencies. Run doc checks
   through existing pnpm scripts if the local toolchain is available. Record
   exact outcomes/skips, changed files and migration instructions.

## Verification and work ownership

The named implementer owns task 2 only. Parent owns root, validator, documentation,
integration proof, final verification, and audit records. Shared source state
allows web tests to use the added root API once ready. Never revert concurrent
changes. Initial HEAD `9339c574`; user work includes Firestore and shared release,
architecture, README, Makefile, and workflow files. No SDK source was dirty at
start. Recheck before close; use `git diff` scoped to this work.

## Execution record

Implementation follows the decisions above. No new dependency, generic rule
engine, or error-tree traversal was added. All framework call sites and live
documentation using the removed APIs were migrated; historical plans retain
their original evidence.

Changed files owned by this work:

- Root: `sdk/faults.go`, `sdk/faults_test.go`.
- Validators: `sdk/foundation/validation/validate.go`, `validate_test.go`, new
  `example_test.go`; removed `errors.go`, `errors_test.go`, and `custom.go`.
- Web: `sdk/foundation/web/request.go`, `request_test.go`, `errors.go`,
  `errors_test.go`, `web_test.go`.
- Documentation: `sdk/README.md`, the stale collector comment in
  `sdk/foundation/crud/crud.go`, `workshop/documentation/docs/sdk/foundation.md`,
  `web.md`, and only the new validation migration section in `RELEASING.md`.
- Plans: this file plus `plans/framework-audit.md`,
  `plans/framework-audit-validation.md`, and `plans/framework-audit-sdk-root.md`.

Verification:

- Demonstrated failing regressions before fixing Unicode minimum/maximum
  bounds and present-empty pointer parity; the named web implementer also
  reproduced pointer decoder/null failures before fixing them.
- Passed SDK `go build ./...`, `go test ./...`, and `go vet ./...` and root
  `make guard` (23 configured targets). Changed package tests executed; some
  unrelated package results were cached.
- `goimports` formatted changed Go files; its final `-l` check returned no
  files. Scoped `git diff --check` passed.
- Passed `make docs-build`: existing pnpm scripts ran `tsc --noEmit` and
  `docusaurus build`. Docusaurus printed a non-fatal update-check permissions
  notice after successfully generating the site.
- Passed from `sdk/`:
  `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go run /tmp/gopernicus-validation-flow.go`.
  This temporary composition proof sends real JSON requests through the new
  validators, a DTO's `Validate`, value/pointer `DecodeJSON`, and response
  writers using `httptest`. Both target forms produced 200 for valid Unicode
  input, 400 with both ordered/code-bearing fields for missing-name/invalid-email
  and short-Unicode/invalid-email inputs, and 400 for null/malformed bodies.
  It checked exactly one validation call, preserved `errors.Is`/`errors.As`,
  and identical structured output through both web mappers after wrapping.
- Passed `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check`: build/test/vet
  across all 41 configured modules, template and UI asset drift checks,
  scaffold-cache warming, seven integration-tag and two integration/live-tag
  vet runs (compile-only), and all 23 guards. Log:
  `/tmp/gopernicus-validation-make-check.log`. No generated files changed.
  The initial default-cache run
  stopped on sandbox cache-write denial; using the writable cache passed
  scaffold-cache warming, then hit the sandbox's loopback-listener restriction.
  The successful rerun used the execution allowance required for local test
  listeners. Both environmental failures are resolved; no code check remains
  failing within the executed scope.

External PostgreSQL, Turso, and Firestore service tests are not configured:
`POSTGRES_TEST_DSN`, `TURSO_DATABASE_URL`, `TURSO_AUTH_TOKEN`, and
`FIRESTORE_EMULATOR_HOST` were confirmed unset without reading credentials.
Consumer applications and browser flows were not run. No consumer pins,
datastore schemas, release tags, or publishing changed. Current branch/base
remains `firestore-authorization` / `9339c574`; concurrent Firestore and shared
documentation/workflow edits are preserved.

Next: continue the SDK audit with conversion and slug. Before adopting a future
breaking SDK release, migrate
GPS 360's `internal/inbound/domains/echo/notes.go` and any other external users
of the removed APIs using [AUDIT.md](../AUDIT.md#audit-001-validation-consolidation).

### Consumer-guide follow-up (2026-09-09)

At the owner's request, created root `AUDIT.md` as the standalone, cumulative
guide for implemented breaking audit changes. AUDIT-001 includes validation
API replacements, behavior differences, affected consumer evidence, and upgrade
verification. `RELEASING.md` now links to that entry; the shared audit plan
requires maintaining the guide during future implementation.

This documentation-only follow-up changed `AUDIT.md`, `RELEASING.md`,
`plans/framework-audit.md`, `plans/framework-audit-validation.md`, and this file.
Source/tests were unchanged, so Go and site build checks were not repeated.
Verified the guide against the implemented APIs, checked local links and the
release-note anchor, and ran scoped `git diff --check`. Concurrent work advanced
HEAD to `3606eb9c`; its Firestore/release history is preserved.
