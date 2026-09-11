# SDK package directory rename

Status: COMPLETE — 2026-09-10.
Parent: [framework-audit.md](framework-audit.md).

## Scope and interpretation

The owner requested sdk/foundation → sdk/pkg and asked about Go's naming rules.
pkg is a project convention, not a reserved Go keyword or a special public-package
mechanism. Apply the requested grouping name as a path-only move. Root sdk is also
a Go package; pkg holds the eight cohesive mechanism packages, capabilities keeps
behavioral contracts. Preserve all existing dependency directions, public package
names, APIs, implementations, cryptids naming and module versions.

Move async, cryptids, environment, list, logging, validation, web and workers.
Update all repository imports, build/guard paths, scaffold templates and canonical
docs. Append AUDIT-017 without changing prior entries. No compatibility forwarding
packages, new module, code redesign or unrelated cleanup. CMS changes are strictly
mechanical path updates. Jobs/worker audit fixes remain queued.

The owner's separate authorization-listing question is scoped and queued in
authorization-listing-audit.md; no authorization implementation changes in this move.

## Preconditions and sequence

1. Baseline /tmp/gopernicus-sdk-pkg-baseline.json records the current dirty tree;
   /tmp/gopernicus-sdk-pkg-AUDIT-before.md preserves AUDIT-001..016. It covers 2017 files and 799 prior dirty entries. Branch is
   firestore-authentication / 6807ed06. Compare against this task baseline, not HEAD.
2. Rename the directory and exact path references. Preserve historical plans and
   release entries, while updating active handoff and adding new migration notes.
3. Keep all 42 workspace modules and requirements unchanged. Adjust guards to
   pkg's root-only imports and capabilities' root+pkg allowance; SDK remains
   stdlib-only. No root SDK imports of subpackages.
4. Use named lead-backend-engineer for read-only migration/guard review; configured
   opus is unavailable, so retain role scope on the available inherited model.
5. Verify moved files differ only in import/comment paths, goimports is clean,
   full make check and docs-build pass, and standalone generated hosts/pockets plus
   a direct SDK pkg consumer exercise actual behavior. Record inventory and limits.

Go 1.26.1; formatter /Users/jrazmi/go/bin/goimports;
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Docs use the existing pnpm lockfile.
No provider/service traffic, external consumer writes, publication, schema changes
or manual generated-artifact edits.

## Results

Moved all 91 existing files across the eight SDK packages. Updated active Go
imports, scaffold/build/guard paths, canonical documentation and the docs sidebar;
renamed the SDK documentation page to sdk/pkg.md. The directory is not a new
package or module. Package selectors, exported APIs, implementations and dependency
directions are unchanged. The root SDK remains a package in its own right.

All 91 moved files match their baseline content after reversing import/comment
path changes. Read-only lead-backend-engineer review found no blocking issues and
confirmed the remaining Go/template changes are mechanical imports, comments or
formatter changes. Nine example files also received ordinary goimports whitespace
normalization; their pre-task originals were verified against baseline hashes.
No go.mod, go.sum, go.work, go.work.sum or generated _templ.go changed in this task.
Historical NOTES.md content and AUDIT-001..016 remain byte-preserved. Historical
plans/release entries retain their old paths as historical context.

AUDIT-017 is the consumer import migration guide; RELEASING.md records the
coordinated, unreleased SDK/dependent-module upgrade. The master audit index now
links both this completion and the queued authorization-listing brief. That brief
explicitly compares different stores, the same SQL database without joins, and
the same SQL database with joins. Authorization implementation remains unchanged;
the source-location review is not a completed authorization audit.

### Verification

All commands completed successfully:

- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check`: build/test/vet across
  all 42 modules, all 23 dependency/architecture guards, scaffold tests including
  GOWORK=off generated host/core/store checks, generated-artifact comparisons and
  live-tag compile checks. Log: /tmp/gopernicus-sdk-pkg-check.log.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache go test -race ./sdk/pkg/web/... ./sdk/pkg/workers/... ./pockets/...`:
  all three suites passed. Log: /tmp/gopernicus-sdk-pkg-race.log.
- `make docs-build`: static documentation generated successfully. Log:
  /tmp/gopernicus-sdk-pkg-docs.log.
- `pnpm typecheck` from workshop/documentation: tsc --noEmit passed. Log:
  /tmp/gopernicus-sdk-pkg-typecheck.log.
- `python3 /tmp/gopernicus-sdk-pkg-http-probe.py`: standalone GOWORK=off consumer
  imported sdk/pkg/web and shared pockets using local replacements. An actual
  request through WebHandler and nested PrefixRegistrar/Group returned the expected
  route parameter, status/body and global → group → route → handler middleware
  order. The graph contained exactly the host, SDK and shared pockets modules.
  Log: /tmp/gopernicus-sdk-pkg-http-probe.log.

The HTTP probe used httptest without a persistent listener; the full suite used
owned test loopback servers. No external provider, live SQL service, production
mutation, browser session, release or external consumer execution was performed.
Published versions do not yet provide the new import paths; consumer upgrades
must follow a coordinated release. No unresolved test failures remain.

Final task-relative inventory, formatter and whitespace checks are recorded below.

Final checks passed: `git diff --check`; goimports reported no unformatted files
among all 422 changed Go files; no stale sdk/foundation references remain in active
sources/docs/tooling. Baseline hashes confirm all 91 moves, unchanged module/sum
and generated files, byte-preserved earlier AUDIT entries, and unchanged NOTES.md.
The docs build emitted a nonfatal Docusaurus update-notifier configuration-access
warning after successfully generating the site; no permissions were changed.

### Exact task-relative changed paths

561 paths relative to the pre-task baseline, including both ends of 91 file moves.
This inventory separates this task from the 799 existing dirty entries; it is not
a claim that the complete working tree is solely this change. Temporary logs,
probe scripts, baseline hashes and the JSON inventory live under /tmp. Prior
uncommitted changes in files touched by this rename are preserved.

```text
ARCHITECTURE.md
AUDIT.md
Makefile
README.md
RELEASING.md
examples/README.md
examples/auth-cms/README.md
examples/auth-cms/cmd/server/apikey_search_test.go
examples/auth-cms/cmd/server/authorization.go
examples/auth-cms/cmd/server/authorization_test.go
examples/auth-cms/cmd/server/az3_proof_protocol_test.go
examples/auth-cms/cmd/server/browser_cookie_flow_test.go
examples/auth-cms/cmd/server/demo.go
examples/auth-cms/cmd/server/goth_proof_test.go
examples/auth-cms/cmd/server/inprocess_delivery_test.go
examples/auth-cms/cmd/server/jobs_delivery_live_test.go
examples/auth-cms/cmd/server/jobs_delivery_proof_test.go
examples/auth-cms/cmd/server/jobs_delivery_retry_test.go
examples/auth-cms/cmd/server/main.go
examples/auth-cms/cmd/server/membership.go
examples/auth-cms/cmd/server/outbox_job_handoff_test.go
examples/auth-cms/cmd/server/product.go
examples/auth-cms/cmd/server/purge.go
examples/auth-cms/cmd/server/role_routes_proof_test.go
examples/auth-cms/cmd/server/route_no_mutation_test.go
examples/auth-cms/internal/authmem/ports_v2.go
examples/auth-cms/internal/authmem/user_admin.go
examples/auth-cms/internal/authpages/authpages.go
examples/auth-cms/internal/authpages/authpages_test.go
examples/auth-cms/internal/memstore/memstore.go
examples/cms/README.md
examples/cms/cmd/server/main.go
examples/cms/internal/theme/theme.go
examples/cms/internal/theme/theme_test.go
examples/cms/workshop/migrations/main.go
examples/goth-showcase/cmd/server/main.go
examples/goth-showcase/internal/showcase/all.go
examples/goth-showcase/internal/showcase/showcase.go
examples/goth-showcase/internal/showcase/showcase_test.go
examples/goth-showcase/internal/showcase/specimens.go
examples/goth-showcase/internal/showcase/specimens_components.go
examples/goth-showcase/internal/showcase/specimens_primitives_anchored.go
examples/goth-showcase/internal/showcase/specimens_primitives_compact.go
examples/goth-showcase/internal/showcase/specimens_primitives_data.go
examples/goth-showcase/internal/showcase/specimens_primitives_date.go
examples/goth-showcase/internal/showcase/specimens_primitives_messaging.go
examples/goth-showcase/internal/showcase/specimens_primitives_palette.go
examples/goth-showcase/internal/showcase/specimens_primitives_selection.go
examples/goth-showcase/internal/showcase/specimens_primitives_sidebar.go
examples/jobs-minimal/cmd/server/main.go
examples/minimal/cmd/server/catalog_cache_test.go
examples/minimal/cmd/server/cursor_http_test.go
examples/minimal/cmd/server/goth_doc_snippets_test.go
examples/minimal/cmd/server/goth_htmx_proof_test.go
examples/minimal/cmd/server/main.go
examples/minimal/cmd/server/product.go
examples/minimal/internal/inbound/domains/catalog/routes.go
examples/minimal/internal/memstore/memstore.go
examples/minimal/internal/memstore/pagination_test.go
examples/minimal/internal/outbound/domains/catalog/cms.go
integrations/cryptids/golang-jwt/README.md
integrations/cryptids/golang-jwt/golangjwt.go
integrations/cryptids/golang-jwt/testdata/README.md
integrations/datastores/firestore/README.md
integrations/datastores/firestore/list.go
integrations/datastores/firestore/list_fixtures_test.go
integrations/datastores/firestore/list_integration_test.go
integrations/datastores/firestore/list_internal_test.go
integrations/datastores/firestore/list_live_test.go
integrations/datastores/firestore/search.go
integrations/datastores/firestore/search_test.go
integrations/datastores/pgxdb/README.md
integrations/datastores/pgxdb/cursor_type_test.go
integrations/datastores/pgxdb/list.go
integrations/datastores/pgxdb/list_test.go
integrations/datastores/pgxdb/listquery.go
integrations/datastores/pgxdb/listquery_test.go
integrations/datastores/pgxdb/live_test.go
integrations/datastores/pgxdb/search.go
integrations/datastores/pgxdb/search_test.go
integrations/datastores/turso/README.md
integrations/datastores/turso/cursor_type_test.go
integrations/datastores/turso/list.go
integrations/datastores/turso/list_regression_test.go
integrations/datastores/turso/list_test.go
integrations/datastores/turso/projection_regression_test.go
integrations/datastores/turso/scan_test.go
integrations/datastores/turso/search.go
integrations/datastores/turso/search_test.go
integrations/email/sendgrid/posture_test.go
integrations/filestorage/s3/s3.go
integrations/kvstores/goredis/README.md
integrations/kvstores/goredis/client.go
integrations/kvstores/goredis/client_test.go
integrations/kvstores/goredis/doc.go
integrations/tracing/otel/README.md
integrations/tracing/otel/config.go
integrations/tracing/otel/http.go
plans/authorization-listing-audit.md
plans/framework-audit.md
plans/sdk-pkg-rename.md
pockets/README.md
pockets/authentication/README.md
pockets/authentication/auth_test.go
pockets/authentication/authentication.go
pockets/authentication/config_env_test.go
pockets/authentication/delivery_jobs_test.go
pockets/authentication/delivery_mode_test.go
pockets/authentication/domain/apikey/order.go
pockets/authentication/domain/apikey/repository.go
pockets/authentication/domain/invitation/order.go
pockets/authentication/domain/invitation/repository.go
pockets/authentication/domain/securityevent/order.go
pockets/authentication/domain/securityevent/repository.go
pockets/authentication/domain/serviceaccount/order.go
pockets/authentication/domain/serviceaccount/repository.go
pockets/authentication/domain/user/status.go
pockets/authentication/html_policy_test.go
pockets/authentication/internal/inbound/authentication/account_forms.go
pockets/authentication/internal/inbound/authentication/account_forms_test.go
pockets/authentication/internal/inbound/authentication/challenge_codes_test.go
pockets/authentication/internal/inbound/authentication/csrf_bootstrap_test.go
pockets/authentication/internal/inbound/authentication/dispatch.go
pockets/authentication/internal/inbound/authentication/errors.go
pockets/authentication/internal/inbound/authentication/forms.go
pockets/authentication/internal/inbound/authentication/helpers_test.go
pockets/authentication/internal/inbound/authentication/html.go
pockets/authentication/internal/inbound/authentication/html_test.go
pockets/authentication/internal/inbound/authentication/identifiers.go
pockets/authentication/internal/inbound/authentication/identifiers_test.go
pockets/authentication/internal/inbound/authentication/invitation.go
pockets/authentication/internal/inbound/authentication/invitation_test.go
pockets/authentication/internal/inbound/authentication/isolation_test.go
pockets/authentication/internal/inbound/authentication/machine.go
pockets/authentication/internal/inbound/authentication/machine_gate_test.go
pockets/authentication/internal/inbound/authentication/machine_test.go
pockets/authentication/internal/inbound/authentication/me.go
pockets/authentication/internal/inbound/authentication/me_test.go
pockets/authentication/internal/inbound/authentication/methods.go
pockets/authentication/internal/inbound/authentication/methods_test.go
pockets/authentication/internal/inbound/authentication/oauth.go
pockets/authentication/internal/inbound/authentication/oauth_link_page_test.go
pockets/authentication/internal/inbound/authentication/oauth_test.go
pockets/authentication/internal/inbound/authentication/outcome_notice_test.go
pockets/authentication/internal/inbound/authentication/password.go
pockets/authentication/internal/inbound/authentication/password_flows_test.go
pockets/authentication/internal/inbound/authentication/password_test.go
pockets/authentication/internal/inbound/authentication/passwordless.go
pockets/authentication/internal/inbound/authentication/passwordless_test.go
pockets/authentication/internal/inbound/authentication/principal_posture_test.go
pockets/authentication/internal/inbound/authentication/refresh_cookie_path_test.go
pockets/authentication/internal/inbound/authentication/resend.go
pockets/authentication/internal/inbound/authentication/reset_token_retain_test.go
pockets/authentication/internal/inbound/authentication/routes.go
pockets/authentication/internal/inbound/authentication/saturation_test.go
pockets/authentication/internal/inbound/authentication/security.go
pockets/authentication/internal/inbound/authentication/security_test.go
pockets/authentication/internal/inbound/authentication/sessions.go
pockets/authentication/internal/inbound/authentication/stepup.go
pockets/authentication/internal/inbound/authentication/stepup_test.go
pockets/authentication/internal/inbound/authentication/token_test.go
pockets/authentication/internal/inbound/authentication/useradmin.go
pockets/authentication/internal/inbound/authentication/views.go
pockets/authentication/internal/logic/authsvc/credential.go
pockets/authentication/internal/logic/authsvc/credential_test.go
pockets/authentication/internal/logic/authsvc/delivery.go
pockets/authentication/internal/logic/authsvc/machine.go
pockets/authentication/internal/logic/authsvc/machine_test.go
pockets/authentication/internal/logic/authsvc/oauth_test.go
pockets/authentication/internal/logic/authsvc/securityevent_test.go
pockets/authentication/internal/logic/authsvc/service.go
pockets/authentication/internal/logic/authsvc/token_test.go
pockets/authentication/internal/logic/authsvc/useradmin.go
pockets/authentication/internal/logic/delivery/command/codec.go
pockets/authentication/internal/logic/delivery/command/codec_test.go
pockets/authentication/internal/logic/delivery/command/engine.go
pockets/authentication/internal/logic/delivery/commandcodec.go
pockets/authentication/internal/logic/delivery/dispatcher_test.go
pockets/authentication/internal/logic/delivery/inprocess.go
pockets/authentication/internal/logic/delivery/jobsprocessor.go
pockets/authentication/internal/logic/delivery/processor_char_test.go
pockets/authentication/internal/logic/delivery/service.go
pockets/authentication/internal/logic/delivery/service_test.go
pockets/authentication/internal/logic/invitationsvc/authorized_test.go
pockets/authentication/internal/logic/invitationsvc/service.go
pockets/authentication/internal/logic/invitationsvc/service_test.go
pockets/authentication/internal/redirect/redirect.go
pockets/authentication/runtime_mode_compat_test.go
pockets/authentication/security.go
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
pockets/authentication/views/goth/views.go
pockets/authentication/views/goth/views_test.go
pockets/authorization/authorization.go
pockets/authorization/authorization_test.go
pockets/authorization/codes.go
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
pockets/authorization/internal/logic/authorizersvc/middleware.go
pockets/authorization/internal/logic/authorizersvc/service.go
pockets/authorization/internal/logic/authorizersvc/service_test.go
pockets/authorization/internal/logic/decisionsvc/middleware.go
pockets/authorization/internal/logic/decisionsvc/middleware_test.go
pockets/authorization/internal/logic/decisionsvc/roles.go
pockets/authorization/internal/logic/decisionsvc/roles_test.go
pockets/authorization/internal/logic/rolesvc/service.go
pockets/authorization/internal/logic/rolesvc/service_test.go
pockets/authorization/memstore/memstore.go
pockets/authorization/memstore/memstore_test.go
pockets/authorization/memstore/roles.go
pockets/authorization/memstore/roles_pagination_test.go
pockets/authorization/middleware.go
pockets/authorization/relationship_writer_test.go
pockets/authorization/role_routes.go
pockets/authorization/role_routes_test.go
pockets/authorization/roles_gate_test.go
pockets/authorization/stores/firestore/effective.go
pockets/authorization/stores/firestore/grants.go
pockets/authorization/stores/firestore/lookups_test.go
pockets/authorization/stores/firestore/portcalls_test.go
pockets/authorization/stores/firestore/relationships.go
pockets/authorization/stores/firestore/roles.go
pockets/authorization/stores/firestore/roles_test.go
pockets/authorization/stores/firestore/writes_test.go
pockets/authorization/stores/pgx/collation_test.go
pockets/authorization/stores/pgx/relationships.go
pockets/authorization/stores/pgx/roles.go
pockets/authorization/stores/turso/relationships.go
pockets/authorization/stores/turso/roles.go
pockets/authorization/storetest/mutations.go
pockets/authorization/storetest/roles.go
pockets/authorization/storetest/roles_decision.go
pockets/authorization/storetest/storetest.go
pockets/authorization/storetest/transactional.go
pockets/cms/cms.go
pockets/cms/cms_test.go
pockets/cms/config_env_test.go
pockets/cms/domain/content/entry_repository.go
pockets/cms/domain/content/order.go
pockets/cms/domain/content/registry.go
pockets/cms/domain/content/registry_test.go
pockets/cms/internal/inbound/cms/admin_middleware_test.go
pockets/cms/internal/inbound/cms/contact.go
pockets/cms/internal/inbound/cms/entries.go
pockets/cms/internal/inbound/cms/helpers_test.go
pockets/cms/internal/inbound/cms/media.go
pockets/cms/internal/inbound/cms/menus.go
pockets/cms/internal/inbound/cms/page_cache_test.go
pockets/cms/internal/inbound/cms/public.go
pockets/cms/internal/inbound/cms/public_views_test.go
pockets/cms/internal/inbound/cms/routes.go
pockets/cms/internal/inbound/cms/terms.go
pockets/cms/internal/inbound/cms/views.go
pockets/cms/internal/inbound/cms/views_stub_test.go
pockets/cms/internal/logic/entrysvc/service.go
pockets/cms/internal/logic/entrysvc/service_test.go
pockets/cms/stores/pgx/entries.go
pockets/cms/stores/turso/entries.go
pockets/cms/storetest/reference_test.go
pockets/cms/storetest/storetest.go
pockets/cms/views/goth/helpers.go
pockets/cms/views/goth/views.go
pockets/cms/views/goth/views_test.go
pockets/events/README.md
pockets/events/config_env_test.go
pockets/events/events.go
pockets/events/events_test.go
pockets/events/internal/inbound/events/routes.go
pockets/events/internal/inbound/events/routes_test.go
pockets/events/poller.go
pockets/events/poller_test.go
pockets/events/poller_worker_test.go
pockets/jobs/README.md
pockets/jobs/config_env_test.go
pockets/jobs/domain/job/job.go
pockets/jobs/domain/job/order.go
pockets/jobs/domain/schedule/order.go
pockets/jobs/domain/schedule/schedule.go
pockets/jobs/fenced.go
pockets/jobs/internal/logic/queuesvc/service_test.go
pockets/jobs/internal/logic/runtime/lifecycle_test.go
pockets/jobs/internal/logic/runtime/runtime.go
pockets/jobs/internal/logic/runtime/runtime_test.go
pockets/jobs/internal/logic/schedulesvc/service.go
pockets/jobs/internal/logic/schedulesvc/service_test.go
pockets/jobs/jobs.go
pockets/jobs/jobs_test.go
pockets/jobs/kinds_test.go
pockets/jobs/memstore/defer.go
pockets/jobs/memstore/fenced.go
pockets/jobs/memstore/queue.go
pockets/jobs/memstore/schedules.go
pockets/jobs/middleware_test.go
pockets/jobs/stores/pgx/defer.go
pockets/jobs/stores/pgx/fenced.go
pockets/jobs/stores/pgx/queue.go
pockets/jobs/stores/pgx/schedules.go
pockets/jobs/stores/turso/defer.go
pockets/jobs/stores/turso/fenced.go
pockets/jobs/stores/turso/queue.go
pockets/jobs/stores/turso/schedules.go
pockets/jobs/storetest/defer.go
pockets/jobs/storetest/fenced.go
pockets/jobs/storetest/storetest.go
pockets/mount.go
pockets/mount_test.go
pockets/routes.go
pockets/routes_test.go
sdk/README.md
sdk/capabilities/cacher/middleware.go
sdk/capabilities/cacher/middleware_behavior_test.go
sdk/capabilities/cacher/middleware_test.go
sdk/capabilities/notify/email/delivery_test.go
sdk/capabilities/notify/email/posture_test.go
sdk/capabilities/notify/posture.go
sdk/capabilities/notify/posture_test.go
sdk/capabilities/ratelimiter/middleware.go
sdk/capabilities/tracing/completion_test.go
sdk/capabilities/tracing/middleware.go
sdk/capabilities/tracing/middleware_test.go
sdk/capabilities/tracing/tracing.go
sdk/capabilities/work/work.go
sdk/context.go
sdk/faults.go
sdk/foundation/async/lifecycle_test.go
sdk/foundation/async/pool.go
sdk/foundation/async/pool_test.go
sdk/foundation/cryptids/aesgcm.go
sdk/foundation/cryptids/aesgcm_test.go
sdk/foundation/cryptids/cryptids.go
sdk/foundation/cryptids/jwt.go
sdk/foundation/cryptids/sha256.go
sdk/foundation/cryptids/sha256_test.go
sdk/foundation/environment/config.go
sdk/foundation/environment/config_test.go
sdk/foundation/environment/mode.go
sdk/foundation/environment/mode_test.go
sdk/foundation/environment/secret.go
sdk/foundation/environment/secret_test.go
sdk/foundation/environment/tags.go
sdk/foundation/environment/tags_test.go
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
sdk/foundation/logging/context_test.go
sdk/foundation/logging/handler.go
sdk/foundation/logging/logging.go
sdk/foundation/logging/logging_env_test.go
sdk/foundation/logging/logging_test.go
sdk/foundation/validation/example_test.go
sdk/foundation/validation/validate.go
sdk/foundation/validation/validate_test.go
sdk/foundation/web/composition_test.go
sdk/foundation/web/crossorigin_example_test.go
sdk/foundation/web/errors.go
sdk/foundation/web/errors_test.go
sdk/foundation/web/groups.go
sdk/foundation/web/groups_test.go
sdk/foundation/web/handler.go
sdk/foundation/web/handler_test.go
sdk/foundation/web/methods.go
sdk/foundation/web/middleware.go
sdk/foundation/web/middleware_test.go
sdk/foundation/web/readbody.go
sdk/foundation/web/readbody_test.go
sdk/foundation/web/render.go
sdk/foundation/web/request.go
sdk/foundation/web/request_test.go
sdk/foundation/web/response.go
sdk/foundation/web/response_test.go
sdk/foundation/web/run.go
sdk/foundation/web/run_test.go
sdk/foundation/web/server.go
sdk/foundation/web/server_env_test.go
sdk/foundation/web/server_test.go
sdk/foundation/web/sse.go
sdk/foundation/web/sse_frames_test.go
sdk/foundation/web/sse_heartbeat_test.go
sdk/foundation/web/sse_test.go
sdk/foundation/web/static.go
sdk/foundation/web/static_regression_test.go
sdk/foundation/web/static_test.go
sdk/foundation/web/template.go
sdk/foundation/web/template_test.go
sdk/foundation/web/trustproxies.go
sdk/foundation/web/trustproxies_test.go
sdk/foundation/web/web_test.go
sdk/foundation/workers/errors.go
sdk/foundation/workers/example_test.go
sdk/foundation/workers/fenced.go
sdk/foundation/workers/fenced_test.go
sdk/foundation/workers/lifecycle_test.go
sdk/foundation/workers/middleware.go
sdk/foundation/workers/middleware_test.go
sdk/foundation/workers/outcomes_test.go
sdk/foundation/workers/pool.go
sdk/foundation/workers/pool_test.go
sdk/foundation/workers/process.go
sdk/foundation/workers/runner.go
sdk/foundation/workers/runner_test.go
sdk/foundation/workers/stats.go
sdk/foundation/workers/work.go
sdk/pkg/async/lifecycle_test.go
sdk/pkg/async/pool.go
sdk/pkg/async/pool_test.go
sdk/pkg/cryptids/aesgcm.go
sdk/pkg/cryptids/aesgcm_test.go
sdk/pkg/cryptids/cryptids.go
sdk/pkg/cryptids/jwt.go
sdk/pkg/cryptids/sha256.go
sdk/pkg/cryptids/sha256_test.go
sdk/pkg/environment/config.go
sdk/pkg/environment/config_test.go
sdk/pkg/environment/mode.go
sdk/pkg/environment/mode_test.go
sdk/pkg/environment/secret.go
sdk/pkg/environment/secret_test.go
sdk/pkg/environment/tags.go
sdk/pkg/environment/tags_test.go
sdk/pkg/list/cursor.go
sdk/pkg/list/cursor_test.go
sdk/pkg/list/cursor_validation_test.go
sdk/pkg/list/doc.go
sdk/pkg/list/example_test.go
sdk/pkg/list/list.go
sdk/pkg/list/list_test.go
sdk/pkg/list/order.go
sdk/pkg/list/order_test.go
sdk/pkg/list/pagination.go
sdk/pkg/list/pagination_test.go
sdk/pkg/list/query.go
sdk/pkg/list/query_test.go
sdk/pkg/list/request_validation_test.go
sdk/pkg/list/search.go
sdk/pkg/list/search_test.go
sdk/pkg/logging/context_test.go
sdk/pkg/logging/handler.go
sdk/pkg/logging/logging.go
sdk/pkg/logging/logging_env_test.go
sdk/pkg/logging/logging_test.go
sdk/pkg/validation/example_test.go
sdk/pkg/validation/validate.go
sdk/pkg/validation/validate_test.go
sdk/pkg/web/composition_test.go
sdk/pkg/web/crossorigin_example_test.go
sdk/pkg/web/errors.go
sdk/pkg/web/errors_test.go
sdk/pkg/web/groups.go
sdk/pkg/web/groups_test.go
sdk/pkg/web/handler.go
sdk/pkg/web/handler_test.go
sdk/pkg/web/methods.go
sdk/pkg/web/middleware.go
sdk/pkg/web/middleware_test.go
sdk/pkg/web/readbody.go
sdk/pkg/web/readbody_test.go
sdk/pkg/web/render.go
sdk/pkg/web/request.go
sdk/pkg/web/request_test.go
sdk/pkg/web/response.go
sdk/pkg/web/response_test.go
sdk/pkg/web/run.go
sdk/pkg/web/run_test.go
sdk/pkg/web/server.go
sdk/pkg/web/server_env_test.go
sdk/pkg/web/server_test.go
sdk/pkg/web/sse.go
sdk/pkg/web/sse_frames_test.go
sdk/pkg/web/sse_heartbeat_test.go
sdk/pkg/web/sse_test.go
sdk/pkg/web/static.go
sdk/pkg/web/static_regression_test.go
sdk/pkg/web/static_test.go
sdk/pkg/web/template.go
sdk/pkg/web/template_test.go
sdk/pkg/web/trustproxies.go
sdk/pkg/web/trustproxies_test.go
sdk/pkg/web/web_test.go
sdk/pkg/workers/errors.go
sdk/pkg/workers/example_test.go
sdk/pkg/workers/fenced.go
sdk/pkg/workers/fenced_test.go
sdk/pkg/workers/lifecycle_test.go
sdk/pkg/workers/middleware.go
sdk/pkg/workers/middleware_test.go
sdk/pkg/workers/outcomes_test.go
sdk/pkg/workers/pool.go
sdk/pkg/workers/pool_test.go
sdk/pkg/workers/process.go
sdk/pkg/workers/runner.go
sdk/pkg/workers/runner_test.go
sdk/pkg/workers/stats.go
sdk/pkg/workers/work.go
ui/goth/README.md
ui/goth/assets/assets.go
workshop/documentation/docs/architecture/hexagonal-apps.md
workshop/documentation/docs/architecture/overview.md
workshop/documentation/docs/architecture/repository-layout.md
workshop/documentation/docs/getting-started/choose-your-path.md
workshop/documentation/docs/intro.md
workshop/documentation/docs/pockets/jobs.md
workshop/documentation/docs/project-status.md
workshop/documentation/docs/sdk/capabilities.md
workshop/documentation/docs/sdk/foundation.md
workshop/documentation/docs/sdk/overview.md
workshop/documentation/docs/sdk/pkg.md
workshop/documentation/docs/sdk/web.md
workshop/documentation/docs/ui/react.md
workshop/documentation/sidebars.ts
workshop/gopernicus/internal/commands/templates/init/main.go.tmpl
workshop/gopernicus/internal/commands/templates/init/migrations.main.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/memstore.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/order.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/repository.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/service.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/socket.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/store.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/turso/store.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/storetest.go.tmpl
```
