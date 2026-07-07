# Phase 2 — kernel hardening: test the sdk, ship conformance suites, kill dead stubs

Status: READY — ratified 2026-07-02
Depends on: 01-truth-and-guards.md (a fixed `make check` gate must exist first)

## Goal

Every sdk package the framework actually leans on has tests; facility ports get
reusable conformance suites (the original repo's best testing idea, generalized);
the two dead spots (cacher tracer stub, dormant ratelimiter) are resolved per
decisions D9 and D6.

## Context an executor needs (verified 2026-07-02)

- sdk packages **with** tests: `cacher`, `config`, `errs`, `id`, `logging`,
  `repository`, `web`.
- sdk packages **without** tests: `email`, `feature`, `filestorage`,
  `ratelimiter`, `slug`.
- `examples/minimal/internal/memstore` (the in-memory implementation of all five
  CMS repository ports) has no tests — it's only exercised by running the example.
- `sdk/cacher`: `Cache` wraps a `Storer`; it has a `tracer string // stubbed out`
  field and a `WithTracer` option that does nothing (D9: remove).
- `sdk/ratelimiter`: full port design (`Limiter` iface: `Allow/Reset/Close`;
  `RateLimiter` service; `Limit`/`Result`; `PerSecond/PerMinute/PerHour`), zero
  usage anywhere in the tree, no backend implementation (D6 default: keep + add a
  `Memory` default, do NOT wire it into examples yet).
- The original repo's conformance-suite pattern (worth copying in spirit):
  `gopernicus-original/infrastructure/{cachetest,storagetest,ratelimitertest,...}`
  — a package exporting a `RunXxxTests(t, newImpl)` style runner so every adapter
  is verified against the same behavioral contract. In the new world these live
  as sdk subpackages: `sdk/cacher/cachertest`, etc. (Go idiom precedent:
  `net/http/httptest`, `golang.org/x/tools/go/analysis/analysistest`.)
- sdk is stdlib-only (constitution rule 1) — conformance suites may import
  `testing` and other sdk packages ONLY. No testify.

## Preconditions

1. Phase 1 merged: `make check` covers all 6 modules and all guards. Verify by
   running it.
2. Confirm D6 and D9 statuses in 00-overview.md's decision log haven't been
   overridden by jrazmi. If D6 was flipped to "delete", do W5-alt instead of W5.

## Work items

### W1 — sdk/slug tests

Table-driven `slug_test.go`: ASCII lowering, space/punctuation collapsing, unicode
handling, leading/trailing separator trimming, empty input, idempotence
(`Make(Make(x)) == Make(x)`). Read `slug.go` first and derive cases from the actual
transform rules — do not invent behavior; if a case reveals a genuine bug, flag it
in the execution log rather than silently changing the algorithm.

### W2 — sdk/email tests

- `Message.Validate()`: valid message; missing From/To/Subject; empty body rules —
  derive exact rules from the source.
- `Console` sender: inject a `*slog.Logger` writing to a `bytes.Buffer` (or
  whatever sink the constructor accepts — read it), send a message, assert the
  log line carries to/subject.
- `SMTP`: constructor + config validation only. Do NOT attempt a live SMTP test
  and do NOT add a fake SMTP server dependency; the network path stays untested
  here (note that in the package's doc comment if not already stated).

### W3 — sdk/feature tests

- `MigrationSource` field semantics (Name as namespace).
- A fake `RouteRegistrar` + fake `MigrationRegistrar` recording calls; assert a
  function that registers through `Mount` hits both.
- Compile-time interface satisfaction assertions:
  `var _ feature.RouteRegistrar = (*web.WebHandler)(nil)` (adjust to the real
  concrete type — read `sdk/web` first) so the structural-typing seam can't
  silently drift.

### W4 — conformance suites + filestorage/Disk tests

1. Create `sdk/cacher/cachertest` exporting `Run(t *testing.T, newStorer func(t
   *testing.T) cacher.Storer)`: covers Get-miss semantics, Set/Get roundtrip,
   GetMany partial hits, Delete, DeletePattern, TTL expiry (use short TTLs +
   sleeps bounded to tens of ms; if the port exposes a clock seam use it — read
   the interface first), Close idempotence.
2. Create `sdk/filestorage/filestoragetest` exporting `Run(t *testing.T, newStorer
   func(t *testing.T) filestorage.Storer)`: Upload/Download roundtrip, Exists,
   Delete, List, DownloadRange, GetObjectSize, not-found error mapping. Also a
   separate helper asserting the optional-capability story: a Storer lacking
   `ResumableUploader`/`SignedURLer` yields the sentinel errors via whatever
   type-assert helpers exist (read `sdk/filestorage` first).
3. Wire the suites: `sdk/cacher/memory_test.go` runs `cachertest.Run` against
   `cacher.Memory`(new one per test); `sdk/filestorage/disk_test.go` runs
   `filestoragetest.Run` against `Disk` over `t.TempDir()`.

### W5 — ratelimiter: keep + Memory default (D6 default)

- Add `sdk/ratelimiter/memory.go`: an in-process fixed-window or token-bucket
  `Limiter` (pick whichever the existing `Limit`/`Result` types imply — read them
  first; do not redesign the port). stdlib only; mutex + map keyed by
  limit key; document eviction behavior.
- `sdk/ratelimiter/ratelimitertest/` conformance runner (allow under limit, deny
  over limit, Reset, window/refill behavior) + run it against `Memory`.
- Do NOT wire ratelimiter into any example — it becomes load-bearing with the
  auth feature (phase 4+).

**W5-alt (if jrazmi flips D6 to delete):** delete `sdk/ratelimiter/` entirely,
remove it from sdk/README's table, note the deletion + rationale in NOTES.md.

### W6 — remove the cacher tracer stub (D9)

Delete `Cache.tracer` and the `WithTracer` option from `sdk/cacher`. First verify
zero callers: `grep -rn "WithTracer" --include='*.go' .` must show only the
definition. Existing cacher tests must still pass unmodified (if they reference
the option, update them and log it).

### W7 — memstore tests

`examples/minimal/internal/memstore` gets a port-semantics test: for each of the
five repositories it exposes, assert Create/Get/List/Delete happy paths AND the
sdk/errs sentinel contract (`Get` of unknown id → `errs.ErrNotFound`; duplicate
create → `errs.ErrAlreadyExists` where the real store enforces uniqueness — read
`features/cms/stores/turso` to know which uniqueness rules the SQL enforces, and
mirror those; where memstore doesn't enforce one, flag the divergence in the
execution log instead of silently passing).

## Acceptance

```sh
make check                       # all 6 modules + guards, green
go test ./... (per module)       # includes the new suites
grep -rn "WithTracer\|tracer string" --include='*.go' sdk/cacher/   # empty
```

Every package listed in "without tests" above (minus ratelimiter if W5-alt) now has
a `_test.go`. Conformance suites import stdlib + sdk only (guard G1 from phase 1
already enforces this — confirm it ran).

## Real-interaction check

Standing check from 00-overview.md (build/test loop + boot `examples/minimal` +
curl `/` and one seeded public page). Additionally, since W6 touched cacher and the
public site render-caches pages: request the same public page twice and confirm the
second response is still correct (200, same content).

## Out of scope

- New sdk packages or port redesigns (the ports as they exist are the contract).
- Wiring ratelimiter into examples.
- features/cms service tests (they exist and pass).

## Execution log

### 2026-07-02 — executor pass (phase 2 complete)

Repo is not a git repository (confirmed per constitution note) — no commits possible;
this log entry is the only record of the work.

Preconditions verified: `make check` was green (all 6 modules, all 4 guards) before
any edit. D6/D9 statuses re-confirmed RATIFIED in 00-overview.md's log (no override) —
executed W5 (keep + Memory default), not W5-alt; executed W6 (remove tracer stub).

**W1 — sdk/slug tests: DONE.** `sdk/slug/slug_test.go` (12 table cases in `TestMake` +
1 `TestMake_Idempotent` covering 6 inputs). Derived cases directly from `slug.go`'s
transform (ASCII-only keep-set, non-ASCII runes treated as separators and dropped —
e.g. `Make("Café") == "caf"`, not a transliteration). No bugs found; all green on
first run.

**W2 — sdk/email tests: DONE, with one flag.** `sdk/email/email_test.go`
(`TestMessage_Validate`, 11 cases), `sdk/email/console_test.go` (3 tests),
`sdk/email/smtp_test.go` (constructor/config only, no live network).
- **Genuine bug found (not fixed, per ground rules):** `email.NewConsole(nil)`'s doc
  comment says "A nil logger discards output," but it actually builds
  `slog.New(slog.NewTextHandler(nil, nil))` — a text handler over a **nil
  `io.Writer`** — which panics with a nil-pointer dereference on the first log write,
  not a discard. Documented via `TestConsole_NilLogger` (asserts the current panic via
  `recover()`, with a comment explaining the doc/behavior mismatch) rather than
  silently "fixing" `console.go` (email production code is not on this phase's
  allowed-change list: only `sdk/ratelimiter/memory.go`, the cacher tracer-stub
  removal, and conformance-suite packages). **Flag for jrazmi:** likely fix is
  `io.Discard` instead of `nil` in `NewConsole`; small, one-line, out of this phase's
  scope.
- **Deviation flagged:** the work item also asked to note in the package doc comment
  that SMTP's live-network path stays untested, "if not already stated." Doing so
  would mean editing `smtp.go`/`email.go`, which the executor's ground rules
  explicitly restrict (only the three files/packages listed above may change
  non-test code this phase). Resolved the tension in favor of the stricter
  executor-level ground rule: the caveat is documented in `smtp_test.go`'s package
  comment instead of the production doc comment. Flag for jrazmi if the doc-comment
  version is still wanted — it's a one-line follow-up.

**W3 — sdk/feature tests: DONE.** `sdk/feature/feature_test.go`: compile-time seam
assertion `var _ RouteRegistrar = (*web.WebHandler)(nil)`, a fake `RouteRegistrar` +
fake `MigrationRegistrar` proving a `Register`-shaped function through `Mount` hits
both ports, `MigrationSource.Name`-as-namespace semantics (duplicate-name rejection,
distinct names don't collide), and a Mount-with-nil-Migrations construction check
(the shape `examples/minimal` actually uses). All green.

**W4 — conformance suites + filestorage/Disk tests: DONE, with a structural note.**
- `sdk/cacher/cachertest/cachertest.go`: `Run(t, newStorer)` — Get-miss, Set/Get
  round trip, GetMany partial hits, Delete (+ delete-of-absent-key no-op),
  DeletePattern, TTL expiry, TTL=0-never-expires, Close idempotence. Wired via
  `sdk/cacher/memory_conformance_test.go` (`cacher_test` external package) +
  existing `sdk/cacher/memory_test.go` (untouched, still passes).
- `sdk/filestorage/filestoragetest/filestoragetest.go`: `Run(t, newStorer)` —
  Upload/Download round trip, Exists, Delete (+ delete-of-absent no-op), List,
  DownloadRange (mid-range and to-end), GetObjectSize, not-found error mapping
  (`errors.Is(_, ErrObjectNotFound)` on Download/DownloadRange/GetObjectSize).
  Plus `RunOptionalCapabilityAbsent(t, newStorer)` asserting `FileStore`'s
  `InitiateResumableUpload`/`SignedURL` yield `ErrResumableNotSupported`/
  `ErrSignedURLNotSupported` for a Storer implementing neither optional interface.
  Wired via `sdk/filestorage/disk_test.go` (`filestorage_test` external package,
  `Disk` over `t.TempDir()`) — also added `TestDisk_ContainsPathTraversal`
  documenting that `Disk.full()`'s root+Clean trick contains `../../etc/passwd`
  inside the store's base dir (resolves to `base/etc/passwd`, not an escape) rather
  than returning `ErrInvalidPath` — that guard clause looks unreachable for ordinary
  POSIX paths; not asserted as a bug, just documented as current behavior.
- **Structural note (not a scope deviation, a Go-cycle necessity):** the plan's
  prose says "`sdk/cacher/memory_test.go` runs `cachertest.Run`" — literally adding
  that call to the existing (in-package, `package cacher`) `memory_test.go` produces
  an import cycle (`cacher`'s test variant → `cachertest` → `cacher`). Standard Go
  fix: an external test package (`cacher_test` / `filestorage_test`) in the same
  directory, same pattern `net/http` uses for `net/http/httptest`. Applied identically
  to W5's ratelimiter wiring below. Existing in-package test files were left
  untouched; the conformance wiring lives in new sibling files.

**W5 — ratelimiter Memory default: DONE (D6, not D6-alt).**
`sdk/ratelimiter/memory.go` (new): in-process **fixed-window** `Limiter` (not token
bucket — chosen because `Result.RetryAfter`/`ResetAt` map onto a window boundary
cleanly, and `Limit.Burst` has no natural fixed-window meaning, so it's documented as
ignored rather than approximated). Mutex + map keyed by limit key; eviction
documented (state persists until the key's next post-expiry `Allow` overwrites it;
unbounded growth for an ever-changing key space is called out in the doc comment,
matching the existing `cacher.Memory` dev/single-node framing).
`sdk/ratelimiter/ratelimitertest/ratelimitertest.go`: `Run(t, newLimiter)` — allow
under limit (with remaining-count assertions), deny over limit (+ `RetryAfter > 0`),
Reset, window refill after elapse, key independence, Close idempotence. Wired via
`sdk/ratelimiter/memory_test.go` (`ratelimiter_test` external package, same cycle
reason as W4) + two Memory-specific tests (`TestMemory_BurstIgnored`,
`TestMemory_FixedWindowResetsAtWindowBoundary`). Not wired into any example
(out of scope, confirmed unchanged). `go mod tidy` run in `sdk` after adding the new
production file — `go.mod` unchanged (still stdlib-only, no `require` block).

**W6 — remove cacher tracer stub (D9): DONE.** Deleted `Cache.tracer` field and
`WithTracer`/`CacheOption`'s tracer wiring from `sdk/cacher/cacher.go` (kept
`CacheOption` type itself — still used by `New`'s variadic `opts`, just currently has
no constructors). Verified zero callers first: `grep -rn "WithTracer" --include='*.go' .`
matched only the (now-deleted) definition. No existing cacher test referenced
`WithTracer`, so no test changes were needed for this removal.
`grep -rn "WithTracer\|tracer string" --include='*.go' sdk/cacher/` is empty (exit 1,
matching the acceptance command).

**W7 — memstore tests: DONE, two genuine divergences flagged (not fixed).**
`examples/minimal/internal/memstore/memstore_test.go`, 14 test functions covering
all 5 repositories' actual port surface (Inquiry only has Create/List — no Get/Delete
in `messaging.InquiryRepository`, so tested as such rather than assuming full CRUD).
- Entries: full CRUD + `(type,slug)` collision → `errs.ErrAlreadyExists`, matching
  `features/cms/stores/turso/migrations/0018_entries.sql`'s `UNIQUE (type, slug)`.
  Correctly enforced by memstore.
- **Flag — Terms:** `taxonomy.TermRepository.Create`'s doc comment promises
  "`(kind,slug)` collision → `errs.ErrAlreadyExists`," matching turso's
  `CREATE UNIQUE INDEX idx_terms_kind_slug ON terms (kind, slug)`
  (`0010_terms_kind_slug_idx.sql`). memstore's `termRepo.Create` does **not** check
  for an existing `(kind,slug)` before inserting — a duplicate silently succeeds.
  `TestTermRepo_KindSlugCollisionNotEnforced` documents this rather than asserting
  the (false) promised behavior.
- **Flag — Menus:** `menus.MenuRepository.CreateMenu`'s doc comment promises
  "slug collision → `errs.ErrAlreadyExists`," matching turso's
  `slug TEXT NOT NULL UNIQUE` (`0013_menus.sql`). memstore's `menuRepo.CreateMenu`
  does **not** check for an existing slug before inserting.
  `TestMenuRepo_SlugCollisionNotEnforced` documents this the same way.
  Both are real divergences from the *documented port contract* (not just
  "the real store happens to enforce more than memstore" — the Go doc comments on
  the ports themselves promise `ErrAlreadyExists`), on a store whose own package doc
  says it is "a reference 'bring your own store', not a production datastore" — so
  whether to tighten memstore or soften the doc comments is **jrazmi's call**, out of
  this phase's scope (memstore.go is not on the allowed-production-change list).
- Menu items, assets, inquiries: no uniqueness beyond primary key in either store
  (verified against `0014_menu_items.sql`, `0016_assets.sql`, `0017_inquiries.sql`)
  — no divergence to flag there.
- Delete-of-unknown-id: entries and menu-item `UpdateItem` explicitly promise
  `ErrNotFound` in their port doc comments and memstore honors both; term/menuItem/
  asset `Delete` methods make no such promise in their doc comments and memstore's
  `delete()`-without-existence-check behavior is consistent with that (not a
  divergence — only tested where the port actually promises the sentinel).

**Acceptance: all green.**
```
make check                                                          # PASS — 6 modules, 4 guards
grep -rn "WithTracer\|tracer string" --include='*.go' sdk/cacher/    # PASS — empty
```
Every previously-untested sdk package (`email`, `feature`, `filestorage`,
`ratelimiter`, `slug`) now has `_test.go` coverage. Conformance suites
(`cachertest`, `filestoragetest`, `ratelimitertest`) import stdlib + sdk only — G1
(`guard-sdk-stdlib`) covers `sdk/` recursively and passed.

**Real-interaction check: PASS.**
```
cd examples/minimal && go run ./cmd/server        # localhost:8081 (confirmed from main.go)
curl http://localhost:8081/                        -> 200 (first),  200 (second, X-Cache: HIT), identical bytes
curl http://localhost:8081/products/widget-3000    -> 200 (first),  200 (second, X-Cache: HIT), identical bytes
```
Slug `widget-3000` confirmed from `cmd/server/main.go`'s seed data
(`content.NewEntry("product", "Widget 3000", ...)` → `slug.Make` → `widget-3000`).
Home page `<title>Home</title>` present; product page renders "Widget 3000" via the
host's custom `productRenderer`. Double-request cache check (added scope for this
phase, since W6 touched `sdk/cacher`): second request to each route served
`X-Cache: HIT` with byte-identical bodies to the first — render-caching via
`web.CachePages` (backed by `cacher.Memory`, now tracer-field-free) is unaffected by
the W6 removal. Server killed after the checks; confirmed `localhost:8081` is free
again (`lsof -i :8081` empty).

**Out-of-scope items confirmed untouched:** no new sdk packages, no port redesigns,
ratelimiter not wired into any example, `features/cms` service tests untouched.

**Not started:** phase 3 (per protocol — stop after one phase, jrazmi ratifies next).

**2026-07-02, orchestrator addendum (post-executor):** independently re-verified with
a cold test cache (`go test -count=1 ./...` in sdk) plus full `make check` — all
green; the mid-edit gopls diagnostics (import cycle / type mismatch) were stale
snapshots of intermediate states, not real. Fixed one staticcheck finding in this
phase's own output: `sdk/email/smtp_test.go` passed a nil `Context` (SA1012) — now
`context.Background()`. Three production-code findings remain OPEN for jrazmi (see
executor log above): (1) `email.NewConsole(nil)` panics on Send despite docs
promising nil-discards; (2)/(3) memstore term/menu Create don't enforce the
uniqueness their port doc comments promise (turso does).
