# Promote common primitives into root SDK

Status: COMPLETE — 2026-09-10. Implemented the owner-approved root-SDK proposal in
[cryptids-name-and-sdk-shape.md](cryptids-name-and-sdk-shape.md).
Parent: [framework-audit.md](framework-audit.md).

## Scope and public names

Root SDK contains common vocabulary and small, explicitly named primitives.
Separate foundation packages remain for useful conceptual boundaries. Move the
implementations and tests for pointer, slug, id and identity into separate root
files. Keep validation/environment/cryptids in foundation; preserve the remaining
dependency rules and stdlib-only root. No compatibility forwarding packages.

| Old qualified name | New root name |
|---|---|
| pointer.Deref / DerefOr | sdk.Deref / DerefOr |
| slug.Make | sdk.Slugify |
| id.Generator / GenerateFunc / NewGenerator | sdk.IDGenerator / IDGenerateFunc / NewIDGenerator |
| id.NanoID / Database | sdk.NanoID / DatabaseID |
| id.Alphabet / DefaultLength | sdk.DefaultIDAlphabet / DefaultIDLength |
| identity.Principal / WithPrincipal / FromContext | sdk.Principal / WithPrincipal / PrincipalFromContext |
| identity.Info / Address / Resolver | sdk.IdentityInfo / IdentityAddress / IdentityResolver |
| identity.User / ServiceAccount | sdk.PrincipalTypeUser / PrincipalTypeServiceAccount |
| identity.KindEmail / KindPhone | sdk.AddressKindEmail / AddressKindPhone |

Keep exported struct field/method names, strings, ID/slug outputs, pointer nil
semantics, principal validity checks and resolver meaning unchanged. The private
principal context key must remain distinct from request/trace/span context keys.
Preserve custom ID generation and database delegation. No global configuration,
new ID policy, identity subsystem or domain behavior. Current ID error diagnostics
can keep their descriptive `id:` prefix.

## Preconditions and ownership

Branch/base: firestore-authentication / 6807ed06; 476 pre-existing dirty paths.
Snapshot: /tmp/gopernicus-root-promotion-baseline.json. Preserve prior audit and
concurrent Firestore changes. Go 1.26.1, no root module; use module-local ./... or
workspace-qualified paths. GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache;
formatter /Users/jrazmi/go/bin/goimports. No generated files are hand-edited.
No consumer edits, dependencies, release/version pins, commits or deployments.

1. **Named implementer:** own only the four promoted SDK source/test directories
   and their new root files. Apply the names above, reconcile private/test names,
   preserve behavior. Exercise principal alongside existing context keys.
2. **Parent:** migrate all other Go imports/selectors using parsed import scope;
   handle existing sdk aliases/local variables safely. Update Workshop templates,
   current docs, architecture, SDK package docs and public migration guide. Do not
   edit the implementer's files while active.
3. **Named backend reviewer:** read-only final review of context/ID/port migration
   and the inward module graph. The root promotion is already authorized; review
   correctness and consistency rather than reopen the package choice.
4. **Verification:** goimports; root/package compatibility and focused race;
   make check for all module build/test/vet, scaffold compilation, tagged sources,
   generated drift and guards; make docs-build. Existing behavior tests move with
   implementation; add only coverage needed for actual root-context coexistence.
5. **Handoff:** AUDIT-009 gives complete old/new mappings. Earlier unreleased
   entries point to final destinations so consumer upgrades do not traverse
   temporary packages. Record all changed files, exact checks and limitations.

## Result and verification

Implemented all four promotions with the public names above. The eight source/test
files now live in root SDK; the eight old files and four old package directories
are removed. Migrated 112 Go callers with parsed import/selector scope and nine
Workshop templates. Updated current guides, package documentation and the
architecture/admission policy. Validation, environment and cryptids remain named
foundation packages; root has no subpackage or third-party dependency.

Existing behavior tests moved with the implementation. Added principal/request/
trace/span context coexistence coverage, including child overrides, parent
preservation and malformed principal shadowing. Named backend review found no
remaining interface/type mismatch in authentication, authorization, events,
notification, UUID or CMS wiring. No actionable findings remain for this change.

[AUDIT-009](../AUDIT.md#audit-009-common-primitives-promoted-into-root-sdk)
records every removed import and renamed public symbol, matching interface
signatures and release coordination. Earlier unreleased migration entries now
point directly to the final root names. Consumer applications are not upgraded;
no module pins or release tags changed. Next audit slice: S8 cacher, ratelimiter
and events, starting with cacher.

### Verification

All Go commands used `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`.

- **PASS:** `/Users/jrazmi/go/bin/goimports -w` on changed, extant Go files;
  final `goimports -l` and scoped `git diff --check` are clean.
- **PASS (from sdk):** `go build .`, `go test . -count=1`, `go vet .`, and
  `go test -race . -count=20`.
- **PASS (repository root):** `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check`.
  All 42 modules ran `go vet ./...`, `go build ./...` and `go test ./...`.
  Includes existing HTTP behavior tests, Workshop scaffold compilation, generated
  template/asset drift checks, tagged-source vet and all architecture guards.
  Log: `/tmp/gopernicus-root-promotion-make-check.log`.
- **PASS (repository root):** `make docs-build` (`pnpm typecheck` and
  `pnpm build` in workshop/documentation). Log:
  `/tmp/gopernicus-root-promotion-docs-build.log`. Docusaurus printed a nonfatal
  update-check permissions warning after successfully generating the site.
- **PASS:** final source/template/current-guide search found no removed import
  paths or old qualified calls. Historical plans and the migration tables retain
  old names intentionally. No generated source or module requirement changed
  relative to this task's starting snapshot.

An initial sandbox SDK test run could not bind an httptest loopback listener;
its preceding build passed and the chained vet was not reached. The full
`make check` rerun with approved loopback access passed, resolving that check.
Live PostgreSQL, Turso and Firestore suites were not run; tagged sources were
checked without live I/O. No consumer application, live backend or interactive
browser smoke test was run. No unresolved verification failures remain.

### Changed-file inventory

194 paths relative to the task's starting content snapshot (including eight
removed files and nine new files), not the cumulative dirty branch diff. Prior
audit/Firestore changes are preserved. The source snapshot is
`/tmp/gopernicus-root-promotion-baseline.json`; machine-readable inventory:
`/tmp/gopernicus-root-promotion-changed-files.json`. Migration helper and detail
lists remain at `/tmp/gopernicus-root-promote-callers.go` and
`/tmp/gopernicus-root-promotion-{mapping,callers,templates,comments-docs}.json`.
The complete inventory below is durable across context windows.

<details>
<summary>Changed paths</summary>

```text
ARCHITECTURE.md
AUDIT.md
NOTES.md
RELEASING.md
examples/auth-cms/README.md
examples/auth-cms/cmd/server/authorization.go
examples/auth-cms/cmd/server/authorization_test.go
examples/auth-cms/cmd/server/guard.go
examples/auth-cms/cmd/server/identity_crypto_test.go
examples/auth-cms/cmd/server/main.go
examples/auth-cms/cmd/server/production_test.go
examples/auth-cms/internal/authmem/authmem.go
examples/auth-cms/internal/memstore/memstore.go
examples/minimal/cmd/server/main.go
examples/minimal/internal/memstore/memstore.go
integrations/cryptids/golang-jwt/README.md
integrations/cryptids/google-uuid/README.md
integrations/cryptids/google-uuid/googleuuid.go
integrations/cryptids/google-uuid/googleuuid_test.go
integrations/notify/mailer/mailer.go
integrations/notify/mailer/mailer_test.go
plans/cryptids-name-and-sdk-shape.md
plans/framework-audit.md
plans/identity-cryptography-cleanup.md
plans/sdk-root-promotion.md
plans/utility-package-cleanup.md
pockets/README.md
pockets/authentication/README.md
pockets/authentication/auth_test.go
pockets/authentication/authentication.go
pockets/authentication/domain/apikey/apikey.go
pockets/authentication/domain/apikey/apikey_test.go
pockets/authentication/domain/credential/credential.go
pockets/authentication/domain/credential/policy_test.go
pockets/authentication/domain/identifier/identifier.go
pockets/authentication/domain/identifier/identifier_test.go
pockets/authentication/domain/invitation/invitation.go
pockets/authentication/domain/invitation/invitation_test.go
pockets/authentication/domain/oauthstate/oauthstate.go
pockets/authentication/domain/securityevent/securityevent.go
pockets/authentication/domain/serviceaccount/serviceaccount.go
pockets/authentication/domain/serviceaccount/serviceaccount_test.go
pockets/authentication/domain/session/session.go
pockets/authentication/domain/user/user.go
pockets/authentication/internal/inbound/authentication/invitation.go
pockets/authentication/internal/inbound/authentication/invitation_test.go
pockets/authentication/internal/logic/authsvc/browser_middleware_test.go
pockets/authentication/internal/logic/authsvc/context.go
pockets/authentication/internal/logic/authsvc/credential_test.go
pockets/authentication/internal/logic/authsvc/delivery.go
pockets/authentication/internal/logic/authsvc/identifier_management_test.go
pockets/authentication/internal/logic/authsvc/machine.go
pockets/authentication/internal/logic/authsvc/machine_test.go
pockets/authentication/internal/logic/authsvc/oauth.go
pockets/authentication/internal/logic/authsvc/oauth_test.go
pockets/authentication/internal/logic/authsvc/resend.go
pockets/authentication/internal/logic/authsvc/resetlink_test.go
pockets/authentication/internal/logic/authsvc/resolver.go
pockets/authentication/internal/logic/authsvc/resolver_test.go
pockets/authentication/internal/logic/authsvc/service.go
pockets/authentication/internal/logic/authsvc/service_test.go
pockets/authentication/internal/logic/authsvc/useradmin.go
pockets/authentication/internal/logic/delivery/command/command.go
pockets/authentication/internal/logic/delivery/deliverychar/deliverychar.go
pockets/authentication/internal/logic/delivery/dispatcher_test.go
pockets/authentication/internal/logic/delivery/inprocess_char_test.go
pockets/authentication/internal/logic/delivery/inprocess_retry_test.go
pockets/authentication/internal/logic/delivery/processor_char_test.go
pockets/authentication/internal/logic/delivery/router.go
pockets/authentication/internal/logic/delivery/router_data_test.go
pockets/authentication/internal/logic/delivery/router_test.go
pockets/authentication/internal/logic/delivery/service_test.go
pockets/authentication/internal/logic/invitationsvc/authorized_test.go
pockets/authentication/internal/logic/invitationsvc/service.go
pockets/authentication/internal/logic/invitationsvc/service_test.go
pockets/authentication/invitation_lookup_test.go
pockets/authentication/security.go
pockets/authentication/security_test.go
pockets/authentication/stores/firestore/SCHEMA.md
pockets/authentication/stores/firestore/fixtures_test.go
pockets/authentication/stores/firestore/identifiers_doc.go
pockets/authentication/stores/firestore/users.go
pockets/authentication/stores/firestore/users_doc.go
pockets/authentication/stores/pgx/api_keys.go
pockets/authentication/stores/pgx/invitations.go
pockets/authentication/stores/pgx/security_events.go
pockets/authentication/stores/pgx/service_accounts.go
pockets/authentication/stores/turso/api_keys.go
pockets/authentication/stores/turso/invitations.go
pockets/authentication/stores/turso/security_events.go
pockets/authentication/stores/turso/service_accounts.go
pockets/authentication/storetest/reference_test.go
pockets/authentication/storetest/storetest.go
pockets/authorization/README.md
pockets/authorization/authorization.go
pockets/authorization/domain/relationship/relationship.go
pockets/authorization/internal/inbound/authorization/roles.go
pockets/authorization/internal/inbound/authorization/roles_test.go
pockets/authorization/internal/inbound/authorization/routes.go
pockets/authorization/internal/logic/authorizersvc/batch_reader_test.go
pockets/authorization/internal/logic/authorizersvc/batch_reason_test.go
pockets/authorization/internal/logic/authorizersvc/explain_test.go
pockets/authorization/internal/logic/authorizersvc/limits_test.go
pockets/authorization/internal/logic/authorizersvc/lookup_test.go
pockets/authorization/internal/logic/authorizersvc/middleware.go
pockets/authorization/internal/logic/authorizersvc/middleware_test.go
pockets/authorization/internal/logic/authorizersvc/model.go
pockets/authorization/internal/logic/authorizersvc/principalref_test.go
pockets/authorization/internal/logic/authorizersvc/service.go
pockets/authorization/internal/logic/authorizersvc/service_test.go
pockets/authorization/internal/logic/decisionsvc/middleware_test.go
pockets/authorization/memstore/memstore.go
pockets/authorization/memstore/memstore_test.go
pockets/authorization/middleware_test.go
pockets/authorization/role_routes_test.go
pockets/authorization/roles_gate_test.go
pockets/authorization/stores/pgx/README.md
pockets/authorization/stores/turso/README.md
pockets/authorization/storetest/storetest.go
pockets/cms/cms.go
pockets/cms/domain/content/entry.go
pockets/cms/domain/content/registry_test.go
pockets/cms/domain/content/schema.go
pockets/cms/domain/content/status.go
pockets/cms/domain/media/asset.go
pockets/cms/domain/menus/menu.go
pockets/cms/domain/messaging/inquiry.go
pockets/cms/domain/taxonomy/term.go
pockets/cms/internal/logic/entrysvc/service.go
pockets/cms/internal/logic/entrysvc/service_test.go
pockets/cms/internal/logic/mediasvc/media_test.go
pockets/cms/internal/logic/mediasvc/service.go
pockets/cms/internal/logic/menussvc/menus_test.go
pockets/cms/internal/logic/menussvc/service.go
pockets/cms/internal/logic/messagingsvc/messaging_test.go
pockets/cms/internal/logic/messagingsvc/service.go
pockets/cms/internal/logic/taxonomysvc/service.go
pockets/cms/internal/logic/taxonomysvc/taxonomy_test.go
pockets/cms/stores/pgx/assets.go
pockets/cms/stores/pgx/entries.go
pockets/cms/stores/pgx/inquiries.go
pockets/cms/stores/pgx/menus.go
pockets/cms/stores/pgx/terms.go
pockets/cms/stores/turso/assets.go
pockets/cms/stores/turso/entries.go
pockets/cms/stores/turso/inquiries.go
pockets/cms/stores/turso/menus.go
pockets/cms/stores/turso/terms.go
pockets/cms/storetest/storetest.go
pockets/events/README.md
pockets/events/events.go
pockets/events/events_test.go
pockets/events/internal/inbound/events/routes.go
pockets/events/internal/inbound/events/routes_test.go
sdk/README.md
sdk/capabilities/events/events.go
sdk/capabilities/events/record.go
sdk/capabilities/notify/capabilities_test.go
sdk/capabilities/notify/console.go
sdk/capabilities/notify/console_test.go
sdk/capabilities/notify/notify.go
sdk/capabilities/notify/posture_test.go
sdk/errors.go
sdk/foundation/id/id.go
sdk/foundation/id/id_test.go
sdk/foundation/identity/identity.go
sdk/foundation/identity/identity_test.go
sdk/foundation/pointer/pointer.go
sdk/foundation/pointer/pointer_test.go
sdk/foundation/slug/slug.go
sdk/foundation/slug/slug_test.go
sdk/id.go
sdk/id_test.go
sdk/identity.go
sdk/identity_test.go
sdk/pointer.go
sdk/pointer_test.go
sdk/slug.go
sdk/slug_test.go
workshop/documentation/docs/guides/create-pocket.md
workshop/documentation/docs/integrations/catalog.md
workshop/documentation/docs/pockets/authentication.md
workshop/documentation/docs/pockets/events.md
workshop/documentation/docs/sdk/foundation.md
workshop/documentation/docs/sdk/overview.md
workshop/gopernicus/internal/commands/templates/pocket/entity.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/memstore.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/service.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/socket.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/migration.sql.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/store.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/turso/migration.sql.tmpl
workshop/gopernicus/internal/commands/templates/pocket/stores/turso/store.go.tmpl
workshop/gopernicus/internal/commands/templates/pocket/storetest.go.tmpl
```

</details>
