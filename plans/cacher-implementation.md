# Complete cacher and correct storage/HTTP behavior

Status: COMPLETE — 2026-09-10. Owner approved
[cacher-design.md](cacher-design.md); findings: [framework-audit-cacher.md](framework-audit-cacher.md).
Parent: [framework-audit.md](framework-audit.md).

## Preconditions and decisions

Branch/base firestore-authentication / 6807ed06; 533 prior dirty paths.
Snapshot `/tmp/gopernicus-cacher-implementation-baseline.json`. Preserve prior
audits/concurrent work; compare against this snapshot, not the full HEAD diff.
Go 1.26.1; no root module. GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache;
formatter /Users/jrazmi/go/bin/goimports. No external consumer or original-repo
edits, dependency/pin changes, commits, deployment or generated-source edits.

- Storer: Get/GetMany/Set/Delete; optional PrefixDeleter.DeletePrefix. Remove
  DeletePattern and cache Close methods. Noop implements both contracts without
  storing data. Negative Set TTL returns an error matching sdk.ErrInvalidInput;
  context cancellation is checked consistently. Redis positive TTL rounds up to
  milliseconds (without overflow), zero is immortal, no driver sentinel leakage.
- `NewMemory(MemoryConfig) *Memory`; MaxEntries <= 0 defaults to 10,000. A single
  mutex and stdlib LRU with copied values, expiration reclamation and bounded
  cardinality; no janitor or strategy abstraction. Zero-value Memory need not
  work; document construction. MaxEntries is not a byte budget.
- `New(Storer, Config) *Cache`; nil selects Noop, as does explicit Noop. Typed-nil
  adapters remain caller misuse, with no reflection detection. Config.Namespace
  uses unambiguous length framing even when empty; Config.OnError is
  func(context.Context, string, error), reporting operation/cause without payloads.
  Complete strict raw operations and Cache.InvalidatePrefix (errors.ErrUnsupported
  if absent). No PrefixDeleter claim by the facade; check adapter support at wiring.
- Context-first GetJSON/SetJSON/GetOrLoadJSON functions on *Cache. Only loading
  helpers bypass reported cache/codec failures; they return loader/caller-context
  errors and do not cache failed loads. Validate negative TTL before loading.
  Preserve false/zero/null values, no implicit validation or negative-error cache,
  detached work, singleflight, distributed locks, tags or generated decorators.
- `Pages(Storer, PageConfig) web.Middleware`: PageConfig carries TTL, MaxBodyBytes,
  and optional Scope func(*http.Request) string (extra host-owned partition).
  Zero TTL defaults to 60s, negative disables Pages; MaxBodyBytes <= 0 defaults to
  1 MiB. Nil store bypasses. Page TTL defaults differ deliberately from raw Set's
  zero=no-expiry. CMS exposes PageCache PageConfig, retaining its current default.
- Pages stays narrow public GET HTML: skip credentials/cookies/caller principals,
  conditional/range/freshness requests; honor outer/inner restrictions; bypass
  Vary, encoded/cookie-bearing/unsupported responses. Match scheme+authority+URI+
  scope in a versioned page key. Preserve supported committed metadata and fresh
  outer headers, excluding per-request/hop-by-hop fields; no nonce reuse. Store
  bounded bodies in versioned response records with absolute expiry, continue
  streaming uncached bodies, preserve S6 error/flush behavior. Prefix page: remains
  invalidatable; no strong concurrent-invalidation guarantee.

## Tasks and ownership

1. Named implementer: SDK cacher source/tests except middleware*.go; Redis cacher
   source/tests and cache portions of shared Redis conformance; no other files.
   Implement storage + service + typed helpers + Noop and meaningful regressions.
2. Parent: Pages/middleware tests, CMS config/wiring/tests, all other caller and
   doc migrations, small host data-cache example and its behavior proof.
3. Named backend reviewer: read-only final storage/service review. Named platform
   reviewer: read-only final Pages review. Review is not a new approval boundary.
4. Parent verification: goimports and scoped diff, targeted tests/race, live
   disposable Redis cache conformance/namespace cases, actual HTTP behavior,
   full make check (42 modules/guards/scaffolds/generated drift), make docs-build.
5. Record final API/behavior migration in AUDIT-010 and RELEASING, update design/
   audit handoff and exact task-relative changed-file inventory. Next: ratelimiter.

## Results and verification

All five tasks completed. C1–C6 are fixed; C7's service was completed following
the owner's clarification and original/framework research. Final public APIs
match the decisions above. CMS receives host configuration through Register and
Mount; the minimal host's catalog is a concrete data-cache consumer, with logic,
inbound and CMS outbound packages in the documented host layout. Existing host
layout debt outside these additions was not moved or expanded.

Named backend review confirmed namespace, JSON/loader and Memory behavior, and
identified two final corrections: normalize cancellation after successful Redis
commands (including SCAN before DEL), and require nil errors when conformance
asserts misses. Both are fixed and covered. Prompt Redis network interruption
still depends on the caller-owned client's timeout configuration.

Named platform review identified response metadata conflicts, inherited metadata
deletions, explicit Content-Length/Date freshness hazards, short writes on hits,
and JSON key framing's invalid-UTF-8 collisions. Final Pages uses byte-preserving
length framing, bypasses conflicting/deleted metadata and explicit framing/date
headers, and records short writes. Capture owns both FlushError and Flush paths;
render/write/flush failures prevent storage. Supported committed metadata is
retained; unsupported handler metadata and nonce policies bypass.

### Verification

All commands use Go 1.26.1 and the GOCACHE recorded above. HTTP/live service runs
used approved loopback access and owned temporary resources.

- PASS: goimports -l on all changed Go files (empty output), scoped git diff
  --check, task-relative SHA256 inventory. No generated-source/asset drift.
- PASS: SDK `go test ./capabilities/cacher/... -count=1`; targeted middleware
  `go test ./capabilities/cacher -run 'TestPage' -count=1`. The first parent
  invocation used the root rather than sdk directory and was corrected; it was
  a path error, not a test failure.
- PASS: root workspace-qualified `go test -race ./sdk/capabilities/cacher/...
  ./pockets/cms/... ./examples/minimal/... -count=1`, including real HTTP.
- PASS: implementer's scoped SDK storage/service race tests, count 10; Redis
  `go test -race . -run '^TestCacher' -count=20`; SDK cache build/vet and Redis
  module build/vet. The implementer's initial package-wide race run could not
  bind the newly added HTTP test inside its sandbox; the parent's approved
  full-package race command above resolved that environment limitation.
- PASS: disposable Redis **8.4.0**, `go test -race -run
  '^(TestConformance_Cacher|TestCacher)' -count=3 -v .`. This includes raw and
  optional-prefix conformance, glob-bearing adapter namespaces, isolation from
  neighboring adapters and service namespaces, maximum/tiny TTL, and cancellation
  command-hook regressions. Runner: /tmp/gopernicus-cacher-run-redis.py; log:
  /tmp/gopernicus-cacher-live-redis.log. Owned Redis stopped in finally.
- PASS: built and ran the actual minimal binary from an empty temporary working
  directory, with loopback HOST/PORT and no host .env. GET /catalog.json returned
  the seeded public product with HTTP no-store; real CMS GET / returned MISS
  then HIT with complete HTML. /tmp/gopernicus-cacher-host-smoke.py and
  /tmp/gopernicus-cacher-host-smoke.log record the proof; server stopped via SIGTERM.
- PASS: `make check` — all **42 modules** build/test/vet, scaffold cache/guards,
  integration/live-tag compile-only vet, all **23 architecture guards**,
  regenerated templ and UI asset drift checks. Log:
  /tmp/gopernicus-cacher-make-check.log (ends `all checks passed`).
- PASS: `make docs-build` — existing pnpm typecheck and Docusaurus production
  build. Log: /tmp/gopernicus-cacher-docs-build.log. Docusaurus's optional update
  notifier could not access its config store; typecheck/build succeeded, and no
  permissions/config change was needed.

No unresolved implementation/test failure. Full make check's env-gated live DB
suites remain skipped; the separate live run covered Redis caching only, not
Redis events/ratelimiter, PostgreSQL, hosted Turso, Firestore emulator/GCP or
external services. External consumer apps and the original framework were not
changed or run. No benchmark, distributed-load or browser visual claim is made.
No schema/module-version/pin/release/deployment change was made.

### Handoff and policy boundaries

AUDIT-010 is the standalone consumer migration. RELEASING and canonical SDK/CMS/
Redis/example docs use the final APIs. The prior research and S8a finding records
remain historical evidence and point here for current status.

Memory bounds entry count, not bytes. Prefix invalidation is best effort with
concurrent writers/loaders; CMS write invalidation, singleflight, tags, locks,
background refresh and original auth's broader invalidation needs are not part of
this implementation. Hosts own keys, namespaces, TTL, trusted scope, cache error
reporting, public-page eligibility and underlying resource lifecycle.

Next: audit `sdk/capabilities/ratelimiter`, then SDK events, then pockets. Do not
reopen approved generic workers/job middleware, cryptids naming or root utility
promotion without new evidence.

### Exact changed files

Compared with /tmp/gopernicus-cacher-implementation-baseline.json, not HEAD:


- `AUDIT.md`
- `RELEASING.md`
- `examples/README.md`
- `examples/auth-cms/README.md`
- `examples/auth-cms/cmd/server/main.go`
- `examples/cms/cmd/server/main.go`
- `examples/minimal/cmd/server/catalog_cache_test.go`
- `examples/minimal/cmd/server/goth_htmx_proof_test.go`
- `examples/minimal/cmd/server/main.go`
- `examples/minimal/internal/inbound/domains/catalog/routes.go`
- `examples/minimal/internal/logic/domains/catalog/catalog.go`
- `examples/minimal/internal/outbound/domains/catalog/cms.go`
- `integrations/kvstores/goredis/README.md`
- `integrations/kvstores/goredis/cache_live_test.go`
- `integrations/kvstores/goredis/cacher.go`
- `integrations/kvstores/goredis/cacher_test.go`
- `integrations/kvstores/goredis/conformance_test.go`
- `plans/cacher-design.md`
- `plans/cacher-implementation.md`
- `plans/framework-audit-cacher.md`
- `plans/framework-audit.md`
- `pockets/cms/cms.go`
- `pockets/cms/internal/inbound/cms/page_cache_test.go`
- `pockets/cms/internal/inbound/cms/routes.go`
- `pockets/events/README.md`
- `sdk/README.md`
- `sdk/capabilities/cacher/cache_test.go`
- `sdk/capabilities/cacher/cacher.go`
- `sdk/capabilities/cacher/cachertest/cachertest.go`
- `sdk/capabilities/cacher/cachertest/prefix.go`
- `sdk/capabilities/cacher/json.go`
- `sdk/capabilities/cacher/memory.go`
- `sdk/capabilities/cacher/memory_conformance_test.go`
- `sdk/capabilities/cacher/memory_test.go`
- `sdk/capabilities/cacher/middleware.go`
- `sdk/capabilities/cacher/middleware_behavior_test.go`
- `sdk/capabilities/cacher/middleware_policy.go`
- `sdk/capabilities/cacher/middleware_test.go`
- `sdk/capabilities/cacher/noop.go`
- `workshop/documentation/docs/getting-started/quickstart.md`
- `workshop/documentation/docs/pockets/cms.md`
- `workshop/documentation/docs/sdk/capabilities.md`
