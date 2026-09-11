# Web audit implementation

Status: COMPLETE — 2026-09-09. The owner authorized implementing the S6
fixes and recommendations. Review: [framework-audit-web.md](framework-audit-web.md).
Parent: [framework-audit.md](framework-audit.md).

## Decisions and compatibility

- Keep the stdlib HTTP toolkit, ServeMux/group API, Renderer/Template, Run and
  SSEStream. Fix W1–W10/W13 with behavior regressions and real HTTP checks.
- Keep ReadBody and its presence contract for now; correct trailing JSON and
  its date documentation. Host applications continue to own JSON size and
  unknown-field policies. Do not globally impose strict decoding or upload limits.
  Keep the two strict pocket readers: a shared configurable API would add null
  and validation policy complexity for little code saved. Apply common error
  classification within each. Route/group limits use stdlib MaxBytesHandler.
  Authentication's older third decoder also lacked size/trailing checks; replace
  it with the existing strict helper and 1-MiB pocket limit. Preserve null and
  content-type behavior; record new trailing/oversize rejection in AUDIT-006.
- Remove dead WithLogging/HandlerOption, unused Decode alias, StreamWriter/
  AcceptsStream and unused RespondFile/RespondRaw/RespondStream/RespondText.
  Make the panic-only HTML writer private. Preserve active convenience APIs.
- Remove the unadopted OpenAPI reflection builder and RouteSpec metadata rather
  than repair/expand it. Hosts can serve an owned specification with net/http;
  a future generator is separately scoped opt-in work driven by a real consumer.
- Update local callers, tests, examples/scaffold and canonical docs; leave
  historical plans intact. Add standalone AUDIT-006 and unreleased guidance.
  No new dependencies, module pins, releases, consumer edits or data migrations.

## Preconditions and ownership

Branch/base: firestore-authentication / 6807ed06. Go 1.26.1. No root go.mod;
use module cwd ./... or workspace-qualified paths. Formatter:
/Users/jrazmi/go/bin/goimports. GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
Implementation baseline: /tmp/gopernicus-s6-implementation-baseline.json (1,513
files), including prior audits/concurrent Firestore edits. Preserve that work.
Datastore environment variables unset. No credentials/dotenv, live services,
manual generated edits, commits or tags. Loopback tests use normal approval.

## Tasks

1. **HTTP composition and lifecycle (parent):** middleware slice ownership,
   repeated proxy fields, correct status/flush/abort handling, error forwarding,
   failed-render cache refusal and timeout Close. Preserve controller support,
   streaming rendering and normal drain. Add focused regressions and real-client
   tests. Named platform-sre reviews final behavior read-only.
2. **Static files and SSE (named implementer):** own static.go/static_test.go,
   sse.go/sse tests only. Correct direct/mounted paths, SPA cache and filesystem
   errors, reduce MIME duplication; encode complete safe SSE frames with
   multiline/metadata/serialization/write-error coverage. Keep interfaces and
   record errors through web.RecordError. Parent owns stream.go removal and docs.
3. **Request decoding and cleanup (parent):** ReadBody and pocket limit fixes;
   deliberate shared decoder decision, preserve validation-once/null/unknown
   policies; remove selected unused APIs and migrate local source/tests/templates.
   Named lead-backend-engineer reviews request contract and final errors read-only.
4. **Docs and migration (parent):** canonical web/SDK/architecture references,
   shutdown host comments, S1 IsExpected doc follow-up, standalone AUDIT-006 and
   release note, exact changed-file/verification record and audit handoff.
5. **Verification:** goimports, scoped regressions before/after fixes, SDK and
   affected pocket build/test/vet; targeted web/cache/tracing race checks; full
   make check (generated drift, all module gates, scaffold, guards); docs build;
   real HTTP abort/status/shutdown and public API migration/host wiring checks.
   No live datastores or consumer upgrades; record all skips/failures explicitly.

## Execution and handoff

All five tasks are complete. W1–W13 are resolved through fixes or removal of
the unused APIs. No new parsing configuration framework or dependency was added.
ReadBody retains its presence contract. Generic DecodeJSON retains unknown-field
acceptance, top-level-null rejection and validation once; explicit host route
limits use stdlib MaxBytesHandler. The strict pocket readers retain their own
null/validation/content-type policies, with one common failure-classification
branch per copy. Twelve older authentication decode calls now use the existing
bounded strict reader; valid registration after rejected input proves refusal
occurs before state mutation.

Response composition now uses one StatusRecorder implementation for commitment,
first-error retention/forwarding, FlushError and Hijack. Cacher embeds it, buffers
only successfully written bytes and refuses failed responses. Panics has local
commitment/ownership tracking independently of Logger. Logger emits access lines
on aborts, distinguishes status0/unobserved and hijacked connections, and retains
the original failure. SRE review caught the hijack edge; real HTTP regressions
and the final race/full gates cover its correction. Tracing still has its existing
5xx span policy; broader tracing failure/abort policy remains S9 scope.

The named implementer delivered SSE/static fixes and the Run timeout regression.
Static serving uses stdlib MIME/ServeContent with small error observers; multipart
range file errors are collected safely and forwarded from the serving goroutine.
No arbitrary file/whole-response buffering was added. SSE buffers one complete
frame to prevent malformed metadata/JSON from writing a partial event. The named
backend lead found no blocking request/response regression; its request-policy
review prevented an incompatible generic decoder consolidation.

OpenAPI reflection and metadata, dead handler options, Decode alias, the second
SSE API and unused generic responders are removed. NewWebHandler call sites in
examples, CMS and scaffold/tests/docs are migrated. The reset-token log test now
installs real Logger middleware rather than configuring the former unused field.
The S1 IsExpected documentation follow-up is closed without semantic changes.
Canonical docs, release guidance and standalone AUDIT-006 are current.

### Verification

All Go commands use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Commands below
run at the repository root unless a module cwd is specified. HTTP/race/full gates
used approved local loopback access; servers/connections were cleaned up.

| Command/check | Result and evidence |
|---|---|
| `go test -count=1 ./sdk/... ./pockets/authentication/... ./pockets/authorization/...` | PASS, including malformed bodies, retained null/validation policies, and real registration router/service refusal before mutation. `/tmp/gopernicus-s6-sdk-pockets-tests-final.log` |
| `make check` | PASS: 42 modules each run `go vet ./...`, `go build ./...`, `go test ./...`; 8 integration-tag and 3 integration/live-tag vet legs; 23 guards; generated templ/assets drift and Workshop generated-host compilation. `/tmp/gopernicus-s6-make-check.log` |
| `go test -race -count=1 ./sdk/foundation/web ./sdk/capabilities/cacher ./sdk/capabilities/tracing` | PASS: HTTP status/abort/hijack, cache composition, streaming/static/lifecycle and existing suites. `/tmp/gopernicus-s6-race.log` |
| `make docs-build` | PASS: existing pnpm lockfile/toolchain, TypeScript typecheck and Docusaurus build. `/tmp/gopernicus-s6-docs-build.log`. Nonblocking Docusaurus update-check/config-store notice only. |
| `go run /tmp/gopernicus-s6-host-proof.go` | PASS: actual local HTTP host combines current public router, tracing/logging/panic/proxy middleware, cache MISS/HIT, multiline SSE, static mount, host-owned OpenAPI bytes and host-selected JSON limits (200/413). `/tmp/gopernicus-s6-host-proof.log` |
| Named implementer scoped checks, cwd sdk | PASS: `go test ./foundation/web -run 'Test(SSEStream\|Static\|Run)' -count=1`, same scoped race run, `go test ./foundation/web/run_test.go -run '^TestRun' -count=10`, `go build ./foundation/web`, `go vet ./foundation/web`. Full gates above include final source. |
| Formatter / diff | PASS: `/Users/jrazmi/go/bin/goimports -l` reports no deviations for 49 existing changed Go files; `git diff --check` passes. No module manifests, requirements or generated artifacts changed. |

Pre-fix behavior is documented by S6's isolated probes. Permanent regressions now
exercise those failures independently of their implementation: sibling/caller
middleware mutation, repeated header fields, partial renders and write/flush
failures through cache, nested recorder wire status, explicit/ordinary aborts,
pre-response recovery, hijacking, first-cause retention, expired/normal drain,
malformed JSON, multiline SSE and filesystem/cache paths. Old probes importing
removed APIs are historical evidence, not current verification commands.

Initial intermediate failures were stale MIME assertions during static edits and
a remaining Decode alias reference during migration; both were resolved. There
are no unresolved test failures or approval blocks.

**Not verified:** consumer builds/upgrades, browser EventSource/UI sessions,
HTTP/2 wire behavior, arbitrary third-party response wrappers, deployed proxy
configuration, live PostgreSQL/Firestore/hosted Turso, or whole-workspace race.
Live datastore tests skipped with service environment unset; tagged vet is only
compilation. No credentials/dotenv or production services were used. These checks
do not close the full pocket/cache/tracing audits. Next review is S7
foundation/async, foundation/workers and capabilities/work against real jobs use.

### Changed files

This list is relative to the implementation's 1,513-file working baseline, not
HEAD. Prior audits and concurrent Firestore work remain intact. Hash inventory:
/tmp/gopernicus-s6-implementation-baseline.json. Final paths:
/tmp/gopernicus-s6-changed-files.json. Five removed Go files are included below;
the remaining paths are added or modified by S6 implementation.

```text
ARCHITECTURE.md
AUDIT.md
RELEASING.md
examples/auth-cms/cmd/server/authorization_test.go
examples/auth-cms/cmd/server/browser_cookie_flow_test.go
examples/auth-cms/cmd/server/goth_proof_test.go
examples/auth-cms/cmd/server/inprocess_delivery_test.go
examples/auth-cms/cmd/server/jobs_delivery_proof_test.go
examples/auth-cms/cmd/server/main.go
examples/auth-cms/cmd/server/role_routes_proof_test.go
examples/auth-cms/cmd/server/route_no_mutation_test.go
examples/cms/cmd/server/main.go
examples/goth-showcase/cmd/server/main.go
examples/jobs-minimal/cmd/server/main.go
examples/minimal/cmd/server/goth_htmx_proof_test.go
examples/minimal/cmd/server/main.go
plans/framework-audit-sdk-root.md
plans/framework-audit-web.md
plans/framework-audit.md
plans/web-cleanup.md
pockets/authentication/internal/inbound/authentication/body_test.go
pockets/authentication/internal/inbound/authentication/invitation.go
pockets/authentication/internal/inbound/authentication/oauth.go
pockets/authentication/internal/inbound/authentication/resend.go
pockets/authentication/internal/inbound/authentication/reset_token_retain_test.go
pockets/authentication/internal/inbound/authentication/security.go
pockets/authentication/internal/inbound/authentication/security_test.go
pockets/authentication/internal/inbound/authentication/sessions.go
pockets/authorization/internal/inbound/authorization/body_test.go
pockets/authorization/internal/inbound/authorization/roles.go
pockets/cms/internal/inbound/cms/routes.go
pockets/events/README.md
sdk/README.md
sdk/capabilities/cacher/middleware.go
sdk/capabilities/cacher/middleware_test.go
sdk/capabilities/tracing/middleware.go
sdk/errors.go
sdk/foundation/web/composition_test.go
sdk/foundation/web/groups.go
sdk/foundation/web/groups_test.go
sdk/foundation/web/handler.go
sdk/foundation/web/handler_test.go
sdk/foundation/web/middleware.go
sdk/foundation/web/openapi.go
sdk/foundation/web/openapi_test.go
sdk/foundation/web/readbody.go
sdk/foundation/web/readbody_test.go
sdk/foundation/web/request.go
sdk/foundation/web/request_test.go
sdk/foundation/web/response.go
sdk/foundation/web/response_test.go
sdk/foundation/web/run.go
sdk/foundation/web/run_test.go
sdk/foundation/web/server.go
sdk/foundation/web/spec.go
sdk/foundation/web/sse.go
sdk/foundation/web/sse_frames_test.go
sdk/foundation/web/static.go
sdk/foundation/web/static_regression_test.go
sdk/foundation/web/static_test.go
sdk/foundation/web/stream.go
sdk/foundation/web/stream_test.go
sdk/foundation/web/trustproxies.go
workshop/documentation/docs/getting-started/quickstart.md
workshop/documentation/docs/guides/compose-host.md
workshop/documentation/docs/intro.md
workshop/documentation/docs/project-status.md
workshop/documentation/docs/sdk/foundation.md
workshop/documentation/docs/sdk/web.md
workshop/documentation/docs/ui/react.md
workshop/gopernicus/internal/commands/templates/init/main.go.tmpl
```
