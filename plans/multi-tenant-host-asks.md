# Multi-tenant host asks (issue #232): generic primitives + a guide, a first-admin path, and five smaller gaps

**Status:** EXECUTED 2026-10-28 — PR-0 through PR-5 and PR-O1 (the #249 tag, #250–#254 and the
record PR); six tags on five modules plus `workshop/gopernicus/v0.10.2` for the scaffold. See **As
built** at the end; the tag table, MVS sets and deviations are in `RELEASING.md` "Multi-tenant host
asks"; audit entry AUDIT-058. Was RATIFIED 2026-10-27 (owner: "all recs" — R-1 B-staged, R-2 defer, R-3 won't-do documented, R-4 SHIP PR-O1 (lead lean; planner lean; no planner rec), R-5 defer + recipe, R-6 guide only, R-7 shape + message-arg skip, R-8 DeclineByToken surfaces ErrExpired, R-9 v0.10.0 as announced + H10 as v0.10.1). Was DRAFT rev 2 — 2026-10-27. Drafted from `origin/main` @ `bf1d0d74` (#249 merged; every pin
below is the tag on origin today). The four-reviewer gate has run — lead-backend-engineer
(ratify-with-edits), architecture-steward (aligned-with-edits), product-manager (ship-with-edits, "close
to a rescope"), platform-sre (ship-with-edits) — and is folded under **Decided by gate**. Rev 1 had seven
PRs with three new APIs; rev 2 keeps the three unblock PRs and the guide, makes `Routes()` optional, and
defers the pgxdb probe and the JSON session arm to their own plans. Nothing is ratified until the owner
rules on **Open rulings — YOUR CALL**.

**Owner positions this plan respects (not relitigated):**

- Gopernicus ships **generic primitives and sensible defaults**; hosts own their schema and may drop or
  alter anything. Canonical migration sets are additive-only; nothing destructive.
- **No tenant concept in sdk or pgxdb.** The RLS scope primitive shipped generic: `TransactWithSettings`
  + `(*Tx).Setting` (pgxdb v0.16.0, #237; plan `.claude/plans/pgxdb-local-settings.md`; guide
  `workshop/documentation/docs/guides/row-level-security.md`).
- Prefer the smallest thing that unblocks **two** RLS hosts: battleboardhq (built, on hold for these asks)
  and segovia/v2 (moving to RLS next, two databases, scope on the business `*DB` only).
- Never authn/authz; accept interfaces, return structs; vars and consts first, then interfaces.

## Context

battleboardhq built pooled PostgreSQL row-level-security tenancy, invitation-only sign-in and a
first-admin bootstrap on top of gopernicus, all as host code, and filed #232 asking which parts should
come upstream "as configuration". Two asks have since shipped (the scope primitive; per-instance ambient
transactions, pgxdb v0.15.0, #233–#235). This plan covers what is left: whether gopernicus grows a tenant
concept at all and at which layer, the bootstrap command, and five small gaps (create-time actor,
`jobspgx.Repositories` and the fenced queue, JSON session routes, the H10 false positive, decline by token).

Provenance (L4): items 1–3 are the issue's text. `Routes()` is not in the issue; it comes from
battleboard's `NEXT.md` adopt list. The issue header says "four smaller asks" and lists five.

The reference host code, read for this plan (all read-only):

- battleboardhq `internal/outbound/domains/tenancy/scope.go` — `Scope.Run` /
  `Scope.Querier` (still on the removed `pgxdb.TxFromContext`, line 62; its port to v0.16.0 is written in
  the RLS guide's last section); `internal/logic/domains/tenancy/ports.go` (the `TenantScope` port).
- `workshop/tenancytest/` — `catalog_test.go` (the predicate at line 14, `globalTables` at line 19, the
  per-command policy check at 96–137), `roles_test.go`, `policy_test.go`, `closure_test.go`.
- `workshop/dbroles/` — `roles.sql`, `preflight.sql` (read-only `BEGIN READ ONLY … ROLLBACK` gate),
  `_matrix.sql`, `matrix.md`, `README.md` (the owner/app split).
- `cmd/server/matrix_test.go` — the 51-route endpoint matrix; `recordingRouter` at 192–201 embeds
  `*web.WebHandler` because it cannot list its routes.
- `cmd/bootstrap/main.go` — the converging `-email` command (`findPending` → `Resend` with `WithActor`,
  else `Create` with `InvitedBy: "system:bootstrap"`, lines 43–50 and 133–184).
- `pockets/jobs/outbound/jobs.go:38-40` — the explicit `NewFencedQueueStore`; `pockets/auth/inbound/sessions.go`
  — the host's own JSON session routes (145 lines over the service).
- `HANDOFF.md` / `NEXT.md` — "Use what upstream now provides" (NEXT.md:87-94) lists exactly which of
  these asks the host would adopt; "Known debts" (107–114) names the Decline button. HANDOFF.md:61-65:
  "Don't build around any of these."

## Goal

Every item in #232 has a decision (ship / document / won't-do / deferred), the shipped items land as a
single release train with one `RELEASING.md` record, and the owner's one real decision — whether
gopernicus grows a tenant concept, and where — is presented with trade-offs and a recommendation rather
than taken silently.

## Definition of Done

- PR-1, PR-2, PR-3 merged and tagged; PR-4 (guide) and PR-5 (record + repins) merged; optional PR-O1
  shipped or explicitly dropped by ruling. One `RELEASING.md` section covers the train.
- **Acceptance gate:** battleboardhq's upgrade (its `HANDOFF.md` plan) onto the train's tags, with a
  real-browser drive in which the invitation landing page offers Decline and it works, the
  `invitation_created` security event for `make bootstrap` shows a system actor, and `make check`
  (including `gopernicus guard --posture @v0.10.1`) is green with the original
  `fmt.Errorf("create invitation: %w")` wording restored.
- The RLS guide answers, in writing, every tenancy sub-ask (stores and the ambient transaction,
  RLS-aware ledgers, the catalog posture, the endpoint-matrix pattern, scaffolding, the outbox caveat) so
  the second host does not re-file them.
- `make guard && make check` green on every PR; a green `live-stores` dispatch on `main` covering every
  store tag's commit.

## Out of scope

- Moving segovia/v2 onto current pins (sdk v0.15.0 / pgxdb v0.9.0 / authentication v0.19.1 / jobs v0.6.0
  today; that repin is its own host milestone, and the RLS move follows it).
- Battleboard's own upgrade — it consumes this plan's tags; nothing here edits that repo.
- A tenancy pocket, an sdk tenant context, or tenant-named anything in sdk/pgxdb (rejected below).
- Policy generation or RLS DDL inside any canonical migration set (won't-do below).
- The pgxdb row-security/role posture probe API (deferred to a follow-up plan; design appendix below).
- The JSON session arm (deferred to a follow-up plan unless R-5 (b); design constraints below).
- The transactional-outbox gap (jobs and authentication stores not joining a host transaction for
  atomicity reasons unrelated to RLS) — recorded as a separate open item, not built here.
- An authentication worker-only constructor subset (battleboard flag 1); any authorization pocket change;
  a live-host data upgrade path (none of the hosts has data; standing ruling).

## The central decision: generic primitives + a guide; the tenant model is host domain

The issue calls this "tenancy as a configuration option". The architecture has no slot for such a knob in
`sdk`, `pgxdb` or `pockets` (architecture-steward), so the owner is asked to choose a **posture**, not to
ratify a flag.

### What exists today

| Piece | Where | State |
|---|---|---|
| Transaction-local settings | `integrations/datastores/pgxdb/settings.go:30-62` (`Setting`, `TransactWithSettings`, `(*Tx).Setting`) | shipped v0.16.0 |
| Per-instance ambient tx | `pgxdb/transact.go:31-76` (`Transact`, `TxFrom`, `QuerierFrom`) | shipped v0.15.0 |
| Guide | `workshop/documentation/docs/guides/row-level-security.md` — settings, the store's querier, policies, schema, roles, views, errors, cross-tenant shapes (incl. the `app.scope` branch at 151–164), "pocket tables stay global", two databases, battleboard's `Scope` on v0.16.0, policy-test checklist | shipped |
| Live RLS proof | pgxdb `TestLive_TransactWithSettings_RowLevelSecurity` (superuser-gated) | shipped |
| Catalog posture probes | `pgxdb/probe.go` — `InspectTablePrivileges` (:618), `(*DB).ProbeAppendOnly` (:638), `inspectRoleSQL` (:94, reads `rolsuper` only), `AppendOnlyTable`/`Report`; evidence + caller-supplied canonical bodies | append-only family only |
| Route listing | `sdk/pkg/web/handler.go:18-80` — `WebHandler` records nothing; `pockets/mount.go:21` `RouteRegistrar` is one `Handle` method | absent |
| Scaffold | `workshop/gopernicus/internal/commands/init.go:141` — `init --module --db --dir`; emits a pocket-less `cmd/server` (`templates/init/main.go.tmpl`: "mounts no pockets") | no tenancy flag |
| Guard | `workshop/gopernicus/internal/commands/guard.go:12-16` — a stdlib-only **static source scan**; P0–P9 (`posture_rules.go`) never open a database; G11 forbids the workshop linking a connector | no live rule possible |

### Where a tenant concept would live — tested (architecture-steward)

Nowhere in the framework. The five-point sdk-vs-logic test fails on points 1, 2 and 4 (a tenant-in-context
is a value, not a port; sdk could define no behaviour beyond "a string is in the context"; single-tenant
hosts gain nothing and the two multi-tenant hosts have different tenant models). The `sdk/README`
admission policy fails on plurality and on shared policy worth owning. `pockets/README.md` §5's criteria
1 and 3 fail (no framework producer; the contract would be a host persistence column). The taxonomy
litmus fails: a `pockets/tenancy` would own no canonical schema (the column lives on host tables) and no
routes. The sanctioned home is what battleboard already does: the `TenantScope` port in
`internal/logic/domains/<d>/ports.go` (H1) and the pgxdb-backed `Scope` in
`internal/outbound/domains/<d>/scope.go` (H5), injected by `cmd`.

### The interpretations

**(A) Docs are the product.** Guide sections only. The two primitives already cover the scope;
battleboard's `scope.go`, `tenancytest/`, `dbroles/` and `matrix_test.go` are the reference a second
host copies (its `NEXT.md` already calls them "the reference implementation").

- For: zero new API, zero extra tags, no tenant-shaped surface can creep in.
- Against: two hosts carry ~400 lines of similar catalog/role SQL and a route-recording wrapper each.

**(B-now) Generic primitives shipped in this train.** `Routes()` in sdk, a pgxdb row-security/role
probe, the guide.

- Against (every reviewer, independently): the probe as sketched encodes **battleboard's policy design**
  — the exact `NULLIF` predicate text, one permissive PUBLIC policy per command, a PK led by the column —
  which would reject the guide's own cross-tenant `app.scope` branch and any `uuid`-typed column (deparses
  with `::uuid`); defaults and judgement would be tenant-shaped even with generic names; it ships a pgxdb
  API from the same single data point that defers `init --tenancy=rls`.

**(B-staged) Generic primitives, staged on two data points — RECOMMENDED.**

- Now: the guide grows the remaining sections (catalog posture with the catalog and role queries as
  **copyable SQL**, the owner/app preflight pattern, host-added policy migrations, pocket tables and the
  jobs `tenant_id`, the endpoint-matrix pattern, the outbox caveat, what `init` does and does not emit).
  `Routes()` ships only if the owner wants it (R-4): it is sdk-admissible and twenty lines, but a 10-line
  recording wrapper already meets the need for any `RouteRegistrar`.
- After segovia has its own catalog check and scope: a follow-up plan promotes the pgxdb probe
  (**evidence + caller-supplied predicate**, design appendix below) and decides `init --tenancy=rls`
  with two copies to diff.

- For: smallest thing that unblocks both hosts today; nothing tenant-shaped enters the framework from
  one host's design; the staging rule is the same for the probe and the scaffold.
- Against: segovia copies SQL rather than calling an API for one milestone.

**(C) A tenant concept** — `pockets/tenancy` or an sdk `TenantFrom(ctx)` that pocket stores honour.
Rejected (above): contradicts the ruling; fails every graduation gate; would turn global pocket tables into
tenant tables, which the guide forbids because the jobs worker and the authentication maintenance pass are
cross-tenant by construction.

**Recommendation: (B-staged).**

### The tenancy sub-asks, one by one

**Scope primitive.** Shipped (pgxdb v0.16.0). Remaining framework gap: none. Battleboard's `scope.go:62`
still calls the removed `pgxdb.TxFromContext`; its `HANDOFF.md` carries the port; the guide shows the
v0.16.0 `Scope`. **Nothing to build.**

**Stores joining the ambient transaction.** Today, per store module (production files only):

| Store module | Transaction behaviour | Evidence |
|---|---|---|
| `pockets/authorization/stores/pgx` | joins: `s.db.QuerierFrom(ctx)` / `TxFrom` | `tuples.go:46`, `writes.go:97`, `mutations.go:83`, `operations.go:68,126`, `relationships.go:619`, `read_snapshot.go:19` |
| `pockets/authentication/stores/pgx` | pool only: `s.db.Exec/Query/QueryRow` in 25 files; one `QuerierFrom` in `session_activity.go` | e.g. `invitations.go`, `sessions.go`, `users.go` |
| `pockets/jobs/stores/pgx` | own transactions: `q.db.InTx(...)` | `queue.go:329`; `fenced.go:120,149,215,233,260,282,309`; `schedules.go:133` |
| `pockets/cms/stores/pgx`, `events/stores/pgx`, `secrets/stores/pgx` | pool only | `cms/stores/pgx/entries.go:22`, `events/stores/pgx/outbox.go:34`, `secrets/stores/pgx/pgx.go:86` |

For **RLS** this is a won't-do (R-3): the jobs stores cannot join (`InTx` opens its own transaction) and
the runtime's claim loop runs with no request tenant — a worker is cross-tenant by construction. The jobs
`tenant_id` (`pockets/jobs/stores/pgx/migrations/0001_job_queue.sql:19`, `0002`, `0003:23`, `0004:9`;
`pockets/jobs/logic/schedules/schedule.go:28-32` "the OPTIONAL host-defined boundary") is a **label the
host filters and reports on, not a wall RLS enforces**. `job_queue` under FORCE RLS would make every
claim return zero rows. Pocket tables stay global (guide section already shipped).

For **atomicity** there is a real, separate gap the gate surfaced (lead-backend #12): an enqueue inside
a host `Transact`/`TransactWithSettings` runs on a second connection, so the job can run before the
domain row commits, survives a domain rollback, and can starve a tight pool; authentication's invitation
and user stores have the same shape, so "create organization + invite owner" cannot be atomic. This plan
**documents** the caveat in the guide ("never enqueue inside a held transaction under a tight pool") and
records the outbox gap as an open item for a later plan. It is not an RLS item.

**RLS-aware ledgers, or a host hook to add policies.** Canonical sets are additive-only and global; policies
on pocket tables are a won't-do (above). For the host's **own** tables the hook already exists: the host
owns its ledger, so it appends `NNNN_<name>_row_security.sql` after the exported canonical files (guard P9
requires byte-identical copies of the **store's** files and permits any host file beside them;
battleboard's `battleboard` stream does exactly this). **Document (PR-4).** Not breaking.

**Catalog posture rule.** Options: (a) a `guard --posture` rule — impossible (static scanner; G11); the only
legitimate later P-rule is static presence on the P1 model ("a host whose scanned files call
`TransactWithSettings(` also calls `ProbeRowSecurity(`"), a candidate once the probe exists; (b) **copyable
SQL in the guide now** — the catalog query, the per-command policy check, the role-attribute query, with
the exempt list spelled out (`schema_migrations`, `ratelimit_windows`, and the jobs tables if they share
the schema — "exempt whether or not it has the column"); (c) the pgxdb probe — deferred (appendix).
**Recommendation: (b) now, (c) staged.** The host's privilege **matrix** and object ownership
(`dbroles/preflight.sql`, `_matrix.sql`) stay host-owned in every option — they cannot generalize.

**Endpoint-matrix pattern and `Routes()`.** The matrix test itself stays host code (its classes — public /
self / cross-tenant / platform — are the host's authorization model). The guide documents the pattern
(record every registration; classify; probe cross-tenant routes with another tenant's real id and a missing
id; require byte-identical non-2xx answers; a POST-only decline so link scanners cannot trigger it) with the
recording-wrapper shape, and cites `Routes()` if PR-O1 ships. `pockets.RouteRegistrar` is **never** widened
(steward MED-3: it is the pocket→host port; hosts and tests enumerate; a lister would leak a host
capability into the pocket contract).

**`gopernicus init --tenancy=rls`.** Deferred (R-2). The genuine blocker is not segovia alone: a scope
adapter, a matrix test and a bootstrap all presuppose mounted pockets and a named domain
(H8 requires the `internal/logic` counterpart of every outbound domain), and `init` emits a pocket-less
host. The anatomy slots are clear and H-clean when the time comes: `internal/logic/domains/<d>/ports.go`,
`internal/outbound/domains/<d>/scope.go`, `workshop/dbroles/*.sql`, `workshop/tenancytest/` (opening pgxdb
+ `Underlying()`, H9-legal in workshop, rather than importing `jackc/pgx` directly and forcing a new
`require` into every scaffolded `go.mod`), `cmd/server/matrix_test.go`.

## Item 2: a default `cmd/bootstrap`

**Today:** `examples/production-baseline/cmd/bootstrap/main.go` grants the global `admin` role to an
existing subject through the authorization pocket (external-IdP shape; README 217–232). Battleboard's
`cmd/bootstrap -email` is the native `invitation_only` shape. It works on v0.31+ because `InvitedBy` is a
free string; its attribution wart is fixed by PR-3. `gopernicus init` cannot emit it (no pocket is mounted
to bootstrap). The gate adds (lead-backend #4): battleboard's file carries **behaviour** in `cmd/` — the
pagination loop, the "being accepted, nothing to do" decision, the converge choice between Create and
Resend — which passes H10's floor but not its normative sentence ("cmd wires"); the guide must not canonize
that file as-is.

- Options: (a) a docs guide, "First admin", with both shapes — the grant shape for an external identity
  provider, and the invitation shape for an invitation-only native host — where the converge step is
  written as a host composition or host-pocket function and `cmd/bootstrap` is a flag parse plus one call;
  (b) a second binary in an example — none fits (`auth-cms` is in-memory, production-baseline is
  external-IdP); (c) a core helper `invitations.Service.EnsurePending(ctx, PreparedCreate)`
  (create-or-resend by resource + relation + identifier, actor-attributed), which makes (a)'s host function
  one line — real value, but a new use case on a security-sensitive service for one caller today.
- **Recommendation: (a)** in PR-4; (c) is ruling R-6. Effort: S. Modules: docs only. Not breaking.

## Item 3: the smaller asks

### 3a. Create-time actor on invitations

**Today:** `CreateInput` (`pockets/authentication/logic/invitations/service.go:226-242`) has `InvitedBy`
(required, `:441`) and no actor. `CreatePrepared` (`:490`) → `recordCreated` (`:1049`) → `record(ctx,
inv.InvitedBy, …)` (`:1085`) writes `invitation_created` with `UserID = InvitedBy` and an empty `Actor`.
`recordGrant` (`:1007`) writes the `invitation_granted` rows with `UserID = the granted subject` on every
path (`:535-538`). `Resend`/`Cancel` use `PreparedManagement.WithActor` (`management.go:28`) and
`attribution()` (`management.go:70-80`). The HTTP create handler sets `InvitedBy` from `CurrentUser`
(`inbound/http/invitation.go:188-199`) and feeds `PreparedCreate.Input()` into `InviteCheckRequest`
(`:208-219`); the resend handler calls `WithActor` (`:419`).

- Options: (a) `CreateInput.Actor` — rejected by the gate: `CreateInput` comes back through
  `PreparedCreate.Input()` into host policy, which invites policy code to read `Actor` as authority;
  (b) **`PreparedCreate.WithActor(sdk.Principal) PreparedCreate`**, mirroring management exactly: audit
  attribution only, never authorization; carried in `grantProvenance` so the direct-add granted rows (success
  and failure) gain `Actor` while keeping `UserID = subjectID`; `recordCreated` applies the `attribution()`
  rule (user actor → `UserID = actor.ID`; non-user actor → `UserID = ""`, `Details["invited_by"]` present;
  zero actor → today's `InvitedBy` fallback). Headless `Create` keeps the fallback; a host bootstrap goes
  `PrepareCreate` → `WithActor` → `CreatePrepared`. `InvitedBy` stays required and stays the host's string
  (`system:bootstrap` is fine; the column is `TEXT NOT NULL`, `0009_invitations.sql:29`).
- **Recommendation: (b).** The HTTP create handler calls `prepared.WithActor(user principal)` like
  resend. Effort: S. Module: `pockets/authentication` (minor). Not breaking; no column; no storetest
  additions (so no store repins — SRE). Note for audit consumers: a non-user actor's created row has
  `user_id = ""`, so queries keyed `user_id = invited_by` no longer see it — this matches management.

### 3b. `jobspgx.Repositories` omits `FencedQueue`

**Today:** `pockets/jobs/stores/pgx/postgres.go:134-139` fills `Queue` and `Schedules`; turso
(`stores/turso/turso.go:29-34`) the same. `jobs.New` (`pockets/jobs/jobs.go:30-60`) accepts both queues;
the boot line logs `fenced_queue=false` (`logic/queue/service.go:60`). pgx `StatusCheck`
(`postgres.go:100-118`) probes every table in `probeTables`, including `fenced_job_queue`; **turso has no
`StatusCheck`** (gate). `NewFencedQueueStore` exists on both drivers (pgx `fenced.go:50`, takes `opts` —
`WithSchema` matters, `WithLease` is accepted and ignored; turso `fenced.go:44`, no options). No in-repo
host calls either `Repositories` (battleboard builds its own; auth-cms tests use `NewFencedQueueStore`).

- Options: (a) fill `FencedQueue` in both, passing `opts...` on pgx, and add a turso `StatusCheck` probing
  the store's tables so both drivers fail at boot rather than first enqueue; (b) document the split only.
- **Recommendation: (a).** Effort: XS–S. Modules: `pockets/jobs/stores/pgx` (minor),
  `pockets/jobs/stores/turso` (minor). Not breaking; visible change: the boot line flips to
  `fenced_queue=true` for hosts on `Repositories`.

### 3c. JSON session list/revoke — DEFERRED to its own plan (R-5)

**Today:** `mountSessionManagement` (`inbound/http/session_management.go:14-20`) registers HTML
`GET /auth/sessions`, `POST /auth/sessions/{id}/revoke`, `POST /auth/sessions/revoke-all` only when
`d.OAuth2 != nil` (`routes.go:302-305`) over `OAuth2Config.Sessions` (`oauth2_config.go:12-16`); the root
wires `Sessions: authService` (`constructor.go:642`). `RevokeUserSession` returns `sdk.ErrForbidden` when
the session-management repository is nil (`logic/authentication/session_management.go:36`; that repo is the
OAuth2 store, `stores/pgx/postgres.go:159`). The adapter's dual-transport rule (`dispatch.go`: every shared
mutation endpoint is registered exactly once; Content-Type selects the transport; Accept never reinterprets
a GET) means separate JSON POST routes beside the HTML POSTs need either content-type dispatch on
`/auth/sessions/{id}/revoke` and `revoke-all` or a recorded exception. A second option taking the same
`SessionManagementService` would give one service two injection points (constructor-options convention).

The gate's constraints for whoever builds it: one wiring (fold `OAuth2Config.Sessions` into a single
option — dropping the field is breaking for pocket consumers, an owner call); mount only when the repository
exists; an optional interface by type assertion, not a widened `AuthenticationService`; `GET
/auth/me/sessions` for the list (GET cannot be dispatched); revoke-current and revoke-all clear cookies on
the cookie transport; another user's id and an unknown id answer byte-identically (the HTML arm's blanket
400 is not the model); first-party only — a delegated OAuth access token must not list or revoke sessions;
no hashes in the projection; IP/user-agent only to the owner. Battleboard has working routes on its own
paths and would have to change SPA paths to adopt; no second JSON host has asked; the owner's React hosts
want JSON contracts first-class, which is why this is deferred, not dropped. **This train ships a recipe**
in the authentication pocket page: the three service methods, `FirstParty()` filtering, the gates to reuse.

### 3d. H10 SQL-literal false positive

**Today:** `isSQLLiteral` (`workshop/gopernicus/internal/commands/guard_rules.go:580-592`) flags any
`cmd/**` string literal whose trimmed, upper-cased text starts with one of `sqlVerbs` (`:38`) followed by
whitespace **or end of string** (so a bare `"DELETE"` HTTP-method literal is flagged too). The floor's
prose definition is `examples/README.md:590`; the normative sentence (`:149`, `sentenceH10`,
docs `architecture/host-contract.md:43`) says only "SQL statement literal" and does not change.

- Options: (a) require SQL shape after the verb; (b) skip literals that are the message/format argument of
  `fmt.Errorf`, `errors.New`, `fmt.Sprintf`, `log`/`slog` calls (AST parent check); (c) **both** — the gate
  showed (a) alone still flags `"delete from cache: %w"`, `"insert into map"`, `"select one from the list"`.
- **Recommendation: (c).** Shape rules, token-based on whitespace-normalised text (not prefixes — `UPDATE
  profile SETTINGS` must not pass the `SET` check): `CREATE|ALTER|DROP` + one of `TABLE INDEX UNIQUE OR
  POLICY SCHEMA ROLE EXTENSION FUNCTION TRIGGER VIEW TYPE SEQUENCE TEMP TEMPORARY MATERIALIZED DATABASE
  DEFAULT`; `INSERT INTO`; `DELETE FROM`; `UPDATE <ident> SET`; `SELECT` followed by `*`, a digit, `$`,
  `(`, or `<ident>(`, or containing ` FROM `. `WITH …` CTEs are not caught today and stay out of scope.
  Effort: S. Module: `workshop/gopernicus`. Not breaking: strictly fewer findings; hosts on an older pin
  stay red for the false positive only.

### 3e. Invitation decline by token alone

**Today:** `Decline(ctx, id, token)` (`service.go:680-706`) needs the id, compares the token hash, and has
**no expiry check** (an expired pending row declines). The route is `POST /auth/invitations/{id}/decline`
(`invitation.go:176`, origin-only gate + the `invitation_decline` IP limiter built once at `routes.go:535`).
The emailed link carries only `#token=`; `Accept` resolves by token through `GetByTokenHash`
(`service.go:589`; contract `repository.go:71-73`: unknown → `ErrNotFound`, unclaimed past expiry →
`ErrExpired`; backed by the unique index in `0009`). `InvitationService` (`invitation.go:24-34`) is a port
hosts fake in tests.

- Options: (a) `Service.DeclineByToken(ctx, token)` via `GetByTokenHash`, sharing a private
  `declinePending` with `Decline`; new public route `POST /auth/invitations/decline` `{token}` (sibling of
  `/auth/invitations/accept`), mounted only when the service satisfies an optional `invitationTokenDecliner`
  interface by type assertion (precedent `logic/queue/fenced_runtime.go:190`); the id route stays; (b) make
  the id optional on the existing route — changes an existing contract's shape.
- **Recommendation: (a).** Semantics: unknown hash → `invitationNotFound()`; `ErrExpired` surfaces (the
  caller holds the invitee's own high-entropy token; `Accept` does the same); not pending → `ErrNotPending`
  (a second click is "already done"); an `accepting` claim is never changed. By-id `Decline` keeps its
  current no-expiry behaviour in this minor; the difference is documented (R-8). Effort: S. Module:
  `pockets/authentication` (minor). Not breaking. Safe to document in a public repo: a one-click decline
  on a secret in a POST body is a standard pattern; the token never appears in logs, security-event details
  or error strings, and the route is POST-only so mail scanners and prefetchers cannot trigger it.

## Schema / datastore impact

None. No canonical migration file changes; no EAV impact; no new columns (`invited_by` and
`security_events.user_id` already hold what PR-3 writes).

## Module / API impact

| PR | Module | Tag after merge | Public surface |
|---|---|---|---|
| PR-0 (tag only) | `workshop/gopernicus` | `v0.10.0` at `bf1d0d74` | the owed #249 tag; changes no rule set |
| PR-1 | `workshop/gopernicus` | `v0.10.1` | H10 evaluator; scaffold README self-pin `@v0.10.1`; README prose |
| PR-2 | `pockets/jobs/stores/pgx`, `pockets/jobs/stores/turso` | `v0.9.0`, `v0.8.0` | `Repositories` fills `FencedQueue`; turso `StatusCheck` |
| PR-3 | `pockets/authentication` | `v0.34.0` | `PreparedCreate.WithActor`; `Service.DeclineByToken`; route `POST /auth/invitations/decline` |
| PR-4 | docs | none | guide sections, first-admin guide, JSON-sessions recipe |
| PR-5 | record, examples, scaffold pins | `workshop/gopernicus/v0.10.2` only if the scaffold's sdk pin moves (PR-O1) | — |
| PR-O1 (optional) | `sdk` | `v0.22.0` | `web.Route`, `(*WebHandler).Routes()` |

`pockets` (v0.1.0) is untouched. The authentication store modules do not change and need no repin (no
storetest additions; no DDL). PR-3 must **not** require sdk v0.22.0 (do not use `Routes()` in its tests), so
the authentication tag never waits on PR-O1. pgxdb is untouched in this train (stays v0.17.0). All tags
are MINOR/PATCH and additive, annotated, unsigned, cut by Claude at the squash commit after the owner
merges and after a green `live-stores` dispatch on `main` covers the commit (one run may cover several),
then cold-verified from a scratch consumer (`GOWORK=off`, empty module cache, proxy + sumdb). Workshop
tags use a cold `go run …@<tag> init` instead of a dispatch (the v0.10.0 row's precedent).

## Generated-artifact impact

None. No `.templ` changes; the goth views and HTML session pages are untouched.

## Risks

1. **PR-3 attribution.** Touching `recordGrant`'s `UserID` would turn the invitee row into an inviter row;
   the gate caught it in the sketch. The task text below is explicit: grant rows gain `Actor` only.
2. **PR-2 changes a default.** A turso host on `Repositories` that skipped `0003` used to fail fast at
   `NewFencedRuntime` (`ErrFencedQueueRequired`) and would now fail at first enqueue without the new
   `StatusCheck`; the task adds it. Boot-line flip recorded.
3. **PR-1 loosens a guard.** Every example host must still pass; the corpus test keeps the existing
   `"SELECT 1"` / `"DELETE FROM items"` verdicts (`guard_test.go:235,241`).

## Tasks (one PR each; the unblock set first)

Standing rules for every PR: one worktree at `../gopernicus-<slug>-<n>` branched from `origin/main`;
`make guard && make check` before push; never `go work sync`; never ports 8082/55432; nothing
exploit-shaped in a public repo; vars/consts first, then interfaces; accept interfaces, return structs;
**each PR updates its own pocket page / store README / upgrade-note draft** (PR-5 only assembles).

### PR-0 — cut the owed `workshop/gopernicus/v0.10.0`

- **depends_on:** []
- **model:** sonnet (mechanical)
- **files:** none (tag only, at `bf1d0d74`, the #249 squash commit RELEASING already names)
- **verify:** cold `GOWORK=off go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.10.0 init
  --module example.com/x --db pgx` in a scratch dir builds; `RELEASING.md`'s v0.10.0 row gets its commit.
- **description:** Cut the announced tag as announced ("changes no rule set, so hosts pinning the guard
  need not bump") so PR-1 can ship H10 as a patch instead of making that sentence false.

### PR-1 — guard H10: SQL shape after the verb, and message-argument skipping

- **depends_on:** [PR-0]
- **model:** opus
- **worktree:** `../gopernicus-h10-shape-1`
- **files:**
  - `workshop/gopernicus/internal/commands/guard_rules.go` (`sqlVerbs` → a verb→shape table at the top with
    the other vars; `isSQLLiteral(lit)` → `isSQLLiteral(lit)` + an AST parent check in `evalH10` that skips the
    message/format argument of `fmt.Errorf`, `errors.New`, `fmt.Sprintf`, `log.*`, `slog.*`)
  - `workshop/gopernicus/internal/commands/guard_test.go` (corpus table test; existing verdicts at 235 and
    241 preserved) and the guard-host fixtures under `testdata/` if present (one `cmd/` file carrying the
    battleboard literal and a bare `"DELETE"` method literal)
  - `workshop/gopernicus/internal/commands/templates/init/readme.md.tmpl:153` (`@v0.10.0` → `@v0.10.1`,
    the #219 self-pin precedent)
  - `examples/README.md:590` (the floor's prose; the H10 sentence at `:149` is unchanged);
    `workshop/documentation/docs/architecture/host-contract.md` only if it restates the prose (its `:43`
    is the sentence, unchanged)
- **tests (corpus):** flagged — `SELECT 1`, `SELECT * FROM t`, `SELECT $1::text`, `SELECT (1)`,
  `SELECT set_config($1,$2,true)`, `select count(*) from x`, `INSERT INTO t`, `DELETE FROM t WHERE`,
  `UPDATE t SET`, `CREATE TABLE`, `CREATE UNIQUE INDEX`, `CREATE OR REPLACE FUNCTION`, `CREATE POLICY`,
  `ALTER TABLE`, `ALTER DEFAULT PRIVILEGES`, `DROP SCHEMA`, multi-line and raw-string literals with leading
  whitespace, and a SQL literal assigned to a `const` or passed to `db.Exec`; not flagged — `create
  invitation: %w`, `select the organization`, `update failed`, `delete pending`, `Insert the key`, `alter
  ego`, `Drop it`, `SELECTED`, `Create`, `DELETE` (bare), `UPDATE profile SETTINGS`, and `delete from
  cache: %w` / `insert into map` / `select one from the list` **as `fmt.Errorf` arguments** (shape alone
  would flag them; the parent check must not).
- **verify:** `cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...`; `make guard &&
  make check` (every example passes H10 through `guard-host-contract`; `guard --list` output unchanged).
- **description:** Replace verb-plus-whitespace with verb-plus-shape on whitespace-normalised tokens, skip
  message arguments by AST parent, keep the normative sentence identical, and make the README prose
  describe the new rule exactly.
- **release:** `workshop/gopernicus/v0.10.1` (patch; rule set loosened only). Cold `init` + `guard
  --posture` on `examples/production-baseline` and, read-only, on battleboardhq (expect no H10 line).
- **host upgrade notes:** battleboard bumps `GOPERNICUS` to `@v0.10.1` (it is on `@v0.9.1`) and may
  restore the original `fmt.Errorf("create invitation: %w")` wording. segovia: bump when it next touches
  the guard pin.

### PR-2 — jobs stores: `Repositories` fills `FencedQueue`; turso gains `StatusCheck`

- **depends_on:** []
- **model:** opus
- **worktree:** `../gopernicus-jobs-fenced-default-2`
- **files:**
  - `pockets/jobs/stores/pgx/postgres.go:134-139` (`FencedQueue: NewFencedQueueStore(db, opts...)` — the
    schema option must reach the fenced store; doc comment names the new field)
  - `pockets/jobs/stores/turso/turso.go:29-34` (`FencedQueue: NewFencedQueueStore(db)`; new
    `StatusCheck(ctx, db)` probing the store's tables exactly as pgx's does, with the same "apply the jobs
    migration source before boot" message)
  - `pockets/jobs/stores/pgx/postgres_test.go`, `pockets/jobs/stores/turso/turso_test.go` (hermetic:
    `Repositories` returns a non-nil `FencedQueue` of the concrete type; pgx: with `WithSchema` the fenced
    store's `table()` is schema-qualified; turso: `StatusCheck` against the in-package localfile driver)
  - `pockets/jobs/stores/pgx/README.md`, `pockets/jobs/stores/turso/README.md`,
    `workshop/documentation/docs/pockets/jobs.md` ("Repositories fills all three ports; a host that wants
    only the ordinary queue builds `NewQueueStore` itself"; the boot-line flip)
- **verify:** `go build ./... && go test ./... && go vet ./...` in both store modules; `make guard && make
  check`; local `make test-stores` against PG18 on a non-55432 port **and** the turso fenced leg against
  libsql locally (no turso CI leg — record the local run in the PR).
- **description:** Fill `FencedQueue` in both drivers' `Repositories` so `jobs.New(jobspgx.Repositories(db))`
  boots with the fenced queue authentication's jobs-mode delivery needs, and give turso the boot gate pgx
  already has.
- **release:** `pockets/jobs/stores/pgx/v0.9.0`, `pockets/jobs/stores/turso/v0.8.0`, after a green
  `live-stores` dispatch on `main` (pgx leg). Core `pockets/jobs` unchanged (v0.8.0).
- **host upgrade notes:** battleboard may replace the body of `pockets/jobs/outbound/jobs.go` with
  `jobspgx.Repositories(db, jobspgx.WithSchema(Schema))` (keep the package: it pins the schema name beside
  the ledger). A host on `Repositories` whose ledger lacks `0003` now fails at boot through `StatusCheck`
  on both drivers. segovia (jobs pgx v0.6.0): none until its repin.

### PR-3 — invitations: `PreparedCreate.WithActor` and decline by token

- **depends_on:** []
- **model:** opus
- **worktree:** `../gopernicus-invitations-actor-decline-3`
- **files:**
  - `pockets/authentication/logic/invitations/service.go` (`PreparedCreate` carries `actor`;
    `func (p PreparedCreate) WithActor(sdk.Principal) PreparedCreate`; `grantProvenance` carries the actor;
    `recordCreated(ctx, inv, actor)` applies the attribution rule; `recordGrant` adds `Actor` and keeps
    `UserID = subjectID` on success and failure; `DeclineByToken`; `declinePending` extracted from `Decline`)
  - `pockets/authentication/logic/invitations/management.go` (lift `attribution()`'s rule into a
    package-level helper both prepared types call — declared after the vars/consts and interfaces)
  - `pockets/authentication/logic/invitations/service_test.go`, `management_test.go`, `lifecycle_test.go`,
    `audit_digest_test.go` as applicable (cases below); **no `storetest` additions**
  - `pockets/authentication/inbound/http/invitation.go` (`createInvitation` calls
    `prepared.WithActor(sdk.Principal{Type: sdk.PrincipalTypeUser, ID: invitedBy})` before `CreatePrepared`,
    as resend does at `:419`; `invitationTokenDecliner` optional interface (declared with the file's other
    interfaces); `mountInvitations` registers `POST /auth/invitations/decline` only when the assertion
    holds, with `requireBrowserSafeOrigin(h.mutation.csrf())` and the **same** `declineLimit` instance;
    `declineInvitationByToken` reusing `declineInvitationRequest`)
  - `pockets/authentication/inbound/http/invitation_test.go` (route tests), `routes_test.go` if it
    enumerates routes
  - `pockets/authentication/README.md`, `workshop/documentation/docs/pockets/authentication.md` (route
    table; the `WithActor` step; the response matrix), `SECURITY.md` only if it enumerates public routes
- **tests:**
  - zero actor → `invitation_created` byte-identical to today (`UserID = InvitedBy`, empty `Actor`);
  - user actor → `Actor = {user, id}`, `UserID = id`; system actor `{system, bootstrap}` → `Actor` set,
    `UserID = ""`, `Details["invited_by"]` present;
  - direct-add (`AutoAccept`, known invitee): `invitation_granted` success and failure rows carry `Actor`
    and still `UserID = subjectID`; accept-time grant rows stay actor-less;
  - `DeclineByToken`: pending → declined + `invitation_declined`; unknown → `ErrNotFound`; expired
    unclaimed → `ErrExpired`; accepted/declined → `ErrNotPending`; `accepting` claim → `ErrNotPending`
    with no state change; the token and its hash appear in no log line or event detail;
  - HTTP response matrix for `POST /auth/invitations/decline`: success → 200 `{"status":"declined"}`;
    unknown and wrong token → identical not-found body; expired → the `ErrExpired` mapping; already
    done → the `ErrNotPending` mapping (409 class) so a second click reads as done; cross-origin →
    refused as the id route refuses; missing token → the id route's 400 shape; both routes share the
    `invitation_decline` budget; GET → 405; no collision with `{resource_type}/{resource_id}` or `accept`.
- **verify:** `cd pockets/authentication && go build ./... && go test ./... && go vet ./...`; `make guard
  && make check`; run-and-look: `make run` for `examples/auth-cms` (its README port; never 8082 if the
  owner's instance is up), create an invitation from the admin UI, confirm the `invitation_created` event
  shows the user actor, then `curl -X POST …/auth/invitations/decline -H 'Origin: <allowed>' -d
  '{"token":"…"}'` with the console-mailed token and confirm `{"status":"declined"}` plus the
  `invitation_declined` event; a second POST answers the already-done mapping.
- **description:** Add actor attribution to the create flow the way management already has it, without
  touching who the grant rows are about, and a token-only decline beside the id route so an SPA landing page
  holding only `#token=` can offer Decline.
- **release:** `pockets/authentication/v0.34.0` (minor, additive; no store repins; views/goth untouched).
  `live-stores` optional for a core-only tag; dispatch it anyway (free).
- **host upgrade notes:** battleboard `cmd/bootstrap`: `PrepareCreate` → `WithActor(sdk.Principal{Type:
  "system", ID: "bootstrap"})` → `CreatePrepared`; drop the `inviter` caveat comment; add a Decline button
  to `/invitations/accept` calling the new route; add `POST /auth/invitations/decline` to `matrix_test.go`
  as `public` (the matrix fails the build until it does). Hosts that fake `InvitationService`: nothing
  changes. segovia: none until repin.

### PR-4 — the guide: every remaining tenancy answer, the first-admin guide, the sessions recipe

- **depends_on:** [PR-3 merged] (the first-admin guide uses `WithActor`)
- **model:** opus
- **worktree:** `../gopernicus-rls-guide-4`
- **files:**
  - `workshop/documentation/docs/guides/row-level-security.md` — new sections:
    - **Catalog posture** — the catalog query (tables in the schema with/without the column, `relrowsecurity`,
      `relforcerowsecurity`, PK-leads-with-column), the per-command policy check (one permissive PUBLIC policy
      per command or one `ALL`; `UPDATE` both clauses; the predicate as `pg_get_expr` prints it on PG18 —
      battleboard's `tenantPredicate` is the reference; the `app.scope` branch example from "Cross-tenant
      access" shown as a second accepted predicate), the role-attribute query (SUPERUSER/BYPASSRLS/
      CREATEROLE/CREATEDB/REPLICATION; memberships listed and judged by the host — managed providers add
      some); the **exempt list** ("exempt whether or not it has the column": `schema_migrations`,
      `ratelimit_windows`, the jobs and other pocket tables if they share the schema — or put pockets in
      their own schemas); output names objects only, never setting values or rows; run as the runtime role,
      never as a superuser (its view proves nothing); where the check lives (a host `workshop/` test, run by
      the host's `make check`); "a pgxdb probe is planned once a second host has its own copy to diff".
    - **Roles: the owner/app split and a read-only preflight** — the SQL shape (create roles by hand,
      passwords outside files; `BEGIN READ ONLY … ROLLBACK`; the app role owns nothing and holds exactly the
      matrix); cross-link from `database-posture.md` "Two roles". The matrix itself is the host's.
    - **Adding policies to your own tables** — a host migration file after the exported canonical files;
      P9 permits it; never on pocket tables.
    - **Pocket tables and the jobs `tenant_id`** — a label, not a wall; the worker is cross-tenant; the
      stores' `InTx`; **the outbox caveat**: an enqueue inside a held `Transact`/`TransactWithSettings` runs on
      a second connection — it can run before the domain commit, survives a rollback, and can starve a tight
      pool; the same holds for the authentication stores; keep enqueues after commit; the atomic-outbox gap is
      an open framework item.
    - **An endpoint matrix** — the recording-wrapper shape (embed `*web.WebHandler`, record `Handle`;
      `Routes()` if PR-O1 ships), classes are the host's, cross-tenant probes with another tenant's real id and
      a missing id must answer byte-identically and never 2xx, every new route needs an entry, decline is
      POST-only.
    - **Scaffolding** — what `init` emits and does not; the anatomy slots an RLS host uses (above);
      `--tenancy=rls` is deferred and why.
  - `workshop/documentation/docs/guides/first-admin.md` (new) — the grant shape (production-baseline's
    `cmd/bootstrap`) and the invitation shape: a host function (composition or host-pocket `logic`) that
    pages `ListActiveByResource` for a pending invitation to the address, resends it with a system actor, or
    prepares, `WithActor`s and creates one; `cmd/bootstrap` parses `-email` and calls it (H10: cmd wires);
    the link travels through the notifier, **never stdout or CI logs** (a printed-once fallback is
    development-only and says so); rerunning converges.
  - `workshop/documentation/docs/pockets/authentication.md` — "JSON session inventory over the service"
    recipe (`ListUserSessions`, `RevokeUserSession`, `RevokeAllUserSessions`; `FirstParty()` filtering; the
    first-party live authenticator + browser-safe mutation gate; clear cookies on revoke-current; identical
    not-found for another user's id) with a pointer to the deferred pocket arm.
  - `workshop/documentation/docs/guides/database-posture.md`, `persistence.md` (cross-links);
    `examples/production-baseline/README.md` (link from "First admin").
- **verify:** `make docs-build`; `make check` (docs-site build is part of it if the Makefile says so);
  every SQL snippet executed once by hand against a PG18 on a non-55432 port as a plain role (record the
  run in the PR — these are the snippets two hosts will copy).
- **description:** Finish the RLS guide so each tenancy sub-ask has a written, copyable answer; add the
  first-admin guide with the converge logic placed as app logic; add the JSON sessions recipe.
- **release:** none.
- **host upgrade notes:** battleboard: compare its `tenancytest/` with the guide's SQL (expect identity
  plus the exempt-list wording); move `findPending`/converge out of `cmd/bootstrap` into
  `pockets/auth/logic` when convenient (its own debt list). segovia: the inputs for its RLS plan.

### PR-5 — record and repins

- **depends_on:** [PR-1, PR-2, PR-3 tagged; PR-4 merged; PR-O1 tagged if chosen]
- **model:** opus
- **worktree:** `../gopernicus-multi-tenant-record-5`
- **files:**
  - `RELEASING.md` (one section in the current style: tags table with commit / PR / `live-stores` run /
    cold verification; "What changed"; "By host shape" rows: **battleboardhq** — from pgxdb v0.14.0 /
    authentication v0.31.0 / jobs v0.7.0 / guard @v0.9.1 through the PG18 + recreate path its `HANDOFF.md`
    already plans, then these tags; **segovia/v2** — defer until its repin milestone; **gps-360-go** and
    turso hosts — jobs/turso note only; a cold-verified MVS set "host on these tags with authentication
    stores at pgx v0.17.0 / turso v0.16.0 and pgxdb v0.17.0")
  - `examples/auth-cms/go.mod`, `examples/production-baseline/go.mod`, `examples/jobs-minimal/go.mod`,
    `examples/external-identity/go.mod` (repin to the new authentication / jobs-store tags, and sdk if
    PR-O1; `go mod tidy` per example; never `go work sync`)
  - `workshop/gopernicus/internal/commands/templates/init/go.mod.tmpl` (sdk pin, only if PR-O1) → then
    `workshop/gopernicus/v0.10.2` and the README self-pin again
  - `AUDIT.md` (next AUDIT entry, per convention)
  - `.claude/plans/multi-tenant-host-asks.md` → executed copy under `plans/`
- **verify:** `make guard && make check`; `make docs-build`; `gopernicus guard --posture` on
  `examples/production-baseline` with the newest CLI tag.
- **description:** Write the one record for the train and repin the examples and the scaffold.
- **release:** none beyond the conditional workshop patch.

### PR-O1 (optional, ruling R-4) — sdk: `web.WebHandler.Routes()`

- **depends_on:** []
- **model:** opus
- **worktree:** `../gopernicus-web-routes-o1`
- **files:** `sdk/pkg/web/handler.go` (`type Route struct{ Method, Pattern string; Raw bool }` declared
  before `WebHandler`; a `routes []Route` field; `Handle` appends `{upper(method), path, false}` — an
  empty method records `""`; `HandleRaw` appends `{"", pattern, true}`; `Routes()` returns a copy in
  registration order), `sdk/pkg/web/handler_test.go` (order, copy, raw, empty-method, `Group` prefixing
  recorded as the final pattern), `sdk/pkg/web/groups.go` only if `RouteGroup` bypasses `Handle`,
  `sdk/README.md` / docs sdk page (boot-time read-only like `Use`; handlers behind a `HandleRaw` subtree
  are invisible), the guide's matrix section (cite it).
- **verify:** `cd sdk && go build ./... && go test ./... && go vet ./...`; `make guard && make check`
  (G12: `pkg/web` imports root + stdlib only — unchanged); `pockets/mount_test.go`'s
  `var _ RouteRegistrar = (*web.WebHandler)(nil)` still compiles (no interface change; no `RouteLister`).
- **description:** Record every registration path on `WebHandler` and expose `{Method, Pattern, Raw}`
  read-only — never handlers or middleware — so a host inventory test lists what the server mounted.
- **release:** `sdk/v0.22.0`; no module repins in this train (PR-3 must not require it); examples and the
  scaffold pin in PR-5.
- **host upgrade notes:** battleboard: `recordingRouter` → `router.Routes()`, the completeness check
  becomes a set comparison. segovia: `cmd/server/tenant_routes_test.go` can do the same after repin.

## Sequencing

PR-0 → PR-1; PR-2, PR-3 and PR-O1 are independent of each other and of PR-1 and can run in parallel
worktrees; PR-4 after PR-3 merges; PR-5 last. One release train, one record: battleboard upgrades once.
The **battleboard unblock set** is PR-1 + PR-2 + PR-3 + PR-4's guide; its upgrade drive with Decline
working is the acceptance gate.

## Release plan

| Order | Tag | Needs `live-stores` | Unblocks |
|---|---|---|---|
| 0 | `workshop/gopernicus/v0.10.0` @ `bf1d0d74` | no (cold `init`) | the owed #249 scaffold tag |
| 1 | `workshop/gopernicus/v0.10.1` | no (cold `init` + `guard`) | battleboard's guard line |
| 2 | `pockets/jobs/stores/pgx/v0.9.0`, `pockets/jobs/stores/turso/v0.8.0` | yes (pgx leg; turso proven locally) | — |
| 3 | `pockets/authentication/v0.34.0` | optional | bootstrap actor, Decline button |
| O1 | `sdk/v0.22.0` (if chosen) | no | battleboard matrix |
| 5 | record; `workshop/gopernicus/v0.10.2` only if the scaffold's sdk pin moves | — | — |

Auto mode may deny the tag push; then the owner gets ready-to-run `git tag -a … && git push origin <tag>`
lines, one per tag.

## Open rulings — YOUR CALL

- **R-1 — Posture.** (A) docs only; **(B-staged) guide + copyable SQL now, the pgxdb probe and
  `init --tenancy=rls` in a follow-up once segovia has its own copy (recommended)**; (B-now) ship the pgxdb
  probe in this train under the appendix's constraints (evidence + caller-supplied predicate, no defaults,
  memberships reported not refused); (C) a tenant concept (rejected).
- **R-2 — Scaffolding.** (a) **defer `init --tenancy=rls` (recommended)**; (b) a smaller, tenant-free step
  now: `init --db pgx` also emits `workshop/dbroles/{roles.sql,preflight.sql}` for the owner/app split every
  production pgx host needs (half a day in `workshop/gopernicus`; the privilege matrix stays a host edit) —
  would make PR-4's "Roles" section executable; (c) build the full flavour now from one host.
- **R-3 — Stores under the tenant scope / RLS-aware ledgers.** (a) **won't-do for RLS, documented
  (recommended)**: pocket tables stay global; no `ExportPolicies`; jobs `tenant_id` is a label. The
  atomic-outbox gap (stores joining a host transaction for atomicity) is recorded as a separate open item,
  not part of this ruling. (b) make the jobs/authentication stores honour `QuerierFrom` now (does not help
  RLS; changes claim/complete semantics; its own plan).
- **R-4 — `Routes()`.** (a) drop it; the guide documents the recording wrapper; (b) **ship PR-O1 in this
  train (planner's lean: admissible, twenty lines, the owner has several hosts with route tests; the PM
  would cut it as a permanent API with one consumer)**. No preference strong enough to call it a
  recommendation — your call.
- **R-5 — JSON session arm.** (a) **defer to its own plan; ship the recipe in PR-4 (recommended)**;
  (b) ship it in this train with the constraints under 3c — includes the breaking fold of
  `OAuth2Config.Sessions` into one option (v0 minor, but a pocket-consumer break) and the dual-transport
  exception for `/auth/me/sessions*`.
- **R-6 — Bootstrap helper.** (a) **guide only (recommended)**; (b) also `invitations.Service.EnsurePending`
  in PR-3 so a host bootstrap is a flag parse plus one call.
- **R-7 — H10 fix shape.** (a) shape only; (b) message-argument skip only; (c) **both (recommended)**.
- **R-8 — Decline expiry parity.** (a) **`DeclineByToken` surfaces `ErrExpired`; by-id `Decline` keeps
  declining expired pending rows in this minor; documented (recommended)**; (b) `declinePending` refuses
  expired on both paths (a behaviour change to the id route, noted in the record).
- **R-9 — Workshop tags.** (a) fold H10 into `v0.10.0`; **(b) cut `v0.10.0` at `bf1d0d74` as announced,
  ship H10 as `v0.10.1` (recommended — RELEASING already says v0.10.0 "changes no rule set")**.

## Decided by gate

Folded from four reviews. Each item names the reviewer; where reviewers disagreed, the planner's call is
stated.

**Structure and scope**

- PM H1 / backend #8 / steward MED-1 — the pgxdb probe encodes battleboard's policy shape (exact predicate,
  one policy per command, PK-leads) and would reject the guide's own `app.scope` branch and a `uuid` column;
  pgxdb precedent is evidence + caller-supplied canonical (`AppendOnlyTable.Canonical`). **Call:** probe
  deferred (B-staged); its design (appendix) takes `Predicate(s)` from the caller, has no zero-value
  defaults, reports `pg_policy` evidence with judgement methods, treats PK-leads as evidence only, and
  refuses on ENABLE/FORCE/predicate only.
- Backend #10 / SRE HIGH — "zero memberships" as a hard refusal bricks boot on managed Postgres
  (platform roles). **Call:** refuse on SUPERUSER/BYPASSRLS/CREATEROLE/CREATEDB/REPLICATION and on
  transitive membership (NOINHERIT included) in a bypass role or an owner of a probed table (reuse
  `probe.go:216`'s owner-membership logic); report other memberships; in the guide, the host judges.
- Backend #9 — `Global` must mean "exempt whether or not it has the column" (jobs tables carry
  `tenant_id`; the ledger and limiter tables need listing). Folded into the guide section and the appendix.
- PM M1 / backend #5–#7 / steward MED-2 — the JSON session arm conflicts with the dual-transport rule, a
  second `SessionManagementService` injection point violates the constructor-options convention, the
  session-management repository can be nil (always-403 routes), bearer policy and byte-identical not-found
  were unspecified, and the one known consumer has working routes on its own paths. **Call:** deferred
  with the constraints recorded (3c); recipe ships in PR-4; the owner's React hosts keep it on the list.
- PM L1 / backend #17 / steward Q2 — `Routes()` is admissible but a convenience with one consumer.
  **Call:** optional PR-O1 under R-4; if shipped: all three registration paths, `{Method, Pattern, Raw}`,
  upper-cased method, a copy, boot-time only, `HandleRaw` subtrees documented as invisible; never a
  `RouteLister` on `pockets.RouteRegistrar` (steward MED-3).
- PM H3 / SRE — no release-train decision. **Call:** one train, one record; the battleboard unblock set is
  PR-1 + PR-2 + PR-3 + the guide; the acceptance gate is battleboard's upgrade drive with Decline working.
- PM H2 — docs lumped into the record PR. **Call:** each PR carries its own pocket page / README / upgrade
  note; PR-5 assembles.
- PM M4 — "stores joining the tenant scope" was buried in a guide line. **Call:** R-3 is now an explicit
  won't-do ruling; the atomicity gap (backend #12) is split out as an open item.
- Steward framing — "tenancy as a configuration option" names a knob the architecture has no slot for.
  **Call:** the decision is a posture: generic primitives + guide; the tenant model is host domain.
- Steward Q3 — `init --tenancy=rls` is blocked on `init` growing pocket mounting and on H8's named domain,
  not on segovia alone; the anatomy slots are recorded in the sub-ask text.

**PR-3 (invitations)**

- Backend #1 — the sketch would have overwritten the invitee on the direct-add granted row. **Fixed:**
  grant rows gain `Actor` only, `UserID = subjectID`; only `recordCreated` applies `attribution()`.
- Backend #2 — actor on `CreateInput` leaks into `InviteCheckRequest` via `PreparedCreate.Input()`.
  **Fixed:** `PreparedCreate.WithActor`, mirroring management; headless `Create` keeps the fallback.
- Backend #3 — adding `DeclineByToken` to `InvitationService` breaks host fakes. **Fixed:** optional
  interface by type assertion; route mounted only when it holds.
- Backend #14 / SRE LOW — `ErrExpired` surfaces (as `Accept`); by-id `Decline` has no expiry check today —
  ruling R-8. Backend #15 / SRE — the new route reuses the one `declineLimit` instance and the
  `invitation_decline` budget. SRE — POST-only, token never in logs or details, double-click reads as done;
  no storetest additions so no store repins; `invited_by` stays the host's string. PM M2 — the response
  matrix is now specified. Backend #16 — battleboard's matrix fails the build until the route is added;
  `user_id = ""` on non-user-actor created rows changes what `user_id = invited_by` queries see. L3 — the
  first-admin guide uses `WithActor`.

**PR-2 (jobs stores)**

- Backend #13 / SRE — turso has no `StatusCheck`; a turso host that skipped `0003` would regress from
  fail-fast at `NewFencedRuntime` to fail-at-first-enqueue. **Fixed:** PR-2 adds a turso `StatusCheck`.
- SRE — pgx must pass `opts...` (schema) to the fenced store; turso's fenced store takes no options; the
  boot-line flip is a recorded visible change. Backend #13 — no in-repo host breaks; `WithLease` is
  accepted and ignored by the fenced store.

**PR-1 (guard)**

- Backend #11 — shape rules alone still flag `"delete from cache: %w"`. **Fixed:** shape + AST parent
  skip (R-7 (c)); existing `"SELECT 1"` / `"DELETE FROM items"` verdicts preserved.
- SRE HIGH — the keyword list was unspecified; `SELECT $1` / `SELECT (` must match; token-based matching
  (`UPDATE profile SETTINGS`). **Fixed:** enumerated, including `DEFAULT` for `ALTER DEFAULT PRIVILEGES`.
- Steward LOW-4 — `rest == ""` flags a bare `"DELETE"` method literal today; the shape rule removes that;
  `sentenceH10` and `guard --list` stay identical, only the prose at `examples/README.md:590` changes.
- SRE HIGH — `workshop/gopernicus/v0.10.0` is owed at `bf1d0d74` and RELEASING says it changes no rule
  set. **Fixed:** PR-0 cuts it; H10 ships as `v0.10.1`; the scaffold README self-pin bumps in PR-1
  (`readme.md.tmpl:153`). Backend #18 noted v0.10.0 does not exist yet; the SRE's reasoning wins.
- SRE — `WITH …` CTEs are a known gap, not widened here; hosts on older pins stay red only for the false
  positive.

**PR-4 / PR-5 (docs, record)**

- SRE HIGH — a bootstrap that prints the token to stdout leaks it into CI logs. **Fixed:** the guide routes
  the link through the notifier; printed-once is development-only and labelled.
- Backend #4 — battleboard's `cmd/bootstrap` carries converge behaviour in `cmd/`. **Fixed:** the guide
  places it as a host composition/host-pocket function; `cmd` wires.
- SRE — battleboard's RELEASING row must name the pgxdb v0.14.0 → PG18 recreate path it already plans;
  cold-verify an MVS set with authentication stores at pgx v0.17.0 / turso v0.16.0.
- SRE — if any authentication PR used `Routes()`, sdk would have to tag first. **Fixed:** PR-3 must not
  require sdk v0.22.0.
- SRE Q6 / steward — no new grep guard; a future static P-rule tying `TransactWithSettings(` to a probe
  call is a candidate once the probe exists; behavioural tests (every `/auth/*` route no-store, every public
  POST cross-origin-refused and limited) are noted for the follow-up plans.

**Dissent recorded.** The PM would also cut PR-O1 and rank the train purely by what changes for an app
user (Decline is the only such item). The steward and SRE found all three APIs correctly placed. The
planner's call is above: probe and session arm deferred, `Routes()` the owner's call.

## Appendix — the deferred pgxdb probe (design ceiling for the follow-up plan)

Beside `probe.go`, mirroring the append-only pair (`InspectTablePrivileges` as the migrator-side preflight;
a `(*DB).Probe…` for the server's own role under the mode semantics `ProbeAppendOnly` uses: refuse in
production, WARN in development, no query in zero mode; a waiver flag decided then):

```go
// RowSecurityTable names one table a transaction-local setting fences and the
// predicate text its policies must carry, exactly as pg_get_expr prints it.
// Nothing is defaulted: an empty Column or Predicates refuses with sdk.ErrInvalidInput.
type RowSecurityTable struct {
    Schema     Schema
    Column     string   // the fencing column, for example "tenant_id"
    Predicates []string // accepted USING/WITH CHECK texts (pg_get_expr form); one is the common case
    Exempt     []string // tables in Schema the check skips, whether or not they carry Column
}

// RowSecurityPolicy is one pg_policy row as the catalog prints it (names, never values).
type RowSecurityPolicy struct{ Name, Command string; Permissive bool; Roles []string; UsingMatches, CheckMatches bool }

// RowSecurityEvidence is one table: flags and policies, judged by methods, never by hidden refusal.
type RowSecurityEvidence struct {
    Table      string
    HasColumn  bool
    Exempt     bool
    Enabled    bool
    Forced     bool
    KeyedFirst bool // evidence only; never a refusal
    Policies   []RowSecurityPolicy
}
func (e RowSecurityEvidence) Violations() []string // not enabled / not forced / <command>: missing, extra permissive, non-PUBLIC, predicate mismatch (policy name, never the text)

type RowSecurityReport struct{ Role string; Tables []RowSecurityEvidence }
func InspectRowSecurity(ctx context.Context, q Querier, scope RowSecurityTable) (RowSecurityReport, error)
func (d *DB) ProbeRowSecurity(ctx context.Context, scope RowSecurityTable) error

// RoleAttributes extends inspectRoleSQL (rolsuper today) with the other attributes
// and transitive memberships; Violations refuses only on the real hazards.
type RoleAttributes struct{ Name string; Superuser, BypassRLS, CreateRole, CreateDB, Replication bool; Memberships []string; BypassMembership, OwnerMembership []string }
func (a RoleAttributes) Violations() []string
func InspectRole(ctx context.Context, q Querier, role string) (RoleAttributes, error)
```

Constraints the gate set: catalog reads only (`pg_class`, `pg_policy`, `pg_attribute`, `pg_index`,
`pg_roles`, `pg_auth_members`; `SET LOCAL search_path TO pg_catalog` as `probe.go` does); never SELECT
from a scoped table; output names objects only — never setting values, row counts or raw predicate text;
the live test creates unique per-run roles (server-wide) with `t.Cleanup` and a DESTRUCTIVE comment;
a superuser running it is a finding under the mode semantics, not a refusal to run; the runtime call site
is the host's outbound constructor (`NewScope` / the scoped repository constructor), with `-posture-role`
in `workshop/migrations` as the migrator preflight — never a `RowSecurityTable` literal in `cmd/`; name
collision with battleboard's `tenancy.Scope` and authorization's tuple `scope` avoided by using
`RowSecurityTable`. Trigger: segovia's own catalog test exists and can be diffed with battleboard's.

## Consultation notes

Four reviewers received the same paragraph sketch (seven PRs; A/B/C; the won't-do and deferred items) with
role-specific questions. lead-backend-engineer: ratify-with-edits (five landmines, all folded).
architecture-steward: aligned-with-edits (every placement correct; B2 tenant-free in defaults, not only
names; `RouteRegistrar` untouched; one wiring for sessions; framing). product-manager: ship-with-edits,
close to a rescope (cut the three one-consumer APIs; one train; per-PR docs; Decline is the headline; the
host says nothing here blocks its next milestone). platform-sre: ship-with-edits (owed v0.10.0; H10
keyword corpus; turso boot gate; memberships; token never to stdout; live-stores per store tag).

## Recommended reviews

- product-manager (post-hoc on the ratified plan: the train and the host upgrade notes).
- platform-sre (PR-2 before tagging; PR-5's record).
- lead-backend-engineer (code review of PR-3 before the owner merges — security-sensitive pocket surface).
- data-integration-reviewer (PR-2: pgx/turso parity, the turso `StatusCheck`).
- architecture-steward (the follow-up plans: the probe's exported names; the session arm's single wiring).
- lead-frontend-engineer: not needed in this train (no views, no HTML surface changes).

## Notes

- Line numbers are from `bf1d0d74`; re-locate by symbol if `main` moves.
- battleboard's `NEXT.md` adopt list (87–94) and "Known debts" (107–114) are the host-side acceptance
  list; `HANDOFF.md:61-65` is the before picture and says "don't build around any of these".
- segovia/v2 is several minors behind (sdk v0.15.0, pgxdb v0.9.0, authentication v0.19.1, jobs v0.6.0) and
  has no session-list routes, endpoint matrix or catalog test today; its RLS move starts with a repin
  milestone in its own repo, where PR-4's guide is the input and its catalog test becomes the second data
  point that unlocks the probe follow-up.
- Follow-up plans this one names: `pgxdb-row-security-probe` (after segovia), `authentication-json-sessions`
  (R-5), `init-tenancy-scaffold` (R-2), and the atomic-outbox gap (jobs and authentication stores inside a
  host transaction).

## As built

Executed 2026-10-27 → 2026-10-28. Every PR passed `make guard && make check`; one `live-stores`
dispatch on `main` (40702865522 @ `0850deff`) covered the train, and 40629529349 @ `bf1d0d74`
covered PR-0.

| PR | Merged as | Tag(s) |
|---|---|---|
| PR-0 | (tag only) `bf1d0d74`, #249 | `workshop/gopernicus/v0.10.0` |
| PR-O1 | `57019402`, #250 | `sdk/v0.22.0` |
| PR-1 | `14b59468`, #251 | `workshop/gopernicus/v0.10.1` |
| PR-2 | `eaf1af26`, #252 | `pockets/jobs/stores/pgx/v0.9.0`, `pockets/jobs/stores/turso/v0.8.0` |
| PR-3 | `0850deff`, #253 | `pockets/authentication/v0.34.0` |
| PR-4 | `ae32b413`, #254 | none (docs) |
| PR-5 | the record PR | `workshop/gopernicus/v0.10.2` after merge |

Deviations from the task text:

- **PR-1:** `fmt.Sprintf` is not in the H10 message skip. The lead removed it: a `Sprintf` builds a
  value, and skipping it would let SQL built in `cmd` through. A fixture pins it. The skip covers
  package functions only; `*slog.Logger` / `*log.Logger` methods are still read (follow-up).
- **PR-2:** turso `StatusCheck` also WARNs on a missing `0005` retention index, matching pgx.
- **PR-3:** a decline that loses the race to an accept answers `ErrConflict` (409) with no declined
  row, not `ErrNotPending` (also 409). A declined or cancelled row past `ExpiresAt` answers 410
  (review MED-1; documented in the response matrix).
- **PR-5:** the conditional `workshop/gopernicus/v0.10.2` fires (PR-O1 moved the scaffold's sdk pin
  to v0.22.0, in `init` and `new pocket`). Every example was repinned, including the sdk-only ones.

Reviews: PR-3 lead-backend ship-with-edits (MED-1, LOW-1..4 folded); PR-1, PR-2 and PR-O1 lead
self-review. Cold-verified: the train set, the battleboardhq shape, a cold `init` from `@v0.10.1`
and from the PR-5 tree, and set C (auth-cms, production-baseline, jobs-minimal). `examples/auth-cms`
run against the tags: an invitation declined by token answered 200, a second decline 409.

Still open, each for its own plan: the JSON session arm (R-5), the pgxdb row-security probe (after
segovia's catalog test), `init --tenancy=rls` (R-2), the atomic outbox, and the H10 method-call
skip. No turso jobs README (owner rec: none). The acceptance gate — battleboardhq's upgrade drive
with Decline working — runs in that host's repository.
