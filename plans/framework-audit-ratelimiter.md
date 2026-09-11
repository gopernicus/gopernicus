# SDK audit S8b: Rate limiter

Status: REVIEW COMPLETE — 2026-09-10. Owner-approved fixes are implemented in
[ratelimiter-implementation.md](ratelimiter-implementation.md), with final live
verification complete. Parent: [framework-audit.md](framework-audit.md).
The findings and probe results below describe the pre-fix review snapshot.

## Preconditions and scope

Branch/base: firestore-authentication / 6807ed06; 556 existing dirty paths.
Snapshot: /tmp/gopernicus-ratelimiter-review-baseline.json (1961 files).
Preserve all earlier audits/concurrent work. Go 1.26.1; no root go.mod;
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter
/Users/jrazmi/go/bin/goimports. No production-source, consumer, original-repo,
generated-artifact, dependency/pin, release or service-state changes in this
review. Disposable local probes/services are allowed; record cleanup and limits.
AUDIT.md contains implemented changes only and will not gain a proposal entry.

Review the core limit/result vocabulary, Memory default, resolver-backed service,
Acquire and HTTP middleware, conformance, Redis and pgx adapters, authentication
and worker callers. Compare original implementations and sampled consumer use
before recommending removal of apparently unfinished APIs. Keep host-owned
policy, generic worker usefulness and SDK stdlib-only composition intact.

## Plan and ownership

1. Parent: trace SDK behavior and tests, inventory actual callers and original
   precedent, distinguish bugs from policy/algorithm choices and missing features.
2. Named backend reviewer: read-only Redis/pgx contract, atomicity, timestamps,
   invalid input, mutable limits, cancellation and test coverage review.
3. Named platform reviewer: read-only HTTP/Acquire and authentication failure,
   trust, lifecycle and production-posture review.
4. Parent: reproduce concrete cases outside the source tree; run SDK/adapter
   build/test/vet, rate limiter race tests, architecture guards and applicable
   disposable live checks. Do not mistake skipped DB tests for conformance.
5. Record prioritized findings, justified simplifications/retentions, migration
   impact, exact verification and next-session handoff. Recommend a bounded
   implementation; do not turn an audit into an unreviewed rewrite.

## Findings and recommendation

### Overall judgment

Keep ratelimiter as an SDK capability. The reusable responsibility is admission
against a keyed budget, with a blocking helper for workers and rejection middleware
for HTTP. Memory, Redis and Postgres are real implementations; the authentication
pocket and Coordination Hub supply real consumers. Host-owned limits and keying
belong at this boundary. There is no reason to split out another throttler package.

The main problem is an underspecified common contract, not excessive abstraction
throughout the package. Several adapters accept different inputs, enforce different
budgets or report inconsistent timing. Some surrounding scaffolding can be removed
or made explicit, especially the inert logger and custom Redis script loader.

### R1 — Invalid limits and numeric resolution behave differently (high; reproduced)

Sources: sdk/capabilities/ratelimiter/memory.go:45–75;
integrations/kvstores/goredis/limiter.go:25–42,134–178;
integrations/datastores/pgxdb/limiter.go:171–202.

Live probes over the same Limit values:

| Input | Memory | Redis | Postgres |
|---|---|---|---|
| Requests=0, Window=1m | Denies, nil error | First call allowed, Remaining=-1 | ErrInvalidInput |
| Requests=1, Window=0 | Every call can allow | Every call can allow; expiry removes state | ErrInvalidInput |
| Window=1ns | Usable tiny fixed windows | Precision/expiry cannot represent it faithfully | SQL division by zero (22012) |
| Requests=2, Burst=-1 | Ignores burst, allows 2 | Ceiling becomes 1 | Ceiling becomes 1 |
| Requests=MaxInt64 | Exact initial remaining MaxInt64-1 | Remaining saturates at MaxInt64 and does not decrement in the probe | Exact initial remaining MaxInt64-1 |
| Window=MaxInt64 duration | Reset in 2318 | Reset saturated at UnixNano maximum, year 2262 | Reset in 2318 |

Redis always admits a fresh key before comparing the ceiling. Its Lua doubles
also cannot represent absolute Unix nanoseconds or the full Go int range exactly.
Postgres checks positivity before truncating duration to microseconds; it therefore
accepts a value that becomes zero in SQL. Requests+Burst is added without an
overflow guard in Redis and Postgres. These are boundary bugs, not intended rate
policies. The saturation observations above replace the source reviewer's initial
hypothesis of negative replies on this Redis version.

Recommendation: one SDK validation/normalization rule, applied before storage.
Require positive Requests and Window, nonnegative Burst, overflow-safe ceiling,
and explicit supported numeric precision/range. Prefer a common millisecond time
unit, rounding positive fractional milliseconds upward without duration overflow,
and numeric ceilings representable exactly by Redis Lua. State the supported bound
rather than silently saturating. Use integer epoch milliseconds and relative retry
values in Redis; avoid adding epoch nanoseconds to large durations. Validate
configuration at wiring and classify runtime invalid input with sdk.ErrInvalidInput.
Zero should not secretly mean disabled/unlimited; a host can omit its middleware.

Compatibility: previously accepted malformed input becomes an error; Burst and
sub-resolution windows become consistent. Choose/pin the exact safe upper bound
and arithmetic in the implementation plan; no arbitrary small business-rate cap.

### R2 — Burst and window algorithms are not interchangeable (high; reproduced)

Sources: ratelimiter.go:109–122; memory.go:9–23,45–75;
memory_test.go:23–39; goredis/limiter.go:44–86; pgxdb/limiter.go:37–52.

PerMinute(1).WithBurst(2) allows one request in Memory and three in the distributed
adapters. The test deliberately codifies ignoring Burst, but that contradicts the
shared Limit field's meaning and NewDefaultResolver's advertised budgets.
Fixed-window counting does not prevent adding an explicit extra allowance; the
Memory comment's token-bucket rationale does not justify ignoring it.

Memory uses fixed windows; Redis/Postgres blend a decaying prior-window count.
This difference is documented, so classify it as a contract/design mismatch rather
than an accidental implementation of the wrong documented algorithm. Replacing a
store currently changes boundary behavior even after correcting Burst.

Recommendation: use one clearly documented two-window approximation for the three
bundled implementations, retaining the distributed adapters' existing general
policy and bringing Memory into agreement. Keep atomic backend admission and
cross-adapter boundary vectors. This adds a small amount of Memory state but avoids
silently weakening deployed distributed limits. Requests+Burst is the effective
ceiling; this is not token-bucket refill or exact last-N-seconds enforcement.
An alternative is fixed windows everywhere, which is simpler but changes Redis/pgx
boundary admission; it should be an explicit algorithm migration, not hidden cleanup.
Do not add an algorithm-selection framework without a concrete second use case.

### R3 — Memory retains expired keys indefinitely (high for long-lived hosts; reproduced)

Source: memory.go:17–23,24–39,51–55. A public API probe admitted 2,000 unique keys
with 1ns windows, waited, then admitted another: 2,001 map entries remained.
The comment calls this acceptable because the capability is unwired, but current
authentication defaults to this Memory. Earlier cacher guidance cited there is
also obsolete after S8a.

Recommendation: explicit MemoryConfig with a bounded key count; record each
entry's actual expiry and reclaim expired entries on access and at capacity. Keep
one mutex and no background janitor/Close lifecycle unless measured need requires
it. **Do not copy cache LRU eviction:** evicting an active rate-limit key restores
its quota, so key churn could bypass enforcement. At capacity, preserve existing
active budgets and return a specific observable capacity error for new keys. HTTP
failure policy must be explicit so a fail-open route's saturation behavior is
visible and intentional. A no-expired-keys scan can be O(MaxEntries); acceptable
for the initial simple bounded default, with benchmarks only if scale demands it.

Compatibility: new constructor config and a bounded-capacity failure mode. Existing
active-key budgets must not reset to make space. Key count is not a byte budget;
hosts still control key construction/length and cardinality.

### R4 — Canceled operations can mutate quota or succeed (high; reproduced)

Sources: memory.go:45,80; acquire.go:24–30;
goredis/limiter.go:134–145,175–189.

- An already-canceled Acquire on Memory returned nil and consumed its only token;
  the subsequent live-context call was denied.
- Canceled Memory.Reset returned nil and restored quota.
- A custom backend that canceled the context while returning Allowed=true made
  Acquire return nil. Redis command-hook simulation reproduced the same raw
  adapter result after successful command completion under cancellation.

Recommendation: check context before operations and after lock/command boundaries;
Acquire checks before every attempt and before returning success. It should accept
existing Allower, since it never uses Reset or Close. Keep its bounded retry wait
and timer cleanup. Cancellation racing an accepted backend write cannot undo that
write; document this rather than promising refunded quota. Remove the raw key from
Acquire's error wrapper: keys may contain IPs/identifiers and add no error-class
information. Driver/network interruption remains host client configuration.

### R5 — Timing can use the wrong clock or an obsolete instant (high; reproduced)

Sources: Memory.Allow at memory.go:46–49;
Redis RetryAfter at goredis/limiter.go:166–172;
Postgres allowSQL at pgxdb/limiter.go:63–67,83–93.

- Redis reads server time for admission but computes RetryAfter with application
  time.Until. A command-hook simulation with Redis five minutes behind the caller
  and a still-active one-minute window returned RetryAfter=0. This can turn worker
  waiting into repeated 1ms checks; the opposite skew over-waits. No machine clock
  was modified.
- Postgres captures clock_timestamp before waiting for its conflicting row lock.
  A real probe held the row lock for >2 windows: the queued Allow denied, returned
  an already-past ResetAt, and still asked the caller to wait about 192ms. The
  immediately following fresh call allowed. Existing clamps do not fix stale time.
- Memory similarly samples time before acquiring its mutex. A temporary in-package
  overlay probe confirms it uses the pre-lock sample even if the decision's clock
  would now be past the window. The repository source was not edited for the probe.

Recommendation: derive both admission and relative retry from one decision time
inside the serialized state transition. Memory samples after locking. Postgres
samples in the conflict transition after acquiring the row lock, preserving its
atomic one-statement admission. Redis returns a relative server-computed duration.
ResetAt is informational; correctness of worker waits must not rely on synchronized
application/backend wall clocks. Do not replace atomic SQL with a read/check/write
sequence to make it shorter.

### R6 — Garbage collection changes policy after a window change (medium; reproduced)

Sources: memory.go:51–56; pgxdb/limiter.go:83–93; goredis/limiter.go:41,72,78.

After consuming a one-request/50ms budget and waiting beyond its 125ms retention,
changing the same key to PerHour(1) allowed in Redis but denied in Memory and
unpruned Postgres for almost an hour. Pruning an equivalent expired Postgres row
before the second call made it allow. Postgres writes expires_at but never consults
it for admission. Memory recomputes expiration from the newly passed Window.

Recommendation: expired state is logically absent whether or not physical cleanup
has run. Persist original state expiry and honor it before applying a new budget.
Document active-window changes separately: hosts should use stable policy windows
per key and version/change the key when intentionally replacing the window. A
window-change reset should not emerge accidentally from which backend was wired
or when garbage collection ran. Pin the exact active-change rule during the
implementation design; changing only Requests/Burst for a stable window can retain
consumed quota and apply the new ceiling.

### R7 — HTTP failure policy is hidden in the API; retry headers have a bug (medium)

Sources: middleware.go:20–52,59–66; middleware_test.go's deliberate fail-open test;
authsvc/service.go:1341–1351 and refresh.go:58–62; security.go:426–456.

Middleware is explicitly, silently fail-open on every limiter error. This is a
current documented policy, not an accidental branch. It is a poor universal
framework policy: protecting an upstream paid API, a login endpoint and a public
read route can require different outage behavior. Merely adding a logger to the
RateLimiter service does not help (R8).

The default Retry-After rounding overflows: a maximum positive time.Duration
produced header `-9223372035` with HTTP 429. Authentication's custom rejection
callback duplicates the default JSON response but drops retry guidance entirely.

Recommendation: explicit middleware configuration for host-selected fail-open or
closed behavior plus a synchronous host error hook. Default closed for new
configuration, with authentication's deliberately open refresh/IP paths migrated
to an explicit open setting to preserve their decision. Validation failures and
caller cancellation should not be silently treated as dependency availability
failures. Keep custom key/reject seams, use safe quotient/remainder rounding, and
let auth's duplicate reject use the shared default. Default dependency failure
should be a service-unavailable response, distinct from quota-exhausted 429.
Record the HTTP/API migration before implementation; this is a justified policy
change, not a claim that the existing tested policy was an unintentional bug.

Authentication review boundary: login/token, passwordless, resend and identifier
change operations already propagate limiter errors; refresh/IP gates deliberately
open. Client attribution uses trusted-proxy context/RemoteAddr with spoofed-XFF
coverage. Production validation rejects known Memory/in-process implementations
but accepts unknown wrappers by design. A decorator around Memory can evade this
heuristic; do not advertise it as proof of shared storage. Full authentication
posture/metadata redesign belongs in the later pocket audit, not this SDK slice.

### R8 — Surrounding API scaffolding obscures a small capability (design/cleanup)

Sources: ratelimiter.go:19–103; resolver.go:9–104; errors.go:6–9;
goredis/limiter.go:101–105,197–238.

- Limiter.Close is required by every mock/adapter, but all three implementations
  are no-ops and hosts own the database/Redis clients. Remove it from the port and
  bundled implementations; keep resource lifecycle on concrete owners.
- Acquire only needs Allower. Move that core interface's definition out of the
  middleware file if it becomes the common admission seam. Keep Reset on Limiter
  for now; it is a real administrative capability. No need to create another
  optional reset framework just for this cleanup.
- WithLogger promises logging, but its field is never read. The errors
  ErrRateLimitExceeded/ErrLimiterClosed are never produced by the current package.
  Remove the inert option/field and unproduced sentinels. Quota exhaustion is Result,
  not an exception; actual storage/config/capacity failures remain errors.
- RateLimiter.Allow takes ResolveRequest instead of Limit, so this service itself
  does not implement Allower and cannot be passed to the generic HTTP/worker seam.
  Its methods otherwise only forward to a resolver/backend. DefaultLimitResolver
  hardcodes user/service-account/anonymous budgets; Resolve cannot return a lookup
  error and instructs a DB-backed future implementation to silently default.
  ResolveRequest/APIKeyInfo bake authentication schema details into a generic SDK.

**Recommendation for the resolver:** retain the use case of per-subject, tier or
API-key policy, but let host/pocket policy resolve a Limit and call Allower. Remove
the current authentication-shaped service/default resolver/DTO bundle; demonstrate
that two-step flow in a small host example before deleting it. This is justified by
policy ownership, error transparency and incompatible method shapes, not merely
lack of current callers. A future shared resolver seam should be fallible and
purpose-built from actual host policy examples; do not import auth schema into SDK.
The alternative is completing a host-configured, fallible resolver service. It
would need meaningful reusable behavior and direct HTTP/worker composition to earn
its additional layer, as cacher's JSON/namespacing layer now does.

Replace Redis's hand-written script cache (mutex, SHA, loaded flag, SCRIPT LOAD
and NOSCRIPT branches) with its existing dependency's redis.NewScript(...).Run.
That implementation already computes the SHA and retries EVAL after NOSCRIPT.
Preserve error/cancellation behavior and add a SCRIPT FLUSH regression. This is
concrete complexity removal with no new dependency or generic abstraction.

### R9 — The conformance suite misses the important differences (verification gap)

ratelimitertest currently covers six cases: ordinary allow, deny, reset, refill,
independent keys and idempotent Close. It does not pin Burst, invalid input,
empty-key policy, numeric resolution, cancellation, concurrent admission, timing
relationships, state retention, active policy changes or expiration after a window
change. The Memory test specifically preserves one of the disagreements.

Recommendation: shared validation/ceiling/cancellation/atomicity/boundary vectors,
then backend-specific numerical and lock/clock/expiry tests. Keep the useful pgx
exact-K concurrency proof. Replace Close conformance with the contract that
matters; separate deterministic algorithm tests from the small number of live
clock/lock tests. All currently passing suites are a baseline, not evidence that
the reproduced failures are correct behavior.

## Usage and original-framework evidence

Current framework: authentication owns explicit per-operation limits and calls the
raw Limiter. No production SDK RateLimiter/NewDefaultResolver/ResolveRequest use
was found outside their own definitions/comments. Acquire has test coverage but no
current inspected production caller; it remains an intended SDK worker seam and
should be retained. No production consumer of the two SDK error sentinels or
WithLogger was found. Search included aliases/imports and excluded generated,
vendored and node-module code; this is not proof about unknown external apps.

Read-only consumer snapshots:

- Segovia v2: main, SDK v0.8.0, pgxdb v0.6.1; no direct limiter construction or
  Acquire/resolver use found in inspected production Go sources.
- Coordination Hub: clean main, SDK v0.7.0, pgxdb v0.6.1. Its
  pockets/auth/outbound/authconfig.go:625 wires pgxdb.NewLimiter with a host prefix;
  it has a boot StatusCheck and host migration/pruning for ratelimit_windows.
  This is a real deployment-sensitive consumer of the pgx findings.
- GPS360: clean main, SDK v0.7.1, pgxdb v0.6.1, jobs v0.5.0, no local replacements;
  no direct limiter or Acquire/resolver use found in inspected production sources.
  Its generic worker requirements remain valid regardless of that search result.
- Original: docs/fix-cli-and-framework-reference-drift, only pre-existing NEXT.md
  untracked. Its SSE bridge actually invokes resolver-backed rate middleware;
  therefore subject-dependent limit policy is not imaginary. The old middleware
  had explicit fail-open configuration, default closed. Do not copy its double
  resolver call, raw-key logging, old retry rounding or auth coupling.

Original Memory had sliding counters, bounded retention, LRU and a cleanup
worker. Retention and shared budget semantics are useful precedent; active-entry
LRU eviction is inappropriate for enforcing hard quotas. Original throttler had
the same wait-on-RetryAfter pattern plus an optional Redis token bucket. The SDK's
single Acquire helper preserves the generic waiting use case with less plumbing;
adding a token bucket or restoring the old throttler object is outside this slice.

## Proposed implementation order and compatibility

1. Specify/validate Limit, numeric precision, Burst, algorithm boundary, key and
   active-window-change contracts; strengthen shared tests with the reproduced
   cases. Empty keys should be invalid for new APIs (a global budget can use an
   explicit stable key) rather than silently merging missing identities.
2. Fix context and decision-time handling, Redis numerical/relative retry values,
   Postgres expired-row admission and atomic post-lock timestamps. Keep both
   adapters' atomic state transitions; do not weaken them for shorter code.
3. Bring bounded Memory into the same documented approximate-window contract;
   retain active budgets at capacity. Add MemoryConfig, expiry and capacity tests.
4. Simplify lifecycle, worker admission seam, Redis script loading and inert APIs;
   replace the subject-shaped resolver bundle with an explicit host policy example.
5. Add host-owned HTTP failure configuration, safe Retry-After and observable
   errors, migrating authentication's selected open paths explicitly. Do not roll
   unrelated authentication pocket configuration into this slice.
6. Migrate in-repository callers/docs, add AUDIT-011 and RELEASING only when the
   implementation exists, and run full make check/docs/live conformance then.

Expected breaking surfaces: Memory constructor; Limiter.Close and concrete closes;
RateLimiter/New/Option/WithLogger/Resolver/DefaultResolver and their DTOs; unproduced
sentinels; middleware signature and error policy; stricter accepted limits/keys;
Memory Burst/window/capacity behavior; expired-state/window-change semantics.
Redis's persisted time representation will need a versioned key prefix or an
explicit old-state transition; never read old nanosecond hashes as milliseconds.
Determine pgx schema effects during implementation; hosts own migrations and
shared tables. Redis/pgx/CMS-cache changes from S8a stay independent of this rollout.
Do not update consumer app pins or flush deployed quota state during this audit.

## Primary-source checks

The recommendations above come from local implementation and probes. External
checks confirm the relevant platform behavior:

- [Redis TIME](https://redis.io/docs/latest/commands/time/) returns server time;
  deriving a duration on another machine reintroduces clock skew (our inference).
- [Redis Lua API](https://redis.io/docs/latest/develop/programmability/lua-api/)
  documents Lua-number/RESP conversion; the numeric-range findings are independently
  reproduced against installed Redis 8.4.0.
- [PostgreSQL clock_timestamp](https://www.postgresql.org/docs/current/functions-datetime.html)
  is wall-clock time during execution, distinct from transaction/statement start.
  The pre-lock sampling bug is in this query's placement of that call.
- [go-redis v9.18.0 Script.Run](https://github.com/redis/go-redis/blob/v9.18.0/script.go)
  provides SHA evaluation and NOSCRIPT fallback, supporting removal of our duplicate
  loader. The checked dependency is the version in the local module graph.

## Verification and handoff

PASS, with Go 1.26.1 and the GOCACHE above:

- SDK module: go build ./..., go test ./..., go vet ./... (approved loopback for
  existing HTTP tests). Existing tests pass, some cached.
- Redis and pgxdb modules: go build ./..., go test ./..., go vet ./.... Their first
  hermetic runs skipped env-gated live checks; the separate owned service run below
  exercised limiter suites.
- Fresh SDK limiter race: go test -race ./sdk/capabilities/ratelimiter/... -count=10.
- make guard: all 23 architecture guards passed.
- Disposable Redis 8.4.0: go test -race -run '^TestConformance_Limiter$' -count=3 -v .
- Disposable PostgreSQL 17.4: go test -race -run
  '^TestLive_(ConformanceLimiter|Limiter)' -count=1 -v .; includes exact-K atomicity,
  timing, burst, key isolation, cancellation, pruning, status and missing-table checks.
  Both live suites passed before independent probes exposed their gaps.
- The owned Redis/Postgres runner stopped both services in finally. It used a new
  temporary data directory and loopback ports; no existing datastore was touched.

Reproduced findings, not expected-behavior regression tests:

- /tmp/gopernicus-ratelimiter-sdk-probe.go + .log: cancellation, quota mutation,
  invalid input, Burst, retained expired keys and negative Retry-After.
- /tmp/gopernicus-ratelimiter-live-probe.go: same inputs across Memory/Redis/pgx,
  numeric ranges, expiry/window changes and delayed Postgres row locks.
- /tmp/gopernicus-ratelimiter-run-live.py + /tmp/gopernicus-ratelimiter-live.log:
  service versions, green live suites and reproduced failures above.
- /tmp/gopernicus-ratelimiter-redis-hook-probe.go + .log: simulated cross-machine
  clock skew and successful-command cancellation. No real clock was changed.
- /tmp/gopernicus-ratelimiter-lock-probe_test.go with
  /tmp/gopernicus-ratelimiter-lock-overlay.json: go test -overlay=<that JSON>
  ./sdk/capabilities/ratelimiter -run '^TestAuditMemory' -v -count=1. Added only a
  virtual test file for execution; no SDK source/test file changed on disk.

Named backend and platform reviews were read-only and contributed the adapter,
context, policy and lifecycle findings; parent independently reproduced the
concrete failures and checked the original/consumer evidence. Initial external
search used zsh's special `path` variable and failed in that subprocess; rerun
using Python/subprocess was successful and changed no checkout/environment state.

No unresolved setup/test failure. Confirmed product defects remain intentionally
unfixed in this review. No full make check/docs build was repeated for two audit
Markdown edits; the previous cacher implementation's full gates remain historical
coverage, not verification of future rate-limiter fixes. No consumer, original
framework, browser, production/distributed load, other live Postgres contracts,
Redis events/cache, or external service test is claimed by this slice.

Exact changed files relative to /tmp/gopernicus-ratelimiter-review-baseline.json:

- plans/framework-audit-ratelimiter.md (this review).
- plans/framework-audit.md (progress and next-session handoff).

No AUDIT.md migration entry or RELEASING update yet: recommendations are not
implemented. Next concrete step: implement the bounded ratelimiter corrections
and approved contract choices above, then audit SDK events. The unresolved design
choices to pin are numeric bounds, active window-change handling and exact HTTP
configuration shape; all recommendations and their compatibility costs are now
concrete enough for owner review.

## Implementation follow-through — 2026-09-10

R1–R9 are addressed by [ratelimiter-implementation.md](ratelimiter-implementation.md):
one documented anchored millisecond contract, normalization bounds and immutable
live windows; bounded Memory preserving active budgets; context and relative retry
corrections; explicit HTTP outage policy; Allower/Acquire and lifecycle/resolver
simplification; authentication caller migration and runnable host policy example;
expanded shared, exact-state and live regressions. AUDIT-011 records consumer API,
behavior and distributed-state migration. RELEASING coordinates affected modules.

Final review additionally found old/new physical-key collision limits and a
Postgres insert blocked behind a rolled-back transaction. Disjoint rollout
namespaces are required; existing legacy collisions are preserved. Postgres now
initializes missing keys with an expired zero-count placeholder and makes every
quota decision in the locked conflict branch. Only an initialization marker allows
one repeat; SQL/context errors are not retried. See the implementation plan for
final verification, bounded initialization behavior and task-relative inventory.
Historical probes above use removed APIs and are not current verification commands.

Final implementation verification passed: SDK/auth/minimal race and real HTTP,
all 42 modules' make check gates, docs build, running-host smoke, and expanded
live Redis/Postgres race/conformance (including both timing regressions and
bounded initialization). Owned services stopped. No limiter findings remain
open; unrelated pgxdb pool lifetime defaults are recorded for the integration
pass. Next audit: SDK events.
