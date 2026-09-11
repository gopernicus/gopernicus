# SDK audit S8a: Cacher

Status: REVIEW AND IMPLEMENTATION COMPLETE — 2026-09-10. The original findings
below describe the pre-fix snapshot. The owner-approved
[cacher-design.md](cacher-design.md) revised the initial C7 removal recommendation;
[cacher-implementation.md](cacher-implementation.md) now closes C1–C6 and completes
C7's data service. AUDIT-010 records the final consumer migration. Read the
implementation record for current code, tests and remaining policy boundaries.
Parent: [framework-audit.md](framework-audit.md).
Working baseline: firestore-authentication / 6807ed06; 531 pre-existing dirty
paths. Snapshot: `/tmp/gopernicus-s8a-review-baseline.json`.

## Scope and audit plan

Review `sdk/capabilities/cacher`, its conformance helpers, Redis cache adapter,
CMS page-cache wiring and representative host usage. Determine the intended
contract, establish reproducible correctness findings and recommend the smallest
useful simplifications. This is the review phase; production changes and
unimplemented AUDIT.md migration entries are outside this slice.

1. Read current code, tests, architecture and consumer instructions; inventory
   actual use before recommending removals.
2. Parent reviews page middleware, CMS/invalidation wiring and consumer usage.
   The named lead-backend-engineer independently reviews Storer/Memory/Redis
   contracts, data ownership, expiry, cancellation and lifecycle, read-only.
3. Run baseline build/test/vet and architecture guards. Use disposable probes
   outside tracked code to distinguish observed failures from design concerns.
   Verify uncertain HTTP/Redis protocol claims against primary documentation.
4. Record ranked findings with trigger, consequence, source evidence, recommended
   change, alternative and compatibility cost. Preserve intentional extension
   points even without current adoption.
5. Update the parent handoff with coverage, exact verification, changed files,
   implementation recommendations and the next slice.

## Preconditions and constraints

Go 1.26.1; no root module. Use module-local `./...` or workspace-qualified paths;
`GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`; formatter
`/Users/jrazmi/go/bin/goimports`. Preserve all earlier audit/concurrent work.
No consumer changes, production services, credential reads, dependency changes,
generated-file edits, commits or releases. Named consumers are read-only and
their current checkout/pins must be checked. Use a disposable local Redis only
if available; otherwise mark live adapter behavior unverified. HTTP listener
tests may need approved loopback access. No blanket redesign of cache, CMS or
host applications is authorized by this review.

## Intended shape and usage

Keep `cacher` as a capability package. A byte-store contract, independent memory
and Redis implementations, and opt-in page middleware justify the namespace.
The main issue is incomplete contracts/correctness, including an unfinished
service. The original review proposed removing it; the subsequent owner-requested
comparison supports completing it as described in cacher-design.md. More explicit code is warranted where it protects real
behavior; a general HTTP caching engine or configurable eviction framework is
not warranted by current usage.

Framework usage:

- CMS accepts `cacher.Storer` through Config.Cache (nil disables caching), and
  `internal/inbound/cms/routes.go:61–78` wraps its public routes in Pages.
- Three example hosts supply Memory: minimal, cms and auth-cms. auth-cms's
  content-event subscriber invalidates `page:*` at cmd/server/main.go:441–453.
- Redis supplies the second implementation; the shared conformance suite covers
  both. GetMany has no inspected application caller but is a useful bulk-read
  seam with a real MGET implementation; retain it.
- There are no inspected calls to `cacher.New` or uses of its Cache/CacheOption.
- Read-only searches of current Segovia v2, Coordination Hub and GPS360 Go
  source found no direct cacher/Pages/DeletePattern consumers. This does not
  prove absence of unknown external consumers. Checkout/pins were checked:

| Consumer | Branch / revision | SDK pin |
|---|---|---|
| Segovia v2 | main / 76b3d78 | v0.8.0 |
| Coordination Hub | main / 84ff08a | v0.7.0 |
| GPS360 | main / e1ab3f0 | v0.7.1 |
| Original framework | docs/fix-cli-and-framework-reference-drift / 0f763a9 | monolithic module |

Segovia's parent and v2 AGENTS.md were read; no AGENTS.md was found in the other
three source trees or their ancestors. Original framework
`infrastructure/cache/memorycache/store.go` already copies bytes and bounds entries
with LRU. Those protections are useful precedent. The expanded comparison in
cacher-design.md also finds meaningful original service/JSON and cache-aside
behavior; it supersedes the initial blanket removal/avoidance recommendation.

## Findings

Priorities describe the reproduced failure and its trigger. Cross-host/private
response examples prove SDK middleware behavior; they are not evidence of a
private-data incident in the inspected consumer applications.

| ID | Priority / classification | Finding |
|---|---|---|
| C1 | High, response isolation/cacheability | Pages ignores host, Vary and cache restrictions |
| C2 | High, response correctness | Hits replay body bytes without the corresponding headers |
| C3 | High, data ownership/concurrency | Memory stores and returns caller-mutable slices |
| C4 | High, resource retention | Expired keys persist forever; page capture has no size limit |
| C5 | High conditional namespace failure; contract inconsistency | Pattern deletion is nonportable and Redis namespace text is interpreted as a glob |
| C6 | Medium, underspecified contract | Negative TTL and cancellation behavior depend on adapter details |
| C7 | Incomplete feature; design revised | Cache/New/CacheOption currently have no usable behavior; complete the service rather than delete it |

### C1: Explicit cacheability and key scope

`cacher/middleware.go:25–45` only checks GET, status 200, render/write success and
an HTML Content-Type prefix. Its key at line 29 is `page:` + RequestURI. It never
checks request credentials, cookies, directives, Vary or response cache policy.

Observed through real Pages handlers using httptest recorders:

- Populate `http://one.example/`, request `http://two.example/`: the second
  receives `one.example` as a HIT.
- A handler varying on Accept-Language returns French to an English request.
- Each of `Cache-Control: no-store`, `private`, `no-cache`, and `max-age=0`
  still becomes a HIT; the handler only runs once for two requests.
- `web.NoStore()(Pages(...))` also renders once and then serves a HIT while
  still emitting `Cache-Control: no-store`.

HTTP cache selection distinguishes the target URI and Vary dimensions; no-store
and private constrain reuse/storage. These checks are grounded in
[RFC 9111 §§3–4](https://www.rfc-editor.org/rfc/rfc9111.html#section-3) and
[§5.2](https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2). Pages is an
application middleware rather than a claimed full intermediary implementation;
its public-only warning still leaves these surprising compositions possible.

Recommendation: keep an intentionally narrow public-HTML cache. Default keys
include authority and an explicit scheme/scope policy plus URI; allow the host
to scope further when the same authority serves different sites. Do not infer
trusted tenant/proxy identity from arbitrary headers. Skip authenticated/cookie
requests by default, conditional/range requests and requests requiring freshness.
Honor restrictions already present on the writer before lookup. Do not store
private/no-store/no-cache responses; reject unsupported Vary, Set-Cookie, encoded
or otherwise unsupported responses rather than invent variant/revalidation logic.
Use parsed media types, not `HasPrefix("text/html")`. A host TTL cannot extend
an explicitly shorter response freshness limit; bypass policies the small cache
cannot correctly evaluate. Request-cookie bypass is a conservative application
policy recommendation, not a claim that HTTP universally prohibits such caching.

Compatibility: hits may become misses. Key changes require a new page-cache
format/key version so old raw values cannot be misread. Keep an identifiable
`page:` namespace for coarse invalidation; no persisted domain data migration.
Host applications retain responsibility for mounting only genuinely public,
cacheable routes, and for scoping tenants that are not distinguished by host.

### C2: A response is more than its body

`middleware.go:31–36` always returns status 200 and UTF-8 HTML with the stored
bytes. It drops every handler-produced header. A compressed HTML response returns
identical gzip bytes on a hit with no Content-Encoding; a public page's
Content-Security-Policy disappears, and a declared ISO-8859-1 charset becomes
UTF-8. The probe establishes all three. Header behavior also explains why C1's
cache-policy fields disappear on hits.

Recommendation: cache a small versioned response record containing the body and
explicitly supported response metadata, captured when the response commits.
Preserve Content-Type and applicable stable representation/security headers;
exclude hop-by-hop and per-request fields such as request IDs. Do not replay
Set-Cookie or blindly copy all outer middleware headers. Preserve newly computed
outer headers on hits; nonce-dependent bodies/headers must bypass this cache.
Bypass compressed responses initially (compression can wrap the cache), instead
of adding content-negotiation variants. The header-preservation/bypass contract
must be tested with actual middleware ordering and multi-valued headers.

Alternative: require every header except UTF-8 Content-Type to be supplied outside
Pages and reject all other responses. This avoids a response record but makes
ordinary handlers unexpectedly uncacheable and weakens the middleware's usefulness.
A limited response record is the recommended balance; no generic response cache,
validator engine or automatic compression is needed.

### C3: Own stored bytes and return independent values

`memory.go:43,53,66` exposes the same slices through Set, Get and GetMany. The
public-API probe stores `one`, mutates the original input to get cached `Xne`,
then a Get result to get `XYe`, then a GetMany value to get `XYZ`. The map mutex
cannot protect byte mutation performed outside it. Redis does not expose its
stored bytes this way. Existing race tests passing does not cover that aliasing.

Copy on Set and both read paths, and state ownership in Storer. Callers may reuse
inputs after Set returns and modify returned bytes independently; simultaneous
mutation during the same call remains the caller's responsibility. Test nil/empty
values as present entries without requiring nil and empty slice identity across
adapters. This fixes data corruption without a signature change; code relying on
mutation as an implicit Set is unsupported by the proposed contract.

### C4: TTL hides entries but does not reclaim them; capture is unbounded

Memory's expiry branches only return a miss (`memory.go:31–56`). Nothing removes
expired entries, and Set has no capacity limit. After inserting 1,000 one-nanosecond
entries and reading each after expiry, the probe sees zero live values and all
1,000 entries still retained. Pages keys include arbitrary query strings, so
ordinary public requests can generate indefinitely many one-off entries.

Separately, `middleware.go:41–59` buffers every GET response in full before deciding
whether to cache. A two-MiB non-HTML response leaves two MiB in its capture buffer;
a large HTML response has no bound either. TTL changes do not solve either issue.

Recommendation: one mutex, clone ownership, delete expired entries on read, and a
host-configurable finite capacity. When admitting a new key at capacity, sweep
expired entries, then evict an arbitrary remaining entry if still full. Preserve
replacement of existing keys. No background janitor, timers or eviction strategy
interface is necessary. This was the initial arbitrary-eviction recommendation;
cacher-design.md now recommends a small stdlib LRU for hot-key retention after
reviewing the original implementation and intended data-cache use. A MaxEntries limit caps cardinality, not
payload bytes; say so explicitly. Prefer that small entry limit plus a separate
page-body byte limit for the demonstrated use. A generic Memory byte budget is
an additional feature, not something an entry limit proves.

Pages should stop capturing/discard the buffer once the configured body limit is
exceeded or the response is known ineligible, while continuing to stream the
original response normally. Limits belong in plain host-supplied configuration
with documented defaults. CMS currently hardcodes 60 seconds at routes.go:14–15;
thread page TTL/limits through its existing Config when implementing the page
change, preserving 60 seconds as the default rather than hiding policy in routes.

Compatibility: cache entries may be evicted early and oversized responses become
misses. Explicitly permit eviction in the port; a cache is not durable storage.
Constructor/config evolution and defaults need a concrete implementation plan.

### C5: Literal prefix deletion gives a portable contract

Memory trims one trailing `*` then always uses HasPrefix (`memory.go:80–85`). Redis
uses glob matching (`goredis/cacher.go:108–119`). The port explicitly permits
backend-dependent syntax, so this is partly a design problem rather than a
violation of a well-specified existing port. Live probes show `DeletePattern("user:1")`
deletes `user:10` in Memory and retains it in Redis.

A separate adapter bug is more consequential: Redis concatenates its supposedly
literal namespace directly into SCAN's glob. With prefix `tenant[1]:`, deleting
`page:*` leaves its own literal key; with prefix `tenant*:`, it deletes a key in
`tenant-other:`. Both failures reproduced against disposable Redis 8.4.0. Glob
semantics are documented by [Redis SCAN](https://redis.io/docs/latest/commands/scan/).

Replace DeletePattern with `DeletePrefix(ctx, prefix)` and define literal byte
prefix semantics. Redis must escape the entire configured namespace plus supplied
prefix before appending its own wildcard. Memory uses HasPrefix directly. Exact
keys use Delete. The only inspected application invalidation call changes from
`DeletePattern(ctx, "page:*")` to `DeletePrefix(ctx, "page:")`. Empty prefix should
mean all keys within that store's namespace; test namespace isolation explicitly.

Alternative: implement a common glob language in Memory and correctly escape
Redis's namespace. It preserves more API but adds parsing/escaping semantics no
inspected caller needs. Literal prefixes express the actual use more clearly.
This is a justified breaking change, including custom stores/fakes and
conformance helpers. The subsequent cacher-design.md refines DeletePrefix to an
optional raw-store PrefixDeleter and a namespaced Cache.InvalidatePrefix service
method. Original authorization also uses middle wildcards; its requirements are
not covered by mechanically replacing a terminal wildcard with a prefix. Lifecycle and concurrency guarantees must be stated too:
prefix deletion is not an atomic barrier against concurrent writes.

### C6: Define TTL, cancellation and ownership of resources

TTL zero is specified; negative TTL is not. The adapter forwards durations directly
to go-redis (`cacher.go:97–98`). Its `-1ns` sentinel selects KEEPTTL. In the live
probe, overwriting a 60ms key with -1ns produces an immortal Memory value but an
expired Redis value after the original deadline. Command capture also shows
1.5ms becoming PX 1. The SDK contract should not accidentally expose driver flags.

Reject negative TTL in both adapters. Retain zero = no expiry and positive =
expiry, document Redis millisecond resolution and normalize positive durations
without unintended early truncation. Test overwrites that add/remove/change TTL.
Already-canceled Memory operations currently still mutate/return success; add a
pre-operation context check and specify the guarantee, while avoiding a promise
that cancellation rolls back completed I/O or interrupts a mutex acquisition.
Redis I/O cancellation/timeouts also depend on its host-owned client configuration.

Remove Close from the required storage-operation port. Both supplied cache Close
methods are no-ops; Redis's client is shared with other capabilities and explicitly
owned by its host. Hosts can close concrete resource owners or use io.Closer where
needed; this does not forbid future adapters with real cleanup. Retain useful
GetMany rather than pruning it solely because application callers are absent.

Compatibility: negative durations become errors; generic `Storer.Close` users
must move shutdown to the resource owner. No inspected host relies on that method.
Keep cache misses distinguishable from empty values and backend errors. Pages
should treat backend errors as misses without taking the website down, and only
accept a hit when the Get error is nil. Adapter logging/tracing can expose outages;
a new cache service wrapper is not required for that.

### C7: The cache service is unfinished (revised after owner clarification)

`cacher.go:38–59` defines Cache, CacheOption and New. Cache currently has one
unexported field and no methods; its comments promise convenience methods that
are not implemented. That observed limitation remains true.

The initial recommendation to delete these APIs was too narrow. The owner asked
for comparison with the intended original framework and other frameworks first.
The original has real service operations, typed GetJSON/SetJSON helpers, an actual
authorization consumer and generation logic for cache-aside reads. The expanded
research supports completing Cache with namespace handling, explicit failure
reporting and typed load-on-miss helpers. See [cacher-design.md](cacher-design.md)
for the proposed contracts, alternatives and staged implementation. Redesign the
empty option layer around meaningful configuration; do not delete a useful
service concept merely because its methods have not been ported yet.

## Keep, defer and test deliberately

Keep the package, byte-store seam, GetMany, Memory and Redis adapters, middleware
placement in the owning capability, optional host wiring and the existing
render/write/flush error handling added in S6. Do not turn Pages into an auth layer.

A deterministic probe pauses an old render, deletes `page:*`, then finishes the
render: Pages repopulates the old value. This is the ordinary cache-aside race,
not a promise currently made by Storer. auth-cms's invalidation comment overstates
"the next request re-renders fresh". Document best-effort invalidation; defer any
stronger generation/transaction protocol to the CMS/pocket audit if the host needs
immediate unpublish guarantees. Async event delivery already implies a stale
window. Do not add distributed locks, stampede suppression, transactional
invalidation or stale-while-revalidate during this SDK slice.

Conformance additions should cover input/output ownership, empty values, overwrite
and expiry boundaries, negative TTL, canceled contexts, prefix literals/siblings/
namespaces and repeated deletion. Existing 30ms immediate-hit timing can fail after
a scheduler pause; separate immediate round-trip checks from eventual expiry.
Use a controlled clock for Memory boundaries and generous eventual assertions for
Redis. Page behavior tests must cover both middleware orders, host separation,
cache restrictions, headers, byte caps, adapter errors and the S6 failure cases.


## Recommended implementation sequence

Superseded by the owner-requested design refinement in
[cacher-design.md](cacher-design.md): correct storage, complete the application-data
service/typed helpers, then repair HTTP caching. The historical sequence below
identifies the same correctness work; it no longer calls for removing Cache/New.

1. Implement C3/C4 Memory ownership/reclamation and C5/C6 portable contracts in
   SDK and Redis. Keep the existing module split and direct adapter wiring;
   complete C7's service in the data-cache phase described by cacher-design.md.
   Decide plain Memory configuration with an explicit entry limit and one simple
   eviction policy; no eviction framework.
2. Implement C1/C2 and the page capture cap together so response eligibility,
   key scope, metadata and limits have one coherent contract. Plumb host-owned
   TTL/limits through CMS with its existing 60-second default. Preserve S6's
   streaming/error guarantees and publish the supported middleware ordering.
3. Expand meaningful conformance/HTTP regressions, run the existing real Redis
   cache suite and new adapter probes, then module/workspace checks. Update
   canonical docs, examples and a new implemented AUDIT.md migration entry.
   Record the actual Cache completion/config decisions, Storer.Close removal,
   optional prefix invalidation contract and page entry format/key changes
   together. The earlier proposed removal of Cache/New is superseded.

At this review checkpoint the recommendations were not yet implemented. They
are now implemented and verified in cacher-implementation.md and recorded in
AUDIT-010. Ratelimiter and SDK events remain unreviewed.

## Verification and handoff

All Go commands used `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.

- **PASS, sdk module:** `go build ./... && go test ./... && go vet ./...`.
  Existing suites were cached; approved loopback access was available for HTTP
  tests. This is a baseline, not evidence that the newly found cases work.
- **PASS, sdk module:** `go test -race ./capabilities/cacher/... -count=3`.
  Existing cache suites ran fresh; no shared-slice mutation test exists yet.
- **PASS, integrations/kvstores/goredis:**
  `go build ./... && go test ./... && go vet ./...`. Ordinary module tests were
  cached and did not verify environment-gated Redis event/limiter suites.
- **PASS, repository root:** `make guard`;
  `/tmp/gopernicus-s8a-guard.log`.
- **PASS, fresh real cache conformance:**
  `REDIS_TEST_ADDR=<owned loopback address> go test -run '^TestConformance_Cacher$' -count=1 -v .`
  in integrations/kvstores/goredis. All eight current cases passed on a disposable
  Redis 8.4.0 started with persistence disabled; no existing server was used.
  The same server reproduced the namespace deletion, pattern and TTL differences
  in C5/C6. It was stopped in the runner's finally block.
- **OBSERVED FAILURES, public API probes:**
  `go run /tmp/gopernicus-s8a-pages-probe.go` from sdk;
  `go run /tmp/gopernicus-s8a-cache-probe/main.go` from repository root;
  `go run /tmp/gopernicus-s8a-redis-probe.go` from the Redis module with the
  disposable server. These observation programs exit zero while printing the
  incorrect behavior; they are not passing regression assertions. Their source
  and logs preserve exact triggers for implementation tests.
- **PASS:** scoped `git diff --check` and review-baseline comparison; only the
  two plan files below changed. Production source, tests, generated files,
  dependencies, consumer checkouts, AUDIT.md and RELEASING.md are unchanged.

Artifacts:

- `/tmp/gopernicus-s8a-review-baseline.json` — starting source hashes.
- `/tmp/gopernicus-s8a-pages-probe.go` and `.log` — actual Pages middleware calls.
- `/tmp/gopernicus-s8a-cache-probe/main.go` and
  `/tmp/gopernicus-s8a-cache-probe.log` — Memory ownership/retention/cancellation
  and go-redis command capture with networking forbidden by its hook.
- `/tmp/gopernicus-s8a-run-redis.py`, `/tmp/gopernicus-s8a-redis-probe.go` and
  `/tmp/gopernicus-s8a-redis.log` — owned Redis lifecycle, cache conformance and
  live parity failures. Rerun the Python helper through normal loopback approval.

Exact changed files: `plans/framework-audit-cacher.md` (new) and
`plans/framework-audit.md` (status/index/handoff). Named backend reviewer was
read-only; parent wrote the review and verified live Redis findings.
No deployment, consumer build, full host/browser run, full 42-module check or
live Redis event/ratelimiter test was performed for this read-only cache audit.
Those omissions do not block the review. Production defects above remain open;
no setup/test execution failure remains unresolved.
