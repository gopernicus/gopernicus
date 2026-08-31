# crud: the write vocabulary — sparse-patch Field[T], the violations fault, the strict body reader, the CAS token

**Status: PLANNED 2026-08-31 — gopernicus/gopernicus#21.** Plan A of the
three-plan batch; ships FIRST as its own release train targeting **sdk/v0.7.0**
(current: `sdk/v0.6.0`). Additive only — no breaking change to any exported
symbol or existing wire body. Origin: gps-360-go slice S1 (directory writes, 29
routes), which built this once as host code; its S2 ruling R1 records that each
domain copies the vocabulary until it is filed upstream — this plan is the
filing, before the copies multiply across five more domains.

The gps-360-go reference implementation
(`internal/logic/domains/directory/write.go`,
`internal/inbound/domains/directory/writes.go`) was **not available locally**
at planning time (`~/code/gps-360-go` does not exist on this machine); this
plan is written from issue #21's own description of it, which names the four
pieces and their semantics precisely. Where the reference and this plan could
diverge (wire key casing, getter names), this plan follows **repo convention
over the reference** and says so.

## Context

Every JSON write slice re-derives the same four pieces: a sparse-PATCH
`Field[T]` representation with an overlay fold; a collect-every-problem
violations error that unwraps `sdk.ErrInvalidInput`; a strict one-object,
declared-keys-only, bounded body reader with typed getters; and a CAS
`expected_updated_at` token whose mismatch is a typed stale error unwrapping
`sdk.ErrConflict` and carrying the current token. Issue #21 files them
upstream. The issue's proposed home ("`web.ErrValidation` learns to render
`*crud.ValidationError`") collides with THE INTRA-SDK IMPORT LAW: foundation
packages are root-only-flat (G12b — `foundation/web` may never import
`foundation/crud`, tests included), and `foundation/crud` may never import
`net/http` (G21, because every store adapter carries crud's imports). This
plan resolves that collision explicitly (Decision D1 below).

## Goal

`sdk/v0.7.0` ships the shared write vocabulary — kernel fault types, `crud`
sparse-patch types, `web` rendering + strict body reader — additive,
guard-clean, and test-pinned, so gps-360-go can retire its directory copy and
resume S2 on the standard.

## Out of scope

- **No generic PATCH endpoint, no reflection-driven struct patching** (issue
  non-goals). The domain still writes its own `XWrite`/`XPatch` and
  validators; upstream owns only the vocabulary they share.
- **No custom JSON marshal/unmarshal on `Field[T]`.** `Field` is a
  domain-layer sparse-write representation built by handlers via the body
  reader's getters, not a DTO decode target. (An `UnmarshalJSON` affordance is
  a possible follow-up; it drags omitempty/marshal asymmetry questions this
  train does not need.)
- **No civil-date type.** `Body.Date` returns a midnight-UTC `time.Time`
  parsed with strict `time.DateOnly` layout. Recorded ruling: when a civil
  date type lands (already deferred at v0.6.0), `Date()` may change or be
  deprecated — a v0.x break we accept now rather than discover later.
- **No changes to `validation.Errors` or `web.FieldErrors`** beyond the
  additive `FieldError.Code` field and the normative "which collector, when"
  doc (D5).
- **No pocket, store-adapter, integration, or example-host retags.** No
  in-repo pocket adopts the vocabulary this train; pins upgrade at hosts via
  MVS.
- **gps-360-go's migration of its directory copy** — downstream/owner work
  (see Downstream notes).
- **No per-call body-limit option on `ReadBody`.** The package const is the
  bound; a struct-options variant waits for a host that needs a different one
  (optional-params-struct-input convention when it comes).

## Decisions

### D1 — the layering resolution: the fault vocabulary is promoted to the root kernel

The issue's mechanism ("web renders `*crud.ValidationError`") is illegal:
G12b forbids `foundation/web` → `foundation/crud` (flat tier, tests
included). The three candidate mechanisms, weighed:

- **(a) Kernel promotion — CHOSEN.** `Violation`, `ValidationError`, and
  `StaleError` move to the root `package sdk`, beside the sentinels they
  unwrap (`sdk.ErrInvalidInput`, `sdk.ErrConflict`). Both `crud` and `web`
  import the root only — legal for every tier, visible to domains, pockets,
  and store adapters alike. This is exactly the promotion test the kernel
  charter describes: vocabulary needed by two foundation packages that may
  not see each other, plus every domain above them. Precedent: the sentinels
  themselves, and `web.ErrFromDomain`'s existing `errors.Is` mapping over
  them.
- **(b) Duck-typed `errors.As` interface in web — REJECTED.** A structural
  target can only be a per-violation getter interface (a shared struct
  re-creates the forbidden edge). That is an unnamed contract with no
  conformance test, where *any* type accidentally satisfying it puts
  domain-authored text on the wire — rejected on security grounds (backend
  lead, concurring).
- **(c) crud owns its own HTTP rendering — REJECTED.** G21 forbids
  `net/http` in crud, and every store adapter would carry the rendering's
  imports.

**Guardrail on the precedent this sets.** This is the first kernel promotion
admitting behavior (an `Add`/`Err` collector), not just values. Task-1 amends
the layering-law section of `sdk/README.md` with the actual admission
criterion — *shared by two or more foundation packages, stdlib-only, zero
transport semantics* — so the next promotion argues against a rule, not
against precedent.

`Field[T]`/`Some`/`Overlay` do **not** move to the kernel: they are pure
generic vocabulary with no boundary-crossing need (web never touches them —
handlers compose `crud.Some` around the reader's plain-typed getters), so
they land in `sdk/foundation/crud` per the issue. No crud aliases of the
kernel fault names (single spelling, despite the `crud.ErrNotFound`
precedent — the alias habit is what produces two-spelling drift).

### D2 — the strict body reader lives in `sdk/foundation/web`

The issue left this open ("web, or crud beside the ListRequest parser"). It
goes in web:

- It needs `http.MaxBytesReader` + `*http.MaxBytesError` so the documented
  413 branch in `web.ErrValidation` keeps firing — G21 forbids that in crud.
- `ParseListQuery` stayed in crud only because `url.Values` let it; a body
  reader has no transport-neutral input that preserves the 413 contract.
- Its sibling `DecodeJSON` already lives in web.

Consequence: the reader's getters return **plain values plus presence**
(`Has`), never `crud.Field[T]` — web cannot import crud. Handlers compose
`crud.Some(body.Str("name"))` under a `body.Has("name")` guard. crud gains
**zero new imports** (its `field.go` imports nothing), so G21 and
store-adapter weight are untouched.

### D3 — `web.ErrFromDomain` recognizes the two new fault types (posture amendment)

`ErrValidation` alone would leave every pocket/handler that responds through
`ErrFromDomain` emitting a fieldless generic 400 — the per-host mapping
boilerplate the issue exists to delete. So `ErrFromDomain` gains `errors.As`
branches for `*sdk.ValidationError` and `*sdk.StaleError`. This amends the
"nothing but SafeDomainError is recognized" leak posture: like
`SafeDomainError`, both new types are **explicit wire-text contracts** — the
whole point of `Refuse` is a caller-facing sentence. Task-3 rewrites the
now-false normative paragraph in `web/errors.go` in the same change, and adds
the counter-rule: `Violation.Message` is caller-facing text only — never
`Refuse(field, code, err.Error())` around a store/driver error.

**Branch order (pinned by test):** `SafeDomainError` → `*sdk.ValidationError`
(guarded `len(Violations) > 0`, else fall through) → `*sdk.StaleError` → the
existing `errors.Is` switch. Documented consequence: a `SafeDomainError`
wrapping a `ValidationError` wins and drops the fields.

### D4 — wire naming is snake_case, per this repo, not the reference's camelCase

gps-360-go's copy used `expectedUpdatedAt`/`organizationId`. gopernicus is
snake_case on the wire everywhere (`created_at`, `next_cursor`, …). This train
ships `expected_updated_at` (request, exported as
`web.BodyKeyExpectedUpdatedAt`, the `crud.QueryKey*` pattern) and
`current_updated_at` (response). Frozen at tag time; gps-360-go's adoption is
a deliberate wire-key change on their side (Downstream notes).

### D5 — three collectors, one rule (doc-only)

After this train there are three accumulators. The normative paragraph
(task-3, in `web/errors.go` near `FieldErrors`, cross-referenced from the
kernel file):

- `validation.Errors` — pure field validators inside domain/DTO helpers;
- `web.FieldErrors` — transport-edge request-shape validation in a DTO's
  `Validate()` (the `DecodeJSON` path), no codes;
- `sdk.ValidationError` — domain-authored refusals that must cross layer
  boundaries (the write vocabulary): carries codes, unwraps
  `sdk.ErrInvalidInput`, rendered by both `ErrValidation` and `ErrFromDomain`.

No fourth collector without amending this rule.

## Schema / datastore impact

None. No SQL, no migrations, no store-adapter changes, no EAV spine impact.
`sdk/foundation/crud` gains **no new imports** (G21 holds; store adapters
carry no new weight). One doc obligation toward stores: `UnknownReference` is
a **domain pre-check artifact** — the pgxdb/turso connectors map FK faults to
a bare `sdk.ErrInvalidReference` with no field name and cannot know domain
field naming. The kernel doc (task-1) states: pre-check for the message, the
FK constraint still guards the race, and the race's fallback is the generic
400 "invalid reference". `UnknownReference(field, id)` echoes an id — restrict
it (doc) to an id the caller supplied in that field, never a server-resolved
one.

## Module / API impact

One module changes: `sdk`. One tag: **`sdk/v0.7.0`** (minor — additive but
materially expands the host contract, per the RELEASING.md bump rule). No
`go.mod` or `go.work` changes anywhere (sdk has no require block; nothing
repins). No sibling retags; pockets/stores/hosts upgrade via MVS when they
choose. Compatibility assertion to verify, not assume: every new symbol is
additive; every new wire field is `omitempty` (existing response bodies are
byte-identical); `examples/*` build unchanged.

**New exported symbols, exactly:**

`sdk` (root kernel — ONE new file `sdk/faults.go`, the kernel's
one-file-per-admitted-vocabulary convention):

- `type Violation struct { Field, Code, Message string }` — no json tags; the
  kernel stays transport-agnostic, web owns the wire shape.
- `type ValidationError struct { Violations []Violation }` with:
  - `func (e *ValidationError) Error() string` — **pointer receiver ONLY**
    (a value-receiver `Error()` makes `errors.As(err, &ve)` silently miss
    value-stored errors → nondeterministic 500s). Format: first violation's
    `field: message`, `" (and N more)"` when N > 0, `"validation failed"`
    when empty — mirroring `web.FieldErrors.Error`.
  - `func (e *ValidationError) Unwrap() error` → `ErrInvalidInput`
    (`IsExpected` true with no `expectedErrors` change).
  - `func (e *ValidationError) Add(field, code, message string)` — the
    collector.
  - `func (e *ValidationError) Err() error` — **explicit `return nil` when
    `len(e.Violations) == 0`** (typed-nil trap; test-pinned).
- `func Refuse(field, code, message string) *ValidationError` — one-violation
  constructor; doc: Message is caller-facing text, never a driver/store
  error string.
- `func UnknownReference(field, id string) *ValidationError` — code
  `CodeUnknownReference`, message naming the id; doc per the pre-check rule
  above.
- Violation-code consts (transport-agnostic strings, shared by domains and
  the reader): `CodeRequired = "required"`, `CodeInvalidType =
  "invalid_type"`, `CodeInvalidFormat = "invalid_format"`, `CodeUnknownField
  = "unknown_field"`, `CodeUnknownReference = "unknown_reference"`.
- `type StaleError struct { CurrentUpdatedAt time.Time }` with pointer-only
  `Error()` (message carries the RFC3339Nano token) and `Unwrap()` →
  `ErrConflict`. Doc pins the comparison contract: domains compare with
  `time.Time.Equal` **at the store's precision** (e.g.
  `Truncate(time.Microsecond)` against Postgres `timestamptz`; turso stores
  text) — never a string compare of formatted tokens. A CAS that never
  matches on one store is worse than no CAS.

`sdk/foundation/crud` (new file `field.go`):

- `type Field[T any] struct { Set bool; Value T }` — zero value = absent =
  leave unchanged.
- `func Some[T any](v T) Field[T]`
- `func Overlay[T any](current T, f Field[T]) T` — the fold: `f.Set ?
  f.Value : current`.
- **Normative doc rule (closes the three-state gap):** a nullable column
  rides `Field[*T]` — absent = `Field[*T]{}` (unchanged), explicit clear =
  `Some[*T](nil)`; a NOT NULL column rides `Field[T]`. Ruled here so five
  domains don't each pick differently — the exact copy-drift #21 exists to
  stop.

`sdk/foundation/web` (edits to `errors.go`, `openapi.go`; new file
`readbody.go`):

- `web.FieldError` gains `Code string \`json:"code,omitempty"\`` (additive;
  existing bodies unchanged).
- `web.Error` gains `CurrentUpdatedAt string
  \`json:"current_updated_at,omitempty"\`` — typed field, deliberately not a
  generic `Details map[string]any` (which would reopen the
  arbitrary-payload-on-the-wire posture D3 keeps bounded). Set only by
  `ErrStale`.
- `func ErrStale(msg string, current time.Time) *Error` — 409, code
  `"stale"`, `CurrentUpdatedAt = current.UTC().Format(time.RFC3339Nano)`.
  Handlers never hand-build the literal.
- `ErrValidation` gains an `errors.As` branch for `*sdk.ValidationError`
  (after the `*http.MaxBytesError` branch, before the `FieldErrors` branch;
  guarded `len > 0`): 400, message `"validation failed"`, code
  `"validation_failed"`, `Fields` mapped Field/Message/Code. `ErrValidation`
  and `ErrFromDomain` render a `*sdk.ValidationError` through **one shared
  unexported helper** so the two bodies cannot drift (parity test).
- `ErrFromDomain` branches per D3.
- `openapi.go` `errorSchema()`: **two** edits — `current_updated_at` on the
  error envelope and `code` on the field-item schema. Nothing fails if
  forgotten; task-3 names them and tests them.
- `const DefaultBodyLimit = 1 << 20` and
  `const BodyKeyExpectedUpdatedAt = "expected_updated_at"`.
- `func ReadBody(w http.ResponseWriter, r *http.Request, keys ...string)
  (*Body, error)` — wraps `r.Body` in
  `http.MaxBytesReader(w, r.Body, DefaultBodyLimit)`; decodes exactly one
  JSON object; **structural failures return a non-nil error and a nil
  Body**:
  - overrun → error wrapping `*http.MaxBytesError` (the existing
    `ErrValidation` 413 branch fires);
  - empty body, JSON `null`, non-object, trailing content → error wrapping
    `sdk.ErrInvalidInput`, sentence first, sentinel last (the `DecodeJSON` /
    `ParseListRequest` posture);
  - any key not in `keys` → `*sdk.ValidationError` with one
    `CodeUnknownField` violation **per unknown key, all collected** (this is
    the "a body must not smuggle a path-owned id" rule: an undeclared
    `organization_id` is a named 400, not silently ignored);
  - `{}` (a declared-keys object with no keys) is **valid** — whether an
    empty PATCH is acceptable is the domain's call.
  - `BodyKeyExpectedUpdatedAt` is **not** implicit: a CAS route declares it
    in `keys` explicitly.
- `type Body struct { … }` (unexported fields: raw values, presence,
  accumulated `sdk.ValidationError`) with getters that **never
  short-circuit** — every getter records violations and returns its zero
  value on failure; the single terminal check is `Err()`:

  | getter | present + valid | absent | JSON null | wrong type/format |
  |---|---|---|---|---|
  | `Has(field) bool` | true | false | true | true |
  | `Str(field) string` | value | violation `CodeRequired` | violation `CodeInvalidType` | violation `CodeInvalidType` |
  | `OptStr(field) *string` | ptr | nil, no violation | nil, no violation | violation `CodeInvalidType` |
  | `Bool(field) bool` | value | violation `CodeRequired` | violation `CodeInvalidType` | violation `CodeInvalidType` |
  | `Strs(field) []string` | slice | violation `CodeRequired` | violation `CodeInvalidType` | violation `CodeInvalidType` (message names the offending element index) |
  | `Date(field) time.Time` | midnight-UTC, strict `time.DateOnly` | violation `CodeRequired` | violation `CodeInvalidType` | violation `CodeInvalidFormat` |
  | `Instant(field) time.Time` | RFC3339 (fractional seconds accepted) | violation `CodeRequired` | violation `CodeInvalidType` | violation `CodeInvalidFormat` |
  | `ExpectedUpdatedAt() time.Time` | as Instant, on `BodyKeyExpectedUpdatedAt` | violation `CodeRequired` | violation `CodeInvalidType` | violation `CodeInvalidFormat` |

  PATCH semantics: handlers guard required-style getters with `Has` and wrap
  results in `crud.Some`; `Date` doc carries "format with `time.DateOnly`,
  store in a DATE column — a zone conversion changes the calendar day".
- `func (b *Body) Err() error` — nil or the accumulated
  `*sdk.ValidationError` (explicit nil on empty).

## Generated-artifact impact

None. No `.templ` sources, no `*_templ.go`, no `make generate`.

## Risks

1. **CAS precision divergence across stores (correctness).** Postgres
   `timestamptz` is microsecond; turso stores text; the known
   postgres-turso CI precision flake is live history. Mitigation: the
   comparison contract is pinned in `StaleError`'s doc (Equal at store
   precision, never string compare) and the emit format is RFC3339Nano so
   the token round-trips what the store returned. The framework cannot
   enforce a domain's compare — the doc is the mitigation, flagged for the
   product-manager review.
2. **Leak-posture amendment (D3).** `ErrFromDomain` now puts domain-authored
   sentences on the wire for two typed errors. Bounded by: only the two
   concrete kernel types are recognized (no duck typing), `Refuse`'s doc
   forbids driver strings in `Message`, and the `SafeDomainError` paragraph
   is rewritten in the same change so the normative text stays true.
3. **Kernel-promotion precedent creep.** First behavior-bearing promotion;
   mitigated by writing the admission criterion into `sdk/README.md`
   (task-1) so flat-foundation pressure argues against a rule next time.

## Tasks

### task-1: promote the fault vocabulary to the kernel

- **depends_on:** []
- **model:** opus
- **files:**
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/faults.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/faults_test.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/errors.go (package-doc touch only)
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/README.md
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk && go build ./... && go test ./... && go vet ./...` then `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make guard`
- **description:** Add `sdk/faults.go` exactly per the symbol spec above
  (Violation, ValidationError with pointer-only `Error()`/`Unwrap`/`Add`/`Err`,
  Refuse, UnknownReference, the five `Code*` consts, StaleError) — consts at
  top of file per repo convention, stdlib imports only (`errors`, `fmt`,
  `strconv` or `strings`, `time`). Tests pin: `Unwrap` chains
  (`errors.Is(…, sdk.ErrInvalidInput)` / `sdk.ErrConflict`, `IsExpected`
  true for both), `Err()` explicit-nil-on-empty, the `errors.As`
  pointer-target match (and a test that a value-stored copy is NOT expected
  to match — documenting the pointer-only contract), message formats, and
  `UnknownReference`'s code/message. Update `errors.go`'s package doc (the
  kernel now holds the write-fault vocabulary too) and `sdk/README.md`: the
  layering-law kernel bullet gains the promotion criterion (shared by ≥2
  foundation packages, stdlib-only, zero transport semantics) and names
  `faults.go`; include the UnknownReference domain-pre-check/FK-race
  paragraph in the godoc.

### task-2: Field[T]/Some/Overlay in crud

- **depends_on:** []
- **model:** opus
- **files:**
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/crud/field.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/crud/field_test.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/crud/crud.go (package-doc addition only)
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk && go build ./... && go test ./... && go vet ./...` then `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make guard` (G21 must stay green; `field.go` imports nothing)
- **description:** Add `field.go` (Field, Some, Overlay) with the normative
  nullable rule in the godoc: `Field[*T]` for nullable columns
  (`Some[*T](nil)` = explicit clear), `Field[T]` for NOT NULL, zero value =
  absent = unchanged. Tests pin: zero-value passthrough in `Overlay`,
  `Some`-set replacement, explicit-clear via `Field[*string]`, and a
  compiled `ExampleOverlay` showing a two-field sparse patch. Extend
  `crud.go`'s package doc with a short "write vocabulary" section
  cross-referencing `sdk.ValidationError`/`sdk.StaleError` (doc-only — no
  import edge; the `validation`/`FieldErrors` precedent) and the
  handler-composition recipe (`body.Has` + `crud.Some`), noting the reader
  lives in web per G21. NOTE: no custom JSON methods (out of scope), and no
  crud aliases of kernel names.

### task-3: web rendering — FieldError.Code, ErrStale, the two ErrFromDomain/ErrValidation branches, posture rewrite, openapi

- **depends_on:** [task-1]
- **model:** opus
- **files:**
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/web/errors.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/web/errors_test.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/web/openapi.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/web/openapi_test.go
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk && go build ./... && go test ./... && go vet ./...` then `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make guard`
- **description:** Per the symbol spec: `FieldError.Code` (omitempty),
  `Error.CurrentUpdatedAt` (omitempty, RFC3339Nano, set only by `ErrStale`),
  `ErrStale(msg, current)`, the shared unexported
  ValidationError→`*Error` helper used by BOTH `ErrValidation` and
  `ErrFromDomain`, and the pinned branch order in `ErrFromDomain`
  (SafeDomainError → ValidationError len>0 → StaleError → `errors.Is`
  switch). Rewrite the SafeDomainError "nothing but this wrapper is
  recognized" paragraph to name the three recognized wire-text contracts and
  add the no-driver-strings-in-`Violation.Message` rule; add the D5
  three-collectors paragraph near `FieldErrors`. Update `errorSchema()` in
  `openapi.go`: `current_updated_at` on the envelope, `code` on the
  field-item schema. Tests pin: ErrValidation/ErrFromDomain body parity
  (deep-equal) for the same `*sdk.ValidationError`; empty-collector
  fall-through (a typed non-nil `&sdk.ValidationError{}` renders the generic
  400, never `"fields":[]`); branch-order tests (SafeDomainError wrapping a
  ValidationError wins and drops fields — documented; StaleError before the
  `ErrConflict` Is-branch → 409 code `"stale"` with the token,
  RFC3339Nano/UTC); MaxBytesError still wins in ErrValidation; existing
  bodies byte-identical when the new fields are zero (marshal assertions);
  openapi schema assertions for both edits.

### task-4: the strict body reader in web

- **depends_on:** [task-1, task-3]
- **model:** opus
- **files:**
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/web/readbody.go
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/foundation/web/readbody_test.go
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk && go build ./... && go test ./... && go vet ./...` then `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make guard`
- **description:** Implement `DefaultBodyLimit`, `BodyKeyExpectedUpdatedAt`,
  `ReadBody`, and `Body` with the getter matrix exactly as specified
  (consts at top; getters never short-circuit; `Err()` is the single
  terminal check; structural failures per the spec). Tests pin every matrix
  cell plus: all unknown keys collected in one `*sdk.ValidationError`
  (including the smuggled path-owned `organization_id` case); oversized body
  → `*http.MaxBytesError` in the chain → `ErrValidation` answers 413; empty
  body / `null` / non-object / trailing-content sentences wrapping
  `sdk.ErrInvalidInput`; `{}` valid; multiple violations accumulate across
  getters and surface once; `ExpectedUpdatedAt` accepts fractional-second
  RFC3339; a compiled `ExampleReadBody` handler showing the
  read→Has→getters→`Err()`→`ErrValidation` flow using plain locals — **G12b
  applies to tests: the example must NOT import `foundation/crud`** (the
  crud composition recipe lives in task-2's doc instead).

### task-5: docs, sweep, and release prep

- **depends_on:** [task-1, task-2, task-3, task-4]
- **model:** opus
- **files:**
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/RELEASING.md
  - /Users/jrazmi/code/gopernicus-ecosystem/gopernicus/sdk/README.md
- **verify:** `cd /Users/jrazmi/code/gopernicus-ecosystem/gopernicus && make check`
- **description:** Update `sdk/README.md`'s Packages-table rows for `crud`
  (Field/Some/Overlay + the nullable rule) and `web` (strict body reader,
  ErrStale, the recognized fault types) — surgical, matching the table's
  existing density. Add the RELEASING.md header entry + upgrade note "sdk —
  v0.7.0 (next tag): the write vocabulary (minor)": every addition, the D3
  posture amendment, the D4 key names, the compatibility assertion (all new
  wire fields omitempty — existing bodies byte-identical), the CAS
  precision contract, and the adopter recipe. Run `make check` across the
  workspace and confirm `git status` shows only the planned files.

## Sequencing

task-1 and task-2 are independent; run task-1 first (task-3/4 depend on it),
task-2 anywhere before task-5. task-3 before task-4 because the reader's
tests assert rendering through `ErrValidation`. task-5 last, over a green
tree.

## Release train — sdk/v0.7.0 (owner cuts the tag)

Per RELEASING.md, after the plan lands on `main` (PR + squash, `make check`
green on the tagged commit):

```sh
git tag sdk/v0.7.0 -m "sdk v0.7.0 — the crud write vocabulary (#21)"
git push origin sdk/v0.7.0

# THE PROXY LESSON (web-crud-list-request train): poll the .info URL until it
# answers BEFORE any consumer runs `go mod tidy` — a tidy that races the proxy
# caches a negative lookup for minutes.
curl -sf https://proxy.golang.org/github.com/gopernicus/gopernicus/sdk/@v/v0.7.0.info

# cold-resolution verification from an empty module cache
GOMODCACHE=$(mktemp -d) GOFLAGS= go mod download github.com/gopernicus/gopernicus/sdk@v0.7.0
```

No sibling tags. No pin moves in-repo. Post-release: copy the executed plan
to the tracked `plans/` directory per the standing convention, and update the
RELEASING.md entry from "next tag" to "tagged".

## Downstream notes (owner work, not planned here)

- **gps-360-go** retires its directory-domain copy
  (`internal/logic/domains/directory/write.go`,
  `internal/inbound/domains/directory/writes.go`) onto the standard and
  resumes S2 on it. Two deliberate divergences to absorb: wire keys go
  snake_case (`expectedUpdatedAt` → `expected_updated_at` — a client-visible
  change on their 29-route surface), and the reader's getters return plain
  values (compose `crud.Some` under `Has`), not Field-typed values. Their
  429-route real-credential suite is the acceptance harness.
- Plans B and C of the batch stack on this tag; neither is planned here.
- No in-repo pocket adopts this train; when one does (a JSON write surface),
  that plan carries its own run-and-look verification.

## Consultation notes

`lead-backend-engineer` reviewed the sketch ("ship-with-edits"): endorsed the
kernel promotion as forced by G12b and rejected duck-typing on security
grounds (unnamed contract putting domain text on the wire). Their landmines,
all folded in: pointer-receiver-only `Error()` (the `errors.As` miss trap);
`Err()` explicit-nil + empty-collector fall-through in web; the pinned
`ErrFromDomain` branch order and the SafeDomainError-wins field-drop;
rewriting the now-false leak-posture paragraph; `UnknownReference` as a
domain pre-check with the FK race fallback; the typed `CurrentUpdatedAt`
field over a `Details` map; the two hand-written `errorSchema()` edits; the
CAS store-precision compare rule; snake_case wire keys with an exported
const; the `Field[*T]` nullable ruling; the three-collectors rule; one
kernel file (`faults.go`) not two; and the README admission-criterion
amendment. Their open questions (ReadBody signature/limit, unknown-key
posture, getter matrix, body edge cases, render parity, the future
civil-date break) are all answered in the symbol spec and Out of scope
above. Final calls are the planner's.

## Open questions

_None._

## Recommended reviews

- **product-manager** — the wire-contract additions (`code` on field errors,
  `"stale"`/`current_updated_at`, snake_case keys) and the CAS precision
  risk.
- **architecture-steward** — the kernel promotion (first behavior-bearing
  root admission) and the D3 leak-posture amendment.
- **platform-sre** — the sdk/v0.7.0 train steps and the proxy poll.
- **lead-backend-engineer** — post-hoc pass over the landed diff against
  their review (already consulted pre-plan).

## Notes

- Auth naming rule respected: nothing here touches authentication or
  authorization surfaces; no authz/authn spelling anywhere.
- Rule 10 check: no task makes a pocket core import an integration, store,
  or another pocket — nothing outside `sdk` changes.
- Repo conventions carried into every task: consts/vars at top of files;
  accept interfaces, return structs (`ReadBody` returns `*Body`; `Refuse`
  returns `*ValidationError`); surgical diffs (the `openapi.go`
  limit/cursor/order doc drift noted at v0.6.0 stays as found).

## Execution record

**EXECUTED 2026-08-31**, branch `crud-write-vocabulary` off `main` @ `f32d9ff`.
Not tagged — `sdk/v0.7.0` is the owner's cut.

| task | commit | note |
|---|---|---|
| task-1 | `684ba0c` | `sdk/faults.go`: Violation/ValidationError/Refuse/UnknownReference/Code* consts/StaleError |
| task-2 | `cfb588b` | `crud/field.go`: Field[T]/Some/Overlay, zero imports |
| task-3 | `4bcdcf9` | web rendering: FieldError.Code, Error.CurrentUpdatedAt, ErrStale, shared validationErrorBody, branch order |
| task-4 | `e24c58c` | `web/readbody.go`: ReadBody/Body getter matrix/BodyKeyExpectedUpdatedAt |
| task-5 | `50ae0cc` | RELEASING.md v0.7.0 upgrade note + sdk/README kernel bullet |
| review | `40f01e1` | lead-review edits: pocket wire ruling in SafeDomainError doc; ErrFromDomain 413 branch; guard G23 (violation-message-not-error; G22 stays reserved for host-layout PR-B); G21→G12b comment fixes; UseNumber justification + "not on this tag" gaps; doc lines; README kernel row |

**Verification.** `go build/vet/test -count=1` green in `sdk` after every task;
`make guard` green (22 guards after G23, fire-drill verified); `make check`
whole-workspace green; gofmt clean on all touched files. Independent verifier
pass: full `make check` across all 39 modules, diff scope confined to `sdk/` +
`RELEASING.md`.

**Review.** lead-backend-engineer post-hoc: ship-with-edits, all applied @
`40f01e1`. RULING recorded in the SafeDomainError doc: a pocket MAY return
`sdk.ValidationError`/`sdk.StaleError` and have those sentences reach a host's
wire — field-shape refusals are product-neutral; free-form policy text still
requires `SafeDomainError`.

**Deviations.** Internal test packages (repo convention); `faults.go` imports
`strconv`+`time` only; kernel-authored stale-write message (safer than the plan
assumed — no domain string can leak on that branch); `Strs` reports the first
offending element index only.

**Named gaps (follow-up issues, not this tag).** No numeric getter and no raw
accessor on `Body` (`UseNumber` is future-proofing); `web.DecodeJSONStrict`
(pockets carry verbatim strict-reader copies).

**Downstream, unchanged:** gps-360-go migrates
`internal/logic/domains/directory/write.go` + `inbound/.../writes.go` to the
standard and resumes S2 — owner, at adoption. Wire divergences to mind there:
snake_case body keys, plain-value getters composed with `crud.Some`.
