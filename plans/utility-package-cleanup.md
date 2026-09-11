# Utility audit implementation: pointer and slug

Root SDK update (2026-09-10): pointer/slug/ID/identity primitives now live in
root SDK. This record preserves the earlier checkpoint and its original paths;
[sdk-root-promotion.md](sdk-root-promotion.md) and AUDIT-009 give the final names.

Status: COMPLETE — 2026-09-09. The owner authorized the recommendations
from [the conversion/slug review](framework-audit-conversion-slug.md).

## Goal and decisions

Keep two focused SDK packages: `foundation/pointer` for nil-pointer reads and
`foundation/slug` for URL slug generation. Remove the miscellaneous conversion
surface; document every consumer migration in root `AUDIT.md`.

- Move `Deref` and `DerefOr` unchanged into `sdk/foundation/pointer`. Keep a
  present zero value, and use the zero/explicit fallback only for nil pointers.
  Move their existing tests and add concise current-contract package docs.
- Remove `Ptr`; Go 1.26.1 is already required and provides `new(value)`.
  Explain type preservation and fresh-copy semantics in the migration guide.
- Remove the rest of `foundation/conversion`, including case/acronym APIs,
  date parsers, JSON default helpers, `Overlap`, and their tests. Do not add
  compatibility wrappers or replacement generic engines.
- Keep `slug.Make`, the accent table, and every output unchanged. Rewrite its
  misleading compatibility/history comments to describe limited ASCII folding,
  empty results, and the consumer's responsibility for stored identifiers.
  Document CMS edit-time regeneration and runtime route derivation accurately.
- Update current package inventories in `ARCHITECTURE.md`, `sdk/README.md`,
  and the foundation documentation page. Preserve historical notes/review
  evidence and concurrent edits to shared files.
- Add AUDIT-002 with every removed symbol family, migration examples, behavior
  traps and verification. Add a short linked unreleased note in `RELEASING.md`.

## Out of scope

Changes to consumer repositories, CMS slug lifecycle/route validation/display
labels, other SDK slices, prior validation work, Firestore changes, dependencies,
versions, release tags, generated source, or publishing.

## Preconditions

Branch/base: `firestore-authorization` / `51798833`. Conversion and slug source
are clean before this task; `foundation/pointer` does not exist. Prior validation
changes and `AUDIT.md`/audit plans remain dirty or untracked. Active unrelated
Firestore work spans the connector and authorization store. Do not revert it.
There are no runtime conversion imports in the framework or sampled consumers;
unknown external callers still need the breaking migration.

## Tasks and verification

1. Move pointer readers/tests, remove remaining conversion source/tests, update
   slug comments and current documentation. Format changed Go files with
   `goimports`; verify slug non-comment code is identical to the baseline.
2. Write the migration entry and a compiled consumer-style probe of the new
   import, nil/default/zero behavior, and `new(value)` type/copy semantics.
3. Run SDK `go build ./...`, `go test ./...`, and `go vet ./...`; fresh tests for
   pointer and slug; run root `make guard`. After generated-file/service preflight,
   run `make check` across all 41 configured modules and `make docs-build` through
   the existing pnpm scripts. Use the named verifier for independent checks
   while the parent completes documentation and the migration probe.
4. Search for stale live references and check scoped diffs/links. Record exact
   passed/skipped/blocked checks and changed files here; update S2b and the
   shared handoff, retaining CMS follow-ups. Add the actual verification result
   to AUDIT-002 after it passes.

## Execution record

Implemented the selected two-package surface. The `pointer` readers and their
existing tests moved without changing the function bodies. The rest of
`conversion` and its tests are removed. Slug code/table are unchanged; comments
and current docs describe its limitations and regeneration risk accurately.

Changed files owned by this work:

- New `sdk/foundation/pointer/pointer.go` and `pointer_test.go` (moved readers
  and retained tests from conversion).
- Removed all twelve files under `sdk/foundation/conversion`:
  `{caser,cases,datetime,json,ptr,slices}.go` and their `_test.go` counterparts.
- Comments only in `sdk/foundation/slug/slug.go`.
- Package inventory entries in `ARCHITECTURE.md` and `sdk/README.md`; utility
  sections/catalog in `workshop/documentation/docs/sdk/foundation.md`.
- AUDIT-002 in root `AUDIT.md`, a linked unreleased note in `RELEASING.md`,
  this plan, `plans/framework-audit.md`, and
  `plans/framework-audit-conversion-slug.md`.

Verification passed:

- `goimports` formatted changed Go files; final `-l` check returned no files.
  A comparison against HEAD with comments/blank lines excluded confirmed
  `slug.go`'s entire implementation and accent table are identical.
- Named verifier: from `sdk/`, with
  `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`, fresh
  `go test -count=1 ./foundation/pointer ./foundation/slug`, then
  `go build ./...`, `go test ./...`, and `go vet ./...` all passed. Unchanged
  package test results were cached where available.
- Root `make guard`: all 23 configured guards passed.
- Root `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check`: build/test/vet
  across all 41 configured modules, seven integration-tag and two
  integration/live-tag vet legs (compile-only), all 23 guards, scaffold-cache
  warming, and generated-template/asset drift checks passed. Templates/assets
  were clean before and after. The first attempt hit the sandbox's loopback
  restriction in `examples/auth-cms`; the rerun with local listeners allowed
  passed. Logs: `/tmp/gopernicus-utilities-make-check.log` and
  `/tmp/gopernicus-utilities-make-check-sandbox.log`.
- `make docs-build`: pnpm's `tsc --noEmit` and Docusaurus production build
  passed. A non-fatal update-check permissions notice appeared after generation.
  Log: `/tmp/gopernicus-utilities-docs-build.log`.
- From `sdk/`, `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go run
  /tmp/gopernicus-pointer-migration.go`: the new exported import compiled;
  JSON requests exercised absent/null/present-zero/nonzero fields, producing
  the intended defaults and preserving zero/false/empty-string values. Verified
  nil readers, fresh-copy behavior, `*int64` and `**Thing` constructor migration,
  and existing slug examples including decomposed accents and empty results.
- Live source/catalog search found no stale conversion imports/API claims;
  historical plans and `NOTES.md` retain the old evidence. Scoped whitespace,
  Markdown links/anchors, and diff checks passed.

Skipped: live PostgreSQL/Turso/Firestore tests (their test connection variables
were confirmed unset), consumer application builds/migrations, browser flows,
and race testing (no concurrent behavior changed). No dependency pins, release
tags, published artifacts, or generated source changes. No executed check is
blocked or failing. Concurrent Firestore code and shared-doc edits are preserved;
the branch/base remains `firestore-authorization` / `51798833` at close.

Next: S3 environment/logging. CMS URL lifecycle, derived route-base validation,
and display-label casing remain recorded follow-ups for the CMS audit. Future
consumer upgrades use AUDIT-001 and AUDIT-002 as applicable.
