# Cacher design: complete the intended capability

Status: IMPLEMENTED — 2026-09-10. The owner approved this researched design;
[implementation and verification](cacher-implementation.md) are complete. The
proposal/research below is retained as the decision record; the implementation
plan and AUDIT-010 record final signatures and behavior.
Parent: [framework-audit.md](framework-audit.md).
Prior correctness review: [framework-audit-cacher.md](framework-audit-cacher.md).

## Owner clarification and scope

The owner asks whether apparently unnecessary cache APIs are unfinished features
and wants the original Gopernicus and other frameworks reviewed before folding
the audit recommendations into a design. Reassess the proposed removal of Cache,
the role of typed helpers and the smallest useful framework caching capability.
Preserve confirmed defects as findings; do not equate incomplete code with an
unneeded abstraction. This turn produces an evidence-backed design, not code
changes or an implemented AUDIT.md migration entry.

## Plan

1. Review original cache interfaces, service, JSON helpers, memory/Redis/no-op
   adapters, tests and code-generation inputs/actual consumers. The named backend
   reviewer independently assesses original behavior and gaps, read-only.
2. Parent compares current official framework documentation for Django, Laravel,
   Symfony and a Go web framework; distinguish data/fragment/HTTP response caching,
   storage adapters, read-through helpers and optional advanced features.
3. Define concrete host-developer use cases, minimal coherent APIs, ownership,
   error/cancellation/invalidation policy, configuration and tests. Keep third-party
   dependencies out of SDK and domain decisions in host/pocket code.
4. Revise earlier recommendations explicitly, connect every retained abstraction
   to behavior and every proposed feature to an example, and stage implementation.
   Update the shared audit handoff; only implemented breaking changes enter AUDIT.md.

## Preconditions

Branch/base: firestore-authentication / 6807ed06; 532 prior dirty paths.
Snapshot: `/tmp/gopernicus-cacher-design-baseline.json`. Original checkout:
`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original`, read-only; inspect
its branch/instructions before drawing conclusions. Preserve previous audit and
concurrent changes. No consumers, source, generated outputs, dependencies,
release/version pins, credentials, production services or publication changed.

## Conclusion

Complete the intended application-data cache service. The current empty Cache is
an unfinished port of useful original behavior, not evidence that a service has
no place here. A small Storer + Cache + typed helpers + opt-in Pages design fits
Gopernicus's purpose: eliminate recurring host boilerplate while preserving explicit
ownership and portable contracts. This revises the earlier blanket recommendation
to delete Cache/New and avoid JSON helpers. Confirmed C1–C6 defects remain open.

Three concerns can share a package without becoming one mechanism:

| Concern | User-facing purpose | Owner |
|---|---|---|
| Store bytes | Exchange Memory/Redis/host implementations | Storer and adapters |
| Cache application data | Read typed values, load misses, namespace keys, report optional-cache failures | Cache and small typed helpers |
| Cache HTTP responses | Reuse eligible public HTML with matching headers and request scope | Pages middleware |

All remain in `sdk/capabilities/cacher`; no new SDK subpackage, pocket, framework
manager or service locator is required. Hosts may use raw stores directly.

## Original framework evidence

Reviewed checkout: `docs/fix-cli-and-framework-reference-drift` / `0f763a9` at
`/Users/jrazmi/code/gopernicus-ecosystem/gopernicus-original`. Prior instruction
inspection found no AGENTS.md in this tree/ancestors; the original is read-only.

| Original implementation | Evidence | Decision |
|---|---|---|
| Real Cache service | infrastructure/cache/cache.go:91–183 implements raw operations and optional tracing | Complete the service with shared behavior; do not restore only forwarding methods |
| Typed data helpers | cache.go:191–226 implements generic GetJSON/SetJSON | Restore this convenience, context first and explicit JSON semantics |
| Real typed consumer | core/auth/authorization/cache_store.go:91 onward caches bools, maps and slices | Evidence for application-data caching beyond public pages; not an endorsement of automatic permission caching |
| Memory copying and capacity | memorycache/store.go:70–73,117–119,139–145; tests for ownership and hot-key retention | Restore protections; a small stdlib LRU is reasonable |
| Disabled implementation | noopcache/store.go | Restore an explicit Noop value with the normal data-store contract |
| Cache-aside generation | workshop/codegen/generators/cache_tmpl.go:85–105 | Extract the useful get/load/store behavior into a hand-written helper |
| Deliberate generated shells | generators/cache.go:44–46 always generates wrappers, even without annotations | An empty generated wrapper alone does not prove an unneeded feature |
| Namespaced invalidation | authorization/cache_store.go:244 onward, cache_tmpl.go:144 onward | Retain invalidation as a use case; define portable scope and its limits |

The earlier S8a search correctly found no direct cacher usage in the three current
named consumer apps, but that does not settle intended framework features. The
original has both implemented and deliberately scaffolded caching surfaces.

Do not copy old behavior indiscriminately:

- Original Redis has the same unescaped namespace/glob issue and forwards driver
  TTL sentinels directly. Current C5/C6 fixes still apply.
- Original StatusCheck uses a shared fixed key, accepts a missing read as healthy,
  and never verifies the returned payload. Do not restore it as a generic health
  requirement. Existing goredis.StatusCheck probes the host-owned client; a real
  cache write/read probe, if desired later, needs a unique key and value checking.
- Generated writes assume method-level invalidation is sufficient, and a comment
  promises logging while discarding Set errors. Transaction commit timing and
  query/filter/tenant key dimensions need actual domain knowledge.
- Original authorization uses wildcards in the middle of keys, not only terminal
  prefixes. Prefix deletion is a narrower portable operation, not a drop-in
  replacement for every original invalidation strategy. See the invalidation
  decision below.
- Original authorization documents nil cache as pass-through but dereferences it
  during successful mutation invalidation. Expanded and direct relation checks
  also share a key despite different result semantics. Generated composite keys
  use unframed fmt.Sprint concatenation. Preserve each operation's meaning in its
  key; do not treat old cache decorators as ready to copy into new pockets.
- Optional tracing is useful, but importing SDK tracing from SDK cacher would
  violate the current capability boundary. Keep stdlib reporting or external
  decorators; no rule change is needed to complete data caching.

## Other frameworks: useful patterns, not feature parity

Sources are official documentation inspected on 2026-09-10. Versions below name
the pages reviewed rather than assert that all are the latest release. These are
API/design comparisons, not execution tests or performance benchmarks.

| Framework | Relevant documented behavior | Design lesson for Gopernicus |
|---|---|---|
| [Django 5.2](https://docs.djangoproject.com/en/5.2/topics/cache/) | Low-level data API, callable get_or_set, bulk reads, prefixes/versions, local-memory and dummy backends, view/fragment caching | Share an adapter contract, provide convenient data access, and keep page/fragment policies distinct |
| [Laravel 13.x](https://laravel.com/framework/docs/cache) | remember loads a missing value; configurable stores; separate stale-while-revalidate, tag and lock facilities with backend restrictions | Loading on misses earns a helper; advanced operations should not silently become universal storage requirements |
| [Symfony Cache](https://symfony.com/doc/current/cache.html) | Cache Contracts use recomputation callbacks, namespaces/pools and stampede prevention; HTTP caching is a separate concern | Provide a small application API with explicit guarantees; concurrency protection is useful but deserves its own defined scope |
| [Fiber cache middleware](https://docs.gofiber.io/middleware/cache/) | Injectable storage, request keys/variation, expiration, response-header options and byte limits | HTTP caching needs its own eligibility/key/metadata/limit configuration, even over the same byte storage |

My inference from these comparisons: the recurring framework value is the common
cache-access flow and configuration, not merely a lowest-common-denominator map.
Their larger feature sets are evidence of legitimate future use cases, not a
requirement to implement them all now. In particular, do not import PHP/Python
facades, serialization conventions or global config patterns into Go.

Keep Gopernicus's explicit `ttl == 0` meaning no expiration. Laravel documents a
different zero/negative TTL convention; similarity of method names does not imply
identical semantics. Likewise, Django documents that some counter operations are
not atomic on every backend. Do not build worker locks, rate limiting or leases
on an ordinary Get/Set facade by analogy with framework convenience methods.

## What host developers should be able to do

1. Cache a tenant-scoped catalog, public CMS projection, reference list, rendered
   fragment or expensive external lookup without writing JSON and miss-handling
   boilerplate in every service.
2. Swap Memory for Redis or disable caching through wiring while retaining the
   same service/helper calls and observable value/error contract.
3. Give a cache an app/environment/schema namespace, and choose an explicit TTL
   per operation. Choose memory capacity, page limits and failure reporting in
   the host. The framework invents no entity TTL or automatic tenant identity.
4. Invalidate one entry or a supported literal prefix after a successful write/
   commit. Broader dependency-based invalidation remains a domain decision.
5. Apply optional caching to suitable public HTML routes with correct request
   scope, cacheability checks, response headers and bounded capture.

Use public or read-only data for the first example. Authorization/session/job state
needs its own freshness and synchronization design; the SDK does not silently
cache these merely because an original decorator did so.

## Recommended API responsibilities

Names below are a concrete proposed shape, not implemented APIs. Constructor
spelling and compatibility details belong in the implementation plan; do not
introduce parallel constructors solely to preserve an empty placeholder.

### Storer: explicit byte operations

Keep Get, Set, Delete and GetMany, with context first, separate found/error and
TTL supplied explicitly. Keep GetMany as a real batching seam; promise one API
call/returned map, not a universally guaranteed physical round trip. Missing and
empty values differ. Values have owned byte storage, reads return independent
bytes, adapters are concurrency safe, early capacity eviction is permitted, and
negative TTL is rejected. Memory and Redis use the same semantic suite.

Close belongs to the actual resource owner. Shared Redis clients do not become
owned by Cache; future adapters can still expose real lifecycle methods. Optional
operations such as prefix invalidation are discussed separately below.

### Cache: complete the service with shared behavior

Keep Cache/New as the intended host-facing service. It should provide:

- Namespace handling consistently across reads, writes, bulk results and
  invalidation. Use an unambiguous namespace boundary, not unchecked concatenation
  that lets one namespace/key pair collide with another. Namespace/schema version
  comes from the host; tenant/filter dimensions in logical keys remain explicit.
- The raw operation methods needed for callers to manage caching deliberately.
  These preserve errors rather than quietly rewriting every failure into a miss.
- One small, optional host reporting callback for cache failures (operation and
  cause; no raw payloads). It may bridge to slog/metrics. No new observer hierarchy
  or SDK capability-to-capability import is needed. A later external tracing
  decorator can cover all operations if wanted.
- Support for typed helpers and the documented miss-loading policy below. These
  replace repeated application code, providing a concrete reason for the service.

Use a plain configuration struct for the namespace/reporting settings. There is
no reason to retain an empty CacheOption layer simply because it was scaffolded;
configuration can be redesigned once around the meaningful fields. Leave TTL
explicit per operation initially, since zero already has an important meaning.

Raw stores remain usable independently. A host uses a non-nil Noop implementation
to disable caching without branching inside every helper. Decide New(nil) in the
implementation plan (reject accidental nil or deliberately normalize to Noop);
never keep the current mixture of nil-safe JSON helpers and raw-method panics
without an explicit documented contract. The recommended host pattern uses Noop
explicitly, so correctness does not depend on nil receivers or typed-nil tricks.

### Typed helpers: explicit serialization and load-on-miss

Propose package functions with context first:

```go
GetJSON[T](ctx, cache, key) (T, bool, error)
SetJSON[T](ctx, cache, key, value, ttl) error
GetOrLoadJSON[T](ctx, cache, key, ttl, load) (T, error)
```

The generic functions handle structured data without requiring codec registries,
reflection over entity methods, a generic service instance per type or changes to
raw byte storage. JSON remains explicit; hosts needing another representation
can use byte operations directly. Supported values must round-trip through JSON;
key schema/version belongs to the host. Do not cache Go pointers/identity across
calls merely because Memory can hold them.

For example (proposed API):

```go
key := "tenant:" + tenantID + ":catalog:v1"
catalog, err := cacher.GetOrLoadJSON[Catalog](
    ctx, s.cache, key, 5*time.Minute,
    func(ctx context.Context) (Catalog, error) {
        return s.catalogs.Load(ctx, tenantID)
    },
)
```

The host chooses the loader, scope and TTL. The framework supplies the repeated
read/decode/load/encode/store flow. Keys with unrestricted multiple components
must be encoded unambiguously; the example assumes an already validated tenant ID.

Define failure behavior before implementing:

| Situation | Raw/GetJSON/SetJSON | GetOrLoadJSON |
|---|---|---|
| Present valid entry | Return value with found=true | Return decoded value |
| Missing entry | found=false, nil error | Call loader |
| Cache read/backend error | Return error | Report cache failure and call authoritative loader |
| Invalid cached JSON | Return decode error | Report failure and load; successful replacement repairs entry |
| Loader fails | N/A | Return loader error; do not cache it |
| Encoding or cache Set fails after successful load | Return error for explicit SetJSON | Report cache failure; return successfully loaded value |
| Context already canceled / caller cancellation observed | Return context error | Return cancellation; do not treat it as an ordinary miss or start detached work |
| Noop store | Miss/successful discarded write | Call loader every time |

Successful false, zero, empty and nil values are legitimate data; a separate found
bit prevents accidental treatment as misses. Do not automatically cache not-found
errors or other failures. Hosts with special negative-caching policies can use
explicit operations. Invalidation errors stay visible and do not inherit the
loader helper's tolerance of an optional cache failure.

Initial GetOrLoadJSON does not promise one loader invocation under concurrent
misses. No goroutine is spawned to detach a load from the caller. Coalescing
concurrent requests is a legitimate next enhancement, but requires scope,
leader/waiter cancellation, result ownership and invalidation tests before it is
advertised. It is not equivalent to a distributed lock or atomic computation.

### Memory, Redis and Noop

Restore copying, expiration cleanup and host-configurable finite Memory capacity.
A small stdlib LRU, like the original, earns its extra lines by keeping frequently
used entries under pressure. The earlier preference for arbitrary eviction was
only one simple option, not a requirement; recommend a single readable LRU policy,
not a pluggable eviction system. Remove expired values on access and prune them
before evicting live values at capacity. No periodic janitor is necessary for a
bounded store. MaxEntries bounds cardinality, not payload bytes; retain the
separate page-body cap and document that distinction. A full payload budget can
be added deliberately if required by generic workloads.

Redis retains the host-owned client and implements actual value/TTL semantics.
Fix namespace escaping and negative-duration sentinel leakage; document duration
resolution. Noop provides explicit disabled behavior and shares common input/
cancellation semantics while intentionally never retaining data. Its conformance
suite must test disabled behavior, not pretend every Set becomes a hit.

### Invalidation: portable operations plus honest extensions

Single-key Delete is fundamental. Replace backend-specific DeletePattern with
literal prefix invalidation for the portable Memory/Redis path. Treat prefix
invalidation as a small optional capability (`PrefixDeleter`) rather than requiring
every future cache backend to enumerate keys. Do not simulate it with an unsafe
key index or a process-local lock and call it distributed atomic invalidation.

The service must namespace prefix deletion identically to reads/writes. Recommend
`Cache.InvalidatePrefix(ctx, prefix)` forwarding only when the underlying adapter
supports PrefixDeleter, returning an error matching `errors.ErrUnsupported`
otherwise. The different service method name avoids making every Cache appear to
implement the optional raw-store port. Hosts that require prefix invalidation
check PrefixDeleter on the selected adapter during wiring. The service operation
must not be described as universally supported; never silently continue after an
unsupported invalidation on a production write. Conformance has core and optional
prefix groups, with no skipped claimed capabilities.

Redis escapes the entire literal physical prefix. Empty logical prefix clears
only this cache's namespace. Avoid global Flush/Reset on shared storage. Actual
key deletion remains non-atomic with respect to concurrent writers and loads.

Original authorization uses multidimensional wildcard deletion. The replacement
requires consciously choosing coarser prefix invalidation, reorganizing keys, or
later adding tags/versioned generations with appropriate shared-store semantics.
The original patterns cannot simply have their last '*' trimmed. Tags and
stronger invalidation are valid future features; they should be driven by a real
pocket requirement rather than imposed on every store today. Authorization cache
migration itself is outside this SDK design.

Do not restore automatic cache decorators on every repository. Provide a hand-written
example where cached reads and post-commit invalidation are explicit. Shared cache
access during an ambient database transaction can violate its read semantics or
publish uncommitted state; the host/pocket owns when caching is appropriate.

### Pages: distinct HTTP policy over the same storage

Retain Pages and its capability-owned location, but finish C1/C2/C4 as one coherent
public HTML policy: request/host scope, directives, Vary restrictions, response
metadata, per-request header handling, eligible media types, render failures and
bounded capture. A raw value helper cannot do this automatically. Data caching
also supports HTML fragments as strings/bytes without requiring a template engine
or a new fragment subsystem.

Keep host-supplied page TTL and capture limits; CMS should expose them through
its existing Config with the current 60-second default. Narrow supported response
shapes are acceptable when bypass behavior is explicit. Preserve streaming and S6
error forwarding. Keep response record/key versions separate from host data keys.
The original framework comparison does not remove any reproduced HTTP defect.

## What changes from the earlier recommendations

| Earlier recommendation | Revised recommendation |
|---|---|
| Remove Cache/New because they currently have no methods | Complete them: original code/callers and framework comparisons establish useful service behavior |
| Avoid restoring JSON helpers | Restore explicit typed JSON helpers and a load-on-miss helper |
| Prefer arbitrary capacity eviction; no need for LRU | A small stdlib LRU is justified by hot-key retention; avoid strategy frameworks and janitors |
| DeletePattern becomes required DeletePrefix | Literal prefix semantics remain recommended; make support an explicit optional capability |
| Only observed current prefix caller matters | Original auth has broader wildcard needs; acknowledge migration limits and future tags/generation requirements |
| Fix byte ownership/expiry, namespace escaping, TTL/cancellation and Pages | Retain all confirmed fixes and integrate them into service/adapter/HTTP contracts |
| Keep shutdown host-owned | Retain; Noop makes disabling explicit without owning shared connections |

## Staged implementation and proof

1. **Storage contract and adapters:** settle Memory config/defaults and prefix
   capability, fix C3–C6, add Noop and shared/optional conformance. Use current
   Memory plus a disposable Redis server. Keep changes independent of Pages.
2. **Usable data-cache service:** complete Cache namespace/reporting/raw API,
   GetJSON/SetJSON/GetOrLoadJSON. Demonstrate a scoped read-only/public lookup in
   an example with a counted loader: miss, hit, expiry/invalidation, disabled
   cache, cache outage/corrupt value and source error. Include separate namespaces
   sharing one store and concurrent caller/cancellation ownership tests. No
   automatic repository wrapping or authorization-cache adoption.
3. **HTTP cache:** repair Pages and pass TTL/body limits through CMS. Exercise the
   actual public route composition plus current S8a probes and S6 error cases.
   A correct data cache alone does not make HTTP caching correct.
4. **Adoption record:** run appropriate module/workspace checks, update examples,
   docs and RELEASING.md; add an implemented AUDIT entry covering actual public
   signature, constructor, prefix and page-key/format changes. No release pins or
   consumer upgrades are part of this research turn.

Defer coalesced loads, distributed locks, tag invalidation, stale-while-revalidate,
multilevel caches, bulk-write APIs, warmers and generic codec registries until a
specific example and guarantee justify them. They are named extension directions,
not declarations that those features should never exist. Health/readiness is
host policy; an optional performance cache outage need not make the whole API
unready, and a critical dependency can be treated differently by its host.


## Review and verification record

Named lead-backend-engineer reviewed the original cache, tests/conformance,
authorization consumer and generators independently. The review supports completing
the service and flags original key/nil/health-probe defects that should not return.
The recommendation above incorporates its narrower optional prefix port, distinct
facade method, strict raw APIs and caller cancellation guarantees.

Only design/audit Markdown changed in this turn:

- `plans/cacher-design.md` — this researched recommendation.
- `plans/framework-audit-cacher.md` — distinguish observed defects from revised
  feature/removal judgments and link to this design.
- `plans/framework-audit.md` — owner clarification, progress and current handoff.

Verification: source references and official documentation inspected; starting
hash comparison confirms only these three files changed; scoped git diff --check
passes. No production code, tests, consumers, original framework files, generated
outputs, dependencies, AUDIT.md or RELEASING.md changed. No builds/tests were rerun
for this design-only turn. Prior S8a's SDK/Redis build-test-vet, race, guards,
real Redis conformance and failure probes remain the correctness baseline, not
validation of these proposed APIs. Original tests were read, not executed; external
frameworks were compared through documentation, not installed or benchmarked.
That design-only checkpoint had no blocker. The subsequent owner-approved
implementation is complete in [cacher-implementation.md](cacher-implementation.md).
Ratelimiter and SDK events are the next audits.
