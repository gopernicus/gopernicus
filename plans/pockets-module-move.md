# Shared pockets module

Status: COMPLETE — 2026-09-10.
Parent: [framework-audit-pocket.md](framework-audit-pocket.md).

## Authorized result and scope

Move sdk/pocket into the root of pockets as package pockets, with its own module
github.com/gopernicus/gopernicus/pockets. Preserve Mount, RouteRegistrar,
PrefixRegistrar and Group behavior and fields. Concrete pocket modules keep their
existing paths and independent go.mod files. Shared pockets requires SDK only;
concrete cores may require SDK and shared pockets, never another concrete pocket.
SDK has no outward import or compatibility forwarding package.

Migrate repository Go imports/selectors, templates, module/workspace/build/release
wiring, guards, canonical documentation and examples. Add AUDIT-016 while
byte-preserving all previous audit entries. New module requirements use v0.1.0
as the initial release pin; existing versions stay unchanged except the four
SDK minimum-version alignments required by standalone resolution below.
No tags or external releases are created. Consumers use local workspace/replaces
until a coordinated release is published.

CMS receives only the mechanically necessary import/module migration. Its domain,
facade and URL work remain out of scope. The prior audit's jobs/events/logger
fixes are separate work and are not bundled into this relocation. No external
consumer edits, service mutations, schema changes or generated-file hand edits.

## Preconditions

- firestore-authentication / 6807ed06; 747 pre-existing dirty entries.
- Baseline: /tmp/gopernicus-pockets-module-baseline.json, 2015 visible files.
  AUDIT bytes copied to /tmp/gopernicus-pockets-module-AUDIT-before.md. Compare
  task-relative hashes, not the full branch diff; preserve all prior changes.
- Go 1.26.1; 41 workspace modules before move, 42 afterward; no root go.mod.
  GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Use goimports at
  /Users/jrazmi/go/bin/goimports. Docs use existing pnpm lockfile/scripts.
- Use the named lead-backend-engineer read-only review for module/guard safety;
  the role's configured opus is unavailable, so retain its scope on available model.

## Sequence

1. Move implementation/tests to pockets/{mount,routes}{,_test}.go. Migrate only
   import-bound selectors with a Go AST helper; maintain explicit custom aliases.
2. Add shared go.mod/workspace/build discovery. Update direct and required
   indirect dependencies and local replacement paths. Verify isolated module
   resolution as well as the workspace to catch parent/child ambiguity.
3. Update emitted host/pocket templates and offline scaffold tests. Keep the
   generated host's no-concrete-pocket behavior. Preserve third-party isolation.
4. Adapt architectural guards to the precise shared-contract allowance and
   enforce the new shared module's SDK-only dependency. Keep SDK outward and
   concrete cross-pocket import checks. Exercise negative guard cases in temp copies.
5. Update current documentation, release instructions, AUDIT-016 and audit handoff.
   Historical plans/release entries remain evidence rather than a global rewrite.
6. Run full make check (build/test/vet, guards, tagged compile/generated checks),
   focused shared-contract race tests, docs build, actual in-process HTTP mounting
   and standalone scaffolds. Resolve read-only review, record exact task-relative
   changed paths and final results. No live adapters needed for a package move.

## Verification and inventory

Standalone resolution initially found four existing SDK minimum-version mismatches:
examples/jobs-minimal needed SDK v0.7.1 (its jobs core's minimum), and authentication
stores/{pgx,turso} plus views/goth needed SDK v0.6.0 (their core's minimum). Aligned
only those SDK requirements; no third-party version changes. Workspace checks hid
these mismatches because the current SDK is a workspace main module. The initial
readonly failures were resolved; exact temporary-modfile deltas are recorded in
/tmp/gopernicus-pockets-module-tidy-diff.log. The final isolated log contains
24 passes.

Final result: complete. Public API shape and runtime behavior are unchanged apart
from the import path/package identity. All four relocated files are byte-identical
to baseline after reversing only package-name/doc-package-name changes. No SDK
forwarder, global registry, ownership fixes or third-party dependency changes.
Named lead_backend_pocket_review returned ship-ready; its module and scaffold
concerns are covered. Applied its optional guard improvement to inspect the whole
shared module (`./...`), which excludes concrete nested modules automatically.

### Passed checks

- `goimports` on every changed Go file; final `goimports -l` is empty.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check` — final exit 0:
  all 42 modules build/test/vet, tagged compile checks, generated artifact checks,
  scaffold cache/tests and all 23 architecture guards. The first run also passed;
  reran after SDK MVS alignment and final scaffold/guard edits.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -race ./pockets/...`
  — exit 0; shared contract module only. Concrete cores are covered by make check.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test
  ./workshop/gopernicus/internal/commands -run 'TestScaffoldPocket' -count=1`
  — exit 0; generated core tidy/build/test/vet and both stores tidy/build/vet
  with integration tags, workspace disabled and proxy off. Full make check also
  exercises plain host scaffolds, which still need no shared pockets dependency.
- `make docs-build` — final exit 0, Docusaurus reports successful static output.
  Nonfatal update-notifier configuration-access message remains; the build passed.
- `python3 /tmp/gopernicus-pockets-module-isolated.py` — exit 0, all 24 affected
  modules pass `go list -mod=readonly -deps -test ./...` with GOWORK=off,
  GOPROXY=off and GOSUMDB=off. Temporary modfiles replace workspace siblings with
  absolute paths; no repository replacements are rewritten by the probe. Tests
  from those modules are included in dependency resolution, not executed here.
- `python3 /tmp/gopernicus-pockets-module-guard-probe.py` — exit 0. Eight temporary
  fixture cases verify valid wiring and refusal of shared-module production/test
  imports of concrete pockets, external requirements, peer dependencies/imports,
  and SDK outward imports. Fixtures are removed after the run.
- `python3 /tmp/gopernicus-pockets-module-http-probe.py` — exit 0. A standalone
  temporary host imports shared pockets and SDK only, registers through nested
  Group/PrefixRegistrar and serves GET /api/widgets/42 with status 200/body 42.
  Middleware order is global → group → route → handler. HTTP uses real WebHandler
  with request/response recorders, without a listener. Its three-module graph is
  the host + shared pockets + SDK; no concrete pockets were selected.
- `git diff --check` — exit 0. Workspace and Makefile module sets are identical
  (42). No old sdk/pocket import exists in Go/template source.
- Baseline SHA-256 audit: no generated Go, module checksum files or existing AUDIT
  bytes changed. AUDIT-016 is appended; AUDIT-001..015 remain byte-preserved.

Command logs are `/tmp/gopernicus-pockets-module-{check,race,scaffold,docs,
isolated,tidy-diff,guard-probe,http-probe}.log`. The standalone probe scripts and
`/tmp/gopernicus-pockets-module-inventory.json` supplement this tracked record.
All probe-owned directories/processes were cleaned up; no persistent service.

### Limits and next work

No unresolved implementation or verification failures. The four standalone MVS
failures were corrected and rerun. No real external providers/datastores,
production services, browser flows or external consumer applications were run or
modified. Full check's env-gated live-store legs skip without their infrastructure;
tagged code is compile-checked. Docs-only edits needed no TypeScript source change.

No tags or publication: module-proxy installation of unreleased requirements is
not verified. Before publishing, choose compatible release pins for the audit
train, publish SDK/shared pockets before updated concrete consumers, and validate
a downstream host against tags without local replacements. AUDIT-016 describes
local and eventual tagged adoption. The original S10 temporary probes use the
removed sdk/pocket import and require the same migration before rerunning.

Next audit work remains the staged jobs runtime/scheduler snapshot fix, then
other bounded ownership fixes. CMS feature/ownership work stays deferred.

### Exact task-relative changed paths

114 paths, including the four source deletions and their four new destinations.
Existing unrelated dirty work is preserved. The inventory is measured against
/tmp/gopernicus-pockets-module-baseline.json, not HEAD.


```text
ARCHITECTURE.md
AUDIT.md
Makefile
README.md
RELEASING.md
examples/auth-cms/cmd/server/authorization_test.go
examples/auth-cms/cmd/server/browser_cookie_flow_test.go
examples/auth-cms/cmd/server/goth_proof_test.go
examples/auth-cms/cmd/server/inprocess_delivery_test.go
examples/auth-cms/cmd/server/jobs_delivery_proof_test.go
examples/auth-cms/cmd/server/main.go
examples/auth-cms/cmd/server/role_routes_proof_test.go
examples/auth-cms/go.mod
examples/cms/cmd/server/main.go
examples/cms/go.mod
examples/jobs-minimal/cmd/server/main.go
examples/jobs-minimal/go.mod
examples/minimal/cmd/server/goth_htmx_proof_test.go
examples/minimal/cmd/server/main.go
examples/minimal/go.mod
examples/minimal/internal/inbound/domains/catalog/routes.go
go.work
plans/framework-audit-pocket.md
plans/framework-audit.md
plans/pockets-module-move.md
pockets/README.md
pockets/authentication/README.md
pockets/authentication/auth_test.go
pockets/authentication/authentication.go
pockets/authentication/delivery_jobs_test.go
pockets/authentication/delivery_mode_test.go
pockets/authentication/go.mod
pockets/authentication/internal/inbound/authentication/html.go
pockets/authentication/internal/inbound/authentication/invitation.go
pockets/authentication/internal/inbound/authentication/machine.go
pockets/authentication/internal/inbound/authentication/me_test.go
pockets/authentication/internal/inbound/authentication/oauth.go
pockets/authentication/internal/inbound/authentication/passwordless.go
pockets/authentication/internal/inbound/authentication/refresh_cookie_path_test.go
pockets/authentication/internal/inbound/authentication/routes.go
pockets/authentication/internal/inbound/authentication/useradmin.go
pockets/authentication/stores/firestore/go.mod
pockets/authentication/stores/pgx/go.mod
pockets/authentication/stores/turso/go.mod
pockets/authentication/views/goth/go.mod
pockets/authorization/authorization.go
pockets/authorization/authorization_test.go
pockets/authorization/go.mod
pockets/authorization/internal/inbound/authorization/routes.go
pockets/authorization/relationship_writer_test.go
pockets/authorization/role_routes_test.go
pockets/authorization/stores/firestore/go.mod
pockets/authorization/stores/pgx/go.mod
pockets/authorization/stores/turso/go.mod
pockets/cms/cms.go
pockets/cms/cms_test.go
pockets/cms/go.mod
pockets/cms/internal/inbound/cms/routes.go
pockets/cms/stores/pgx/go.mod
pockets/cms/stores/turso/go.mod
pockets/cms/views/goth/go.mod
pockets/events/README.md
pockets/events/events.go
pockets/events/events_test.go
pockets/events/go.mod
pockets/events/internal/inbound/events/routes.go
pockets/events/stores/pgx/go.mod
pockets/events/stores/turso/go.mod
pockets/go.mod
pockets/jobs/go.mod
pockets/jobs/jobs.go
pockets/jobs/jobs_test.go
pockets/jobs/kinds_test.go
pockets/jobs/stores/pgx/go.mod
pockets/jobs/stores/turso/go.mod
pockets/mount.go
pockets/mount_test.go
pockets/routes.go
pockets/routes_test.go
sdk/README.md
sdk/capabilities/work/worktest/worktest.go
sdk/foundation/web/groups_test.go
sdk/foundation/web/methods.go
sdk/pocket/pocket.go
sdk/pocket/pocket_test.go
sdk/pocket/prefix.go
sdk/pocket/prefix_test.go
ui/goth/README.md
ui/goth/goth.go
workshop/documentation/docs/architecture/hexagonal-apps.md
workshop/documentation/docs/architecture/overview.md
workshop/documentation/docs/architecture/pocket-contract.md
workshop/documentation/docs/architecture/repository-layout.md
workshop/documentation/docs/getting-started/choose-your-path.md
workshop/documentation/docs/getting-started/quickstart.md
workshop/documentation/docs/guides/compose-host.md
workshop/documentation/docs/guides/create-pocket.md
workshop/documentation/docs/guides/testing.md
workshop/documentation/docs/pockets/authentication.md
workshop/documentation/docs/pockets/cms.md
workshop/documentation/docs/pockets/events.md
workshop/documentation/docs/pockets/overview.md
workshop/documentation/docs/sdk/overview.md
workshop/documentation/docs/ui/react.md
workshop/documentation/docs/workshop/commands.md
workshop/gopernicus/README.md
workshop/gopernicus/internal/commands/pocket.go
workshop/gopernicus/internal/commands/pocket_scaffold_test.go
workshop/gopernicus/internal/commands/templates/init/main.go.tmpl
workshop/gopernicus/internal/commands/templates/init/readme.md.tmpl
workshop/gopernicus/internal/commands/templates/pocket/core.go.mod.tmpl
workshop/gopernicus/internal/commands/templates/pocket/readme.md.tmpl
workshop/gopernicus/internal/commands/templates/pocket/socket.go.tmpl
workshop/gopernicus/internal/commands/warmcache_test.go
```
