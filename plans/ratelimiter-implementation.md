# Implement the rate-limiter audit

Status: COMPLETE — 2026-09-10. Owner approved the recommendations in
[framework-audit-ratelimiter.md](framework-audit-ratelimiter.md).
Parent: [framework-audit.md](framework-audit.md).

## Preconditions

Branch/base firestore-authentication / 6807ed06; 557 prior dirty paths. Snapshot:
/tmp/gopernicus-ratelimiter-implementation-baseline.json (1962 files). Preserve
prior audits and concurrent work; compare to that snapshot, not all of HEAD.
Go 1.26.1, workspace with 42 modules and no root go.mod.
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter
/Users/jrazmi/go/bin/goimports. Redis 8.4.0 and PostgreSQL 17.4 binaries are
available for owned disposable loopback tests. No external consumer/original
edits, dependency/pin/release changes, deployed quota resets, or generated edits.

## Frozen contract and choices

- Allower owns Allow(ctx,key,Limit); Limiter embeds Allower and adds Reset. Remove
  port/concrete Close, RateLimiter service/New/options/WithLogger/resolver DTOs
  and default resolver, unproduced error sentinels, and DefaultLimit (host policy).
  Retain PerSecond/PerMinute/PerHour/WithBurst conveniences. Acquire accepts
  Allower and checks context before attempts and success; error wraps omit keys.
- Limit.Normalize() (Limit,error) validates positive Requests/Window, nonnegative
  Burst, ceiling <= MaxCeiling (2^31-1, portable int and safe state addition), and
  no overflow. Round positive Window up to whole milliseconds; reject durations
  whose rounded value exceeds MaxWindow (largest whole millisecond in Duration).
  Require ceiling*windowMillis <= 2^52-1 so the weighted-count integer product
  and quotient are safe in Redis Lua as well as Go/Postgres. Division checks
  avoid overflow. Errors match sdk.ErrInvalidInput. Nonempty keys required by
  Allow and Reset; namespaces/key policy are host-owned.
- All bundled backends use the same two-window counter approximation. State:
  start (integer epoch milliseconds), current count, previous count, window_ms,
  updated time, expiry. Buckets stay anchored at the original start: advance by
  floor((now-start)/window)*window; carry count for one elapsed bucket, else zero.
  At elapsed == window, advance. Effective = count + floor(previous * remaining
  milliseconds / window_ms). Increment only if effective < Requests+Burst.
  Remaining=max(ceiling-effective-1,0) when allowed, zero when denied. RetryAfter
  is the positive duration to the current bucket end (a retry checkpoint, not
  a reservation or promise of the earliest possible admission); allowed retry=0.
  ResetAt is that bucket end. Clamp decision time to >= prior updated time if
  a wall clock moves backward; all returned retry durations use backend time.
- Expiry is start+2*window, regardless of denial traffic; expired state is absent
  whether physically pruned or not. Compute endpoints without Duration overflow.
  While state is live, changing normalized Window is invalid input and does not
  mutate state. Ceiling changes retain counts. Expired/reset keys accept new
  windows. Hosts can version keys for intentional independent policy changes.
- NewMemory(MemoryConfig) *Memory; MaxEntries<=0 defaults to 10,000. One mutex,
  sample clock after locking, reclaim expired entries on access and at capacity.
  Never evict active budgets; new keys at full capacity return ErrCapacity,
  matching sdk.ErrUnavailable. No goroutine, janitor or Close lifecycle.
- Redis uses redis.NewScript(...).Run, integer millisecond TIME, relative retry
  in its reply, strict reply decoding, and context checks around commands. Every
  adapter key prefix receives an internal v2: suffix (including custom prefixes),
  so old nanosecond hashes are never interpreted as milliseconds. No runtime
  deletion/migration of legacy keys; rollout creates a fresh budget, documented.
  Review amendment: an old logical v2:key can collide with a new key. Both
  adapters validate stored window_ms before interpreting even expired state and
  reject legacy/incompatible records with sdk.ErrConflict; Reset preserves them.
- PostgreSQL preserves atomic INSERT ON CONFLICT admission. Capture decision
  time in the conflict branch after row locking, once, clamped to updated_at;
  milliseconds throughout. Use existing columns plus window_ms BIGINT NOT NULL
  DEFAULT 0. Host reference migration adds that column; old writers can still
  use legacy keys. New keys also carry v2: so old state and new semantics do not
  mix. StatusCheck probes the required new column; missing-column diagnostics
  match sdk.ErrNotFound. No schema creation at runtime. Expired rows are empty;
  reject active window mismatch without quota mutation (returned marker/error).
- PostgreSQL review amendment: a speculative insert blocked behind a rolled-back
  insert can retain a pre-lock timestamp. Missing keys now insert a zero-count,
  explicitly expired placeholder (no admission), then repeat the same upsert once.
  All quota decisions use the locked conflict clock. Existing keys use one
  statement; new keys use two. Bound each Allow to two statements; if Reset or
  pruning interrupts initialization again, return sdk.ErrConflict without admission.
  Never retry a backend error or an already consumed quota. No new schema field.
- Middleware(l Allower, cfg MiddlewareConfig) web.Middleware. Config fields:
  Limit Limit, Key func(*http.Request) string, Reject func(http.ResponseWriter,
  *http.Request,Result), FailOpen bool, OnError func(context.Context,error).
  Default closed; nil Reject -> shared JSON 429 + safe Retry-After. Dependency
  failures ->503 unless explicitly open; invalid config/key/window ->500 even
  if open. Canceled caller -> no downstream invocation/write, report context
  error. Hook synchronous and concurrency safe; no keys added. Validate static
  limit before delegating; nil allower/key are configuration errors, not panics
  or a disabled sentinel. No exported reflection to detect typed-nil misuse.
- Authentication RateLimitByIP explicitly sets FailOpen:true and logs through its
  existing logger, uses default rejection to retain retry guidance. Its selected
  service-level policies (login propagates, refresh opens) stay intact; report
  refresh limiter errors with the existing logger. Scope excludes production
  durability metadata redesign and other authentication policies.
- Host example: demonstrate fallible host/pocket limit resolution followed by
  Allow (and usable Acquire), without restoring auth-schema SDK DTOs. Keep host
  limits/config in composition/domain code and make actual behavior testable.

## Tasks and ownership

1. Parent: SDK core/Memory/Acquire/middleware and all SDK tests/conformance;
   caller migrations, auth integration/tests, host example, docs and AUDIT-011.
2. Named implementer: integrations/kvstores/goredis/limiter*.go and limiter-only
   shared conformance changes; integrations/datastores/pgxdb/limiter*.go and
   required limiter reference DDL/migration docs in pgxdb README. No other paths.
3. Named backend/platform reviewers: read-only contract/atomicity/HTTP reviews
   at useful checkpoints, no new approval boundary.
4. Parent: formatter, targeted/race, live Redis/Postgres shared suites and audit
   regression cases, real HTTP/runnable host, full make check/docs-build. Preserve
   and report any environment failure separately from product regressions.
5. Record final API/data/behavior migration in AUDIT-011 and RELEASING. Update
   review/master handoff, exact task-relative inventory and verification here.
   Next audit: SDK events.

## Results and verification

Implementation and all required verification complete. SDK and
adapter code have named read-only review. Backend review found and corrected the
rolled-back-insert timestamp defect; platform review found no SDK blocker.

Host example is a runnable domain example/test, not a new application endpoint:
fallible host account policy, Allow, worker Acquire, and failed resolution without
quota consumption. Authentication explicitly retains its selected open policy and
logs classified errors; the shared rejection adds Retry-After. Existing defaults,
production durability checks and other authentication policy remain intact.

Verification so far:

- Formatter: /Users/jrazmi/go/bin/goimports on changed Go files.
- SDK targeted tests passed. Parent SDK/authentication/minimal `go test -race`
  count 1 passed, including real HTTP admission/rejection and the host example.
- Both adapter modules' `go build ./...`, `go test ./...`, `go vet ./...` passed;
  their focused hermetic `go test -race . -run '^TestLimiter' -count=10` passed
  (named implementer). Environment-gated cases skipped in those local runs.
- Initial owned Redis 8.4.0/PostgreSQL 17.4 live race/conformance passed. Expanded
  live run passed shared suites, exact anchored state/expiry, policy preservation,
  legacy collisions, migration, initialization interruption/cancellation and older
  PG concurrency/pruning/error cases. Two lock tests failed setup while querying
  a released backend PID; explicit test pool lifetimes corrected the setup.
  Final expanded Redis/PG race run passed with no skips, including both
  post-lock/rolled-back-insert timing cases and bounded initialization.
  Redis 1.543s, PG 6.327s. All owned services stopped after every run.
- Full `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check` passed, and a
  second full run after the PG production correction passed. Includes all 42
  module build/test/vet legs, generated drift checks, scaffold/integration-tag
  compile gates and 23 architecture guards.
- `make docs-build` passed pnpm typecheck and Docusaurus production build.
  Docusaurus's optional update-check config write was unavailable; the build
  succeeded. No permission/ownership change to that external config was made.
- Running minimal binary smoke passed: /catalog.json seeded published product
  with no-store, real CMS home MISS then HIT with complete HTML, liveness and
  SIGTERM cleanup. Real SDK HTTP test separately exercises 429 and Retry-After.

Commands/logs (task-specific temporary artifacts):

- /tmp/gopernicus-ratelimiter-parent-tests.log: first parent run; auth and example
  policy passed, real HTTP listeners were sandbox-denied. Re-run with approved
  loopback passed in /tmp/gopernicus-ratelimiter-parent-race.log.
- /tmp/gopernicus-ratelimiter-implementation-live.py: owns scoped local services,
  disposable PG cluster/random ports, no Redis persistence, and finally cleanup.
  Uses Redis `go test -race -run '^(TestLimiter|TestLive_Limiter|TestConformance_Limiter)' -count=1 -v .`
  and PG `go test -race -run '^(TestLimiter|TestLive_.*Limiter)' -count=1 -v .`.
- /tmp/gopernicus-ratelimiter-implementation-live-initial.log: pre-final-regression
  passing live run; /tmp/gopernicus-ratelimiter-implementation-live-final.log:
  expanded run with the two setup failures (preserved as evidence).
  /tmp/gopernicus-ratelimiter-implementation-live-verified.log: final passing
  expanded run with both corrected contention tests; services stopped.
- /tmp/gopernicus-ratelimiter-make-check.log and -make-check-final.log: full gates.
- /tmp/gopernicus-ratelimiter-docs-build.log: docs gates.
- /tmp/gopernicus-ratelimiter-host-smoke.log: owned host smoke.

Scope limits: no production/distributed load benchmark, external consumer rollout,
other live datastore contracts, Redis bus/cache retest, browser/UI change or live
cloud service verification is claimed. Full workspace tests keep their normal
unrelated live-store skips; integration/live-tag sources are compiled/vetted.

Integration follow-up exposed by test setup: pgxdb.Open currently assigns zero
Config.MaxLifetime/MaxIdleTime directly to pool config (postgres.go:266), causing
connection churn in the PID-based tests. Pin test connection lifetimes here;
review/document default pool lifetime semantics during the later connector audit.
This is outside the limiter API/state correction, not an unresolved limiter test.


No unresolved limiter product/test/setup failure or approval block remains.
No source/pin/generated changes outside this slice were made. AUDIT-011 is the
standalone consumer migration; master handoff points next to SDK events. The
remaining connection-pool-default review above belongs to the integration audit.

## Exact task-relative changed files

Relative to /tmp/gopernicus-ratelimiter-implementation-baseline.json, 72 paths.
Most authentication tests only migrate NewMemory's constructor; five limiter
fakes plus two host fakes drop the obsolete Close method. Previous audits and
concurrent changes remain outside this inventory. No generated or module files.

- `AUDIT.md`
- `RELEASING.md`
- `examples/README.md`
- `examples/auth-cms/cmd/server/password_reset_link_test.go`
- `examples/auth-cms/cmd/server/production_test.go`
- `examples/minimal/internal/logic/domains/catalog/ratelimit_example_test.go`
- `integrations/datastores/pgxdb/README.md`
- `integrations/datastores/pgxdb/limiter.go`
- `integrations/datastores/pgxdb/limiter_live_test.go`
- `integrations/datastores/pgxdb/limiter_state_live_test.go`
- `integrations/datastores/pgxdb/limiter_test.go`
- `integrations/kvstores/goredis/README.md`
- `integrations/kvstores/goredis/limiter.go`
- `integrations/kvstores/goredis/limiter_live_test.go`
- `integrations/kvstores/goredis/limiter_test.go`
- `plans/framework-audit-ratelimiter.md`
- `plans/framework-audit.md`
- `plans/ratelimiter-implementation.md`
- `pockets/authentication/README.md`
- `pockets/authentication/authentication.go`
- `pockets/authentication/internal/inbound/authentication/account_forms_test.go`
- `pockets/authentication/internal/inbound/authentication/challenge_codes_test.go`
- `pockets/authentication/internal/inbound/authentication/csrf_bootstrap_test.go`
- `pockets/authentication/internal/inbound/authentication/helpers_test.go`
- `pockets/authentication/internal/inbound/authentication/html_test.go`
- `pockets/authentication/internal/inbound/authentication/identifiers_test.go`
- `pockets/authentication/internal/inbound/authentication/invitation_test.go`
- `pockets/authentication/internal/inbound/authentication/machine_gate_test.go`
- `pockets/authentication/internal/inbound/authentication/me_test.go`
- `pockets/authentication/internal/inbound/authentication/methods_test.go`
- `pockets/authentication/internal/inbound/authentication/oauth_link_page_test.go`
- `pockets/authentication/internal/inbound/authentication/oauth_test.go`
- `pockets/authentication/internal/inbound/authentication/password_flows_test.go`
- `pockets/authentication/internal/inbound/authentication/password_test.go`
- `pockets/authentication/internal/inbound/authentication/passwordless_test.go`
- `pockets/authentication/internal/inbound/authentication/principal_posture_test.go`
- `pockets/authentication/internal/inbound/authentication/refresh_cookie_path_test.go`
- `pockets/authentication/internal/inbound/authentication/reset_token_retain_test.go`
- `pockets/authentication/internal/inbound/authentication/saturation_test.go`
- `pockets/authentication/internal/inbound/authentication/stepup_test.go`
- `pockets/authentication/internal/inbound/authentication/token_test.go`
- `pockets/authentication/internal/logic/authsvc/browser_middleware_test.go`
- `pockets/authentication/internal/logic/authsvc/challenge_test.go`
- `pockets/authentication/internal/logic/authsvc/credential_test.go`
- `pockets/authentication/internal/logic/authsvc/invitation_test.go`
- `pockets/authentication/internal/logic/authsvc/machine_test.go`
- `pockets/authentication/internal/logic/authsvc/oauth_test.go`
- `pockets/authentication/internal/logic/authsvc/password_policy_test.go`
- `pockets/authentication/internal/logic/authsvc/passwordless_test.go`
- `pockets/authentication/internal/logic/authsvc/ratelimiter_test.go`
- `pockets/authentication/internal/logic/authsvc/refresh.go`
- `pockets/authentication/internal/logic/authsvc/resend_test.go`
- `pockets/authentication/internal/logic/authsvc/resetlink_test.go`
- `pockets/authentication/internal/logic/authsvc/securityevent_test.go`
- `pockets/authentication/internal/logic/authsvc/service.go`
- `pockets/authentication/internal/logic/authsvc/service_test.go`
- `pockets/authentication/internal/logic/authsvc/token_test.go`
- `pockets/authentication/security_test.go`
- `sdk/README.md`
- `sdk/capabilities/ratelimiter/acquire.go`
- `sdk/capabilities/ratelimiter/acquire_test.go`
- `sdk/capabilities/ratelimiter/errors.go`
- `sdk/capabilities/ratelimiter/memory.go`
- `sdk/capabilities/ratelimiter/memory_internal_test.go`
- `sdk/capabilities/ratelimiter/memory_test.go`
- `sdk/capabilities/ratelimiter/middleware.go`
- `sdk/capabilities/ratelimiter/middleware_test.go`
- `sdk/capabilities/ratelimiter/ratelimiter.go`
- `sdk/capabilities/ratelimiter/ratelimitertest/contract.go`
- `sdk/capabilities/ratelimiter/ratelimitertest/ratelimitertest.go`
- `sdk/capabilities/ratelimiter/resolver.go (deleted)`
- `workshop/documentation/docs/sdk/capabilities.md`
