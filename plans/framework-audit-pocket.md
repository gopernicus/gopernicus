# SDK audit S10: pocket wiring and composition

Status: REVIEW COMPLETE — 2026-09-10. Parent: [framework-audit.md](framework-audit.md).
Approved relocation is now implemented in [pockets-module-move.md](pockets-module-move.md)
and AUDIT-016. Jobs P1/P3 fixes are implemented and verified in
[jobs-runtime-implementation.md](jobs-runtime-implementation.md) and AUDIT-018;
events ownership/logger/middleware fixes are now implemented in
[jobs-events-audit-implementation.md](jobs-events-audit-implementation.md), AUDIT-021.
Other pocket findings remain review/planning work. File references
in the original review describe its baseline; the shared contract is now in
pockets/{mount,routes}.go.
Review the last SDK slice after completed OAuth/tracing implementation. This is
an audit: record confirmed defects, design tradeoffs, simplification proposals
and verification before changing public contracts. AUDIT-001..015 remain intact.

## Preconditions and scope

- Branch/base: firestore-authentication / 6807ed06. 746 prior dirty entries;
  /tmp/gopernicus-pocket-review-baseline.json records 2014 visible files. Compare
  task-relative SHA-256 values; preserve prior work and external consumers.
- Go 1.26.1; 41 modules, no root go.mod; GOCACHE is
  /tmp/gopernicus-audit-s1.aMLwCQ/cache. Formatter: /Users/jrazmi/go/bin/goimports.
- Inspect sdk/pocket ports, wrappers/tests, relevant web mounting/lifecycle,
  five pocket construction/Register seams, workshop scaffolds and named hosts.
  Follow construction, optional HTTP, middleware order, prefixes/links, repeated
  registration, errors, event/logger ownership and resource shutdown.
- Full pocket domain/store audits remain separate. No source/API/schema changes,
  real provider calls, publishing, consumer edits or service/production mutations.
  Probes use owned handlers/fakes and temporary files; close any owned listeners.
- Owner follow-up: exclude CMS entirely from the current cleanup. Its recorded
  findings are future context, not prerequisites or permission to change CMS.
  Subsequent module-move approval includes the mechanically required CMS imports
  and module dependencies only; no CMS feature/ownership changes.
- Use named lead-backend-engineer/product-manager read-only reviews as useful.
  The configured opus model is unavailable; inherit the available model while
  preserving role scope. User goals override historical architecture preferences.

## Sequence

1. Read contract, implementation and all SDK tests. Inventory real use of Mount,
   RouteRegistrar, PrefixRegistrar and Group, plus lifecycle ownership.
2. Trace each pocket and representative host/scaffold: headless construction,
   shipped route registration, nil configuration, middleware and route collisions,
   prefixed route behavior versus rendered URLs, subscriptions/background work.
3. Reproduce suspected defects with owned local probes. Run SDK build/test/vet,
   focused race checks and architecture guards; expand only for affected evidence.
4. Evaluate minimal fixes, package/type consolidation and useful unimplemented
   extension points against real consumers. Record compatibility costs separately
   from defects; do not add managers/registries or remove deliberate seams by default.
5. Resolve named source/scope reviews; record commands, results, limits and exact
   changed-file inventory. Update master handoff for implementation or next pocket.

## Verdict

Keep the small pocket contract and its one-method `RouteRegistrar`. Its location
is being reconsidered in the placement follow-ups below. The package already does
little: it carries the mount ports and supplies two small registrar wrappers.
The meaningful defects are in pocket construction and resource/configuration
ownership, not a missing SDK registry, lifecycle manager or plugin abstraction.
The SDK first pass is now reviewed; implementation status is tracked at the top
of this file. Findings and source references below describe the review baseline.

Keep `PrefixRegistrar` as a supported convenience. `Group` can be the primary
example for prefixing with optional middleware; both already share `joinPrefix`.
Forwarding one wrapper through the other or removing the prefix-only type buys
little clarity and creates adoption churn. `web.RouteGroup` is an alternative
for hosts using `*web.WebHandler`; the generic wrappers also support arbitrary
registrars and host route interception, so they serve a distinct purpose.

The useful convention is: construct services with their operational dependencies,
optionally register their shipped HTTP routes, explicitly run workers, and close
resources through their actual owner. `Register` must not become activation or
a required finalization hook. Preserve extension seams that have a concrete use.

### Root SDK placement follow-up

The owner asked whether this small package belongs in root `sdk`, like the
previously promoted primitives. Current dependency evidence: RouteRegistrar and
the wrappers use the named `web.Middleware` type; Mount uses `events.Emitter`.
Both web and events already import root sdk in production. Moving pocket into
root unchanged would therefore create import cycles. This is a concrete dependency
constraint, independent of the existing layering guard's policy.

For the root-sdk proposal, the recommendation is a separate pocket package: root contains primitives the
other packages build on; pocket composes those packages for host wiring. Making
root placement work would require moving or changing the HTTP middleware and
event contracts as well. Consider that only as an explicit broader API decision,
not a consequence of this package's line count. The owner has not yet decided
the placement question; no source or public names were changed in this discussion.

### Shared top-level pockets package follow-up

The owner's next proposal is to put the shared contract in top-level `pockets/`,
with concrete pockets in its existing direct subdirectories. This avoids the
root-sdk import cycles and expresses the composition boundary more directly.
Recommendation: prefer this location if the owner proceeds with the move. Retain
the small contract and wrappers; do not add a plugin registry or aggregate loader.

Proposed layout/API:

```text
sdk/                       existing dependency-free SDK module
pockets/
  go.mod                   module github.com/gopernicus/gopernicus/pockets
  mount.go                 package pockets: Mount, RouteRegistrar
  routes.go                package pockets: PrefixRegistrar, Group
  events/go.mod            existing independent concrete pocket module
  jobs/go.mod              existing independent concrete pocket module
  authentication/go.mod    existing independent concrete pocket module
  ...
```

The shared module requires SDK only and never imports concrete pockets. Concrete
pocket modules may require SDK and the shared pockets module. SDK never imports
pockets. Existing concrete module paths remain unchanged, and importing the
shared contract does not select the concrete pocket modules. Public call sites
use `pockets.Mount`, `pockets.RouteRegistrar`, etc.

Cost/scope: one additional versioned module, a coordinated import/dependency
migration, workspace/build/release discovery, scaffold/docs and boundary-guard
updates, plus an AUDIT migration entry when implemented. Change the existing
"pocket cores require exactly SDK" rule to explicitly permit the shared contract
while retaining the ban on importing other concrete pockets. Do not put a
compatibility forwarding import in SDK: that would reverse the dependency direction.
All existing consumers of sdk/pocket must be accounted for before removing it;
CMS remains outside the current audit/cleanup scope, and this discussion makes
no source changes. This is a placement recommendation, not an implemented move.

The owner subsequently approved this proposal. The relocation is complete in
pockets-module-move.md; AUDIT-016 records its breaking import/module changes.
The discussion above preserves the decision's context. Continue with the bounded
ownership fixes below as separate work.

## Confirmed findings

### P1 — jobs snapshots scheduler kinds before staged handlers exist

`pockets/jobs/jobs.go:205` keeps the caller's handler map but snapshots its kinds
into the scheduler at construction (`:249`). `NewRuntime` (`:302`) later copies
the handlers and derives its queue kinds, while obtaining the scheduler work
function with the original kinds. `schedule.Repository.ListDue` explicitly treats
nil/empty kinds as no filter. An initially empty map therefore produces a runtime
whose scheduler can advance and enqueue schedules belonging to other job kinds.
A map initially containing some handlers can instead omit kinds added later.

This is a real host pattern: Segovia v2 creates jobs before the services its
handlers call, fills the map afterward, then builds the runtime. Its outbound
repository wiring includes the schedule store. We inspected that source; we did
not run the external application or establish which schedules its database holds.

The owned memstore probe constructed an empty registry, added only `owned`, and
ran the runtime against a due `foreign` schedule. The foreign schedule advanced
and a `foreign` job was enqueued. A second probe inserted a nil handler after
construction; `NewRuntime` accepted it because it checks length but does not
repeat entry validation.

**Smallest compatible fix:** validate and copy the final map in `NewRuntime`,
derive kinds from that snapshot, and pass the same fixed kinds to the internal
scheduler work function. Have that function close over its own copied kinds;
do not change shared scheduler state when constructing another runtime. Document
staged boot wiring and prohibit concurrent mutation during snapshot construction.
Later map edits must not alter an existing runtime's behavior. Keep
`EnsureSchedule` usable before runtime construction. `NewFencedRuntime` already
validates and copies its separately supplied configuration correctly.

Do not clone handlers in `NewService`: that would break the staged composition
we are trying to preserve. No new public handler registry is necessary.

### P2 — events acquires a subscription without public cleanup

`pockets/events/events.go:132` subscribes its hub during `NewService`. The internal
hub exposes an idempotent `Close` that unsubscribes (`internal/logic/hub/hub.go:193`),
but the public service offers no corresponding method. Creating/replacing or
discarding services while retaining the shared bus leaves their subscriptions
owned by the bus with no public per-service release. The tracking-bus probe
observed two subscriptions and no public `Close` on the service.

Expose `Service.Close() error` by forwarding to the hub. Closing the service must
not close the host's shared bus or claim to drain HTTP connections: the host
still owns HTTP shutdown/request cancellation, and subscription removal does
not wait for callbacks. Add the close to relevant host examples and explain
shutdown order. A process-lifetime service with a bus closed on exit has a
different lifetime from a replaced service; this is not evidence that every
normal application leaks indefinitely. No generic SDK lifecycle interface needed.

### P2 — construction retains the events middleware slice

`pockets/events/events.go:159` stores `Config.StreamMiddleware` directly. Changing
the caller's slice after construction changes the policy subsequently mounted by
that service. The HTTP recorder probe configured a middleware returning 403,
replaced the caller's element with one returning 204, then mounted the service:
the route returned 204. Clone the slice in `NewService`, as jobs and web already
do for their construction configuration. This captures the middleware list,
not mutable state intentionally enclosed by a middleware function.

### P2 — operational logging depends on mounting, or bypasses its logger

Events' public Config has no logger, and its hub receives none, so construction
and slow-client warnings use the default logger. `Mount.Logger` only logs
registration. Authorization instead assigns its operational `Service.log` during
`Register` (`authorization.go:597`), leaving headless use on the default logger.
Its separately returned `SystemMutator.log` is never assigned at all (`:582` and
`mutation_service.go:212`). The public probe supplied a custom mount logger and
called teardown through a fake mutation repository that returned an error:
registration reached the custom logger; the teardown warning reached only the
default logger. No real authorization mutation was performed.

Give events and authorization an optional `Config.Logger`, wire it at construction
into their operational components, and reserve `Mount.Logger` for registration
messages. Authorization's `Service` and `SystemMutator` must receive the same
configured logger. Preserve nil-to-default behavior. Authentication and jobs
already have construction-time loggers. Record the host migration explicitly:
authorization callers relying on `Mount.Logger` for operational logs must also
pass the logger to Config. Avoid dual configuration precedence or logger mutation
at mounting time. No claim of a live-registration race is needed: boot-time
registration is the intended contract.

### P3 — no-route registration has an unrelated jobs precondition

Jobs `Register` (`jobs.go:358`) mounts no routes and starts no goroutines, yet
rejects an empty unfenced handler map. The probe built a valid fenced-only service
and fenced runtime, then got `ErrHandlersRequired` from `Register`. Its runtime
is usable if registration is skipped; this is a misleading placeholder contract,
not a broken fenced runtime. Keep the method as an optional compatibility
convenience, remove the unrelated unfenced-handler gate, and validate handlers
where they are actually required (`NewRuntime`). Make examples omit pointless
registration calls for headless jobs. The generated pocket skeleton likewise
only logs on Register; make its docs explicit that this is optional transport
scaffolding, not a plugin activation step. Do not require every pocket to have
identical facade return types: authorization's `Components` separation is useful.

## Startup behavior, documented limits and unfinished work

- Authentication, events and CMS dereference the router while mounting; events
  and CMS nil-router panics were reproduced. Authorization returns a specific
  error for a nil router when its routes are enabled and accepts a no-router
  mount when they are disabled. Standardize clear pre-mount errors for an absent
  required router, allowing no-router configurations that actually mount nothing.
  A typed-nil registrar still defeats an ordinary interface nil check (reproduced
  with events). Do not promise complete dependency validation or introduce a
  reflective SDK DI validator; document that a supplied registrar must be usable.
- Prefix wrappers change registration patterns only. They do not rewrite emitted
  links, redirects, form actions, request URLs, OAuth callback configuration or
  cookie paths. Coordination-hub already configures callback and refresh-cookie
  paths for its `/api/v1` mount explicitly. CMS's broken generated links under a
  prefix are a documented unfinished URL-policy feature, not a wrapper defect.
- Group middleware wraps matched handlers. Real `web.WebHandler` recorder probes
  showed the global stack also sees slash redirects, 404 and 405, while group and
  route middleware do not. This is normal route middleware behavior; use the
  host's global middleware for policy that must cover every mux response.
- Canonical prefix inputs work, including nested prefixes, root/wildcard patterns
  and group-before-route ordering. Group builds a fresh combined middleware slice.
  `/api//` and `/api/../v1` prefixes failed loudly when registered on the real Go
  mux. Do not silently clean arbitrary route patterns or treat them as a bypass.
- Registration is boot-time work. Duplicate/conflicting patterns panic through
  Go's ServeMux; registration is not transactional and cannot undo earlier route
  additions. The host resolves collisions and discards a failed router. No
  route-table DSL, registry, repeated-registration promise or rollback mechanism.
- CMS still builds its content registry and services inside package-level
  `Register(mount, repos, cfg)`. This is an acknowledged pre-Service implementation,
  documented in `pockets/README.md` and the CMS guide. Complete its public/headless
  `NewService` facade and URL policy during the full CMS audit, not this SDK slice.
- **Keep `Mount.Events` now.** CMS actually consumes it at `cms.go:118`. When CMS
  moves to headless construction, move that dependency to its Config and then
  assess removing the mount field with a standalone migration. Avoid two event
  sources with precedence rules. The top architecture's quoted Mount omits the
  existing field; authentication's delivery observer still says `Mount.Events`
  despite receiving `Config.DeliveryEventsEmitter`. These are documentation drift.

## Recommended implementation sequence

1. **Implemented:** [jobs-runtime-implementation.md](jobs-runtime-implementation.md)
   and AUDIT-018 fix the jobs snapshot/validation defect with tests for staged handlers,
   a due foreign kind, late nil/empty entries, and two runtimes with independent
   snapshots. Relax the no-route Register gate in that same small jobs change.
   Preserve host middleware and the generic worker/job distinction.
2. Expose events cleanup, copy its middleware and wire a construction logger.
   Verify unsubscribe, shared-bus survival, actual HTTP middleware behavior and
   constructor/runtime log routing. Carry this into the full events-pocket audit.
3. Wire authorization loggers at construction, with Service/SystemMutator and
   headless/HTTP coverage. Keep its full policy/store audit separate.
4. Align router startup errors and docs/scaffold examples with optional transport
   mounting and the boundaries above, excluding CMS per the owner follow-up.
   Count changes to generated templates and
   their owning tests as scope; do not edit checked-in generated artifacts by hand.

These are bounded cleanup slices, not authorization for a sweeping pocket rewrite.
Public additions are low-churn; handler validation and snapshot behavior correct
bugs while preserving staged wiring. Logger behavior and any changed startup
errors need an AUDIT entry when implemented. Future removal of `Mount.Events`
and CMS facade changes need their own migration. This review leaves AUDIT-001..015
byte-identical and records no hypothetical migration as shipped.

After cleanup, continue full pocket audits in the established order: events,
jobs, authentication, authorization, CMS, then outstanding integrations. Looking
at construction seams here does not mark those full audits complete.

## Review and consumer evidence

Read all SDK pocket source/tests, relevant web registrar/global middleware code,
five pocket public construction/mount seams, events hub ownership, jobs runtime
and scheduler construction, authorization logging, canonical pocket/host docs,
and the workshop pocket template. Named `lead_backend_pocket_review` provided a
read-only backend review and resolved the staged-jobs/retained-Mount.Events
recommendations against real consumers. A second agent could not start because
the thread limit was reached; product-manager scope/adoption assessment was
performed locally using the named role guidance. No approval dependency remains.

Read-only consumer checkpoints (no pulls, builds, updates or deployments):

| Host | Branch / commit | Wiring evidence |
|---|---|---|
| `/Users/jrazmi/code/segovia/segovia/v2` | main / 76b3d78; 2 dirty entries | shared Mount; intentional staged jobs map (`cmd/server/main.go:146`); queue/schedule/fenced stores in outbound wrapper |
| `/Users/jrazmi/code/gps/coordination-hub` | main / 84ff08a; clean | PrefixRegistrar under `/api/v1`; explicit OAuth and cookie mount paths |
| `/Users/jrazmi/code/gps/three-sixty/gps-360-go` | main / e1ab3f0; clean | root Mount for authentication/authorization and contract-test use |

Original repository state was also checked (docs/fix-cli-and-framework-reference-drift
/ 0f763a9; 1 dirty entry). No detailed original-framework comparison was necessary
for this slice. External dependency versions and deployed behavior were not verified.

## Verification and exact task inventory

All commands below passed (exit 0); ordinary suites used Go's test cache where
valid. No source was changed to make tests pass.

```sh
# cwd: sdk
env GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache \
  sh -c 'go build ./... && go test ./... && go vet ./...'
# cwd: repository root
make guard
env GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache \
  go test -race ./sdk/pocket/... ./pockets/events/... ./pockets/jobs/...
env GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache sh -c \
  'go build ./pockets/events/... ./pockets/jobs/... ./pockets/authentication/... ./pockets/authorization/... ./pockets/cms/... &&
   go test ./pockets/events/... ./pockets/jobs/... ./pockets/authentication/... ./pockets/authorization/... ./pockets/cms/... &&
   go vet ./pockets/events/... ./pockets/jobs/... ./pockets/authentication/... ./pockets/authorization/... ./pockets/cms/...'
env GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache \
  go run /tmp/gopernicus-pocket-review-probe.go
env GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache \
  go run /tmp/gopernicus-pocket-review-behavior.go
```

Logs: `/tmp/gopernicus-pocket-review-{sdk-checks,guards,race,seams,probe,behavior}.log`.
All 23 guards passed. Core patterns do not include nested store/view modules.
The public probes exercised real handlers with HTTP recorders, owned in-memory
queue/schedule runtimes and fake bus/mutation boundaries; runtimes were canceled
and drained. The logger probe initially used an invalid short mutation ID and
correctly hit validation first; using a valid-length owned ID reached the intended
logging path and confirmed the finding. No unresolved verification failures.

Skipped: full 41-module `make check`, live store/provider/service tests, external
consumer builds, browser/UI flows and docs build. This was source review plus
bounded behavioral probes, not a source or generated-site implementation. No
new persistent services/listeners, database schemas or provider calls.

Task-relative changed files (SHA-256 comparison with the starting baseline):

- `plans/framework-audit-pocket.md` — new review, findings and implementation scope.
- `plans/framework-audit.md` — S10 status and next-session handoff.

All source, AUDIT, canonical docs, module/workspace and generated files remain at
the task baseline. Existing dirty work is preserved. Carry this plan and the
baseline path into implementation; temporary probes are supplemental evidence,
while the findings above preserve the reproduction if /tmp is later cleared.
