# SDK audit S2b: conversion and slug

Status: IMPLEMENTED AND VERIFIED — 2026-09-09. Original review baseline:
framework HEAD `51798833`. Implementation:
[utility-package-cleanup.md](utility-package-cleanup.md).
Parent: [framework-audit.md](framework-audit.md).

The review evidence and recommendations below describe the pre-change source.
The owner subsequently authorized them; resolution is recorded at the end.

## Scope and questions

Audit every source/test file in `sdk/foundation/conversion` and
`sdk/foundation/slug`, trace framework and representative consumer usage, and
check correctness, clarity, SDK placement, package boundaries, and names. The
owner explicitly allows merging, renaming, moving, or removing packages and
helpers where there is a concrete benefit. Keep SDK stdlib-only; explain the
cost of preserving versus changing public or persisted behavior.

## Work plan

1. Read implementations/tests and relevant architecture contracts. Trace
   alias-aware imports and real usage in the framework, Segovia v2,
   coordination-hub, and gps-360-go; compare relevant original helpers.
2. Exercise suspicious edge cases with temporary probes and run fresh package
   tests/build/vet. Distinguish a documented tradeoff from a correctness defect
   and a proposed capability from a demonstrated consumer need.
3. Use the named lead-backend-engineer for a read-only critique of structural
   alternatives and CMS use while the parent checks external consumers and
   runtime behavior. No implementation delegated or authorized in this slice.
4. Record evidence, a recommended bounded direction, alternatives and migration
   implications; update the shared handoff. Keep `AUDIT.md` for implemented
   breaking changes only. Discuss material policy/structure choices before
   preparing any implementation plan.

## Preconditions and ownership

Branch `firestore-authorization`; latest observed HEAD `51798833`. Earlier
validation source/docs/plans and `AUDIT.md` remain dirty/untracked. Firestore
work and shared release history have advanced independently; preserve them.
This slice owns this review record and the shared audit-plan update only.
Consumer repositories are read-only. No generated code, services, credentials,
dependency pins, release tags, or publishing are needed for this review.

## Assessment

Keep the existing `slug.Make` implementation and output. Retire the broad
`conversion` package, preserving `Deref` and `DerefOr` in a small
`sdk/foundation/pointer` package. Remove `Ptr` in favor of the language's
`new(value)`, and remove the unsupported case/date/JSON/overlap APIs. This is a
recommendation, not an accepted migration or implementation plan.

The important distinction is between shared work and current imports. None of
the sampled apps imports either package, but all three repeat nil-pointer
defaulting and Segovia implements domain-specific slug derivation. Those are
evidence for reusable mechanisms. They do not justify the entire historical
conversion collection, or forcing domain policies into SDK helpers.

## Consumer and responsibility inventory

Searches included import paths, so aliased imports were covered. Vendor,
node_modules, and generated templ files were excluded; templates and original
Go source were inspected. Declared SDK pins below are not proof of the effective
runtime/vendor resolution. No consumer build or migration was performed.

| Family | Framework runtime usage | Representative consumer evidence | Recommendation |
|---|---|---|---|
| Slug generation | Six CMS import sites across content/schema/status, taxonomy, menus, and media | Segovia tenancy has local `NormalizeSlug`/`deriveSlug`, with separate acceptance and length policy | Keep `slug.Make` and its output; keep domain validation/uniqueness outside it |
| `Ptr` | No conversion imports | No direct helper users found; SDK and all three apps declare Go 1.26.1 | Remove; Go 1.26 supports `new(value)` |
| `Deref` / `DerefOr` | No conversion imports | Six nil-string helper definitions in GPS 360; nil-time helpers in Segovia and Coordination Hub | Retain as one focused `pointer` package; explicit fallback is the same operation with caller-selected policy |
| Case functions / configurable `Caser` | No production users, including scaffolder | No helper users found | Remove unless the owner identifies a concrete capability to retain; avoid fixing/building unused naming machinery by default |
| Date parsers | No production users | No helper users found | Remove SDK format guessing; consumers select explicit layouts/timezone policy with `time.Parse` or local parsers |
| JSON fallbacks | No production users | No helper users found | Remove implicit object-default policy; define absence/object requirements in the consuming model |
| `Overlap` | No production users | No helper users found | Remove this unrelated utility; introduce a shared operation if recurring callers establish a contract |

The absence of imports is a bounded sample, not proof that no external caller
or future use exists. The structural recommendation also depends on thin
language/stdlib wrappers, consumer-specific parsing/default policy, unclear
package cohesion, and unsupported configuration machinery.

Representative paths, relative to roots in the parent plan:

- **Segovia v2** (`sdk v0.8.0`):
  `internal/logic/domains/tenancy/tenant.go:83–139` keeps explicit slugs or derives
  them, then checks domain length/pattern constraints. Its derivation does not
  fold accents, so substituting SDK slugging would change accepted names and
  generated identifiers. `internal/logic/domains/timelines/query.go:342` has a
  nil-time reader whose zero fallback is consumed by date-range formatting.
- **Coordination Hub** (`sdk v0.7.0`):
  `internal/logic/domains/coordination/query.go:808` repeats nil-time reading.
- **GPS 360** (`sdk v0.7.1`, local SDK replace declared):
  nil-string readers in `internal/logic/domains/directory/contact.go:159`,
  `internal/logic/compositions/clienthub/hub.go:241`,
  `internal/outbound/domains/directory/contacts.go:158`,
  `internal/inbound/domains/directory/helpers.go:39`,
  `internal/inbound/compositions/clienthub/helpers.go:9`, and
  `cmd/gps360/client/format.go:15`. These demonstrate the shared zero-fallback
  operation; the audit did not establish repeated use of an explicit fallback.
- **Scaffolder:** `workshop/gopernicus/internal/commands/pocket.go:248` uses a
  local first-byte capitalization helper after an ASCII identifier check.
  `db.go:178` uses a migration filename sanitizer with underscore policy. Neither
  is an unadopted general case converter or URL slugger merely due to similar
  string operations.
- **Original Gopernicus:** `sdk/conversion/{cases,json,urls}.go` supplied much
  of the current behavior. The casing/JSON defects below are inherited. Its
  `AddAcronym` global mutation is a real problem that today's isolated casers
  avoid; that does not establish demand for acronym customization. Its URL
  slugger drops punctuation differently from today's separator policy, so
  copying it back would also change output.

## Findings

### C1: Casing corrupts Unicode and loses known word boundaries

**Confirmed correctness defects, medium priority.**
`conversion/caser.go:62` uppercases `part[:1]`, splitting a UTF-8 rune.
`ToCamelCase` then infers the first word from the already-concatenated Pascal
string (`:70–86`), losing boundaries the snake-case input supplied.

| Call | Observed | Expected under the advertised conversion |
|---|---|---|
| `ToPascalCase("éclair")` | Invalid UTF-8 (`utf8.ValidString == false`) | Valid Unicode, `Éclair` |
| `ToPascalCase("user_éclair")` | Invalid UTF-8 | `UserÉclair` |
| `ToCamelCase("api_id")` | `apiid` | `apiID`, preserving the non-leading registered acronym |
| `ToCamelCase("a_b")` | `ab` | `aB`, preserving the second word |

The camel conversion also produces invalid UTF-8 on the accented inputs. No
ASCII-only input restriction is documented. Existing tests cover ordinary ASCII
words and acronym-plus-word inputs, not these cases. If casing is retained,
fix it with rune-safe operations and preserve the original word boundaries;
do not expand to a configurable naming engine. Even an ASCII-only restriction
would not resolve `api_id`/`a_b`.

### C2: JSON fallback is not the object validation it promises

**Confirmed contract defect, medium priority.**
`conversion/json.go:5–11` promises a valid JSON object, but passes through `[]`,
`42`, whitespace-padded `null`, and malformed `{`. The malformed result fails
when marshaled as a `json.RawMessage` field. `JSONOrEmpty` applies a different
fallback: only a nil pointer becomes `{}`, while pointed-to empty/null data
passes through.

These operations mix missing-value policy, object validation, and representation
conversion. Do not silently replace corrupt JSON with `{}` to make the comment
true; that loses errors/data. If retained, a caller requirement must determine
whether to validate and return errors or merely default absent data. Removal is
recommended given no observed consumer and this undefined shared contract.

### C3: Date helpers encode input policy, not just conversion

**Design tradeoff/documentation concern, not a demonstrated live defect.**
`conversion/datetime.go` combines instants, date-only strings, and wall times
without zones. The latter become UTC, which should be an explicit consumer
choice. `ParseFlexibleDate("01/02/2024")` produces January 2 as its comment
warns; the audit does not call that documented precedence a bug.

The flexible parser is not a superset: it rejects `2024-01-15T10:30:00`, which
`ParseDateTime` accepts. The public name does not clearly communicate these
different format lists. Both functions try RFC3339Nano after RFC3339, even
though the first attempt handles the fractional example exercised here.
No broad parser rewrite is justified without callers. Prefer explicit
`time.Parse` at the consuming boundary or an app-owned import parser.

### C4: Pointer construction is already in the required Go language

**Redundant API.** SDK and the sampled apps declare Go 1.26.1. Go 1.26 added
expression operands to `new`; both the official
[release notes](https://go.dev/doc/go1.26#language) and a compiled `*new(42)`
probe confirm it. `Ptr` now adds no mechanism to the required language.

If removed, preserve types and copy semantics during migration:
`conversion.Ptr(value)` becomes `new(value)`;
`conversion.Ptr[int64](1)` becomes `new(int64(1))`. Do not blindly replace it
with `&existingVariable`, which aliases that variable instead of a fresh copy.
`Deref`/`DerefOr` remain useful and currently correct: nil selects the zero or
explicit fallback; a present zero value is retained. No pointer/value object,
interface, or options machinery is needed.

### C5: Slug compatibility documentation overlooks edit-time regeneration

**Confirmed misleading compatibility claim, medium priority.**
`slug/slug.go:31–34` says stored slugs are computed once and only content-type
routes recompute. Actual entry edits, taxonomy edits, and menu renames recompute
them (`content/entry.go:93`, `taxonomy/term.go:78`, `menus/menu.go:63`).

A temporary CMS probe loaded an entry with `Title: "Café", Slug: "caf"` and
called `ApplyEdit` with the same title and a changed body. Its slug became
`"cafe"`. `entrysvc.Edit` persists the resulting entry. This reproduces the
mechanism by which an old algorithm's stored slug can change on a later edit;
it does not claim a deployed app has experienced it.

`ContentType.AdminBase`/`PublicBase` also recompute from configured plural names,
potentially changing routes immediately. Correct the documentation and carry
URL lifecycle/redirect decisions into the CMS audit. Preserve slug output here.

### C6: Slug's limited policy is simple, but must remain explicit

**Documented algorithm tradeoffs, not defects in the implementation.**
The helper produces ASCII output, folds a small accent table, and treats other
characters as separators. Examples: `"école" → "ecole"`, decomposed
`"e\u0301cole" → "e-cole"`, `"東京" → ""`, and `"Straße" → "strase"`.
It does not normalize canonically equivalent Unicode or promise uniqueness.
Changing these rules affects stored identifiers and routes; do not change them
as incidental cleanup.

CMS correctly owns empty-result choices: content rejects unusable titles and
media falls back to `"file"`. Two nearby issues belong in the future CMS review:

- A registry probe accepted distinct content type slugs whose plurals were
  `"Café"` and `"Cafe"`, producing the same `cafe` route bases, and a third
  plural `"東京"` producing empty bases. `validateType`/`Registry.Register`
  check nonblank labels and unique type slugs, not derived route bases. Actual
  route mounting/collision behavior was not exercised; trace it before declaring
  a particular HTTP failure. SDK slugging should not own registry uniqueness.
- `content/schema.go:33` uses first-byte capitalization in `DisplayLabel`.
  Inspect its input constraints and Unicode behavior during CMS review.

`Overlap` has no demonstrated implementation defect: it filters in requested
order, preserves duplicate requested entries, and returns nil for no matches.
Those semantics should be copied deliberately if an external caller migrates;
it is not an automatically deduplicating mathematical-set operation.

## Structural recommendation and alternatives

The named backend reviewer independently recommended preserving slug and,
after receiving the external duplication evidence, retaining only the pointer
readers from conversion under the explicit name `pointer`.

| Alternative | Assessment |
|---|---|
| Keep both unchanged | Avoids migrations but retains defects and unrelated responsibilities |
| Merge slug into conversion | Reduces package count, but gives a clear operation a less descriptive home; no implementation is shared |
| Rename the whole collection `util`/`helpers` | Makes the miscellaneous scope explicit without improving it |
| Move slug into CMS | Possible, but adds no implementation simplification; independent Segovia slug needs support keeping a reusable mechanism available |
| Delete all of conversion | Ignores repeated pointer-reading work in every sampled app |
| Keep `slug`; replace conversion with `pointer.Deref`/`DerefOr` | Recommended: two coherent packages and much less public behavior to maintain |
| Retain a focused case-conversion package | Reasonable if the owner identifies actual naming/code-generation requirements; fix C1, decide its input contract, and simplify customization separately |

The `pointer` recommendation adds only a package boundary around two existing
plain functions. No extra abstractions or blanket migration of consumer-local
helpers is proposed. Naming is provisional until the owner selects the direction.

## Compatibility and next implementation boundary

Deleting conversion breaks every external import, even though none was found in
the sample. A migration would map the two pointer readers, replace `Ptr`, and
spell out local replacements for removed helpers. Date acceptance, JSON null
handling, case/acronym output, and `Overlap` ordering/duplicates must not change
silently while fixing compilation. Record the selected migration in `AUDIT.md`
when implemented; it remains unchanged in this review.

Next: discuss retaining only pointer readers versus any specific case/date/etc.
capability the owner still wants. Once selected, create a bounded implementation
plan covering SDK removals/moves, docs, regressions for retained APIs, and migration
guidance. Correct C5's documentation in that change without altering slug output.
The proposed structure is not yet implemented or accepted.

## Verification and handoff

Passed with `go1.26.1 darwin/arm64`, from `sdk/`, using the already-writable
`GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`:

- `go test -count=1 ./foundation/conversion ./foundation/slug` — both packages
  passed despite the newly demonstrated gaps.
- `go build ./foundation/conversion ./foundation/slug`.
- `go vet ./foundation/conversion ./foundation/slug`.
- `go run /tmp/gopernicus-conversion-slug-audit.go` — exercised the cases above,
  JSON marshaling, Go's `new(value)`, slice ordering/nil behavior, CMS entry
  edits, and content-type registration. Output:
  `/tmp/gopernicus-conversion-slug-audit.log`.

Only this record and `plans/framework-audit.md` changed in this slice. No runtime
code, consumer repo, release, or `AUDIT.md` migration entry changed. No full
workspace/SDK suite, race suite, browser, live datastore, or consumer app was run
again for this read-only review. Prior validation verification remains recorded
in its implementation plan. No environment blocker remains; C1/C2 and the C5
documentation defect remain unresolved pending the selected implementation.

## Implementation resolution

The owner selected the proposed `slug` + `pointer` surface. The completed
[implementation plan](utility-package-cleanup.md) records all changed files
and verification. `Deref`/`DerefOr` moved unchanged to `foundation/pointer`;
the rest of conversion was removed, including the C1/C2 defective APIs and
the C3 format-guessing policy. `Ptr` is replaced by Go's `new(value)` in the
consumer migration instructions. No compatibility wrappers were introduced.

C5's misleading slug compatibility comment is corrected. The entire slug
implementation/table and output remain unchanged. Current package inventories
and docs are updated, and [AUDIT-002](../AUDIT.md#audit-002-conversion-removal-and-pointer-package)
contains the breaking migration. Full workspace and documentation checks pass.

Next: S3 environment/logging. Carry the CMS lifecycle, route-base, and display
label observations into that pocket's later audit. Preserve ongoing Firestore
work and prior validation changes. No utility implementation decision or
verification failure remains pending.
