# Environment and logging audit implementation

Status: COMPLETE — 2026-09-09. The owner authorized the recommendations in
[the S3 review](framework-audit-environment-logging.md).

## Goal and decisions

Keep both packages, fix demonstrated parsing/record defects, reduce duplicate
APIs, and make default host logging include available request/trace/span IDs.

- Dotenv remains a single-line format: assignments, optional export prefix,
  literal unquoted/single/double-quoted values, whitespace-separated trailing
  comments. No interpolation, escapes, or multiline parsing. Reject malformed
  assignments, empty/whitespace/NUL keys, unterminated quotes, and unexpected
  text after a closing quote. Preserve non-overwrite and missing-file behavior.
  Return filename/line/key diagnostics without raw values; apply incrementally
  and document that earlier assignments survive later failures.
- Parse tagged fields using the destination type/bit width. Support named
  string slice elements, reject unsupported tagged types even when unset,
  and return value-free diagnostics with full key/field path/type. Preserve
  syntax/range error causes without retaining value-bearing parser errors.
  Preserve current default/empty/required and nested/collaborator semantics.
- Simplify zero checks; remove the two namespace lookup compositions.
  Keep Mode and secret APIs, shortening comments without changing behavior.
- Logging always wraps its handler in ContextHandler, cloning records before
  appending IDs. Rename TracingHandler/NewTracingHandler; remove Option,
  WithTracing, NewDefault, and NewNoop. Retain Options, New, and ParseLevel.
  Preserve forgiving option parsing and IDs inside the active slog group.
- Examples and scaffold check dotenv errors before reading configuration.
  Construct their configured logger in main and pass it into run explicitly,
  so returned startup errors use that same logger. Do not mutate slog's global
  default from SDK or add a bootstrap framework. Update relevant tests/callers.
- Document every implemented removal, stricter input rule, diagnostic change,
  automatic ID field, and discard-handler evaluation change in AUDIT-003.
  Update live docs and add a linked unreleased note; preserve historical notes.

## Preconditions and ownership

Branch/base: firestore-authorization / 51798833. Prior validation/utility work
and parallel Firestore changes are dirty. Environment/logging source is clean.
No consumer repositories, real .env files, credentials, services, dependency
pins, migrations, release tags, or generated artifacts need changing.
Go 1.26.1; use /Users/jrazmi/go/bin/goimports and the existing temporary Go cache.

## Tasks

1. **Environment (parent):** config.go/tags.go and behavioral regressions;
   mode.go/secret.go comments. Fresh environment tests/build/vet.
2. **Logging (named implementer):** foundation/logging source/tests, direct
   capability tracing tests, and eight CMS test no-op callers. Fresh logging
   tests including actual output, record reuse, grouping, option fallback;
   targeted dependent tests. Do not edit example hosts or shared docs.
3. **Hosts (named implementer):** example cmd/server mains, CMS migration main,
   init server/migration templates and affected scaffold/example tests. Explicit
   logger lifetime, checked dotenv loading, and remove CMS WithTracing use.
   Exercise a scaffolded host's malformed dotenv and configured startup error
   paths without a datastore. No shared docs or SDK edits.
4. **Docs/integration (parent):** SDK README, foundation docs, AUDIT-003,
   RELEASING link, audit records/handoff, source/reference sweep and review.
5. **Verification (named verifier after edits):** SDK build/test/vet, fresh
   affected tests, root make check (including generation drift and guards).
   Parent runs docs typecheck/build, migration/HTTP probes, scoped formatter
   and diff checks. Record exact results and any skips/failures here.

Independent task edits have disjoint ownership. The parent's plan resolves
routine choices; no additional approval stage is required. Concurrency testing
should cover the handler's record-ownership change; no live datastore tests
are required for this scope. Full verification uses the current Makefile
inventory rather than older module/guard counts in agent role descriptions.

## Execution record

Implemented the authorized S3 changes. Environment and logging retain their
package names; parsers and diagnostics are corrected, context IDs are automatic,
records are cloned, and the redundant public APIs are removed. All local callers,
example/scaffold startup wiring, current docs, and AUDIT-003 are updated.

Changed files owned by S3 (earlier dirty edits in shared docs are preserved):

- `sdk/foundation/environment/{config,config_test,tags,tags_test,mode,secret}.go`.
- `sdk/foundation/logging/{logging,logging_test,handler,context_test}.go`.
- `sdk/capabilities/tracing/{middleware,middleware_test}.go` (name migration).
- `pockets/cms/internal/inbound/cms/{helpers,public_views,admin_middleware}_test.go`.
- `examples/{auth-cms,cms,goth-showcase,jobs-minimal,minimal}/cmd/server/main.go`.
- `examples/cms/workshop/migrations/main.go`.
- `workshop/gopernicus/internal/commands/templates/init/{main,migrations.main}.go.tmpl`.
- `workshop/gopernicus/internal/commands/scaffold_test.go`.
- `sdk/README.md`, `workshop/documentation/docs/sdk/foundation.md`, and
  `workshop/documentation/docs/guides/compose-host.md`.
- AUDIT-003 in `AUDIT.md`, its unreleased link in `RELEASING.md`, this plan,
  `plans/framework-audit-environment-logging.md`, and `plans/framework-audit.md`.

Verification passed, using `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache` for Go:

- Fresh environment `go test -count=1 ./foundation/environment` after parser
  edits. Tests cover quotes/comments, located value-free errors, failed writes,
  partial application/non-overwrite, named slices, absent unsupported fields,
  width overflow, nested diagnostics, and preserved zero/default precedence.
- Logging implementer, from `sdk/`: `go test -count=1 ./foundation/logging
  ./capabilities/tracing`, corresponding scoped build/vet, and
  `go test -race -count=1 ./foundation/logging`; from `pockets/cms/`,
  `go test -count=1 ./internal/inbound/cms`. Output, fallbacks, filtering,
  grouping, record reuse, and concurrent reuse regressions passed.
- Host implementer, from `workshop/gopernicus/`: `go build ./...`,
  `go vet ./...`, and `go test ./internal/commands -run
  '^TestScaffoldInit(NoneCompiles|TursoCompiles|EnvTemplate)$' -count=1 -v`.
  Generated SDK-only server and Turso server/migration binaries reject dummy
  malformed dotenv before listening/datastore setup. An invalid READ_TIMEOUT
  produces one JSON error on the SDK-only host's configured STDOUT.
- `examples/{minimal,cms,jobs-minimal,goth-showcase}`: build, fresh test, and vet
  passed. auth-cms build/vet passed; its fresh sandbox tests initially denied
  an httptest listener, resolved by the full-gate rerun below.
- Independent verifier, from `sdk/`: `go build ./...`, `go test ./...`, and
  `go vet ./...`; fresh `go test -race -count=1 ./foundation/environment
  ./foundation/logging` passed. Source review found no additional blocker.
- Root `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check`: all 41 modules,
  seven integration-tag vet legs, two integration/live-tag vet legs (compile
  only), 23 guards, scaffold-cache/compilation/startup checks, and generated
  template/asset drift checks passed. The first sandbox attempt denied a fake
  SendGrid httptest listener; automatic approval allowed the successful rerun.
  Logs: `/tmp/gopernicus-s3-make-check.log` and
  `/tmp/gopernicus-s3-make-check-sandbox.log`.
- Root `make docs-build`: pnpm typecheck (`tsc --noEmit`) and Docusaurus build
  passed. The existing nonfatal update-check permissions notice remains.
  Log: `/tmp/gopernicus-s3-docs-build.log`.
- From `sdk/`, `go run /tmp/gopernicus-s3-migration.go`: public replacement APIs,
  dotenv-to-typed-config flow, range-error identity, custom grouped handler,
  disabled LogValuer behavior, and HTTP response/access-log correlation passed.
  This uses dummy values, temporary files, and httptest.NewRecorder without a
  listener. The old environment audit probe also reproduced the corrected
  outcomes; the old logging audit probe names removed APIs and is historical.
- goimports, scoped diff/whitespace checks, live-reference sweep, and migration
  link checks passed. Removed API references remain only in historical evidence
  and migration instructions. Generated templates/assets remain unchanged.

Skipped: live PostgreSQL/Turso/Firestore conformance (connection settings were
confirmed unset), consumer builds/upgrades, browser flows, and 32-bit runtime
execution. No services, real env files, credentials, migration SQL/data, dependencies,
release tags, published output, or generated source were changed. No failing
or blocked verification remains. Branch/base at close: firestore-authorization /
bb5642f0. The branch advanced through the parallel Firestore merge while this
work completed; those commits captured the independently owned Firestore work.
S3 files remain uncommitted, and prior validation/utility work is preserved.

Next: S4 identity/cryptids audit. AUDIT-003 is ready for future consumer upgrades;
S1's documentation finding and later CMS/integration review concerns remain
tracked in the parent plan.
