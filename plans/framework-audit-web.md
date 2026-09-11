# SDK audit S6: Web

Status: IMPLEMENTED AND VERIFIED — 2026-09-09. This is the historical review;
the authorized implementation and final decisions are in [web-cleanup.md](web-cleanup.md).
W1–W13 are resolved through fixes or API removal; ReadBody is retained. Consumer
migration: AUDIT-006. The decoder consolidation proposal was refined after
checking real null/validation policies; no new configurable reader API was added.
Parent: [framework-audit.md](framework-audit.md).
Reviewed working baseline: firestore-authentication / 6807ed06, including earlier
uncommitted audits and concurrent work. Baseline hashes:
/tmp/gopernicus-s6-review-baseline.json.

## Scope and audit plan

Review foundation/web as an HTTP toolkit, following real pocket and host callers.
Judge correctness, clarity, package ownership and API value. Review findings and
implementation remain distinct: record reproducible defects and justified
recommendations before restructuring. AUDIT.md records implemented breaking
changes only; this review will not add an unimplemented migration entry.

The package is large enough to track in four independently reviewable groups:

| Group | Files / concerns | Owner |
|---|---|---|
| S6a Routing and middleware composition | handler, groups, methods; stdlib ServeMux behavior and real route wiring | Parent |
| S6b Input, output and errors | request, readbody, response, errors, render, template; validation, limits, status/error exposure | Named lead-backend-engineer, read-only; parent verifies probes |
| S6c Lifecycle and HTTP middleware | middleware, trustproxies, server, run, sse, stream; wrappers, cancellation, proxy trust, CORS and streaming | Named platform-sre, read-only; parent verifies probes |
| S6d Static files and OpenAPI | static, openapi, spec; filesystem/cache behavior, reflection and explicit route metadata | Parent |

Named implementer builds a read-only-source usage inventory and disposable
behavior probes when assigned. Inspect framework plus Segovia v2, Coordination
Hub and GPS360 consumers before recommending removal or moving packages. Read
consumer instructions before entering their source. Do not change consumers.
Use local stdlib implementation/docs for protocol mechanics; verify uncertain
standards claims against primary sources when needed. Keep source-derived
findings distinct from external protocol requirements.

### Tasks

1. Snapshot branch/dirty state and package inventory; read current architecture,
   web docs and named-agent instructions. Existing source changes, particularly
   request/errors/readbody and OpenAPI/spec, are baseline and must be preserved.
2. Read all production files and relevant tests; run SDK build/test/vet baseline.
   Classify public symbols by actual use, duplication and meaningful behavior.
3. Trace representative router, request/error, static and streaming paths. Add
   disposable probes outside tracked source to establish observed failures;
   do not treat tests sharing an assumption as proof the assumption is correct.
4. Record ranked findings with trigger, consequence, exact source references,
   smallest useful correction, retained guarantees and consumer compatibility.
   Separate defect, doc drift, design tradeoff and intentionally missing feature.
5. Recommend a staged implementation sequence and update the parent handoff with
   coverage, exact commands/artifacts, changed files and open decisions.

## Preconditions and constraints

Go 1.26.1; no root go.mod. Run ./... inside sdk or use workspace-qualified paths.
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter:
/Users/jrazmi/go/bin/goimports. SDK is stdlib-only and foundation stays flat unless
an explicitly reasoned audit decision revises a detailed rule. No real dotenv,
credentials, datastore services, production mutations, generated-file edits,
commits, releases or consumer changes. Datastore environment variables are unset.
Existing SDK HTTP tests need local loopback access; use the tool's normal
approval review for a necessary listener-enabled run. The working tree contains
substantial unrelated/prior edits, so verify changed paths against this review's
snapshot rather than treating git diff against HEAD as S6 work.

## Findings

All four review groups are complete: 18 production files (2,631 lines) and
19 test files, plus exercised cache/tracing wrappers, pocket routes and hosts.
The SDK baseline passes, but independent probes establish failures the current
tests miss. Priority below weighs consequence and actual adoption; an unused
builder crashing is different from a request path already wired into CMS.

| ID | Priority / classification | Finding | Evidence |
|---|---|---|---|
| W1 | High, correctness | Sibling route groups can overwrite another group's middleware | Protected probe route returns 200 instead of 403 |
| W2 | High, conditional trust failure | Repeated X-Forwarded-For fields can select a forged client address | Correct hop count still selects first, forged field |
| W3 | High, composition | Failed streamed renders are cached and their errors disappear | Partial CMS-style response becomes a cache HIT; no error in access log |
| W4 | High, response correctness | Panic recovery swallows intentional HTTP aborts and appends HTML to partial responses | Real client sees successful 200 instead of interrupted body |
| W5 | Medium, lifecycle | Run returns after shutdown timeout with active connections still alive | Handler completes successfully after Run returns DeadlineExceeded |
| W6 | Medium, observability | Recorded status differs from the final wire status | Wire 201 / recorded 103; wire 200 / recorded 500 |
| W7 | Medium, streaming correctness | SSE multiline payloads lose data or create extra events | Both streaming APIs split one input into two events |
| W8 | Medium, input correctness | ReadBody accepts malformed trailing closers | Both `{"title":"ok"}]` and `{"title":"ok"}}` accepted |
| W9 | Medium, pocket input correctness | Both strict pocket decoders return 400 for limits reached during trailing reads | Disposable tests in both actual packages confirm 400 instead of 413 |
| W10 | Medium, observability | JSON marshal/write errors depend on callers inspecting return values | Ignored marshal failure produces an empty 500 without logged cause |
| W11 | High consequence, currently unadopted | Recursive OpenAPI types crash the process | Bounded subprocess exits 2 with fatal stack overflow |
| W12 | Medium, currently unadopted | OpenAPI schemas and paths disagree with ordinary Go JSON/routes | Wrong field shapes, component collision, invalid component/path names |
| W13 | Medium/low, static serving | SPA cache policy and filesystem error handling are inconsistent; direct handler mounting fails | Direct index lacks no-store; permission error becomes SPA 200; ordinary mount returns 404 |

### W1: Route middleware needs owned slices

`sdk/foundation/web/groups.go:21–26` retains the caller's slice; `:35` and `:47`
append into that shared storage. A parent with spare slice capacity can create a
protected child, then a public sibling, then register the protected route: the
sibling's middleware replaces the protection. The public-API probe supplies
`make([]web.Middleware, 1, 4)` and observes `200 "secret"` instead of 403. No race
or runtime route mutation is needed. Existing tests cover a single nested child,
not sibling isolation with spare capacity.

Copy middleware when a group takes ownership and when extending a chain. Keep
the small ServeMux/group design and registration order. This is a bug fix with
no intended signature or URL change. Groups are heavily used by Segovia/GPS360;
the probe proves a valid construction fails, not that their current routes have
been demonstrated to expose protected data.

### W2: Combine proxy header fields before applying the hop count

`sdk/foundation/web/trustproxies.go:64` uses Header.Get, which ignores later field
lines. With separate `198.51.100.99` and `203.0.113.10` values, TrustProxies(1)
selects the forged first address instead of the proxy-appended second address.
Authentication uses ClientIP for rate-limit/audit attribution.

Combine Header.Values in received order, then apply the existing trusted-hop
selection. Ordered combination follows [RFC 9110 §§5.2–5.3](https://www.rfc-editor.org/rfc/rfc9110.html#section-5.2).
Retain host control of trusted hop count and ingress access. Coordination Hub's
platform adapter already sanitizes its configured ingress header. This fix does
not make a directly exposed backend or an incorrect hop count trustworthy, and
does not require a new proxy-configuration abstraction. Address attribution can
change for repeated-field requests; document that behavior in implementation.

### W3: Render failures must survive response wrappers and prevent caching

`sdk/foundation/web/render.go:32` offers render errors to RecordError, but
`response.go:23` checks only the outer writer. `sdk/capabilities/cacher/middleware.go:50`
neither records nor forwards errors; `:42` caches every 200 HTML response. The
actual CMS public-route wiring uses Pages at
`pockets/cms/internal/inbound/cms/routes.go:77`, with a 60-second TTL.

The probe composes Logger → Pages → Render. A renderer writes partial HTML and
then errors. Request one is MISS; request two is HIT with the same partial HTML;
the renderer ran once and the original error never reached the access log.

Make the capture writer track/forward render and write failures, and skip cache
storage when either occurs. Preserve ResponseController support through Unwrap;
ensure RecordError also works through transparent wrappers. Keep streaming Render:
buffering every page would change a deliberate contract and add unnecessary
memory cost. An already-started response cannot become a clean 500, but a failed
response must not become reusable successful cache content. W6's status rules
also apply to this wrapper. Broader cache eligibility/key/header semantics remain
for S8; this finding does not close the cacher audit.

### W4–W6: Preserve net/http response and shutdown semantics

**W4.** `sdk/foundation/web/middleware.go:35–41` catches http.ErrAbortHandler like
an application panic. In the real HTTP probe, plain net/http yields partial data
plus unexpected EOF. With Panics, the same handler yields a completed 200 body
containing the partial data followed by the framework's HTML 500 page. This also
affects components such as stdlib ReverseProxy that intentionally use the abort
sentinel after response-copy failure.

Re-panic the sentinel. Recover ordinary panics before response commitment; after
commitment, log the cause and abort rather than append a replacement document.
Track commitment locally so behavior does not depend on Logger being installed
or ordered a particular way. Preserve writer/controller capabilities. Tests must
exercise real clients, recovery before/after writes and flushes, and middleware
composition. This deliberately changes failure behavior on the wire.

**W5.** `sdk/foundation/web/run.go:34–35` returns a shutdown error without closing
remaining connections. The probe holds one request open, cancels Run, and sees
DeadlineExceeded while the request context remains live; releasing the handler
afterward still delivers 200. Shutdown intentionally drains without interrupting
active connections. [Go Server.Shutdown documentation](https://pkg.go.dev/net/http#Server.Shutdown).

Retain normal graceful draining, then close remaining server connections when
the grace period expires and return the shutdown failure. Correct
`examples/auth-cms/cmd/server/main.go:625–628`, which says draining cancels SSE
request contexts. Events streams derive from their request context and may last
15 minutes. Do not cancel every normal request at shutdown start, or promise to
stop handlers ignoring cancellation or to manage hijacked connections implicitly.

**W6.** `sdk/foundation/web/middleware.go:290` records the first informational
response as final. Separately, ResponseController.Flush unwraps the recorder and
commits the underlying 200 without updating its bookkeeping. Real HTTP probes:
103 → 201 records 103 while the client gets 201; flush → attempted 500 records 500
while the client gets 200. Both Logger and tracing consume this status.

Pass informational responses through without recording them as the final status;
handle protocol switching deliberately. Intercept flush commitment while keeping
ResponseController forwarding. Verify the same semantics in cacher's wrapper and
nested middleware. The improvement is accurate observability, not a new response
writer framework.

### W7: Encode complete SSE frames before writing them

`sdk/foundation/web/sse.go:123–156` interpolates Event/ID and prefixes the whole
payload once. A string `first\nsecond` becomes `data: first\nsecond\n\n`; only
`first` reaches the event's data. `first\n\nevent: injected\ndata: second` emits
two events. Both SSEStream and StreamWriter share this behavior. Event and ID
metadata also accept CR/LF verbatim; NUL in an ID cannot round-trip.

Validate metadata and serialize the complete event before any frame bytes are
written. Normalize line endings and prefix every data line. This follows the
[WHATWG event stream parsing rules](https://html.spec.whatwg.org/multipage/server-sent-events.html#parsing-an-event-stream).
Retain the active SSEStream API, heartbeat and per-write deadline renewal. Its
events-pocket caller uses structured JSON; no current consumer injection incident
was established. Corrected multiline/invalid-metadata behavior needs migration
documentation. If StreamWriter is retained, also fix its SendJSON string handling,
substring Accept negotiation (including q=0), and lack of deadline renewal.

### W8–W10: Input contracts and response diagnostics

**W8.** `sdk/foundation/web/readbody.go:101` uses Decoder.More after a top-level
object. More stops at `]`/`}`, so malformed trailing closers are accepted. Require
a second decode to return io.EOF, preserving body limits, exact declared keys,
field-presence handling and existing error vocabulary. There are no inspected
production callers; whether to retain this API is a separate decision below.

**W9.** `pockets/authentication/internal/inbound/authentication/security.go:349`
and `pockets/authorization/internal/inbound/authorization/roles.go:402` map every
second-decode error to 400. A small valid object plus over-limit trailing
whitespace reaches MaxBytesReader only during that second read. Temporary tests
execute both private helpers with a 16-byte bound and confirm 400 instead of the
documented 413. Detect MaxBytesError on both reads. Consolidating these identical
readers is useful, but replacing them with current DecodeJSON would silently
relax unknown-field rejection and introduce automatic validation.

**W10.** `sdk/foundation/web/response.go:35–44` returns marshal/write failures
without recording them. Existing handler calls routinely ignore the result, for
example `pockets/authentication/internal/inbound/authentication/sessions.go:291`.
Logger + RespondJSONOK(math.NaN()) returns an empty 500 without an error attribute
in the access log. This demonstrates a diagnostic gap, not a known bad production
DTO. Record failures inside responders while retaining the useful returned error;
keep internal details out of client bodies. Review ignored encoder/write results
in the other responders as the same error-reporting contract, without introducing
a second logging owner.

**Related host policy gap, not an undocumented DecodeJSON guarantee:**
`request.go:39` calls io.ReadAll without a limit. DecodeJSON is used 48 times in
the three consumers, including a generic GPS360 write helper, and the inspected
normal host middleware stacks supply no common body-size boundary. Transport
timeouts and header limits do not bound allocation for an accepted body. Agree
on explicit host-selected JSON body limits and unknown-field policy before a
decoder cleanup; do not silently impose one upload limit on every endpoint.
Retain S2's pointer/value validation-once and null-rejection guarantees.

Consumer evidence: normal host stacks are Segovia `cmd/server/main.go:132`,
Coordination Hub `cmd/server/main.go:155`, and GPS360 `cmd/server/main.go:148` under
the parent plan's reference roots. Their specialized multipart routes have some
body limits; upstream proxy limits were not inspected. GPS360's
`internal/inbound/domains/directory/writes.go:35–53` deliberately accepts unknown
keys and treats null fields as absent. Hub performs further sequential domain
parsing in `internal/inbound/domains/coordination/items.go:245`. Segovia's optional
note route ignores decoding errors at
`internal/inbound/domains/timelines/items.go:441`. Strictness is therefore a real
compatibility choice, not a safe global toggle inferred from the pocket copies.

### W11–W12: The reflection-based OpenAPI builder is not dependable

**W11.** `sdk/foundation/web/openapi.go:305–317` does not register a type before
recursing through its fields. A `Node` containing `Next *Node` causes unbounded
reflection recursion and a fatal stack overflow during BuildOpenAPISpec. The
isolated probe lowers the runtime stack bound to 128 KiB and exits 2; this is not
a recoverable panic or a demonstrated request-triggered production vulnerability.

**W12.** Independent serialization and builder probes establish:

- An embedded field tagged `json:"meta"` is flattened in the schema but nested
  by encoding/json (`openapi.go:336`, tags handled later at `:356`).
- `[]byte` is described as integer-array, `[]*Struct` items as strings, and fixed
  arrays as strings; actual JSON is respectively base64-string, object-array,
  and array. A nil `*string` emits null while its schema allows only string.
- An application type named Error is silently overwritten by the framework's
  standard Error component (`:70–71`). Anonymous types receive an empty component
  name (`:310`). Package-homonym collisions follow from using only Type.Name;
  these were source-established, not separately exercised across packages.
- `/{path...}` and `/{$}` are retained in paths while parameter extraction strips
  the wildcard suffix or omits the anchor. The documented paths and their
  parameter names no longer correspond.

Empty component names and unmatched path-template parameters violate the
[OpenAPI 3.1 component and path contracts](https://spec.openapis.org/oas/v3.1.0.html).
The manually copied pagination schema also omits list.Page.Total; its parity
test repeats the old key list rather than comparing the real JSON contract.
That omission is documentation drift, not proof every response violates the
schema, since extra properties are allowed. A cursor-pagination boolean and
JWT-only authentication metadata cannot describe all host policies.

This is 555 production lines across openapi.go/spec.go with no inspected
production callers. Recommendation: remove the current reflection builder from
web and defer a replacement until a real documentation consumer defines the
requirements. Serving a host-owned OpenAPI document needs no bespoke generator.
Moving the same implementation to another folder alone solves none of these
defects. If retaining generation is an owner priority, scope it as separate
opt-in work: cycle-aware type identity/references, collision errors, explicit
schemas for unsupported/custom serialization, validated route metadata, and
errors returned to the host. A third-party implementation belongs in an optional
integration; SDK remains stdlib-only. Do not expand this reflection engine as an
incidental part of the routing fixes. Any removal is a published API break even
when the inspected callers are zero.

### W13: Simplify static serving without discarding real usage

`sdk/foundation/web/static.go:77` relies on an undocumented PathValue("path")
binding. NewStaticFileServer used directly as an http.Handler returns 404 for an
existing `/app.js`; AddRoutes supplies the special wildcard and works. Resolve
paths normally for direct use and let mounting handle the prefix explicitly, or
make the route-adapter contract explicit; ordinary handler composition is the
clearer target.

With SPA mode, `/` has no-store but direct `/index.html` does not (`:108–116`
versus `:147–148`), contradicting WithSPAMode's promise. An Open permission error
at `/blocked.js` also becomes a successful SPA page (`:87–90`). Apply the same
index policy on both paths and distinguish missing files from actual filesystem
failures. Record read/write failures. No traversal escape was established.

Keep the mount/cache/SPA behavior actually provided, but use stdlib MIME/file
serving where it fits rather than the parallel MIME switch (`:159`). Preserve
the intentional directory/SPA behavior when considering FileServerFS: that is
not a behavior-identical replacement. Non-seekable file fallbacks do not support
ranges; document the limit instead of buffering arbitrary files. Real net/http
suppresses HEAD bodies, so a recorder's fallback body alone is not evidence of a
wire-level HEAD leak. StaticFileServer has four example callers; SPA mode itself
has no inspected production caller.

## API value and simplification decisions proposed

Inventory covers framework production, tests and generated Go separately, plus
Segovia v2 (SDK v0.8.0), Coordination Hub (v0.7.0), and GPS360 (v0.7.1 with local
SDK replace). It honors actual import aliases/shadowing. Method counts use
partial type resolution and are lower bounds: 270 unresolved candidates are
retained, not silently counted as zero. Non-Go docs/templates were checked for
key removals separately; unknown external users remain a migration concern.

| Surface | Recommendation | Reason / compatibility cost |
|---|---|---|
| ServeMux wrapper, groups, verb helpers | Keep; fix ownership | Real routing use; transparent net/http behavior. No custom router/context or interface layer needed. |
| Param / QueryParam | Keep for now | Trivial, but 260 / 86 production references across inspected trees. Removing them offers little clarity gain relative to migration breadth. |
| WithLogging, log field, HandlerOption | Remove together | Ten production WithLogging calls plus scaffold configure a field never read. Logger/Panics already receive explicit loggers. Remove the constructor arguments in a coordinated migration. |
| Decode alias | Remove | No inspected production calls; DecodeJSON communicates the actual format. |
| DecodeJSON and duplicated strict pocket readers | Consolidate carefully | Typed decoding is actively used; choose explicit host limits and unknown-field policy. Preserve validation, null rejection, 400/413 and pocket content-type policy. Avoid a large functional-option parser framework. |
| ReadBody / Body accessors | Keep separate pending an explicit use-case decision | No production callers, but it provides presence tracking and exact keys, unlike ordinary DTO decoding. Do not merge into DecodeJSON just to reduce file count. If no upcoming route needs this contract, removal is preferable to expanding it. |
| Renderer, Render, Template | Keep | Small generic rendering seam, real CMS/templ and stdlib template use. Preserve deliberate streaming and safe error mapping. |
| SSEStream | Keep; fix framing | Active events-pocket boundary with heartbeat, cancellation and write deadlines. |
| StreamWriter / NewStreamWriter / AcceptsStream | Remove | No inspected production callers; duplicates SSE framing while lacking the active API's lifecycle guarantees. |
| RespondFile / RespondRaw / RespondStream / RespondText | Remove unused conveniences | No inspected production callers; stdlib provides direct alternatives. RespondFile also overpromises non-seekable range support. Keep JSON/error/render helpers that encode shared behavior. |
| RespondHTML | Make private if only panic recovery still needs it | No external production use; one internal panic response. Decide alongside W4, not by blind exported-symbol deletion. |
| ServerConfig / Run | Keep; fix timeout cleanup | Used by framework hosts and all three consumers. Host configuration ownership is clear. |
| StaticFileServer | Keep behavior, reduce duplicated file-serving code | Four example callers. Preserve explicit asset caching and optional SPA policy. |
| OpenAPI builder / RouteSpec | Remove current builder; defer replacement | No active caller, 555 lines and substantial protocol/reflection defects. Optional schema work should be driven by an actual host requirement. |
| CORS, RequestID, default headers, NoStore | Keep current host policies | No material new defect established. Tested origin/credentials/Vary/preflight behavior and override rules are intentional. |
| WebHandler name | Keep in this pass | Renaming to Handler only reduces stutter while creating widespread migration. No demonstrated behavioral/design benefit. |

Foundation/web remains a reasonable cohesive HTTP toolkit. Splitting every
concern into a package would add navigation and wiring without creating useful
boundaries. The strongest simplifications remove duplicate/dead behavior, not
familiar conveniences merely because they have short implementations. Error
mapping's explicit safe-public-error precedence is intentional and tested; keep it.

Documentation follow-ups: fix Body.Date's false claim that Go DateOnly accepts
`2026-8-1` (the implementation correctly rejects it); ServerConfig's obsolete
claim that Run lives outside SDK; sdk/README.md's web table assigning tracing and
caching to web; the templ-specific wording on the generic Renderer seam; and
S1's still-open IsExpected HTTP prediction. Update canonical web docs with the
chosen public surface and middleware/error/limit ownership during implementation.

## Recommended implementation sequence

1. **Composition and lifecycle correctness:** W1–W7 and W10, including the small
   cacher/tracing integration regressions and shutdown documentation. Reuse the
   reproduced HTTP cases as permanent regressions; exercise wire status/abort,
   failed rendering through cache, proxy field order, and normal/expired drain.
2. **Input correctness and policy:** W8/W9 if retained, then agree on one clearly
   bounded typed-decoding contract before replacing the pocket copies. Keep host
   body-size/content-type policy explicit and preserve S2 validation behavior.
3. **Static serving:** W13, with direct and mounted handlers, SPA index/cache
   paths, filesystem failures, and seekable/non-seekable behavior exercised.
4. **Public API cleanup:** remove dead logging configuration, Decode alias,
   duplicate streaming and unused responder conveniences. Select OpenAPI removal
   versus separately scoped replacement, and decide whether ReadBody has a
   concrete upcoming consumer. Update examples/scaffold/docs and standalone
   AUDIT-006 migration instructions for implemented breaks only.

These can be separate context windows/implementation plans. No production fix,
API removal or migration entry was made during this review. Review proposals
remain proposals; the next turn should start from these findings, not rediscover
the whole package. After S6 implementation, proceed to S7 async/workers/work.

## Verification and handoff

Passed baseline (2026-09-09):

```sh
# cwd sdk
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go build ./...
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go vet ./...
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -count=1 ./...
# cwd repository root
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make guard
```

The full SDK test run and actual HTTP probes used approved local loopback access.
All test servers/connections were cleaned up. No datastore was started or used.
Logs: `/tmp/gopernicus-s6-sdk-test.log`, `/tmp/gopernicus-s6-guard.log`.
Focused request/body/response/error/template/render tests also passed during the
backend review; the full SDK run subsumes that baseline.

Disposable reproduction commands, cwd repository root:

```sh
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go build -o /tmp/gopernicus-s6-parent-probe /tmp/gopernicus-s6-parent-probe.go
/tmp/gopernicus-s6-parent-probe
# Intentionally exits 2 with fatal stack overflow; run only as a subprocess.
/tmp/gopernicus-s6-parent-probe recursive
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go run /tmp/gopernicus-s6-composition-probe.go
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go run /tmp/gopernicus-s6-middleware-probe.go
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache GOPROXY=off GOSUMDB=off go test -overlay=/tmp/gopernicus-s6-body-overlay.json ./pockets/authentication/internal/inbound/authentication ./pockets/authorization/internal/inbound/authorization -run '^TestS6TrailingBodyLimitProbe$' -count=1 -v
```

These probes intentionally establish current wrong behavior; their successful
execution does not mean the defects are fixed. The recursive case was bounded
by a 128-KiB runtime stack and an external 10-second subprocess timeout. Logs:
`/tmp/gopernicus-s6-{parent-probe,recursive-schema,composition-probe,middleware-probe,body-probe}.log`.
The body overlay injects disposable tests from /tmp without changing repository
source. Inventory: `/tmp/gopernicus-s6-usage.{go,json,tsv,md}`; detailed occurrences
and unresolved selectors are retained there. Key evidence is durable above even
if temporary artifacts are later removed.

Skipped/unverified: no full make check, docs build, consumer upgrades/builds,
browser EventSource session, race rerun, real proxy deployment, live datastore
or full application startup. This is a source review plus focused HTTP behavior
exercise, not end-to-end pocket verification or proof of compatibility with
unknown external apps. Existing tests are green; W1–W13 remain unresolved until
implementation or deliberate API removal. No approval or environment blocker
remains for completing the review.

Repository changes owned by S6 review: `plans/framework-audit-web.md` (new),
`plans/framework-audit.md` (index/handoff). Comparison against the 1,494-file
baseline confirms no reviewed production source changed during this review.
Preserve all earlier audits/concurrent changes; AUDIT.md remains AUDIT-001–005.
