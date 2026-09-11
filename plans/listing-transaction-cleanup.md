# Listing and transaction audit implementation

Status: COMPLETE — 2026-09-09. The owner authorized fixing S5's bugs and then
implementing its recommendations. Review: [framework-audit-crud.md](framework-audit-crud.md).
Parent: [framework-audit.md](framework-audit.md).

## Decisions and compatibility

- Fix the demonstrated behavior before moving packages. Preserve earlier S2–S4
  changes and concurrent Firestore work. Do not edit consumer applications.
- Cursor decoding requires one complete JSON object, EOF, nonempty field/PK,
  explicit order_value, known type tags and supported scalar/null values.
  Validate structure before stale-field reset. Malformed cursors wrap
  sdk.ErrInvalidInput, preserving causes. Intentional null positions remain
  valid for Firestore. Preserve the existing padded base64url wire format and
  tagged scalar/time encodings; reject unsupported encoder types (including
  named scalars unless explicitly converted) instead of silently losing precision.
  Legacy untagged numbers must not silently lose integer precision.
- Validate resolved parser strategy; blank query values remain absent and docs
  will say so. Share private limit-default resolution while preserving strict
  transport limits and defensive store clamping.
- Previous-page probes include the incoming boundary and overfetch limit+1;
  any match establishes HasPrev and an extra predecessor provides its cursor.
  Change SDK helper, SQL/Firestore adapters, memstores, conformance references,
  and scaffold together. Preserve well-formed existing cursor encoding.
- SQL cursor/search predicates wrap the current authored SELECT in a derived
  table and apply an outer WHERE. Never infer SQL structure from substring
  searches. This preserves OR grouping and nested queries without a SQL parser.
  Order/search columns used outside the authored SELECT must be projected;
  document the migration for store-local row projections and FixedOrder. Keep
  existing query/argument ownership, safe identifiers, and literal search.
- Final review additions: widen float32 before cursor JSON encoding to preserve
  the actual widened database value; regression reproduced a repeated SQLite row.
  SQL keyset rejects null and non-string CastLower values on input and encoding.
  Nullable SQL populations need a non-null projected key or offset mode; other
  scalar compatibility remains host-owned (no schema/type reflection).
  All SQL List ordering uses projected output, including first/offset pages.
  This fixes a transformed alias whose first-page expression otherwise reads
  the raw column while later pages read its projection. Named backend lead owns
  that final correction and its live SQLite regression.
- Keep the raw PK tiebreaker when CastLower changes its ordering expression;
  normalize successful Turso query slices so empty offset pages emit items:[].
- Transaction helpers must clean up on panic, error, failed commit, and
  cancellation. Commit honors the Begin context; rollback receives an independent
  five-second cleanup deadline; driver cooperation determines completion. A failed manual SQL transaction must be rolled back
  or its physical connection discarded before pool reuse. Preserve original
  errors and panic values, expose cleanup causes, and keep existing signatures
  and nesting rules. Do not promise cancellation can undo an already committed
  race, add retries, or add a no-op transactor.
- After fixes: remove foundation/crud; move read helpers to foundation/list.
  Names: ListRequest→Request, ListParams→Params, ListQueryOptions→QueryOptions,
  ParseListRequest→ParseRequest, ParseListQuery→ParseQuery. Other retained list
  names stay. Remove Reader/Writer/CRUD, Field/Some/Overlay, ErrNotFound alias.
  Use sdk.ErrNotFound and domain-owned narrow ports/update semantics.
- Move Transactor unchanged in signature to capabilities/transaction; concise
  shared docs describe callback retries, captured-result reset, effects after
  success, same-instance participating repositories, domain error matching,
  nesting refusal, and connector-specific isolation/read restrictions.
- Update current docs, local callers/templates/guards, and standalone AUDIT-005;
  preserve historical records. No module-path changes or new external deps.
  Coordinated releases/consumer upgrades remain separate.

## Preconditions and ownership

Branch/base: firestore-authentication / 6807ed06. Go 1.26.1, no root module.
Formatter: /Users/jrazmi/go/bin/goimports. GOCACHE:
/tmp/gopernicus-audit-s1.aMLwCQ/cache. Baseline hashes:
/tmp/gopernicus-s5-implementation-baseline.json. Datastore env vars are unset;
no real dotenv/credentials, live services, generated edits, tags, or commits.

## Tasks

1. **Transaction lifecycle (named implementer):** own only pgxdb/turso tx.go,
   transact.go and transaction-specific tests/docs. Implement S5-08/09/10's
   lifecycle corrections. Test actual in-memory SQLite rollback/reuse and
   driver failure/cancellation paths, plus hermetic pgx transaction behavior
   where practical; clearly distinguish live skips. Parent owns package migration
   later, after the implementer finishes. No list/search files or shared docs.
2. **SDK and list behavior (parent):** fix S5-01 through 07 with meaningful
   regressions, including actual multi-page navigation, empty pages, malformed
   cursors and integer boundaries. Apply reverse-probe contract everywhere.
   Named lead-backend-engineer reviews SQL composition/compatibility read-only.
3. **Migration (parent, after fixes):** move SDK packages/names, remove unused
   APIs, migrate all local source/tests/templates and error aliases. Inspect
   package-name shadowing; no blanket replacement of local list variables.
4. **Documentation and verification (parent):** current architecture/SDK/docs,
   affected adapters, examples/scaffold, AUDIT-005, release note and handoff.
   Record exact changed files and commands. Named platform-sre reviews lifecycle
   corrections and migration limits read-only; lead reviews final list behavior.
5. **Final gates:** formatter/diff checks; SDK and affected modules' build/test/vet;
   targeted transaction race/behavior checks; full make check after service
   preflight; docs build; real HTTP invalid-cursor/navigation proof and public
   migration API proof. Run available disposable backends only, without reading
   credentials. Record live PostgreSQL/Firestore/hosted Turso coverage gaps.

## Execution record

Completed all five tasks, preserving prior audits and concurrent Firestore work.
The read-only consumer review found the inspected projected-column/FixedOrder
uses compatible. Named implementer delivered the SQL lifecycle changes; named
backend lead delivered memory/reference/template changes and final projected-scope
correction. Named platform-SRE found no blocking lifecycle defect and clarified
that rollback's five-second context does not guarantee wall-clock completion.

Final review added and fixed two demonstrated bugs: float32's short JSON decimal
changed its widened database boundary, and the first SQL page could order raw
columns while subsequent pages ordered transformed output aliases. Both failed
in real SQLite regressions before correction and now pass. Known SQL cursor
constraints (nonnull, string when CastLower) are checked without adding schema
reflection. Firestore intentionally retains nullable positions.

### Verification

All Go commands use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Module-specific
./... commands run from the named module directory; workspace-qualified commands
below run at the repository root. Formatter: /Users/jrazmi/go/bin/goimports.

| Check | Result and evidence |
|---|---|
| Final `make check` | PASS: 42 modules each run `go vet ./...`, `go build ./...`, `go test ./...`; 8 integration-tag and 3 integration/live-tag vet legs; 23 guards; generated templ/assets drift checks and Workshop generation/scaffold compilation. `/tmp/gopernicus-s5-make-check-final.log` |
| `make docs-build` | PASS: pnpm typecheck and Docusaurus build with the existing lockfile/toolchain. `/tmp/gopernicus-s5-docs-build-final.log`. Nonblocking update-check/config-store notice only. |
| `go test -race -count=1 -run '^TestTransaction' ./integrations/datastores/pgxdb ./integrations/datastores/turso` | PASS after package migration. `/tmp/gopernicus-s5-transaction-race-final.log` |
| `go test -count=1 ./sdk/foundation/list` | PASS, including the public ParseQuery/ParseOrder example. `/tmp/gopernicus-s5-list-sdk-final.log` |
| `go test -count=1 ./examples/minimal/cmd/server ./examples/minimal/internal/memstore ./pockets/authorization/memstore -run '^(TestEntryList\|TestEffectiveRoles)'` | PASS: real CMS router/service/memstore HTTP400 plus ASC/DESC/tied-key previous navigation at limits1/2/3 and effective-role limit1 navigation. `/tmp/gopernicus-s5-router-navigation-final.log` |
| New SQLite behavior regressions | PASS in final full gate: OR/nested filters, search/count, both navigation directions, folded PK case variants, empty offset JSON, float32 boundary and transformed-alias projection parity. SQL cursor type refusal also passed directly: `/tmp/gopernicus-s5-cursor-final.log` |
| SQL lifecycle behavior | PASS: actual local SQLite error/panic rollback, canceled commit, deferred-FK COMMIT failure and pool reuse; driver fakes cover disposal, combined causes and deadline propagation; pgx fakes cover lifecycle/panic/cancellation. Both connectors also passed fresh per-module build/test/vet during implementation. |
| Formatter / final diff | PASS: goimports -l reports no deviations in all 203 existing Go files changed by this implementation; git diff --check passes. No generated files, module manifests or requirements changed in S5. |

The full gate first passed before final-review additions, then was rerun after
those changes. Initial old SQL expectation failures, an unused generated-template
import, embedded CMS field references and SDK listener sandbox failure were all
resolved. Full gates ran with approved local loopback access. The final new CMS
HTTP test uses the real router with an HTTP recorder; it does not open a socket.
There are no unresolved failures or approval blocks.

**Not verified:** live PostgreSQL, hosted Turso/libSQL, Firestore emulator/GCP,
consumer builds/upgrades, browser/UI flows, 32-bit execution, or whole-workspace
race testing. Datastore environment variables remained unset; live tests skipped
and tagged vet is compile-only. No credentials/dotenv or production services were
used. Cooperative fake-driver timeout tests prove propagation, not an unresponsive
driver's completion time. Cross-request pagination does not promise a snapshot
across concurrent mutations. Future pocket audits must inspect actual ambient
transaction participation and same-instance ownership.

### Reproduction and migration records

- Standalone consumer instructions: AUDIT-005 in root AUDIT.md, with coordinated
  unreleased module guidance in RELEASING.md.
- Review's pre-fix probes remain historical evidence in framework-audit-crud.md.
  SDK regressions failed before correction in /tmp/gopernicus-s5-sdk-before.log;
  SQLite list cases in /tmp/gopernicus-s5-list-before.log; float32 cases in
  /tmp/gopernicus-s5-float32-before.log. Do not run removed-package probes as
  current verification commands.
- Branch/base at implementation start and final preflight: firestore-authentication
  / 6807ed06. No commits, tags, external publishing, migrations or consumer edits.
- Exact S5 inventory below is relative to implementation-start hashes in
  /tmp/gopernicus-s5-implementation-baseline.json, not HEAD's combined S2–S5 diff.
  Machine-readable copy: /tmp/gopernicus-s5-final-changed-files.json.
- Next audit slice: S6 foundation/web. Read framework-audit.md and the current
  architecture; do not reopen completed S2–S5 choices without new evidence.

### Changed files (252 paths, including removed SDK files)

```text
ARCHITECTURE.md
AUDIT.md
Makefile
NOTES.md
RELEASING.md
examples/README.md
examples/auth-cms/cmd/server/apikey_search_test.go
examples/auth-cms/cmd/server/az3_proof_protocol_test.go
examples/auth-cms/cmd/server/demo.go
examples/auth-cms/internal/authmem/ports_v2.go
examples/auth-cms/internal/authmem/user_admin.go
examples/auth-cms/internal/memstore/memstore.go
examples/cms/NOTES.md
examples/minimal/cmd/server/cursor_http_test.go
examples/minimal/internal/memstore/memstore.go
examples/minimal/internal/memstore/pagination_test.go
integrations/datastores/firestore/README.md
integrations/datastores/firestore/list.go
integrations/datastores/firestore/list_fixtures_test.go
integrations/datastores/firestore/list_integration_test.go
integrations/datastores/firestore/list_internal_test.go
integrations/datastores/firestore/list_live_test.go
integrations/datastores/firestore/search.go
integrations/datastores/firestore/search_test.go
integrations/datastores/firestore/transact.go
integrations/datastores/firestore/transact_internal_test.go
integrations/datastores/pgxdb/README.md
integrations/datastores/pgxdb/cursor_type_test.go
integrations/datastores/pgxdb/list.go
integrations/datastores/pgxdb/list_test.go
integrations/datastores/pgxdb/listquery.go
integrations/datastores/pgxdb/listquery_test.go
integrations/datastores/pgxdb/live_test.go
integrations/datastores/pgxdb/postgres.go
integrations/datastores/pgxdb/search.go
integrations/datastores/pgxdb/search_test.go
integrations/datastores/pgxdb/transact.go
integrations/datastores/pgxdb/transact_test.go
integrations/datastores/pgxdb/tx.go
integrations/datastores/pgxdb/tx_lifecycle_test.go
integrations/datastores/turso/README.md
integrations/datastores/turso/cursor_type_test.go
integrations/datastores/turso/list.go
integrations/datastores/turso/list_regression_test.go
integrations/datastores/turso/list_test.go
integrations/datastores/turso/projection_regression_test.go
integrations/datastores/turso/scan_test.go
integrations/datastores/turso/search.go
integrations/datastores/turso/search_test.go
integrations/datastores/turso/transact.go
integrations/datastores/turso/tx.go
integrations/datastores/turso/tx_driver_test.go
integrations/datastores/turso/tx_lifecycle_test.go
plans/framework-audit-crud.md
plans/framework-audit.md
plans/listing-transaction-cleanup.md
pockets/README.md
pockets/authentication/README.md
pockets/authentication/auth_test.go
pockets/authentication/authentication.go
pockets/authentication/domain/apikey/order.go
pockets/authentication/domain/apikey/repository.go
pockets/authentication/domain/invitation/order.go
pockets/authentication/domain/invitation/repository.go
pockets/authentication/domain/securityevent/order.go
pockets/authentication/domain/securityevent/repository.go
pockets/authentication/domain/serviceaccount/order.go
pockets/authentication/domain/serviceaccount/repository.go
pockets/authentication/domain/user/status.go
pockets/authentication/internal/inbound/authentication/invitation.go
pockets/authentication/internal/inbound/authentication/invitation_test.go
pockets/authentication/internal/inbound/authentication/machine.go
pockets/authentication/internal/inbound/authentication/machine_gate_test.go
pockets/authentication/internal/inbound/authentication/machine_test.go
pockets/authentication/internal/inbound/authentication/me_test.go
pockets/authentication/internal/inbound/authentication/principal_posture_test.go
pockets/authentication/internal/inbound/authentication/routes.go
pockets/authentication/internal/inbound/authentication/sessions.go
pockets/authentication/internal/logic/authsvc/machine.go
pockets/authentication/internal/logic/authsvc/machine_test.go
pockets/authentication/internal/logic/authsvc/securityevent_test.go
pockets/authentication/internal/logic/authsvc/useradmin.go
pockets/authentication/internal/logic/invitationsvc/authorized_test.go
pockets/authentication/internal/logic/invitationsvc/service.go
pockets/authentication/internal/logic/invitationsvc/service_test.go
pockets/authentication/stores/firestore/apikeys.go
pockets/authentication/stores/firestore/invitations.go
pockets/authentication/stores/firestore/portcalls_test.go
pockets/authentication/stores/firestore/projection_integration_test.go
pockets/authentication/stores/firestore/securityevents.go
pockets/authentication/stores/firestore/serviceaccounts.go
pockets/authentication/stores/firestore/useradmin.go
pockets/authentication/stores/pgx/api_keys.go
pockets/authentication/stores/pgx/collation_test.go
pockets/authentication/stores/pgx/invitations.go
pockets/authentication/stores/pgx/security_events.go
pockets/authentication/stores/pgx/service_accounts.go
pockets/authentication/stores/pgx/user_admin.go
pockets/authentication/stores/turso/api_keys.go
pockets/authentication/stores/turso/invitations.go
pockets/authentication/stores/turso/security_events.go
pockets/authentication/stores/turso/service_accounts.go
pockets/authentication/stores/turso/user_admin.go
pockets/authentication/storetest/reference_test.go
pockets/authentication/storetest/search.go
pockets/authentication/storetest/storetest.go
pockets/authentication/storetest/useradmin.go
pockets/authorization/README.md
pockets/authorization/authorization.go
pockets/authorization/authorization_test.go
pockets/authorization/domain/mutation/mutation.go
pockets/authorization/domain/relationship/order.go
pockets/authorization/domain/relationship/relationship.go
pockets/authorization/domain/relationship/relationship_test.go
pockets/authorization/domain/role/order.go
pockets/authorization/domain/role/role.go
pockets/authorization/domain/role/role_test.go
pockets/authorization/filter_page.go
pockets/authorization/filter_page_test.go
pockets/authorization/internal/inbound/authorization/roles.go
pockets/authorization/internal/inbound/authorization/roles_test.go
pockets/authorization/internal/inbound/authorization/routes.go
pockets/authorization/internal/logic/authorizersvc/model.go
pockets/authorization/internal/logic/authorizersvc/service.go
pockets/authorization/internal/logic/authorizersvc/service_test.go
pockets/authorization/internal/logic/decisionsvc/middleware_test.go
pockets/authorization/internal/logic/decisionsvc/roles.go
pockets/authorization/internal/logic/decisionsvc/roles_test.go
pockets/authorization/internal/logic/rolesvc/service.go
pockets/authorization/internal/logic/rolesvc/service_test.go
pockets/authorization/memstore/conformance_test.go
pockets/authorization/memstore/memstore.go
pockets/authorization/memstore/memstore_test.go
pockets/authorization/memstore/roles.go
pockets/authorization/memstore/roles_pagination_test.go
pockets/authorization/relationship_writer.go
pockets/authorization/relationship_writer_test.go
pockets/authorization/role_routes.go
pockets/authorization/role_routes_test.go
pockets/authorization/stores/UPGRADE.md
pockets/authorization/stores/firestore/README.md
pockets/authorization/stores/firestore/SCHEMA.md
pockets/authorization/stores/firestore/conformance_live_test.go
pockets/authorization/stores/firestore/conformance_test.go
pockets/authorization/stores/firestore/doc.go
pockets/authorization/stores/firestore/effective.go
pockets/authorization/stores/firestore/grants.go
pockets/authorization/stores/firestore/lookups_test.go
pockets/authorization/stores/firestore/portcalls_test.go
pockets/authorization/stores/firestore/relationships.go
pockets/authorization/stores/firestore/roles.go
pockets/authorization/stores/firestore/roles_test.go
pockets/authorization/stores/firestore/writes_test.go
pockets/authorization/stores/pgx/collation_test.go
pockets/authorization/stores/pgx/conformance_test.go
pockets/authorization/stores/pgx/relationships.go
pockets/authorization/stores/pgx/roles.go
pockets/authorization/stores/turso/conformance_test.go
pockets/authorization/stores/turso/relationships.go
pockets/authorization/stores/turso/roles.go
pockets/authorization/storetest/mutations.go
pockets/authorization/storetest/roles.go
pockets/authorization/storetest/roles_decision.go
pockets/authorization/storetest/storetest.go
pockets/authorization/storetest/transactional.go
pockets/cms/domain/content/entry_repository.go
pockets/cms/domain/content/order.go
pockets/cms/internal/inbound/cms/entries.go
pockets/cms/internal/inbound/cms/helpers_test.go
pockets/cms/internal/inbound/cms/public.go
pockets/cms/internal/inbound/cms/public_views_test.go
pockets/cms/internal/logic/entrysvc/service.go
pockets/cms/internal/logic/entrysvc/service_test.go
pockets/cms/stores/pgx/assets.go
pockets/cms/stores/pgx/entries.go
pockets/cms/stores/pgx/menus.go
pockets/cms/stores/pgx/terms.go
pockets/cms/stores/turso/assets.go
pockets/cms/stores/turso/entries.go
pockets/cms/stores/turso/menus.go
pockets/cms/stores/turso/terms.go
pockets/cms/storetest/reference_test.go
pockets/cms/storetest/storetest.go
pockets/jobs/domain/job/job.go
pockets/jobs/domain/job/order.go
pockets/jobs/domain/schedule/order.go
pockets/jobs/domain/schedule/schedule.go
pockets/jobs/internal/logic/queuesvc/service_test.go
pockets/jobs/internal/logic/runtime/runtime_test.go
pockets/jobs/internal/logic/schedulesvc/service_test.go
pockets/jobs/jobs_test.go
pockets/jobs/kinds_test.go
pockets/jobs/memstore/queue.go
pockets/jobs/memstore/schedules.go
pockets/jobs/stores/pgx/queue.go
pockets/jobs/stores/pgx/schedules.go
pockets/jobs/stores/turso/queue.go
pockets/jobs/stores/turso/schedules.go
pockets/jobs/storetest/storetest.go
sdk/README.md
sdk/capabilities/transaction/transaction.go
sdk/errors.go
sdk/foundation/crud/crud.go
sdk/foundation/crud/crud_test.go
sdk/foundation/crud/cursor.go
sdk/foundation/crud/cursor_test.go
sdk/foundation/crud/field.go
sdk/foundation/crud/field_test.go
sdk/foundation/crud/listquery.go
sdk/foundation/crud/listquery_test.go
sdk/foundation/crud/order.go
sdk/foundation/crud/order_test.go
sdk/foundation/crud/pagination.go
sdk/foundation/crud/pagination_test.go
sdk/foundation/crud/search.go
sdk/foundation/crud/search_test.go
sdk/foundation/crud/tx.go
sdk/foundation/list/cursor.go
sdk/foundation/list/cursor_test.go
sdk/foundation/list/cursor_validation_test.go
sdk/foundation/list/doc.go
sdk/foundation/list/example_test.go
sdk/foundation/list/list.go
sdk/foundation/list/list_test.go
sdk/foundation/list/order.go
sdk/foundation/list/order_test.go
sdk/foundation/list/pagination.go
sdk/foundation/list/pagination_test.go
sdk/foundation/list/query.go
sdk/foundation/list/query_test.go
sdk/foundation/list/request_validation_test.go
sdk/foundation/list/search.go
sdk/foundation/list/search_test.go
sdk/foundation/web/openapi.go
sdk/foundation/web/openapi_test.go
sdk/foundation/web/readbody.go
sdk/foundation/web/readbody_test.go
sdk/foundation/web/spec.go
workshop/documentation/docs/getting-started/quickstart.md
workshop/documentation/docs/guides/persistence.md
workshop/documentation/docs/sdk/capabilities.md
workshop/documentation/docs/sdk/foundation.md
workshop/gopernicus/internal/commands/templates/init/Makefile.tmpl
workshop/gopernicus/internal/commands/templates/pocket/memstore.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/order.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/readme.md.tmpl
workshop/gopernicus/internal/commands/templates/pocket/repository.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/service.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/socket.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/store.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/turso/store.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/storetest.go.tmpl
```
