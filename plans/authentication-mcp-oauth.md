# MCP OAuth authorization server and independent delegated sessions

Status: IMPLEMENTED; RELEASE AUTHORIZED — 2026-09-18. Josh approved implementation after accepting
the plan, then explicitly authorized merging to main and publishing the module releases.
Execution is tracked in [the release plan](authentication-mcp-oauth-release.md).
Deployment remains separate work. The defining
requirement is that an MCP connection has a
completely separate session and lifecycle from a web application login.

## Context

Three-sixty needs individual staff identity in Claude's shared Team connector.
The authentication pocket currently signs in people through Google as an OAuth
client; it does not operate the authorization server needed by that connector.
Add the reusable server to this pocket, retaining Google as human login and
leaving MCP resource discovery, API policy and host composition in three-sixty.

This plan incorporates the review of Jake's September 17 brief and Josh's
explicit session-isolation requirement. Three-sixty's `plans/54-mcp-oauth.md`
was not available in this checkout and has not been reviewed. References to its
batch numbers are a handoff mapping, not a validation of that document.

Planning baseline: branch `main`, HEAD `5a388dd1`. This planning pass changes only
`plans/authentication-mcp-oauth.md`. Preserve the pre-existing changes in
`plans/cacher-design.md`, `plans/gps-360-go-audit-upgrade-handoff.md`,
`plans/segovia-v2-audit-upgrade-handoff.md` and `.github/scripts/__pycache__/`.
Recheck the branch, dependency pins and worktree before implementation.

## Goal

A consenting person can establish, inspect and independently revoke multiple
MCP connections while keeping their web session, with strictly bounded OAuth
credentials and immediate revocation enforced by both MCP and the API.

## Decisions and acceptance contract

### Independent sessions, shared identity

- A first-party web/native login and every OAuth connection get different
  session IDs, refresh secrets, refresh families, expiry and revocation state.
  They share `user:<id>` identity and current application permissions only.
  The session type is independent of login method: Google login still creates
  a first-party session, while its consent flow creates a delegated connection.
- An existing live first-party browser session proves who is consenting. Approval
  creates a short-lived authorization code; its successful redemption creates
  a new delegated session atomically. Never reuse, convert or overwrite the
  browser session or cookies. The completed connection has no live dependency
  on the approving browser session. Any retained consent provenance is audit
  data, not a cascading parent-session relationship.
- Signing out of, revoking or expiring one web session leaves every MCP
  connection alone. Disconnecting, expiring or detecting refresh reuse on MCP
  connection A leaves the web session and MCP connection B alone. Connecting
  twice, including to the same client/resource, creates two independently
  manageable connections; do not deduplicate by user or client.
- Explicit account-wide security actions may invalidate all sessions:
  account disable, existing password-reset/change and credential-security policy,
  and a clearly labeled “Sign out everywhere, including connected apps.” Fence
  outstanding authorization codes
  as well as existing sessions with the user's authentication revision so a
  previously approved code cannot resurrect access after global revocation.
  Ordinary single-session logout must not advance that global revision.
- Management responses expose display-safe metadata: session/connection ID,
  type, client identity/name, approved resources, creation and expiry times and
  current-session status. Never expose refresh hashes, tokens or client secrets.
  A live first-party session may manage its owner's sessions; delegated tokens
  cannot list or revoke other sessions or mint credentials.

An OAuth connection is a durable authorization, not an MCP transport session or
an individual chat. Closing a tab, restarting MCP or reconnecting its transport
does not create or revoke the grant. “Disconnect” in this plan means revocation
at our authorization server. Verify whether Claude's own disconnect action calls
the revocation endpoint; the host's account page must always provide an effective
revoke action independently of the client's behavior.

### Separate token audiences and authoritative validation

The MCP client receives an MCP-only token. MCP authenticates as a confidential
client and performs RFC 8693 token exchange to obtain an API-only token. Never
forward the incoming MCP token to the API unchanged or issue a dual-audience
substitute. Exchanged tokens remain delegated, with the same person and the
same MCP session/grant anchor; exchange creates no new session or refresh token.

| Credential | Audience | `client_id` | Session/profile |
| --- | --- | --- | --- |
| Existing web/native access token | First-party policy | Existing first-party semantics | First-party session W |
| Claude OAuth access token | Canonical MCP resource URI | Consenting public client ID | Delegated session A |
| Exchanged API access token | Canonical API resource URI | Authenticated MCP exchange client ID | Same delegated session A |

Persist the originating public client ID on the grant and expose it as
`origin_client_id` on exchanged tokens and their verified credential view.
Their `client_id` identifies the requesting MCP service and `act.sub` identifies
that same exchange actor; the subject remains the person and `session_id` remains
the MCP connection. The issuer derives these values from validated state, never
from caller-selected claims. `iss`, `aud`, `client_id`, token profile and
session binding are verified, not inferred from transport or a supplied header.
Expired tokens, malformed/unknown profiles and missing delegated claims fail
closed. API-token expiry cannot outlive the subject token or delegated session.

The current signer is HMAC-based. Do not share its signing secret with MCP or
assume an unimplemented asymmetric/JWKS facility. Add authenticated RFC 7662
`POST /auth/oauth2/introspect` for MCP's verifier: it checks the token, MCP
audience, active user and live delegated session and returns bounded metadata.
Only the configured confidential client can inspect its authorized resource.
Invalid or unauthorized introspection returns only `active: false` without
disclosing other token metadata.
This inactive response applies to an authenticated caller inspecting an invalid
or out-of-policy token; invalid confidential-client authentication is an OAuth
authentication error, not a successful introspection response.
The API validates its API token and the same live delegated session directly.
No positive liveness cache is allowed if immediate revocation is promised.
Exchange results may be cached only while every operation still checks liveness.

Immediate means admission after a committed revocation fails, including new
operations on an already-open MCP transport. It does not promise to undo an
operation admitted before revocation. Introspection and exchange fail closed
when authoritative state is unavailable.
Bind MCP transport sessions to the delegated session ID as well as the user;
recheck at operation dispatch and before newly authorized protected emissions
so an established stream cannot continue solely on its original login.
Existing first-party routes deliberately using stateless validation retain their
documented access-token revocation window; this feature does not silently change
that contract. Web account/session-management routes use live checks. Delegated
MCP and API requests never inherit the first-party stateless exception.

### First-party boundaries and refresh

- Add verified issuer/audience/client/profile metadata to `Credential`, plus
  `Audience(...)` and `FirstParty()` admission options. Delegated admission is
  opt-in through an explicit configured resource audience and always requires
  live checks. Audience-unaware defaults and `FirstParty()` refuse delegated
  tokens. Delegated tokens never establish cookie sessions, even when pasted
  into a cookie. Preserve valid existing first-party/native and API-key routes.
- Apply first-party gates to bundled consent, session inventory/management,
  credential management, step-up, account browser UI, OAuth-account linking and
  machine-key lifecycle. Audit every bundled group, host override and nested
  middleware path so an already-resolved credential cannot bypass a narrower
  audience/profile requirement. User administration and invitation policies
  retain deliberate API-key support without automatically admitting OAuth.
- The existing `/auth/refresh` service and HTTP endpoint must reject delegated
  refresh credentials before mutation or cookie issuance. OAuth refresh is
  available only through `/auth/oauth2/token`, bound to the original public
  client and approved MCP resource. First-party refresh credentials are refused
  there. Failed cross-profile requests must not rotate or revoke valid sessions.
- Rotate delegated refresh credentials atomically and retain spent-hash family
  state through the grant's lifetime to detect replay beyond the current
  browser implementation's previous-token slot. Reuse revokes only that OAuth
  family/session. Do not inherit browser grace semantics silently; default to
  strict single-use rotation with an explicit client concurrency contract.
- All delegated refresh issuance and rotation uses
  `session.NewDelegatedRefreshToken()` (`oauth2_rt.` plus independent secret
  material). This namespace cannot overlap the existing first-party refresh
  alphabet. Legacy refresh/logout rejects it before storage lookup, including
  when a spent/revoked token no longer resolves, so it can never select a web
  session through access-token fallback. The prefix grants no authority; OAuth
  endpoints still verify hashes and immutable client/session binding.
- Keep fixed independent connection expiry. Refresh and exchange cannot expand
  resources, change client/user/profile or extend past the connection horizon.
  OAuth revocation invalidates the connection and all derived access credentials;
  return RFC 7009's non-enumerating response for unknown/irrelevant tokens.
  Public-client revocation requires the presented token and matching client
  binding; it must never accept a session ID as sufficient proof or revoke the
  consenting browser session. The account UI instead uses its own authenticated,
  owner-constrained management operation. Infrastructure failure must not be
  reported as a successful revocation.

### Authorization protocol and host policy

- Opt-in `/auth/oauth2/{authorize,token,revoke,introspect}` and RFC 8414 discovery;
  no endpoints or changed login behavior when the server feature is disabled.
  Support authorization code with mandatory PKCE S256, refresh, and the narrowly
  configured exchange flow. No implicit flow, password grant or unrestricted
  arbitrary token exchange.
- Validate exact registered redirect URI, client, requested resource, PKCE
  challenge/method and supported parameters before presenting consent. An
  invalid/untrusted redirect must never receive an error redirect. Bind the
  approved request to the consenting user, revision, client, redirect, resource
  and challenge; protect consent POST with CSRF and immutable request binding.
  Carry OAuth `state` unchanged on valid success/error redirects. Code redemption
  is short-lived, hashed at rest and atomically single-use under concurrency.
  Reuse `sdk/capabilities/oauth/pkce.go`'s challenge primitive with server-side
  verifier syntax and constant-time comparison checks.
- Public clients use Client ID Metadata Documents (CIMD) and authentication
  method `none`; advertise `client_id_metadata_document_supported: true` and
  `code_challenge_methods_supported: ["S256"]`, and include `none` in
  `token_endpoint_auth_methods_supported`. Advertise the implemented grant and
  response types and endpoint authentication methods accurately. Emit authorization
  response `iss` and advertise `authorization_response_iss_parameter_supported`.
  The separately configured confidential MCP client uses authenticated
  exchange/introspection; default to `client_secret_basic`, with host-managed
  secret rotation and no raw secret persistence/logging. Token/revoke/introspect
  requests accept the protocol's form encoding, not only the pocket's JSON flow.
- Production requires an explicit client trust allowlist/hook. CIMD retrieval
  uses HTTPS, bounded size/time/concurrency/cache lifetime and validated content.
  Block private, loopback, link-local, metadata and other nonpublic destinations;
  validate resolved addresses at connection time to resist DNS rebinding; reject
  redirects or revalidate every hop; disable unintended proxy bypass. Validate
  document client identity and exact redirect URIs. Untrusted display metadata is
  escaped, never treated as authorization or fetched as arbitrary active content.
  Consent clearly displays the redirect hostname. Hosted Claude's registered
  callback must match exactly; if a trusted native client is enabled, apply only
  its documented loopback-port exception, never wildcard callback matching.
  Local tests use an explicit isolated development transport, not a production
  “disable SSRF” option. Trust withdrawal must not be defeated by stale metadata.
- No scope vocabulary in this release: consent states that the connection may
  use the host's enabled MCP capabilities within the user's current permissions.
  Absent scopes use exactly that documented contract; unsupported requested
  scopes are rejected. Host permission checks remain mandatory at MCP/API entry.
  Requests cannot acquire browser-account, key-management or arbitrary API power
  merely because the same person has it in the web application.
- Production boot validates canonical HTTPS issuer/resource identifiers,
  enabled stores/schema, trust policy, consent renderer, confidential exchange
  client and permitted source/target resources. Metadata derives from explicit
  configuration, never untrusted request Host/proxy headers. Bound and rate-limit
  public protocol endpoints; audit grants, refresh reuse, exchanges and revocation
  without logging codes, tokens, verifier values or secrets.

## Out of scope

- Implementing or deploying three-sixty in this repository; its PRM/challenge,
  MCP verifier/exchange client, API policy, whoami and live Claude drive are a
  separate host workstream with the contract below.
- Dynamic client registration, a general identity provider/OIDC server, new
  Google login behavior, scopes/role redesign, device flow, token passthrough,
  asymmetric-signing/JWKS infrastructure and a second authentication system.
- Firestore OAuth storage support in this feature release. Existing supported
  Firestore authentication must continue to work; enabling OAuth with that
  adapter must fail construction explicitly until its new capabilities exist.
- Release tags, pushes, production migrations, connector creation and publishing
  as part of writing or approving this plan.

## Schema / datastore impact

Reuse the `sessions` table as the revocable anchor, with an explicit persisted
first-party/delegated discriminator and immutable grant metadata. Separate rows
provide separation; a separate database is unnecessary. Existing rows migrate
to first-party and retain their independent lifetimes. Persist origin client,
approved resources and authentication revision for delegated rows. Do not
confuse OAuth authorization grants with existing step-up `authentication_grants`.

Append migrations for `oauth_clients` (validated metadata snapshots),
`oauth_authorization_codes`, delegated session columns and spent refresh-token
family state (proposed `oauth_refresh_tokens`). Pick the next unallocated version
at implementation time; never rewrite existing embedded migration bytes.
Stored raw authorization codes, refresh tokens and client secrets are forbidden.
Retain the current delegated refresh hash in `sessions.refresh_token_hash`
(currently non-null and globally unique); store spent generation hashes in the
new history table and update both atomically. Never create multiple delegated
rows with an empty shared refresh hash.
Add indexes/constraints supporting unique code consumption, rotation and
owner-scoped inventory. Bound metadata/history storage and prune expired data
without weakening replay detection while a grant is still usable.

Define narrow optional OAuth/session-management repository capabilities owned by
authentication. Authorization-code consumption plus fenced session creation,
refresh rotation/replay revocation and owner-constrained single-session deletion
must be atomic. Account-wide sign-out requires a separate atomic operation that
increments the user's authentication revision and deletes all their sessions
under the same user lock as code redemption and credential changes. The existing
`SessionRepository.DeleteByUser` alone does not provide that fence. Single-session
revocation must not call the account-wide operation. Every SQL adapter must
round-trip profile and grant metadata.
Implement Turso, PostgreSQL, the storetest reference and auth-cms memory proof
store; add explicit absent-capability checks for Firestore/custom adapters.

Hosts export and apply new migrations through their own ledger before boot.
Verify old exported files byte-for-byte, update schema probes and test upgrades
from existing data. Drain every old writer before enabling delegated grants:
old binaries cannot be trusted to preserve new profile metadata or reject the
new refresh credentials. Document backup/rollback and a feature-off deployment
stage; never “roll back” to an audience-unaware binary while grants are active.

## Module / API impact

The authentication core remains datastore-free with public `logic/` and
`inbound/http/` packages. New protocol service contracts belong to this pocket;
no SDK OAuth-server port or pocket-to-pocket dependency is needed. Fetching CIMD
is an outbound adapter behind an inward-owned fetch contract. The core's
existing signer accepts custom claims; no SDK release is presumed.

Use current typed constructor-option conventions for one coherent OAuth server
configuration and its required collaborators. Existing Google OAuth client
configuration remains distinct. Consent/session views use optional rendering
ports implemented by `pockets/authentication/views/goth`; an OAuth-enabled
assembly requires a consent implementation but does not force templ on hosts.

Ship one coordinated feature release through multiple reviewable PRs, covering
authentication, both SQL adapters, views and any changed compatibility adapters.
Jake's proposed `pockets/authentication/v0.13.0` and
`pockets/authentication/stores/turso/v0.6.0` are provisional, not reserved tags.
Select all versions from actual tags at release preparation, following
`RELEASING.md`; update affected `go.mod`/`go.sum` pins and verify independent
`GOWORK=off` consumers. One release is not a promise of one sprint.

| Brief item | Expanded upstream deliverable |
| --- | --- |
| U1 | Authorization, token/refresh, revocation, introspection, restricted exchange, CIMD/trust, discovery, consent and connection management |
| U2 | Verified token profiles/claims, first-party boundaries, audience opt-in, legacy-refresh exclusion and live delegated revocation |
| U3 | Append-only SQL migrations, atomic grant/rotation/management contracts, Turso/PostgreSQL/reference parity and explicit unsupported-store gating |

## Generated-artifact impact

Edit `.templ` sources only, then run `make generate`; include regenerated
`*_templ.go` outputs. No UI asset rebuild is needed unless a later implementation
actually changes asset sources. `make check` compares generated files to Git,
so evaluate it against an intended clean candidate or a disposable source copy;
do not discard user edits or commit solely to satisfy its drift check.

## Risks

- Credential confusion can turn an MCP login into account-wide authority. Profile
  isolation, audience opt-in, legacy endpoint rejection and nested-middleware
  tests precede enabling the authorization server.
- A shared-user/session shortcut or missed live check breaks independent
  revocation. Test one web login plus two OAuth connections and both exchanged
  tokens, including races and an already-open MCP transport.
- Protocol interop, CIMD network security and atomic store behavior are larger
  than two new endpoints. Hermetic tests do not replace live SQL and browser/Claude
  drives; report those limits explicitly before host rollout.

## Tasks

All paths below start at the repository root. A path marked **new** is proposed;
retain existing architecture if a narrower placement becomes evident. Task
models name the repository's configured implementer role; use an available
equivalent only when that configured model is unavailable.

### task-1: Introduce session profiles and safe authentication admission

- **depends_on:** []
- **model:** opus
- **files:** `pockets/authentication/logic/authentication/session/session.go`,
  `pockets/authentication/logic/authentication/credential.go`,
  `pockets/authentication/logic/authentication/service.go`,
  `pockets/authentication/logic/authentication/token.go`,
  `pockets/authentication/logic/authentication/authenticate.go`,
  `pockets/authentication/logic/authentication/refresh.go`,
  `pockets/authentication/inbound/http/principal_options.go`,
  `pockets/authentication/inbound/http/principal.go`,
  `pockets/authentication/inbound/http/routes.go`,
  `pockets/authentication/inbound/http/sessions.go`, and adjacent regression tests.
- **verify:** `(cd pockets/authentication && go build ./... && go test ./... && go vet ./...)`; `make guard`.
- **description:** Define the claims/profile contract and centralize signing and
  validation without changing existing first-party login semantics. Reject
  delegated credentials at audience-unaware, first-party, cookie and legacy
  refresh/logout boundaries; enforce explicit delegated audience/liveness and
  nested narrowing. Audit bundled routes and direct service calls, including
  native first-party compatibility. Keep OAuth issuance disabled at this stage.

### task-2: Add atomic grant storage and independent session management

- **depends_on:** [task-1]
- **model:** opus
- **files:** `pockets/authentication/repositories.go`,
  `pockets/authentication/logic/authentication/session/repository.go`,
  `pockets/authentication/logic/authentication/oauth2/repository.go` **(new)**,
  `pockets/authentication/stores/storetest/storetest.go`,
  `pockets/authentication/stores/storetest/reference_test.go`,
  `pockets/authentication/stores/storetest/oauth2.go` **(new)**,
  `pockets/authentication/stores/turso/sessions.go`,
  `pockets/authentication/stores/turso/turso.go`,
  `pockets/authentication/stores/pgx/sessions.go`,
  `pockets/authentication/stores/pgx/postgres.go`,
  both SQL adapters' `oauth2.go` **(new)**, append-only `migrations/*.sql` **(new)**,
  schema/conformance tests, `pockets/authentication/stores/firestore/sessions.go`,
  `pockets/authentication/stores/firestore/README.md`,
  `examples/auth-cms/internal/authmem/authmem.go`,
  `examples/auth-cms/internal/authmem/oauth2.go` **(new)**.
- **verify:** SQL/reference verification commands below; `make check`.
- **description:** Implement code redemption, revision fencing, delegated refresh
  history/rotation, ownership-constrained session list/revoke contracts and the
  atomic account-wide revision-and-revocation operation.
  Round-trip all new fields in supported stores; establish explicit unsupported
  capabilities for Firestore/custom stores and preserve their first-party paths.
  Test migrations, old-byte preservation, races, restart/reconstruction and
  cross-user deletion denial before wiring any live OAuth route.

### task-3: Implement the OAuth protocol service and restricted exchange

- **depends_on:** [task-2]
- **model:** opus
- **files:** `pockets/authentication/logic/authentication/oauth2/service.go`
  **(new)**, `pockets/authentication/logic/authentication/oauth2/client.go`
  **(new)**, `pockets/authentication/logic/authentication/oauth2/service_test.go`
  **(new)**, `pockets/authentication/outbound/oauth2/cimd.go` **(new)**,
  `pockets/authentication/outbound/oauth2/cimd_test.go` **(new)**,
  `pockets/authentication/logic/authentication/service.go`,
  `pockets/authentication/logic/authentication/useradmin.go`,
  `pockets/authentication/logic/authentication/securityevent/securityevent.go`,
  `workshop/gopernicus/internal/commands/pocket_boundaries_test.go`.
- **verify:** `(cd pockets/authentication && go build ./... && go test -race ./... && go vet ./...)`; `make guard`.
- **description:** Implement bound authorization/code exchange, strict delegated
  refresh/reuse, grant revocation, resource-limited token exchange and authoritative
  introspection. Keep identity/credential proof and lifecycle invariants in logic,
  host trust/permission decisions at the inbound policy boundary, and CIMD network
  I/O in outbound. Extend the pocket-logic guard and its negative fixtures to reject
  imports of the concrete outbound adapter from logic; composition wires the port.
  Exercise invalid bindings, disabled users, revision changes,
  foreign-client access, replay and SSRF using controlled fixtures.

### task-4: Wire discovery, HTTP endpoints and fail-closed configuration

- **depends_on:** [task-3]
- **model:** opus
- **files:** `pockets/authentication/config.go`,
  `pockets/authentication/configuration.go`,
  `pockets/authentication/constructor.go`,
  `pockets/authentication/authentication.go`,
  `pockets/authentication/inbound/http/adapter.go`,
  `pockets/authentication/inbound/http/options.go`,
  `pockets/authentication/inbound/http/routes.go`,
  `pockets/authentication/inbound/http/oauth2.go` **(new)**,
  `pockets/authentication/inbound/http/oauth2_test.go` **(new)**,
  `pockets/authentication/configuration_hardening_test.go`.
- **verify:** `(cd pockets/authentication && go build ./... && go test ./... && go vet ./...)`; `make check`; run-and-look using task-6's proof host.
- **description:** Mount the opt-in OAuth endpoints and RFC 8414 metadata with
  standard form/error semantics, appropriate cache controls, exact redirect
  validation and consent CSRF binding. Require production trust/resource/client/
  renderer/storage posture before serving and prove disabled mode is unchanged.
  Keep existing provider OAuth routes and refresh cookies distinct.

### task-5: Add consent and owner-visible connection management

- **depends_on:** [task-4]
- **model:** opus
- **files:** `pockets/authentication/inbound/http/views.go`,
  `pockets/authentication/inbound/http/session_management.go` **(new)**,
  `pockets/authentication/logic/authentication/session_management.go` **(new)**,
  `pockets/authentication/inbound/http/html.go`,
  `pockets/authentication/views/goth/views.go`,
  `pockets/authentication/views/goth/account.templ`,
  `pockets/authentication/views/goth/oauth2.templ` **(new)**,
  `pockets/authentication/views/goth/sessions.templ` **(new)**,
  `pockets/authentication/views/goth/views_test.go`, and generated outputs.
- **verify:** `make generate`; `(cd pockets/authentication/views/goth && go build ./... && go test ./... && go vet ./...)`; `make check`; real-browser acceptance below.
- **description:** Show the consenting account, trusted client identity, resource
  and actual delegated capability contract with allow/deny controls. List web
  sessions and connected apps distinctly, provide owner-scoped revoke controls
  for individual entries and explain exactly which entry/global action ends.
  Protect mutations with first-party live proof and CSRF; make current-web logout,
  one-app disconnect and account-wide revocation visibly different actions.

### task-6: Exercise the complete flow in the local proof host

- **depends_on:** [task-5]
- **model:** opus
- **files:** `examples/auth-cms/cmd/server/main.go`,
  `examples/auth-cms/pockets/authentication/outbound/oauth2fixture/fixture.go`
  **(new; only if host-specific behavior is needed)**,
  `examples/auth-cms/README.md`,
  `examples/auth-cms/cmd/server/oauth2_test.go` **(new)**.
- **verify:** `(cd examples/auth-cms && go build ./... && go test -race ./... && go vet ./...)`; `make guard`; `(cd examples/auth-cms && go run ./cmd/server)` and the browser/HTTP acceptance matrix below.
- **description:** Wire an explicitly local test client, consent views and two
  resource validators through real services; put new provider behavior in a host
  pocket rather than expanding the example's existing composition-root debt.
  Follow `examples/README.md` H0/H6/H7/H10 for new code. At the default
  `http://localhost:8082`, prove the real web login/consent/disconnect journeys
  and actual token HTTP requests with independent cookie jars and client state.

### task-7: Prepare compatibility documentation and coordinated release evidence

- **depends_on:** [task-6]
- **model:** opus
- **files:** `pockets/authentication/README.md`,
  `pockets/authentication/stores/firestore/README.md`, `AUDIT.md`,
  `RELEASING.md`, this plan, affected module `go.mod`/`go.sum` files and the
  `workshop/documentation/docs/pockets/authentication.md`.
- **verify:** `make check`; exact-version `GOWORK=off` build/test/vet for candidate modules and an external composition consumer, following `RELEASING.md`; documentation checks only if those documentation sources change.
- **description:** Publish the API/claims and refresh migration contract, supported
  store matrix, immutable migration handoff, draining/enablement/rollback sequence
  and host acceptance checklist. Record exact test counts, skips, live-store and
  browser evidence. Prepare release candidates only after implementation is
  authorized. Josh subsequently authorized main/tag publication on September 18;
  [the release plan](authentication-mcp-oauth-release.md) records that execution.
  Host deployment still requires its own authorization.

## Sequencing and host handoff

Tasks are sequential reviewable slices. Tasks 1–2 establish safe profiles and
durability; tasks 3–4 provide the server; tasks 5–6 establish user-visible
independence; task 7 closes the coordinated feature milestone. Intermediate PRs
may merge with issuance disabled, but do not enable production grants until all
boundaries, supported adapters and lifecycle tests pass.

Three-sixty can build its original batch 1 (PRM and challenge) disabled while
upstream work proceeds. Its later work must:

1. Re-export new authentication migrations into its existing host ledger with
   historical bytes unchanged; upgrade core/store/views together as applicable.
2. Configure canonical issuer/MCP/API resources, trusted Claude CIMD clients,
   confidential MCP credentials, consent views, active-user policy and feature
   gating in server composition. Keep behavior outside `cmd/` per host rules.
3. Publish MCP PRM and the `WWW-Authenticate` discovery challenge; validate the
   incoming MCP token through authenticated introspection, including each new
   operation on an existing transport. Exchange it for API-only access and make
   the API require the correct audience/profile plus the same live grant.
4. Make whoami/audit display the person, delegated connection, originating client,
   exchange actor and audience accurately. Existing API keys remain their
   existing credential class, not OAuth grants. Document the host's deliberate
   posture for retaining legacy MCP credentials during rollout.
5. Drive consent, tools, independent disconnect/reconnect and revocation from
   the real Claude Team connector with two users where practical. Owner access
   and connector availability are external prerequisites, not upstream test
   substitutes. Report them as blocked/unverified if unavailable.

## Verification and end-to-end acceptance

Before implementation, record branch/HEAD, existing changed files, current module
pins and generated-artifact status. Preserve unrelated user edits. Use the
repository's `goimports` formatter on owned Go changes. There is no root Go
module: run checks inside the named modules or through `make check`.

In addition to task checks, run these commands with independently owned,
disposable PostgreSQL/libSQL fixtures. Verify the environment variables point
only to those fixtures; the suites can truncate tables and drop test schemas.
Do not report skipped database tests as datastore verification.

```sh
(cd pockets/authentication && go test -race ./stores/storetest ./logic/authentication/... ./inbound/http/...)
(cd pockets/authentication/stores/pgx && go build ./... && go test -race ./... && go vet ./...)
(cd pockets/authentication/stores/pgx && POSTGRES_TEST_SCHEMA=mcp_oauth_test go test -race ./...)
(cd pockets/authentication/stores/turso && go build ./... && go test -race -tags=integration ./... && go vet -tags=integration ./...)
(cd pockets/authentication/stores/firestore && go build ./... && go test ./... && go vet ./...)
make check
```

The PostgreSQL runs require `POSTGRES_TEST_DSN`; Turso requires
`TURSO_DATABASE_URL` and, where applicable, `TURSO_AUTH_TOKEN`. Run preserved
Firestore emulator coverage if its session serialization/behavior changes;
that requires `FIRESTORE_EMULATOR_HOST` and `FIRESTORE_PROJECT_ID`, followed by
`(cd pockets/authentication/stores/firestore && go test -race -tags=integration ./...)`.
Local/emulator success does not claim remote Turso or GCP verification.

Create first-party browser session **W** and two independently consented MCP
sessions **A** and **B** for the same user; obtain API tokens **A-api/B-api** by
exchange. Use this matrix in service/store tests and the running proof host;
repeat the applicable user journeys in three-sixty/Claude.

| Action / condition | Required observation |
| --- | --- |
| Approve A, then B | W/A/B have different IDs and refresh credentials; W cookies unchanged; one human principal; each connection appears separately |
| Web logout/revoke/expiry W | A/B and A-api/B-api still work; their refresh remains valid |
| Revoke A from account UI or OAuth endpoint | A, A refresh, A-api and further A exchange/introspection fail immediately; W/B/B-api remain valid |
| Revoke B or expire only B | W/A unaffected; B-derived credentials unusable |
| Reconnect after revocation | New independent session/refresh family; old credentials never revive |
| Replay spent A refresh after multiple rotations | Only A is revoked; W/B unaffected; replay state survives restart |
| Concurrent refresh/code redemption | Atomic defined outcome; at most one code redemption/session; no privilege change, resurrection or orphan usable credential |
| Wrong client, resource, issuer, PKCE, redirect or profile | Generic protocol denial and no unauthorized mint, redirect or mutation |
| MCP token presented to API; API token to MCP | Rejected for audience mismatch; a first-party cookie cannot authenticate the MCP resource (it may authenticate the human approving consent) |
| Delegated access/refresh at web/account/key/legacy refresh paths | Rejected, no Set-Cookie and no widening of authority; first-party/native/key regressions pass |
| Another user's session ID; A attempting to manage B | Rejected without information disclosure or state change |
| Consent denied or CSRF/request binding altered | No code/session issued; web session retained |
| Global security revoke/disable races with code redemption | All W/A/B session rows and refresh families are revoked; all delegated tokens and live-gated W requests fail; outstanding code cannot create a new session; existing first-party stateless TTL behavior is documented |
| Revocation with already-open MCP connection | Next operation denied; no successful operation based on cached liveness |
| Datastore/introspection failure | Delegated admission/refresh/exchange fail closed; no token fallback |
| Unsupported store, missing trust/renderer/resources/client secret | Enabled AS fails boot; disabled feature preserves existing application behavior |
| CIMD private IP, DNS rebind, redirect, oversized/slow response | No prohibited outbound fetch or unbounded work; safe protocol error |

## Open questions

No further architectural ruling is needed before implementation: separate
delegated sessions and token exchange are settled. Before the live host drive,
confirm exact production resource URIs, approved client IDs, delegated lifetimes,
MCP client-secret delivery/rotation and the owner's Claude connector access.
Those values must be explicit host configuration; they do not change session
independence or allow delayed revocation.

## Recommended reviews

- `product-manager`: consent scope language, independent connection management
  and labels for account-wide actions.
- `lead-backend-engineer` and `architecture-steward`: profile boundaries, atomic
  lifecycle contracts, resource validation and adapter capabilities.
- `lead-frontend-engineer`: consent/account views, CSRF integration and real
  browser journeys.
- `platform-sre` and `verifier`: SSRF/credential handling, migration/rollout posture,
  concurrency/live-store proof and Claude acceptance evidence.

## References and planning limits

- [MCP authorization, 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)
  and [access-token security requirements](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations#access-token-privilege-restriction).
- [Claude connector authentication](https://claude.com/docs/connectors/building/authentication)
  and [remote connector request headers](https://claude.com/docs/connectors/custom/remote-mcp#authenticating-with-request-headers).
- [RFC 8693 token exchange](https://www.rfc-editor.org/rfc/rfc8693.html),
  [RFC 7662 introspection](https://www.rfc-editor.org/rfc/rfc7662.html),
  [RFC 7009 revocation](https://www.rfc-editor.org/rfc/rfc7009.html),
  [RFC 8414 metadata](https://www.rfc-editor.org/rfc/rfc8414.html),
  [RFC 8707 resource indicators](https://www.rfc-editor.org/rfc/rfc8707.html),
  [RFC 9700 OAuth security](https://www.rfc-editor.org/rfc/rfc9700.html).
- Repository authorities: [architecture](../ARCHITECTURE.md),
  [pockets charter](../pockets/README.md), [host rules](../examples/README.md),
  [releasing](../RELEASING.md) and the current authentication source paths above.

## Implementation record

Working branch: `authentication-mcp-oauth`, based on `5a388dd1`. The following
implementation evidence predates release preparation; the separate release plan
records committed pins, publication and public verification. Matching repository `implementer` and
`architecture-steward` instructions were used; their configured model names were
unavailable, so the existing agents used inherited models.

- Tasks 1–5: implemented. Independent profiles/admission, atomic stores,
  authorization/refresh/revoke/exchange/introspection, CIMD, optional HTTP wiring,
  consent and owner session management are present.
- Task 6: implemented and exercised through real HTTP, including the running
  local proof host. Visual browser verification remains unverified: no browser
  connector was available, and native Chrome control reported computer-use
  permissions were not granted. No permission or browser security setting was changed.
- Task 7: documentation and unpublished exact-version candidate checks are
  complete for unpublished local candidates; the release manifest records
  versions, archives and live-store evidence.
- Three-sixty deployment, actual MCP protocol transport, PRM/challenges/tool
  authorization and real Claude connector acceptance remain the separate host leg.
  No tag publication, consumer pin update or production deployment was performed.

Implemented behavior: each successful code redemption creates a new delegated
session; approving web session W is not its parent. MCP-only and exchanged API-only
tokens share the same connection A, preserving independent W/A/B revocation.
Token exchange uses the existing OAuth token endpoint and a confidential MCP
client; no second human login, consent or session is created. The MCP specification
requires a separate upstream API token; RFC 8693 is our selected mechanism.

SQL migration `0019_oauth2_sessions.sql` is append-only in pgx/Turso; historical
migration bytes and real module dependency pins remain unchanged. Firestore is
explicitly unsupported for delegated storage, and optional feature construction
fails without the necessary repository/view/trust capabilities. Production CIMD
never permits the demo's local HTTP transport. Metadata snapshots serve display,
not trust; the host schedules `repos.OAuth2.Prune` to remove expired records.

### Verification evidence

All Go/make commands use `GOCACHE=/tmp/gopernicus-mcp-oauth-cache`.

- PASS: `pockets/authentication`: `go build ./...`, `go vet ./...`,
  `go test -race ./...` after the final consent snapshot fixture correction.
- PASS: `examples/auth-cms`: `go build ./...`, `go vet ./...`,
  `go test -race ./...` (server package about 80 seconds); final added constructor
  and OAuth acceptance checks passed with `go test -race ./cmd/server -run '^TestOAuth2' -count=1`.
- PASS: Goth views build/test/vet, generated through the pinned `go tool templ`.
- PASS: real disposable PostgreSQL default and `oauth_scoped` schema race suites,
  including all nine OAuth2 group/case entries and populated pre-0019 upgrades.
  The optional non-C ordering test was skipped (`POSTGRES_NON_C_TEST_DSN` unset).
  A first probe-diagnostic failure came from the retained scoped fixture; after
  removing only that disposable schema, both clean-fixture runs passed. The
  cluster was stopped. The final CSP-only candidate reused this SQL evidence
  after verifying byte-identical store/session/conformance code.
- PASS: Turso build/vet/full tests and race conformance, plus protocol/SQLite
  tests, real local SQLite schema probes and populated pre-0019 upgrade tests.
- PASS: full repository `make guard` in the original working tree, including the
  new logic-to-outbound boundary rejection fixture. Final architectural review
  found no blocking dependency/placement defect; README corrections were applied.
- PASS: `make check` in an isolated working-tree snapshot at
  `/tmp/gopernicus-oauth-check.d4qrtpyy`, including all 42 modules and tagged
  compile/vet checks. Log: `/tmp/gopernicus-mcp-oauth-make-check-final.log`.
  The original-tree invocation intentionally stopped at its HEAD-based generated
  file diff gate because the account template change is uncommitted. The snapshot
  exercises the Makefile's no-.git before/after generation check without altering
  the index; original-tree `make guard` separately covers git-dependent guards.
- PASS: documentation `pnpm typecheck` and `pnpm build`; the Docusaurus update
  notifier could not update its local config, but both checks exited successfully.
- PASS: `git diff --check`, and repository `goimports` on authored Go files.
- PASS: isolated unpublished candidate-module `GOWORK=off` build/test/vet and
  tagged compile checks, plus an external composition consumer with no replacements.
  Exact archive hashes, staging-only pin changes and SQL results are recorded in
  `plans/authentication-mcp-oauth-release-manifest.json`. Public
  proxy/checksum verification for these unpublished versions is not claimed.

Automated full OAuth tests use real TLS listeners, separate HTTP cookie jars,
actual signer, real memory repository and rendered views. They cover incomplete
configuration/disabled routes; PKCE failure without consuming the code; code reuse;
separate W/A/B sessions; exact MCP/API audience and exchange actor bindings;
authenticated live introspection; owner UI revocation preserving siblings; web
logout preserving MCP refresh; global-revoke fencing of approved codes; and old
refresh replay revoking only its connection. Core/store suites additionally cover
concurrency, transaction rollback, foreign-owner denial, trust withdrawal,
datastore failure, legacy profiles/cookies and reused authentication contexts.

The running app was built to `/tmp/gopernicus-mcp-oauth-proof-next` and started
from `/tmp` to avoid loading the user's example `.env`, with explicit
`AUTH_OAUTH2_DEMO=true AUTH_REQUIRE_VERIFIED_EMAIL=false AUTH_RUNTIME_MODE=development
AUTH_DELIVERY_MODE=in_process PUBLIC_BASE_URL=http://localhost:8082 PORT=8082`.
A localhost-only curl/cookie-jar drive exercised web login, two real consent forms,
PKCE callbacks, introspection, token exchange and API calls. It revoked B by its
API-reported session ID and proved A/W survived, logged out W and proved A could
refresh/call the API, then revoked all and proved A was denied. All passed. The
initial drive mistakenly selected the first inventory form (the web session)
instead of B; selecting B by its actual session ID fixed the test assumption.
The disposable app was stopped after verification.

Not verified: visual browser layout/interactions (computer-use permissions),
Claude's real connector, hosted Turso, and Firestore emulator/GCP behavior. The
Firestore module's offline/build/tagged checks do not imply OAuth support or live
GCP verification. First-party stateless routes retain their documented token
expiry window; delegated routes always require live authoritative admission.

Unrelated existing changes preserved: `plans/cacher-design.md`,
`plans/gps-360-go-audit-upgrade-handoff.md`,
`plans/segovia-v2-audit-upgrade-handoff.md`, and `.github/scripts/__pycache__/`.

A final browser-specific review found that self-only `form-action` can block an
OAuth consent form's redirect to an external client. Only the validated consent
page/response now adds the exact registered callback origin to that directive;
queries, wildcard sources and arbitrary directive text are excluded, and all other
pages retain the default policy. Core build/vet/full race and cross-module OAuth
HTTP race tests passed after this fix. Browser execution itself remains unverified.
Candidate archives and the full snapshot check were refreshed after this change.

### Changed-file inventory

- `AUDIT.md`.
- `RELEASING.md`.
- `examples/auth-cms/README.md`.
- `examples/auth-cms/cmd/server/main.go`.
- `examples/auth-cms/cmd/server/oauth2_test.go`.
- `examples/auth-cms/internal/authmem/authmem.go`.
- `examples/auth-cms/internal/authmem/oauth2.go`.
- `examples/auth-cms/pockets/authentication/inbound/http/oauth2demo/demo.go`.
- `examples/auth-cms/pockets/authentication/inbound/http/oauth2demo/demo_test.go`.
- `examples/auth-cms/pockets/authentication/outbound/oauth2fixture/fixture.go`.
- `examples/auth-cms/pockets/authentication/outbound/oauth2fixture/fixture_test.go`.
- `plans/authentication-mcp-oauth.md`.
- `pockets/authentication/README.md`.
- `pockets/authentication/authentication.go`.
- `pockets/authentication/config.go`.
- `pockets/authentication/configuration.go`.
- `pockets/authentication/constructor.go`.
- `pockets/authentication/inbound/http/adapter.go`.
- `pockets/authentication/inbound/http/constructor_fixture_test.go`.
- `pockets/authentication/inbound/http/credential_fence_test.go`.
- `pockets/authentication/inbound/http/credential_test.go`.
- `pockets/authentication/inbound/http/delegated_test.go`.
- `pockets/authentication/inbound/http/forms.go`.
- `pockets/authentication/inbound/http/html.go`.
- `pockets/authentication/inbound/http/oauth2.go`.
- `pockets/authentication/inbound/http/oauth2_config.go`.
- `pockets/authentication/inbound/http/oauth2_test.go`.
- `pockets/authentication/inbound/http/oauth2_views.go`.
- `pockets/authentication/inbound/http/principal.go`.
- `pockets/authentication/inbound/http/principal_options.go`.
- `pockets/authentication/inbound/http/route_policy.go`.
- `pockets/authentication/inbound/http/routes.go`.
- `pockets/authentication/inbound/http/session_management.go`.
- `pockets/authentication/inbound/http/sessions.go`.
- `pockets/authentication/inbound/http/views.go`.
- `pockets/authentication/logic/authentication/authenticate.go`.
- `pockets/authentication/logic/authentication/constructor.go`.
- `pockets/authentication/logic/authentication/context.go`.
- `pockets/authentication/logic/authentication/credential.go`.
- `pockets/authentication/logic/authentication/delegated_test.go`.
- `pockets/authentication/logic/authentication/machine.go`.
- `pockets/authentication/logic/authentication/oauth2/audit.go`.
- `pockets/authentication/logic/authentication/oauth2/authorization.go`.
- `pockets/authentication/logic/authentication/oauth2/client.go`.
- `pockets/authentication/logic/authentication/oauth2/repository.go`.
- `pockets/authentication/logic/authentication/oauth2/service.go`.
- `pockets/authentication/logic/authentication/oauth2/service_test.go`.
- `pockets/authentication/logic/authentication/oauth2/service_types.go`.
- `pockets/authentication/logic/authentication/oauth2/tokens.go`.
- `pockets/authentication/logic/authentication/options.go`.
- `pockets/authentication/logic/authentication/public_test.go`.
- `pockets/authentication/logic/authentication/refresh.go`.
- `pockets/authentication/logic/authentication/service.go`.
- `pockets/authentication/logic/authentication/session/management.go`.
- `pockets/authentication/logic/authentication/session/repository.go`.
- `pockets/authentication/logic/authentication/session/session.go`.
- `pockets/authentication/logic/authentication/session_management.go`.
- `pockets/authentication/logic/authentication/stepup.go`.
- `pockets/authentication/logic/authentication/token.go`.
- `pockets/authentication/logic/authentication/token_test.go`.
- `pockets/authentication/oauth2.go`.
- `pockets/authentication/outbound/oauth2/cimd.go`.
- `pockets/authentication/outbound/oauth2/cimd_test.go`.
- `pockets/authentication/repositories.go`.
- `pockets/authentication/stores/firestore/README.md`.
- `pockets/authentication/stores/firestore/oauth2_unsupported_test.go`.
- `pockets/authentication/stores/firestore/sessions_doc.go`.
- `pockets/authentication/stores/pgx/README.md`.
- `pockets/authentication/stores/pgx/conformance_test.go`.
- `pockets/authentication/stores/pgx/constructor_preconditions_test.go`.
- `pockets/authentication/stores/pgx/helpers.go`.
- `pockets/authentication/stores/pgx/migrations/0019_oauth2_sessions.sql`.
- `pockets/authentication/stores/pgx/migrations_test.go`.
- `pockets/authentication/stores/pgx/oauth2.go`.
- `pockets/authentication/stores/pgx/oauth2_test.go`.
- `pockets/authentication/stores/pgx/oauth2_upgrade_test.go`.
- `pockets/authentication/stores/pgx/passwordless.go`.
- `pockets/authentication/stores/pgx/postgres.go`.
- `pockets/authentication/stores/pgx/sessions.go`.
- `pockets/authentication/stores/pgx/user_admin.go`.
- `pockets/authentication/stores/storetest/oauth2.go`.
- `pockets/authentication/stores/storetest/oauth2_reference_test.go`.
- `pockets/authentication/stores/storetest/reference_test.go`.
- `pockets/authentication/stores/storetest/storetest.go`.
- `pockets/authentication/stores/turso/conformance_integration_test.go`.
- `pockets/authentication/stores/turso/constructor_preconditions_test.go`.
- `pockets/authentication/stores/turso/migrations/0019_oauth2_sessions.sql`.
- `pockets/authentication/stores/turso/migrations_test.go`.
- `pockets/authentication/stores/turso/oauth2.go`.
- `pockets/authentication/stores/turso/oauth2_service_test.go`.
- `pockets/authentication/stores/turso/oauth2_test.go`.
- `pockets/authentication/stores/turso/passwordless.go`.
- `pockets/authentication/stores/turso/schema_probe_test.go`.
- `pockets/authentication/stores/turso/sessions.go`.
- `pockets/authentication/stores/turso/turso.go`.
- `pockets/authentication/stores/turso/user_admin.go`.
- `pockets/authentication/views/goth/account.templ`.
- `pockets/authentication/views/goth/account_templ.go`.
- `pockets/authentication/views/goth/oauth2.go`.
- `pockets/authentication/views/goth/oauth2.templ`.
- `pockets/authentication/views/goth/oauth2_templ.go`.
- `pockets/authentication/views/goth/oauth2_test.go`.
- `pockets/authentication/views/goth/sessions.templ`.
- `pockets/authentication/views/goth/sessions_templ.go`.
- `pockets/authentication/views/goth/views_test.go`.
- `workshop/documentation/docs/pockets/authentication.md`.
- `workshop/gopernicus/internal/commands/pocket_boundaries_test.go`.
- `plans/authentication-mcp-oauth-release-manifest.json`.
