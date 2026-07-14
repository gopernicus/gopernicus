# Phase 9 — upgrade, documentation, and milestone close

Status: READY after phase 8.
Depends on: phases 0–8 and their execution logs.
Design: §§2.5, 8–15.

## Goal

Validate the real host upgrade, synchronize every public contract/document, run
final adversarial/live gates, and leave a release-ready auth-v3 record including
the auth-v4 MFA handoff.

## Task AV3-9.1 — final canonical migration audit

Touch: both auth migration trees and inventory tests only if discrepancies are
found.

Verify:

- identical filename sets and clean numbering;
- final greenfield `users` has no identifier columns and has `auth_revision`;
- `user_identifiers`, challenges, password-reset support, contact changes,
  authentication grants, delivery jobs, session metadata, and invitation index
  match the shipped stores;
- no legacy verification table exists;
- partial indexes encode active authentication-claim and primary uniqueness;
- foreign-key/cascade behavior cannot orphan credentials/identifiers;
- every store query references an existing final column/index assumption.

Run fresh database creation on both dialects and full storetest.

## Task AV3-9.2 — publish and execute the host upgrade runbook

Depends on: AV3-9.1 and the phase-2 draft.
Touch: feature docs/upgrade reference and disposable upgrade fixtures.

Finalize exact pgx and SQLite/libSQL host-owned migrations with:

- backup, maintenance/cutover order, compatibility window, and rollback limits;
- preflight normalization/collision queries that abort instead of choosing;
- email backfill preserving verification state and use flags;
- row-count/orphan/uniqueness validation;
- application deployment order relative to column/table removal;
- SQLite rebuild details;
- session preservation expectations and reset of stale migration-checksum test
  databases; and
- post-upgrade smoke/rollback-forward commands.

Execute from a representative pre-v3 schema/data fixture to final v3 for both
dialects. Compare logical users, passwords, OAuth accounts, sessions,
invitations, and verification state before/after. Also run a deliberate collision
fixture and prove a safe abort with no partial migration.

Do not apply this runbook to segovia or another real host in this milestone.

## Task AV3-9.3 — public feature and sdk documentation

Depends on: AV3-9.1.
Touch: authentication README, sdk capability docs, example README/env, public Go
docs, route/config tables.

Document:

- all repositories, config nil/zero/error semantics, runtime modes, distinct
  secrets and rotation;
- identifier model/uses/normalization/shared notification addresses;
- routes, middleware tier, CSRF/native-client behavior, masked methods;
- JSON/form content-type dispatch, HTML GET/PRG route table, `Views` nil
  semantics, bundled `views/templ` wiring, `html/template` alternative, and
  embed-to-override examples;
- atomic challenges and why OTP HMAC pepper is local code, not a service;
- delivery worker lifecycle, at-least-once duplicates, encryption, retries,
  health, purge, and production metadata;
- password/recovery policy and reset session revocation;
- rate-limit/trusted-proxy requirements;
- security events, PII masking/retention/redress;
- migration/break inventory and removed routes/ports;
- console development-only posture; and
- auth-v4 handoff: typed authenticators for passkeys/WebAuthn, TOTP, recovery
  codes, AAL2, factor replacement/reset; v3 seams reserved, no claim that MFA
  ships now.

Every example must use distinct placeholder secrets and production-safe
comments.

## Task AV3-9.4 — release/change inventory

Depends on: AV3-9.2, AV3-9.3.
Touch: `RELEASING.md`, `NOTES.md`, any module changelog/version inventory used by
the repository.

Record:

- breaking public `Repositories`, `Config`, entity/route, and migration changes;
- feature, both nested store modules, and authentication templ-view module
  version/tag requirements;
- host upgrade order and production checklist;
- all live test artifacts with redacted endpoints;
- deliberate deferrals, especially auth-v4 MFA and real SMS;
- any implementation adaptation from the cut plan.

Do not create or push tags unless separately authorized by the release workflow.

## Task AV3-9.5 — final adversarial audit

Depends on: AV3-9.1 through AV3-9.4.

Run focused tests/review for:

- concurrent code/token redemption and attempt lockout;
- concurrent final-method removals and revision retry;
- reset atomic rollback and prior-session rejection;
- OAuth unverified email/adoption and wrong-provider binding;
- identifier replacement invalidating old links/codes;
- known/unknown timing shape with slow/failing provider;
- secret/PII grep across logs, audit fixtures, limiter keys, SQL columns, and docs;
- CSRF/origin/CORS/body limit/cache headers;
- hostile Host/open redirect/trusted proxy spoofing;
- console/missing-metadata/memory-limiter production rejection;
- delivery lease/retry/idempotency/terminal purge;
- old-key HMAC rotation/removal timing; and
- JSON-versus-form service-call parity, 415 behavior, HTML 303 PRG, default view
  accessibility/security headers, API-only graph, and partial view override.

Use `go test -race` for memory/worker/concurrency packages. Classify every grep
match; zero unexplained secret-bearing output.

## Task AV3-9.6 — implementation-complete hermetic and live gate

Depends on: AV3-9.5.

Run and record:

```sh
make check
make guard
make test-stores   # or current equivalent, with authorized pgx+turso DSNs
```

Then repeat the proof-host critical path on the final commit/tree through JSON
and HTML where applicable: register / verify / login, reset revocation,
identifier bind/change/remove, recent-auth credential mutation, email link,
phone OTP, refresh/logout, worker retry/purge, default/overridden views, and
production-negative construction.

All live legs use fresh/reset test databases and redact credentials. Confirm
ports/processes are stopped afterward.

## Task AV3-9.7 — post-implementation full reviewer gate

Depends on: AV3-9.6. This is the **first and only reviewer-agent wave in the
milestone**. Do not run it early or piecemeal.

With implementation, tests, live proofs, upgrade runbook, templates, and docs
complete, run read-only specialist reviews covering:

- authentication/application security and account-recovery abuse;
- Go/backend concurrency and atomic repository semantics;
- pgx/turso schema, migrations, backfill, locking, and query/index parity;
- framework architecture/module boundaries and public API ergonomics;
- SRE production wiring, secrets/key rotation, worker operation, observability,
  rate limits, and failure recovery;
- JSON/HTML transport parity, CSRF/origin/redirect handling;
- templ view security, accessibility, content safety, and override ergonomics;
  and
- release/upgrade documentation and downstream-host adoption.

Give reviewers the final diff/tree, design, this implementation packet, and
execution evidence. Require findings to name severity, exact file/contract,
reproduction or reasoning, and recommended fix. Reviewers do not edit files.
Deduplicate findings into one disposition table: accept, reject with concrete
reason, or defer only when explicitly out of v3 scope and safe.

No PR is opened from this task.

## Task AV3-9.8 — reviewer remediation, reverification, and PR-ready handoff

Depends on: AV3-9.7.

The owner-gated implementation plan is recorded in the execution log under
"Integrated reviewer addendum + draft AV3-9.8 plan." Recording that plan does
not authorize remediation: first import Claude's canonical 41-row disposition
table, merge the independently reproduced findings by contract, and obtain the
owner's approval of the resulting scope and the named decision gates.

Return accepted findings to the implementer in bounded fix batches, preserving
the same architecture and atomicity rules. Add regression tests for every
security/concurrency correctness fix. Update docs/runbook when a public or
operational contract changes. Record rejected/deferred findings and rationale.

Re-run:

```sh
make generate
make check
make guard
make test-stores   # authorized fresh pgx+turso test databases
```

Repeat affected JSON + HTML proof-host legs and upgrade fixtures. If remediation
materially changes a security boundary, run a targeted reviewer confirmation on
that finding only; do not restart broad iterative review loops.

Produce the PR-ready handoff: accepted/rejected/deferred finding table, files
changed, exact verification/live evidence, breaking/upgrade notes, and any
remaining explicitly safe deferral. Opening or pushing a PR still requires the
user's normal workflow/authorization.

## Phase acceptance

- Every phase task has an execution-log entry.
- Final hermetic, race, dual-store live, upgrade, and proof-host gates pass.
- The one post-implementation reviewer wave is dispositioned and accepted fixes
  pass AV3-9.8 reverification.
- Docs describe shipped behavior, not aspiration.
- No active plan contradiction or unexplained grep finding remains.
- Append the milestone-close entry to `00-overview.md` and `NOTES.md`.
- No reviewer agents were run before AV3-9.7 and no PR was opened before
  AV3-9.8.

## Stop conditions

- Any final test is skipped for missing live prerequisites: milestone remains
  open; report the exact prerequisite.
- Upgrade collision/rollback fixture causes partial writes: stop release work.
- Security audit finds secret/PII exposure or double redemption: reopen the
  owning phase rather than documenting acceptance.

## Execution log

Append dated entries per completed task.

### 2026-07-13 — AV3-9.1 final canonical migration audit

Task: AV3-9.1. Outcome: **PASS, no discrepancies — zero files touched.**

Audit findings (both `stores/pgx/migrations` and `stores/turso/migrations`,
files `0001`–`0014`):

- **Identical filename sets + clean numbering.** Both trees carry `0001`–`0014`
  with byte-identical filename sets; `TestMigrationInventory`/`TestMigrationParity`
  in both modules assert the frozen canonical slice and cross-tree parity. No gap,
  no duplicate, no stray file.
- **Greenfield `users`.** Columns are exactly `id, display_name, auth_revision,
  created_at, updated_at` — confirmed against the freshly-migrated live pg schema.
  No email/identifier/verification column remains; `auth_revision` (the optimistic
  CAS anchor) is present in both dialects.
- **v3 tables present and matching the stores.** `user_identifiers`, `challenges`,
  `contact_changes`, `authentication_grants`, `delivery_jobs` are pure CREATEs at
  `0010`–`0014`; session metadata (`authenticated_at`, `authentication_methods`,
  `assurance_level`) is in `0003`; the invitation `idx_invitations_kind_identifier`
  is in `0009`. Password-reset support is the challenge rail (design §5.9), not a
  table: `stores/{pgx,turso}/password_resets.go` compose `challenges` +
  `user_passwords` + `sessions` + `authentication_grants` in one transaction — all
  referenced columns exist.
- **No legacy verification table.** No `verification_codes`/`verification_tokens`
  file in either tree; live schema has no `%verification%` table.
- **Partial indexes.** `idx_user_identifiers_auth_claim` (unique on
  `(kind, normalized_value)` WHERE active AND login/recovery-enabled) and
  `idx_user_identifiers_primary` (unique on `(user_id, kind)` WHERE active AND
  primary) encode the active authentication-claim and primary-uniqueness invariants;
  verified verbatim in the migrated pg index definitions.
- **No orphanable FK/cascade.** Zero foreign-key constraints in the migrated schema
  (the no-enforced-FK convention across every auth table); credential/identifier
  atomicity lives in `CreateWithPrimaryIdentifier`/`ApplyVerifiedChange`
  transactions, so no cascade can orphan a credential or identifier.
- **Every store query references an existing final column/index.** Proven
  empirically: full `storetest` conformance passes on both dialects against
  fresh-migrated databases (a stale column/index reference would fail at query
  time).

Commands + observed results:

- pgx (fresh C-collation DB): `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/authv3_audit?sslmode=disable' go test ./...` in `stores/pgx` → `ok` (fresh migrate applied `0001`–`0014` then full storetest; includes `TestMigrationInventory`/`TestMigrationParity`/`TestExportMigrations`).
- turso (fresh libsql DB): `TURSO_DATABASE_URL=http://127.0.0.1:8080 TURSO_AUTH_TOKEN=<redacted> go test -tags=integration ./...` in `stores/turso` → `ok`.
- `make guard` → all guards green.

Premise adaptation logged (**important for AV3-9.2/AV3-9.6**): the keyset-pagination
id-tiebreak conformance assumes **byte-wise text collation**. SQLite's default
BINARY collation and Go's byte-wise string sort agree; PostgreSQL must therefore
use a **`C`/byte-wise collation database** or the `Order`/`*Collision`/`PrevPage`
subtests fail on the id tiebreak (observed: a first `authv3_audit` created from the
`en_US.utf8` template produced tiebreak mismatches; recreating it with
`TEMPLATE template0 LC_COLLATE 'C' LC_CTYPE 'C'` — matching the pre-existing
`authv3_cconf` — turned it green). This is an environment/DSN requirement, not a
store or migration defect: any fresh pg conformance/upgrade DB in later phases must
be created with `C` collation. Containers `authv3-pg` and `authv3-libsql` were left
running; the transient `authv3_audit` database was created for the fresh-migrate
proof and may be dropped.

### 2026-07-13 — AV3-9.2 publish and execute the host upgrade runbook

Task: AV3-9.2. Outcome: **PASS — runbook published and executed fresh on both
dialects, all four fixture paths green (incl. collision safe-abort with no partial
migration).**

Dependencies verified: phases 0–8 closed; AV3-9.1 complete and checked off (final
canonical migration audit PASS). Worktree preserved — no resets/reverts; the change
set is docs-only.

Files changed:

- `RELEASING.md` — added the published **Auth v3 host upgrade runbook (v2 → v3
  identity)** section (the full validated 7-step host-owned procedure, exact pgx and
  SQLite/libSQL SQL, runtime caveats, and the AV3-9.2 execution record) plus a keyed
  `### features/authentication + both store modules — next tag: auth v3 identity
  (BREAKING)` upgrade note pointing to it. This is the release-docs home for the
  operational procedure per the current release convention.
- `features/authentication/README.md` — added a mirrored `UPGRADE NOTE — v2 → v3
  identity (host-owned backfill migration)` section summarizing the load-bearing
  operational contract (backfill-first single cutover, collision-abort, verification
  state preserved, sessions/passwords/oauth/invitations untouched) and pointing to the
  full runbook in `RELEASING.md`, matching the two prior README/RELEASING upgrade-note
  pairs.
- `.claude/plans/authv3/host-upgrade-runbook-draft.md` — status flipped DRAFT →
  PUBLISHED (AV3-9.2); retained as drafting history.

Publication home: the phase file pins "feature docs/upgrade reference." Following the
AV3-2.5 adaptation and the draft's own stated target, the full SQL procedure lives in
`RELEASING.md` (the repo's per-module upgrade-runbook home) with the feature README
carrying the mirrored pointer note — the same split the session-hashing and
JWT-refresh runbooks use. The feature module carries only `README.md` (no standalone
upgrade-doc convention exists), so no new doc file was invented.

Execution (fresh/reset databases both dialects, all fixtures torn down afterward; the
long-lived conformance databases `authv3_audit`/`authv3_cconf` and the libsql
conformance schema were never touched; containers left running):

- **pgx clean path** — fresh `av3_upgrade_fixture` created `TEMPLATE template0
  LC_COLLATE 'C' LC_CTYPE 'C'` (byte-order parity per the AV3-9.1 requirement),
  v2-shape seed (4 users / 3 passwords / 1 oauth / 1 session / 1 invitation / 1
  verification-code / 1 verification-token). Steps 1–5: collision dry-run **0 rows**;
  Step-3 backfill **`INSERT 0 4`**; Step-4 **parity 4/4**, 0 missing-primary,
  0 duplicate auth-claims, 0 orphan passwords/oauth/sessions. Step-3 **re-run
  `INSERT 0 0`** (idempotent, count still 4). Step-6 `DROP COLUMN email`/
  `email_verified` + drop both verification tables — `users` left at
  `id, display_name, created_at, updated_at, auth_revision`. BEFORE/AFTER compare:
  passwords (hashes)/oauth/sessions/invitations byte-identical; session metadata
  columns present and defaulted; verification state exact (u_unverified →
  `verified_at NULL`, others non-NULL). **PASS.**
- **pgx collision path** — fresh `av3_upgrade_collide` (`C` collation) with an
  un-normalized duplicate (` Verified@Example.com ` vs `verified@example.com`):
  Step-1 dry-run **returned `verified@example.com` + both user ids** (abort signal);
  forced Step-3 backfill **failed on `idx_user_identifiers_auth_claim`** and left
  `user_identifiers` **empty (0 rows — no partial migration)**. **PASS (expected
  failure).**
- **SQLite/libSQL clean path** — executed against the **live libsql server**
  (`http://127.0.0.1:8080`, `POST /v2/pipeline`) using an isolated `up_` table
  namespace so the standing turso conformance schema was untouched; identical runbook
  SQL. Steps 1–5: dry-run **0 rows**, backfill **4 rows**, parity **4/4**,
  0 missing-primary / 0 duplicate auth-claims / 0 orphans; re-run **0 inserted**
  (idempotent); Step-6 **12-step table rebuild** left `users` at
  `id, display_name, auth_revision, created_at, updated_at`, identifiers intact, both
  verification tables gone; AFTER counts 4/3/1/1/1/4, verification state exact. **PASS.**
- **SQLite/libSQL collision path** — live libsql server, `up2_` namespace,
  un-normalized duplicate: dry-run detected `verified@example.com` (x2); forced
  backfill **aborted** (`UNIQUE constraint failed: user_identifiers.kind,
  normalized_value`) with **0 rows** written. **PASS (expected failure).**

Commands / results:

- `make guard` → **PASS** (all 13 guards green; docs-only change touched no code, so
  build/vet/test are unaffected).
- pgx: `psql` fresh-DB fixtures on `authv3-pg` (superuser); libsql: `POST /v2/pipeline`
  on `authv3-libsql` (token redacted). Fixture DBs (`av3_upgrade_fixture`,
  `av3_upgrade_collide`) dropped and all `up_`/`up2_` fixture tables removed after the
  run; both containers confirmed still running.

Premise adaptations logged:

- **libSQL executed on the live server in an isolated table namespace, not local
  sqlite3.** The AV3-2.5 draft validated the SQLite/libSQL DDL with local `sqlite3`;
  AV3-9.2 re-ran the identical runbook SQL against the actual libsql engine
  (`authv3-libsql`) for stronger dual-dialect evidence. Because that server already
  holds the standing v3 conformance schema (real `users`/`sessions`/… tables from
  `stores/turso` tests) which the handoff forbids disturbing, the fixture used an
  `up_`/`up2_` table-name prefix. The SQL is logically identical (only table names
  differ), so the CREATE/partial-index/backfill/12-step-rebuild/unique-abort behavior
  is faithfully exercised; nothing in the standing conformance schema was modified.
- **`verified_at` proxy** — unchanged from AV3-2.5: v2 persisted only a boolean
  `email_verified`, so the runbook maps verified→`updated_at` / unverified→NULL and
  documents the timestamp as a proxy with a host-substitution hook. State is exact;
  only the timestamp is approximate.
- **Publication split** — the full SQL runbook is published in `RELEASING.md` with a
  pointer note in the feature README (rather than the README carrying the full SQL),
  because RELEASING.md is the repo's per-module upgrade-runbook home and the feature
  has no standalone upgrade-doc file. Mirrors the AV3-2.5 flag.

For AV3-9.3 (public feature and sdk documentation): (1) the feature README is still
the **v2/refresh-era** document — its intro claims "JSON API only — no server-rendered
pages," lists eleven `Repositories` ports and migrations `0001…0011`, and documents
`users.email`/verification-code routes; AV3-9.3 owns the full v3 rewrite (identifier
model, HTML/templ `Views`, passwordless, account-security, delivery worker, the v3
migration/break inventory). My AV3-9.2 edit only adds an upgrade-note section; it does
not touch the stale body. (2) The two doc-gaps AV3-8.10 flagged remain open and are
AV3-9.3's to close: `examples/auth-cms/README.md` still documents the v2 curl protocol
and omits the v3 surface (HTML pages, passwordless login, account/identifier
management, and host override). (3) The migration/break inventory AV3-9.3 must document
is now anchored by this runbook and the `RELEASING.md` keyed note — reuse them rather
than re-deriving the break list.

### 2026-07-13 — AV3-9.3 public feature and sdk documentation

Task: AV3-9.3. Outcome: **PASS — docs-only, `make guard` green.**

Dependencies verified: AV3-9.1 complete (final canonical migration audit PASS,
checked off); AV3-9.2 complete (upgrade runbook published + executed, checked off);
phases 0–8 closed. Worktree preserved — every pre-existing change intact; the change
set is documentation-only (no code, so build/vet/test are unaffected — `make guard`
is the standing verification for a docs task, the AV3-9.2 precedent).

Files changed:

- `features/authentication/README.md` — **full v3 rewrite of the body** (the stale
  v2/refresh-era doc replaced). New/updated sections: v3 intro (identifier model
  replaces the "JSON API only" framing; optional HTML/templ `Views`); the identifier
  model (uses/normalization/partial-unique auth-claim + primary invariants/shared
  notification addresses); the full v3 JSON route surface (step-up, `/auth/methods`,
  password set/remove, provider-bound OAuth unlink, `/auth/identifiers/*`,
  passwordless, removed `oauth/linked` + `oauth/link`, `{email, code}` verify break);
  the HTML surface (`Views == nil` API-only semantics, content-type dispatch, HTML
  GET/PRG route table, bundled `views/templ` / `html/template` alternative /
  embed-to-override, credential-establishment origin vs authenticated CSRF); the full
  v3 `Repositories` bundle (identifier/challenge/passwordreset/contactchange/
  authgrant/credential/deliveryjob ports + nil semantics); the v3 `Config` table
  (RuntimeMode + every enable-time/production gate, five distinct secrets + rotation);
  challenges & recovery (atomic single-use rail + why the OTP HMAC pepper is local
  code, not a service); the delivery worker (lifecycle/at-least-once/encryption/
  retries/health/purge/production metadata); the masked method inventory; the security
  posture (runtime modes, rate-limit/trusted-proxy, CSRF/origin/native-client, security
  events, PII masking/redress); the §5.8 error-code families; the v3 security-event
  inventory; migrations `0001–0014` (host-owned, greenfield, no legacy verification
  table); a v3-minimum quickstart; and the auth-v4 MFA handoff (typed authenticators
  for passkeys/WebAuthn/TOTP/recovery codes, AAL2, factor replacement/reset — seams
  reserved, no claim MFA ships now). All three prior UPGRADE NOTE sections (v1→v2,
  JWT-sessions, and the AV3-9.2 v2→v3 note) are preserved verbatim; the v2→v3 note
  reuses the `RELEASING.md` runbook + keyed break inventory rather than re-deriving it.
- `examples/auth-cms/README.md` — retitled the multi-feature proof host (v2 A9 + v3);
  added the content-type dispatch note (curl `-d` → form arm; JSON legs need the JSON
  header); **fixed the leg-0 verify body `{"code":…}` → `{email, code}`** (the v3
  break); added the auth-v3 wiring subsection (RuntimeMode, five dev secrets, challenge
  rail, delivery worker, passwordless, PublicAuthBaseURL, AllowedOrigins, the
  `authpages` page override + `EmailContentTemplates`, ContactChanges); added the v3
  rows to the host-view Config/port nil-semantics table; refreshed the Environment
  paragraph; added Legs 8–11 (HTML pages twice-through, passwordless + magic link,
  account-security/identifier/step-up, override systems + delivery worker); and updated
  the Route-surface auth bullet to the full v3 JSON + HTML inventory. `.env.example` was
  already current (AV3-8.6/8.9) — untouched.
- `sdk/README.md` — extended the `email` and `notify` packages-table rows with the
  auth-v3-consumed capability metadata (`CapabilityReporter` / `Capabilities`
  {`TransportSecurity`, `DevelopmentOnly`} — a consumer fail-closes in production on a
  `DevelopmentOnly`/metadata-less transport) and named email's `LayerCore`/`LayerApp`
  TemplateRegistry consumption (feature registers at LayerCore, host overrides at
  LayerApp). Scope kept to what the task pins: cryptids HS256 was pre-existing JWT work
  and is left as-is.

Public Go docs: the `authentication.go` socket doc comments (Repositories, Config,
Service methods, `RunDeliveryWorker`, `Views`, `EmailContentTemplates`) are already
current v3 and comprehensive — no doc-comment edit was needed; the README is the
narrative layer over them. Every example in both READMEs uses distinct secret-free
placeholders with production-safe comments (the five-distinct-keys discipline).

Premise adaptations logged:

- **sdk documentation scope = README package-table rows, not a new doc file.** The
  phase task lists "sdk capability docs" among the touch points; the milestone's new
  sdk surface is the email/notify production-safety metadata and the TemplateRegistry
  layer consumption (the design's fail-closed transport rule). Both are documented by
  extending the existing `sdk/README.md` package table rather than inventing a new sdk
  doc file (no such per-capability doc convention exists). `sdk/foundation/cryptids`
  HS256 pre-dates v3, so it is not re-documented here.
- **Feature README fully rewritten, not incrementally patched.** The handoff called for
  a "full rewrite to v3"; the prior body's load-bearing false claims ("JSON API only",
  "eleven Repositories ports", "migrations 0001…0011", `users.email`/verification-code
  routes) are pervasive, so a clean rewrite (preserving the three upgrade notes) is the
  surgical choice over line-by-line patching.
- **Reset-page bare-fragment mechanic noted (AV3-8.10 handoff).** The HTML surface
  section documents the bundled magic/reset landing as a CSP-nonced fragment reader
  that scrubs history; the proof-host reset flow's bare-fragment delivery is an
  example-host delivery detail, not a feature-README contract, so it is covered by the
  general fragment-landing description rather than a standalone caveat (the flagged
  `#token=`-reset-link follow-up remains an open recovery-UX item, unchanged).

Verification (observed):

- `make guard` → **PASS** (all guards green, exit 0; docs-only change touched no code —
  build/vet/test unaffected, feature core still has no `a-h/templ` import).

For AV3-9.4 (release/change inventory): the break inventory this task documented is
already anchored in `RELEASING.md` (the keyed `features/authentication + both store
modules — next tag: auth v3 identity (BREAKING)` note + the Auth v3 host upgrade
runbook) and mirrored in the feature README's v2→v3 UPGRADE NOTE — 9.4 records the
release/version/tag inventory over that same break list (feature + both store modules +
the new `features/authentication/views/templ` bundled-views module, module count
thirty-seven per AV3-8.2). No new breaking surface was introduced by 9.3 (docs-only).
The five-distinct-secrets rotation posture, the production fail-closed gate list, and
the delivery-worker host-lifecycle requirement are all now documented and are the
production-checklist inputs 9.4 should surface. The auth-v4 MFA deferral and real-SMS
deferral are documented in the feature README's handoff section for 9.4 to record as
deliberate deferrals.

### 2026-07-13 — AV3-9.4 release/change inventory

Task: AV3-9.4. Outcome: **PASS — docs-only, `make guard` green.**

Dependencies verified: AV3-9.2 complete (runbook published + executed, checked off);
AV3-9.3 complete (public feature/sdk documentation, checked off); phases 0–8 closed.
Worktree preserved — every pre-existing change intact; the change set is
documentation-only (no code, so build/vet/test are unaffected — `make guard` is the
standing verification for a docs task, the AV3-9.2/9.3 precedent). No tags created or
pushed (tagging is release execution, owner-authorized separately).

Change inventory built from git against the uncommitted worktree (the milestone diff),
reusing the break list already anchored in `RELEASING.md` + the feature README rather
than re-deriving it. Git-verified facts: `sdk/foundation/web` is untouched by auth-v3
(`git status` empty for it); `integrations/datastores/turso/tx.go` carries the
`BEGIN IMMEDIATE` write-intent change; `sdk/capabilities/{email,notify}` gained new
`capabilities.go` (CapabilityReporter/Capabilities/TransportSecurity) + Console/SMTP
`Capabilities()` methods; `features/authentication/views/templ` exists as a new module
(go.work = 37 modules); the HS256 default (`sdk/foundation/cryptids/hs256.go`) belongs
to the separate JWT-refresh workstream, not v3.

Files changed:

- `RELEASING.md` — after the existing keyed `auth v3 identity (BREAKING)` note (reused,
  not re-derived), added three new per-module keyed upgrade notes —
  `features/authentication/views/templ` (NEW module, first tag),
  `integrations/datastores/turso` (patch / `BEGIN IMMEDIATE` behavior fix, with the
  concurrent-step-up caveat), and `sdk/capabilities/{email,notify}` (minor floor,
  additive capability metadata) — plus a `### Auth v3 tag requirements + production
  checklist` block: the per-module semver-floor table (feature + both stores =
  major/breaking; views/templ = first tag; turso = patch; sdk capabilities = minor;
  examples never tagged), the host upgrade order pointer, and the fail-closed
  production checklist (five distinct rotatable secrets, dev-transport/memory-limiter/
  wiring rejection, host-owned delivery-worker lifecycle, host-owned pre-boot
  migrations, the pgx `C`-collation + turso connector caveats). Explicitly recorded
  that `sdk/foundation/web` and `sdk/foundation/cryptids` HS256 were NOT touched by v3.
- `NOTES.md` — appended the dated `## 2026-07-13 — auth-v3 release/change inventory
  (AV3-9.4)` decision-log entry: breaking-change summary, per-module tag requirements,
  host upgrade order + production checklist, live test artifacts (endpoints redacted,
  with the AV3-9.6 final-proof pointer), the six deliberate deferrals (auth-v4 MFA,
  real SMS, `CompleteStepUpWithOAuth`, `#token=` reset-link builder, PII-in-audit
  retention lifecycle, pgx pagination-collation parity), and the implementation
  adaptations from the cut plan.

Premise adaptations logged:

- **Break list reused, not re-derived.** Per the AV3-9.3 handoff the breaking surface
  is already the RELEASING.md keyed note + runbook + feature README UPGRADE NOTE; 9.4
  records the tag/version requirements, production checklist, live artifacts, and
  deferrals *over* that list. The RELEASING.md edit adds the tag-floor classification
  and the three not-yet-keyed modules; it does not restate the break narrative.
- **PII-in-audit retention lifecycle recorded here as a deferral, not in AV3-9.5.** The
  handoff asked which phase owns it. AV3-9.5 audits for PII *exposure* (grep + classify);
  the retention/redaction *lifecycle policy* is future work, so it belongs in 9.4's
  deferral inventory. Design §5.1 WI3 deliberately keeps the plaintext identifier
  (never a secret) in audit details — that is not the exposure 9.5 hunts.
- **HS256 attribution.** The worktree carries the JWT-refresh cut's HS256 default
  alongside v3; it is keyed under its own RELEASING.md JWT-refresh note and explicitly
  excluded from the v3 sdk inventory (only `web`'s non-involvement was named in the
  handoff — HS256 non-attribution is the symmetric confirmation).

Verification (observed):

- `make guard` → **PASS** (all 13 guards printed, exit 0; docs-only change touched no
  code — build/vet/test unaffected).

For AV3-9.5 (final adversarial audit): the deferral inventory now names the exact
follow-ups so the audit can scope around them — the PII-in-audit *lifecycle* is a
recorded deferral (audit for exposure, do not treat the intentional plaintext-identifier
audit detail as a finding); `CompleteStepUpWithOAuth` is deferred (its absence is not a
gap); and the turso `BEGIN IMMEDIATE` connector + pgx `C`-collation are the two
environment preconditions any live/race leg must satisfy. The production fail-closed
gate list (dev-transport / memory-limiter / missing-wiring rejection) that 9.5's
production-negative tests exercise is now enumerated in RELEASING.md's production
checklist.

### 2026-07-13 — AV3-9.5 final adversarial audit

Task: AV3-9.5. Outcome: **PASS — every audit-list surface green under `-race`
(hermetic + live dual-store); secret/PII grep classified with zero unexplained
secret-bearing output; zero defects, zero files touched.**

Dependencies verified: AV3-9.1–9.4 complete and checked off (final migration audit,
runbook publish+execute, public docs, release inventory); phases 0–8 closed.
Worktree preserved — no file resets/reverts (docs-only audit; only transient test
databases were reset by the live legs). No reviewer/consultation agents spawned
(forbidden through AV3-9.6; this audit is implementer-run). Scoped around the six
recorded deferrals in NOTES.md's 2026-07-13 entry: the intentional plaintext-identifier
audit detail (§5.1 WI3) and `CompleteStepUpWithOAuth` absence were treated as recorded
deferrals/expected behavior, not findings.

Environment: turso `BEGIN IMMEDIATE` connector in-tree; pgx `C`-collation DB
`authv3_cconf` and libsql `http://127.0.0.1:8080` (token `local-dev`) used for the
live legs. Containers `authv3-pg`/`authv3-libsql` left running; no container
stopped/removed.

**Race gates (`go test -race`) — all PASS:**

- feature core (`features/authentication` — logic/authsvc, inbound, delivery worker,
  memory reference `storetest`, all domains) → `ok`.
- sdk security/capability packages (`foundation/web` TrustProxies/ClientIP,
  `capabilities/ratelimiter` Memory, `capabilities/{email,notify}`,
  `foundation/cryptids`) → `ok`.
- proof host (`examples/auth-cms` cmd/server, authmem, authpages, memstore, outboxmem)
  → `ok`.
- **live pgx** conformance (`stores/pgx`, `authv3_cconf` C-collation) →
  `ok 21.959s` — atomic challenge redemption / `auth_revision` CAS single-winner on
  real Postgres under `-race`.
- **live turso** conformance (`stores/turso`, `-tags=integration`, libsql server) →
  `ok 26.101s` — same on real libSQL under `-race`.

**Audit-list coverage (phase-file list is authoritative; each item mapped to green
named tests, verified present + passing in the race run):**

1. Concurrent code/token redemption + attempt lockout — `TestConcurrentCodeSingleWinner`,
   `TestConcurrentTokenSingleWinner` (+ live `-race` conformance).
2. Concurrent final-method removal + revision retry —
   `TestCredentialPolicyConcurrentSelfRemoval`,
   `TestPasswordRemoveConcurrentRemovalReevaluatesPolicy`,
   `TestIdentifierRemoveConcurrentReevaluatesPolicy`,
   `TestPasswordRemoveStaleRevisionRetriesAndSucceeds`,
   `TestOAuthUnlinkStaleRevisionReevaluatesPolicy`.
3. Reset atomic rollback + prior-session rejection — `TestPasswordResetRollback`,
   `TestForgotAndResetPassword` (asserts all prior sessions revoked, none minted).
4. OAuth unverified-email/adoption/wrong-provider binding —
   `TestOAuthCallbackUnverifiedEmailRefused`/`…StillLoginsExistingLink`,
   `TestOAuthAdoptionRevokesSquatterCredentials`/`…CapturedFlagTOCTOU`/`…FailsClosedWithoutRail`,
   `TestOAuthUnlinkWrongProvider`, `TestOAuthCallbackWrongProviderState`,
   `TestOAuthUnlink_ProviderBound`.
5. Identifier replacement invalidating old links/codes —
   `TestIdentifierChangeWrongCodeRetainsPendingValue`,
   `TestPasswordRemoveInvalidatesPendingReset`, `TestIdentifierChangeSendsIndependentNotice`.
6. Known/unknown timing shape w/ slow/failing provider —
   `TestPasswordlessStartUniformForUnknownAndMalformed`, `TestFormForgotPRGEnumerationSafe`
   (unauthenticated start never synchronously resolves an account or calls a provider —
   async delivery is why timing is uniform; §5 invariant).
7. Secret/PII grep — classified below; zero unexplained match.
8. CSRF/origin/CORS/body-limit/cache headers — `TestIssueCSRFTokenRoundTrips`,
   `TestOriginAllowedIgnoresWildcard`, `TestCORSNeverWildcardWithCredentials`,
   `TestStrictJSONBody`, `TestHTMLSecurityHeaders`, plus the per-route
   `…RequiresCSRF`/`…RequiresFormCSRF` suite.
9. Hostile Host/open-redirect/trusted-proxy spoofing —
   `TestPasswordlessRedeemHostileHostIgnored`,
   `TestSpoofedForwardedHeaderCannotRotateLimiterBucket`,
   `TestClientIPUsesTrustedProxyResolution`, `TestFormLoginReturnToAllowlist`.
10. Console/missing-metadata/memory-limiter production rejection — the full
    `TestNewServiceProduction*` matrix (rejects console email/notifier/phone,
    metadataless email, non-durable delivery repo, default+explicit+declared memory
    limiter, missing identifier keyer, HTTP passwordless base URL; accepts declared
    durable transports/limiter/delivery).
11. Delivery lease/retry/idempotency/terminal purge — `TestServiceEnqueueIdempotent`,
    `TestWorkerRetryThenSucceed`, `TestWorkerContentionSingleClaimant`,
    `TestWorkerPurgeRespectRetention`, `TestWorkerTerminalFailureCancelsChallenge`,
    `TestWorkerUndecryptablePayloadFailsTerminally`, `TestWorkerGracefulShutdownNoLeak`.
12. Old-key HMAC rotation/removal timing — `TestChallengeProtectorKeyRotation`,
    `…UnknownKeyID`, `…InvalidActiveKeyID`, `…DifferentKeyDiverges`,
    `…ShortAndMissingKey`, `…DomainUserPurposeSeparation`.
13. JSON-vs-form parity / 415 / HTML 303 PRG / default-view a11y+security headers /
    API-only graph / partial override — `TestContentTypeDispatchRoutesByContentType`,
    `TestJSONContractUnchangedByViews`, `TestRequireJSON` (415), `TestFormLoginPRGSetsSession`
    (303), `TestHTMLSecurityHeaders`, `TestContentTypeDispatchNilViews` +
    `TestAPIMountJSONSurvivesNilViews` (API-only when `Views==nil`), `TestEmbed_OverrideOneMethod`
    + `TestPresentationOverrideCannotBypassSecurity` + `TestOverrideSystemsAreDistinct`
    (partial override cannot bypass security).

**Secret/PII grep — every match classified; zero secret-bearing egress:**

- **Audit/security-event `Details`** (10 construction sites) carry only non-secret
  fields: `session_id`, `rotation_count`, `purpose`, `kind`, `provider`, `key_prefix`
  (the apikey lookup prefix, not the key), and the plaintext identifier/`email` (an
  identifier, never a secret — §5.1 WI3, the recorded deferral). No raw code, token,
  password, or pepper in any `Details`, `Actor`, `UserID`, `IP`, or `UA`.
- **Logs** — `refresh token reuse detected`, `compromised-password check failed open`,
  `api key touch-last-used failed`, `AUTH_CHALLENGE_PEPPER unset …` etc. carry secret
  *words* in the message string only; every structured arg is a non-secret
  (`session_id`/`user_id`/`rotation_count`/`ip`/`ua`/`error_kind`). No key material
  logged (the composition root emits only a WARN when a key is ephemeral, never the
  value).
- **Limiter keys** — `loginKey`, `passwordlessVerifyKey`, `passwordlessStartBudget`,
  `passwordlessRedeemBudget`, `refreshSessionKey`, `RateLimitByIP` all key on
  `identifierDigest(kind,value)` (host HMAC keyer, else SHA-256 token hasher) `+`
  trusted client IP or session id — never a raw address. The `identifierDigest`
  plaintext fallback fires only on a `tokenHasher.Hash` error that SHA-256-over-string
  cannot produce (defensive, not a live path).
- **SQL columns** — both dialects: `challenges` persists only `secret_digest`
  (HMAC/SHA-256; no plaintext code/token column; redemption keyed by
  `(user_id,purpose)` / `(purpose,secret_digest)`), `delivery_jobs` persists only an
  AES-256-GCM-sealed `payload` (`BYTEA`/`BLOB`) with deliberately no plaintext
  destination/message/identifier column.
- **Docs/env** — `examples/auth-cms/.env.example` is all empty placeholders with a
  "non-secret PLACEHOLDERS" header and production-safe comments; the five distinct v3
  keys are documented, each with an ephemeral-random dev fallback + WARN. Composition
  root (`cmd/server`) holds no hardcoded secret literal — every key is env-sourced.
- **Print/format egress sweep** — the sole `fmt.Sprintf(...token|code|...)` match in
  non-test code is `storetest.go:2650` building a user-id string (`exp-u%d`); the
  digest is a pre-computed param, no secret in the format. The console mailer logging
  codes/tokens is expected development-only behavior and is production-rejected
  (`TestNewServiceProductionRejectsConsoleEmail`), not a leak.

Findings: **none.** No secret/PII exposure, no double-redemption, no stale-method
mutation race, no boundary violation surfaced. No stop condition (phase §Stop
conditions) triggered — nothing to reopen an owning phase for; nothing deferred to
AV3-9.7.

Commands / observed results:

- `cd features/authentication && go test -race ./...` → all `ok`.
- `cd sdk && go test -race ./foundation/web/... ./capabilities/ratelimiter/... ./capabilities/email/... ./capabilities/notify/... ./foundation/cryptids/...` → all `ok`.
- `cd examples/auth-cms && go test -race ./...` → all `ok`.
- `cd features/authentication/stores/pgx && POSTGRES_TEST_DSN='postgres://…/authv3_cconf?sslmode=disable' go test -race ./...` → `ok 21.959s`.
- `cd features/authentication/stores/turso && TURSO_DATABASE_URL=http://127.0.0.1:8080 TURSO_AUTH_TOKEN=<redacted> go test -race -tags=integration ./...` → `ok 26.101s`.
- Grep classification (audit `Details`, logs, limiter keys, both dialects' SQL columns,
  docs/env, print/format egress) → every match classified non-secret; zero unexplained
  secret-bearing output.

Premise adaptations logged: none beyond the AV3-9.4 scoping already in effect — the
audit ran exactly the phase-file list against the existing per-phase test suites plus
the grep sweep; no test or code was added or changed (the milestone's acceptance model
is that each phase's own tests are the in-flight gate, and this audit re-runs them as a
consolidated adversarial pass under `-race`).

For AV3-9.6 (implementation-complete hermetic + live gate): the audit adds no new code,
so `make check`/`make guard` state is unchanged from AV3-9.4. The two live DSNs are
confirmed working under `-race` this session — pgx `authv3_cconf` (C-collation, required
for the pagination id-tiebreak parity) and libsql `http://127.0.0.1:8080` token
`local-dev` (with the in-tree `BEGIN IMMEDIATE` connector). `make test-stores` will need
`POSTGRES_TEST_DSN` pointed at a C-collation DB (`authv3_cconf`) and the turso env pair.
Both containers are up and must be left running. No worktree file was reset; only
transient test databases were reset by the conformance legs.

### 2026-07-13 — AV3-9.6 implementation-complete hermetic and live gate

Task: AV3-9.6. Outcome: **PASS — full hermetic gate green, dual-store live
conformance green on fresh/reset databases both dialects, and the proof-host
critical path re-driven live through JSON and HTML (form/PRG) transports. Zero
files touched (this is a gate task, no code).**

Dependencies verified: AV3-9.1–9.5 complete and checked off; phases 0–8 closed.
Bookkeeping audit before running: every task AV3-0.1 through AV3-9.5 is checked
off in `TASKS.md`, and every phase file (`01`–`10`) carries a dated
`Execution log` section whose entries cover every task ID in its phase (phase 0
also carries the preflight entry) — **no bookkeeping gap found.** Worktree
preserved: no file reset/revert; only transient test databases were reset. No
reviewer/consultation agents spawned (forbidden through AV3-9.6); no PR/commit/tag.

**Hermetic gate (observed):**

- `make check` → **PASS** — "all checks passed": templ drift-clean (`git diff`
  no-op), warm-scaffold-cache no-op, all 37 modules `go vet ./... && go build ./...
  && go test ./...` ok, integration-tag compile-vet for all five `*/turso`
  modules, all 13 layering guards green.
- `make guard` → **PASS** (exit 0, all 13 guards printed).

**Dual-store live conformance on FRESH/RESET databases (milestone criterion):**

- pgx: `authv3_cconf` **dropped and recreated** `TEMPLATE template0 LC_COLLATE 'C'
  LC_CTYPE 'C'` (the AV3-9.1 byte-order requirement), so migrations `0001`–`0014`
  re-applied from scratch. `POSTGRES_TEST_DSN='postgres://…/authv3_cconf?sslmode=disable'
  TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN=<redacted> make test-stores`
  → **all ten store legs `ok`** (auth pgx 8.056s; cms/jobs/events/authorization pgx
  fresh-migrated + green).
- turso: the libsql database was **fully reset** (all tables incl. `schema_migrations`
  dropped → 0 tables) so migrations re-apply from scratch. The first `make test-stores`
  served the turso legs from the Go **test cache** (`(cached)`), which does not execute
  against the reset DB, so each turso store leg was **re-run with `-count=1`** against
  the freshly-reset libsql — auth turso `ok 10.055s`, cms 2.724s, jobs 5.638s, events
  0.369s, authorization 0.789s (all fresh, not cached). *Premise adaptation logged
  below.*

**Proof-host critical path re-driven LIVE (real boot + curl, both transports):**
booted `examples/auth-cms` (`go run ./cmd/server`, in-memory stores,
`RuntimeMode=development`, console email + phone transports, `RunDeliveryWorker`
live, `AUTH_DEBUG=1`, all five secrets unset → ephemeral). Startup carried the
expected WARN set: four ephemeral-key WARNs, two dev console-transport WARNs
(email sender + phone notifier), the in-process rate-limiter WARN, the debug-route
WARN. All legs redact codes/tokens; host stopped afterward, **port 8082 released**,
both containers left running.

JSON API legs (all PASS): (1) register 201 → pre-verify login **403** (verified-
email gate) → async worker delivered the code → verify 200 → password login 200 +
session cookie; (2) reset — `forgot`→`reset` 200, prior cookie session **401**
(revoked), old password **401**, new password 200; (3) identifier add (bearer,
recent-primary-login shortcut) 200 → confirm `{status:confirmed}` → phone identifier
add delivered its OTP via the console **notifier** → change-uses PATCH 200 → remove
`{status:removed}` → remove last login identifier **409** (policy); (4) recent-auth —
step-up begin `{status:sent,receipt}` → step-up code `{status:verified,expires_at}`
(grant earned) → credential mutation (change password) 200 + remint, prior bearer
session **401**; (5) magic link — passwordless start `link` → redeem 200 + session →
replay same token **401** (single-use atomic); (6) passwordless OTP — start `code` →
verify 200 mint; unknown-identifier start generic **202** with **no delivery job**
(the `ghost-*` address never appears in the log — start never synchronously resolves
or enqueues); (7) refresh — rotate 200 (new refresh), old-token grace replay 200 then
reuse **401** + `refresh token reuse detected` WARN + chain burned, logout cleared
both cookies (`Max-Age=0`).

HTML (form/PRG) legs (all PASS): default-view security headers on `GET /auth/login`
(`Cache-Control: no-store`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`,
`X-Content-Type-Options: nosniff`, restrictive nonce CSP); the host **branded Login
override** (`data-brand="gopernicus-cms"`, title "Sign in — Gopernicus CMS") vs the
**bundled default Register** page (no host brand) — override is presentation-only;
register form **303** PRG → `/auth/verify`, verify form 303 → `/auth/login`, login
form 303 → `/` + session cookie; content-type dispatch parity (JSON arm 200 on the
same `/auth/login` route the form arm rendered); negatives — unsupported
`Content-Type: application/xml` **415**, cross-site `Origin` **403**, bad-cred form
login **401 re-render** (not 303) with **no** session cookie; and the authenticated
form **double-submit CSRF** gate on `/auth/password/change` (form arm) — missing
`csrf_token` **403**, mismatched `csrf_token` **403**, cross-site Origin **403**.

Hermetic-only legs the phase names (memory host cannot boot production; console
transport never fails, so failure-injection is hermetic by design):

- worker retry/replace/terminal/purge/contention/crash/undecryptable/graceful-shutdown:
  `go test ./internal/logic/delivery/... -run 'Worker(Retry|Replace|Terminal|Purge|Contention|Crash|Undecryptable|GracefulShutdown)' -count=1` → `ok`. Live host proved the happy path (23 jobs `outcome=delivered attempt=1` + `initialized`/`skipped` lifecycle rows; email + phone kinds).
- HMAC key rotation: `go test . -run 'ChallengeProtectorKeyRotation|ChallengeProtectorUnknownKeyID' -count=1` → `ok`.
- production-negative construction: `go test ./cmd/server -run 'TestProductionNegatives|TestProductionBaselineConstructs|TestDevelopmentConsoleTransportWarns' -count=1` → `ok` (rejects console transports, insecure http base URL, memory limiter, missing keyer, non-durable outbox metadata, unacknowledged worker; baseline durable wiring constructs; dev console WARN observed).

Premise adaptations logged:

- **Proof-host critical path re-driven at the HTTP layer in BOTH transports via
  live boot + curl, not a browser DOM drive.** AV3-8.10 was the phase-8 browser
  (headless Chromium) run-and-look capstone; that tooling lived in a prior session's
  scratchpad and is gone, and none is checked into the repo. AV3-9.6 re-drives the
  full critical path over real HTTP against the final tree — JSON API legs plus the
  HTML **form/PRG** arm (303 redirects, security headers, branded-override vs
  bundled-default, content-type dispatch parity, and the Origin/415/bad-cred/
  double-submit-CSRF negatives) — which exercises both the JSON and HTML dispatch
  arms and the browser-safe gates end-to-end. The DOM-level browser capstone remains
  AV3-8.10's recorded evidence.
- **turso live legs re-run with `-count=1`.** `make test-stores` passes no
  `-count=1`, so on the first invocation the Go test cache served the turso store
  legs (`(cached)`) rather than executing them against the just-reset libsql. Each
  turso store leg was re-run with `-count=1` against the freshly-reset (0-table)
  database so the fresh-DB conformance record is honest; the pgx legs executed
  fresh on the first run (recreated `authv3_cconf`, non-cached durations shown).
- **Drive-harness calibrations (not product issues).** (a) The v3 password policy
  requires **≥15 code points** (AV3-3.4); the drive uses ≥15-char passwords. (b) JSON
  `POST /auth/login` returns the user projection and sets session **cookies**; bearer/
  refresh material comes from `POST /auth/token` (`IssueToken`), which the drive uses
  for the bearer-authenticated mutation legs and the refresh leg. (c) `login`/
  `register`/`verify` are credential-establishment endpoints protected by the **Origin
  allowlist only** (no pre-session double-submit token — design §9.1); the form
  double-submit CSRF **403** is enforced on **authenticated** account-mutation forms
  (`/auth/password/change` etc.), where it was demonstrated. (d) The per-identifier
  login limiter is **5/min**, so the drive uses a fresh registered+verified user per
  heavy-login leg. (e) `RequireLiveSession` accepts a bearer access JWT; an early
  harness bug (an unquoted `-H Authorization:Bearer $TOK` word-split on the space in
  the value) produced spurious 401s and was fixed by quoting the header — not a host
  defect.

Findings: **none.** No stop condition (phase §Stop conditions) triggered — no skipped
final test for a missing live prerequisite (both DSNs live), no partial-write, no
secret/PII exposure or double-redemption surfaced (magic-token and step-up-code replays
both 401; the unknown-identifier start enqueued nothing).

Commands / observed results:

- `make check` → **PASS** ("all checks passed").
- `make guard` → **PASS** (exit 0, all 13 guards).
- `POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/authv3_cconf?sslmode=disable' TURSO_DATABASE_URL='http://127.0.0.1:8080' TURSO_AUTH_TOKEN=<redacted> make test-stores` → **all ten legs `ok`** (fresh-recreated pgx C-collation DB; turso re-verified `-count=1` on the reset libsql).
- Live proof host: `PORT=8082 … AUTH_DEBUG=1 go run ./cmd/server`; JSON + HTML curl drives as above; host stopped, **port 8082 released**; containers `authv3-pg`/`authv3-libsql` left running.
- Hermetic worker/rotation/production-negative test runs as above → all `ok`.

For AV3-9.7 (post-implementation full reviewer gate — the FIRST and ONLY reviewer wave,
NOT run by this task): the implementation-complete gate is closed. Hand reviewers the
final tree/diff, the design, and this consolidated evidence (hermetic + dual-store live
on fresh DBs + JSON/HTML proof-host transcripts + hermetic worker/production-negative
runs). The milestone remainder is AV3-9.7 (dispositioned read-only specialist reviews)
then AV3-9.8 (bounded remediation of accepted findings, regression tests, re-run of
`make generate`/`check`/`guard`/`test-stores`, and the PR-ready handoff). No PR is opened
before AV3-9.8. The two live DSNs and the `C`-collation pgx / reset-libsql fresh-DB
procedure used here are the reproducible live-gate recipe for AV3-9.8's re-verification.

### 2026-07-13 — AV3-9.7 post-implementation full reviewer gate

Task: AV3-9.7. Outcome: **wave RUN and DISPOSITIONED — 41 findings, 0 critical, 4 high.
Zero files edited (reviewers are read-only); no PR.** Remediation is AV3-9.8's, gated on
the owner's disposition of the table below.

Scope handed to reviewers: the uncommitted working tree as ONE untagged cut — the auth-v3
identity milestone + the AV3D delivery-runtime refactor + the SWP sdk-work-protocol
promotion — plus the three plan packets, `NOTES.md` 2131–2286 (the AV3-9.4 inventory and
the AV3D-5.4 delta), `RELEASING.md`, and the per-phase execution logs as evidence. Every
reviewer was pre-briefed with the parked-item list (pgx pagination-collation; §5.8 named
error codes; `DeliveryStatus.Attempt`=0; the SWP-3 `bytes.Clone` judgment call; the six
NOTES deferrals; the env-gated live legs; LICENSE-absent-by-owner-intent) and required to
DISPOSITION rather than rediscover them.

Eight read-only specialist reviews, one per dimension named in the task:

| Dimension | Findings | Verdict |
|---|---|---|
| Application security / account-recovery abuse | SEC-1..4 (2 med, 2 low) | sound; two hardening gaps |
| Go concurrency / atomic repository semantics | BE-1..4 (1 med, 3 low) | ship-with-edits; **no fence-bypass or stale-write path exists** |
| pgx/turso schema, migrations, parity | DATA-1..3 (1 high, 2 low) | ship-with-edits |
| Architecture / module boundaries / API ergonomics | ARCH-1..4 (1 med, 3 low) | aligned-with-edits; all 15 guards green |
| SRE production wiring / secrets / workers / observability | SRE-1..7 (1 high, 3 med, 3 low) | ship-with-edits |
| JSON/HTML transport parity, CSRF/origin/redirect | HTTP-1..4 (1 med, 3 low) | ship-with-edits |
| templ view security / a11y / override ergonomics | VIEW-1..8 (2 med, 6 low) | ship-with-edits; **no view-layer security defect** |
| Release/upgrade docs + downstream adoption | DOC-1..7 (2 high, 3 med, 2 low) | ship-with-edits |

The four **high** findings: DATA-1 (the RELEASING.md Option-B turso re-enqueue emits
`datetime('now')`, which the turso connector's fixed-width `Time.Scan` cannot parse — every
re-enqueued row becomes poison: it sorts first in `Claim` but errors on the `RETURNING`
scan, so the worker error-loops and delivery never completes); SRE-1 (the `in_process`
worker path has **no `recover()`** — a provider/engine panic takes down the entire host,
HTTP included, where jobs mode is protected by the fenced runner's recover); and DOC-1 +
DOC-2 (the SWP surface — the new `sdk/capabilities/work` package and the `features/jobs`
signature change to `[]byte` / `work.Status` — is absent from BOTH `RELEASING.md` and the
NOTES release inventory, so the breaking/change record is incomplete for the exact tree
being cut).

**Cross-cutting escalation (two reviewers, independently).** The backend reviewer (BE-3)
and the SRE reviewer both escalated the standing live-store owner gate from "parked" to
the top residual risk in their dimension: the lease/generation fence — the entire point of
the AV3D hardening — has never executed against real Postgres or libSQL concurrency. Every
restart / stale-claim / resend proof ran against the in-memory fenced queue; the live
`RunFencedQueue` legs and the eight-proof `livedelivery` harness LOUD-SKIP with the DSNs
unset. Hermetic green must not stand in for it before production sign-off.

**Parked items: all seven upheld, none rediscovered.** Notably the SWP-3 `bytes.Clone` call
was independently ratified by both the architecture and backend reviewers (the clone sits on
the implementation-of-record's protocol surface, so payload isolation holds for every backing
store by construction, and `worktest` pins the semantic under `-race`); `DeliveryStatus.Attempt`=0
was upheld as architecturally correct (carrying the executor's retry counter through the
consumer status seam would push executor mechanics into the sdk protocol) with an SRE doc
note that tooling reading `attempt` must repoint to the `retried` health counter; and §5.8's
named error codes were confirmed NOT a parity defect (every code-carrying sentinel also wraps
an sdk kind, so both transports derive the same status — the only kind-less sentinels are the
rate-limit ones, which is HTTP-3).

The consolidated, severity-ranked disposition table (accept / reject-with-reason / defer) is
the deliverable of this task and was returned to the owner. AV3-9.8 remediation is a separate,
owner-gated step; no fixes were applied here and no PR was opened.

### 2026-07-13 — integrated reviewer addendum + draft AV3-9.8 plan

Purpose: integrate the independent Codex read-only review into the Claude reviewer-wave
record and draft the bounded AV3-9.8 execution order. This entry is **planning only**:
no product/docs remediation, PR, commit, tag, or release action is authorized by it.

#### Canonicalization precondition

Claude's entry above summarizes 41 findings by ID range and describes four high findings,
but the referenced 41-row disposition table is not embedded in this packet and no other
repository file contains those IDs. Exact row-for-row deduplication is therefore impossible
from the checked-in evidence alone. Before AV3-9.8 starts:

1. import the owner's canonical Claude table verbatim into this task record or attach a
   stable path to it;
2. merge rows that name the same file/contract and failure mechanism; preserve the stronger
   severity and the more complete reproduction;
3. keep genuinely independent failure mechanisms as separate rows even when their fixes
   touch the same file; and
4. obtain an owner decision on every `OWNER DECISION` row below. Do not infer approval from
   the recommended default.

The independent review ran read-only against the same uncommitted cut. Its hermetic evidence
was: `make check` green (all modules and 15 guards), template generation drift-clean,
`git diff --check` green, and fresh `-race -count=1` runs green for authentication inbound +
delivery, all jobs packages, and the auth-cms `authjobs`, `deliveryhealth`, and server
packages. These results do not close any live-environment gate.

#### Integrated disposition delta

`Merge` names a proven overlap with Claude's summary. `Possible overlap` means the full
Claude table is required before assigning its final canonical ID.

| ID | Severity | Contract and integrated finding | Merge / disposition | AV3-9.8 treatment |
|---|---|---|---|---|
| IX-01 | **Critical** | `RELEASING.md` delivery-runtime runbook, Option B: opaque copy cannot work in either dialect. Legacy ciphertext encodes the removed envelope, not the new versioned `deliverycmd.Envelope`; the copied legacy rail kind (`email`/`phone`) is not the registered `authentication.delivery` job kind; and the SQL never terminalizes the source rows despite requiring a zero source count. | **Supersedes/expands DATA-1. ACCEPT.** DATA-1's Turso `datetime('now')` poison is an additional defect inside the already-invalid option, not the root issue. | **OWNER DECISION:** recommended default is delete Option B and make drain-only normative. Alternative: authorize a real application migration that decrypts, converts, reseals, enqueues under the canonical kind, verifies, and terminalizes source rows. Never retain the opaque SQL copy. |
| IX-02 | High | `examples/auth-cms/cmd/server/main.go`: an unexpected `Service.RunDelivery` error is logged from an unsupervised goroutine while HTTP stays ready and continues admitting work. | Possible SRE overlap; distinct from summarized SRE-1 panic containment. **ACCEPT.** | Supervise HTTP + delivery as one lifecycle. Runtime exit must cancel the host or make readiness 503 and stop admission. Add injected-run-error coverage. |
| IX-03 | High | `examples/auth-cms` README/main: jobs mode is labelled recommended/durable while it uses `jobsmem.NewFencedQueue`; restart loses queued work and instances do not coordinate. | Possible SRE/DOC overlap. **ACCEPT.** | **OWNER DECISION:** recommended default is correct the example/docs to say non-durable jobs semantics. If the proof host is meant to prove durability, wire a durable backend and corresponding environment/runbook instead. |
| IX-04 | High | `security.go:browserOriginAllowed`: `Sec-Fetch-Site: same-site` bypasses the exact `Origin` allowlist, permitting an attacker-controlled sibling origin to submit credential-establishment forms. | Possible SEC/HTTP overlap. **ACCEPT.** | Auto-allow only `same-origin`; require exact allowlisted `Origin` for `same-site`. Add sibling-origin JSON/form regressions expecting 403. |
| IX-05 | High release gate | The AV3D pgx/Turso fenced-queue and proof-host `livedelivery` legs remain loud-skips/compile-only; the lease/generation fence has not run against real database concurrency after the refactor. | **Merge BE-3 + SRE cross-cutting escalation. ACCEPT.** | Owner supplies authorized fresh/reset pgx C-collation and libSQL environments. Run every fenced-store conformance and eight-proof `livedelivery` leg with `-count=1`; retain redacted evidence. No production sign-off on skips. |
| IX-06 | High | `RELEASING.md` secret inventory and proof-host wiring overstate “five distinct secrets, each rotatable.” The named inventory omits identifier-HMAC/provider-token AES and invents managed magic/reset/CSRF key material; several real keys are single-key/disruptive. | Possible SRE/DOC overlap. **ACCEPT.** | **OWNER DECISION:** choose continuity-supporting multi-key/key-ID work per secret or explicitly disruptive rotation procedures. Correct the inventory either way; document rolling-deploy, drain, session invalidation, and stored-token consequences. |
| IX-07 | High release gate, outside v3 | No `LICENSE` exists; the owner ledger already says first public tags are blocked. | Parked item upheld. **DEFER OUT OF V3 SCOPE.** | No AV3 code fix. Owner selects/adds an SPDX-identifiable license before any public tag. Keep this as a release stop, not a reason to widen AV3-9.8. |
| IX-08 | High | `in_process` delivery has no panic containment, so a provider/engine panic can terminate the host. | **Merge SRE-1. ACCEPT.** | Add a narrow recover boundary at the in-process execution boundary, emit terminal/lifecycle evidence without exposing payloads, and prove provider/engine panic behavior plus clean shutdown under `-race`. |
| IX-09 | High | The SWP package introduction and `features/jobs` `[]byte`/`work.Status` signature change are missing from the release/change inventories. | **Merge DOC-1 + DOC-2. ACCEPT.** | Update `RELEASING.md`, `NOTES.md`, SDK/jobs docs, module/tag floors, and downstream upgrade examples as one docs batch after public signatures settle. |
| IX-10 | Medium | No host invokes bounded terminal purge; durable rows and encrypted delivery metadata grow without limit despite the documented retention posture. | Possible SRE overlap. **ACCEPT.** | Add or document a host-owned scheduled purge loop with retention, batch size, errors/metrics, and shutdown semantics. Test bounded batches and scheduler failure. |
| IX-11 | Medium | Reset/passwordless templ pages scrub `#token` before POST; an error rerender has no fragment/token, so a valid reset cannot be corrected and retried. | Possible HTTP/VIEW overlap. **ACCEPT.** | Retain the token client-side until success without server rendering/logging it. Add a real browser regression: valid token + invalid password, then corrected retry succeeds. Keep distinct from the deferred host reset-link builder. |
| IX-12 | Medium | `deliveryhealth` computes `in_flight = admitted - terminals`, but increments before failed admission and on duplicate/replacement calls; it is not an authoritative backlog. | Possible SRE overlap. **ACCEPT.** | Prefer a durable nonterminal-count metric. Otherwise rename it to request accounting and remove the backlog claim; add failed-submit, idempotent-submit, and replace tests. |
| IX-13 | Medium | `examples/auth-cms/go.mod` omits requirements/replacements for the pgx/Turso modules imported by tagged live tests; `GOWORK=off go mod tidy -diff` tries to resolve sibling modules remotely. | Possible ARCH/DOC overlap. **ACCEPT.** | Put live harnesses in an integration-test module that owns driver dependencies, or make the example module graph self-contained. Gate with `GOWORK=off go mod tidy -diff` and tagged compile. |
| IX-14 | Medium | `RELEASING.md` claims missing `AllowedOrigins` and trusted-proxy wiring fail construction; `auth.NewService` accepts empty origins and cannot observe router proxy configuration. | Possible SRE/DOC overlap. **ACCEPT.** | Add explicit production acknowledgments/validation, or truthfully reframe these as host deployment checks and add proof-host negative tests. Do not retain a false construction guarantee. |
| IX-15 | Medium | §5.8 design promises `challenge_expired`, `challenge_invalid`, and `too_many_attempts`; implementation intentionally emits generic SDK-derived code strings. | **Conflict:** Claude rejects as non-parity because statuses match; independent review accepts as a stable machine-API contract break. **OWNER DECISION.** | Recommended default: treat the design's “stable machine codes, kept” language as normative and add an auth-specific mapper. Alternative: explicitly amend the design/public contract and record the breaking choice; status parity alone does not resolve code-string compatibility. |
| IX-16 | Medium | `/auth/logout` is cookie-driven but has neither origin nor CSRF protection; avoiding live-session middleware does not require accepting same-site sibling form posts. | Possible SEC/HTTP overlap. **ACCEPT.** | Add browser-origin/CSRF protection while preserving expired-access-token logout and bearer/native behavior. Add same-site sibling and expired-session regressions. |
| IX-17 | Medium, pre-existing | pgx cursor ordering depends on C/byte-wise collation. | Parked item upheld. **DEFER OUT OF V3 SCOPE.** | Keep the deployment prerequisite and assign a later shared datastore fix; do not spend AV3-9.8 on it. |
| IX-18 | Low | Hard-coded view CSP has no supported styling/asset policy for branded `Views` overrides. | Possible VIEW/ARCH overlap. **ACCEPT, subject to scope.** | **OWNER DECISION:** either add a constrained host policy/layout hook now, or explicitly narrow v3 override claims to markup-only and defer broader asset ergonomics. Never weaken secure defaults implicitly. |
| IX-19 | Low | In-process saturation/shutdown wraps `sdk.ErrConflict`, while HTTP special-cases it to 503; non-HTTP callers receive the wrong retry taxonomy. | Possible ARCH overlap. **ACCEPT, API-scope gate.** | Prefer a stable unavailable/backpressure kind or classifier. If adding an SDK kind is too broad for the cut, document and explicitly defer it rather than silently keeping conflicting semantics. |
| IX-20 | Low | `authsvc.Service` retains unused `mailer`/`mailFrom` fields after delivery ownership moved to `delivery.Router`. | Possible ARCH overlap. **ACCEPT.** | Remove dead internal dependency plumbing and stale old-delivery comments after behavioral batches, then rerun layering guards. |
| IX-21 | Informational | MFA, real SMS, `CompleteStepUpWithOAuth`, recovery-link builder, and PII audit retention/redaction remain intentionally absent. | Parked deferrals upheld. **DEFER OUT OF V3 SCOPE.** | Preserve triggers and avoid claiming support. Downstream hosts still document notifier and audit-retention choices. |
| IX-22 | Informational | `DeliveryStatus.Attempt == 0` is intentional executor/consumer separation. | Parked item upheld. **REJECT WITH EVIDENCE.** | No v3 change; document that operational attempt counts come from lifecycle/health events. Reconsider the field only in a breaking revision. |
| IX-23 | Informational | Central `bytes.Clone` at the SWP boundary correctly provides store-independent payload snapshot semantics. | **Merge Claude architecture/backend ratification. REJECT WITH EVIDENCE.** | Keep the clone; add protocol ownership/snapshot wording only if not already covered by DOC/SWP remediation. |

#### Owner gates before implementation

The owner approves the canonical merged table and resolves these choices before any
remediation batch begins:

1. **Option B:** delete and require drain-only (recommended), or authorize a real
   decrypt/convert/reseal migration tool.
2. **Proof-host promise:** document the in-memory jobs example honestly (recommended),
   or expand it into a durable-host example.
3. **Key rotation:** truthful disruptive runbooks only, or implementation of the
   necessary key-ID/dual-read mechanisms.
4. **§5.8 codes:** retain the named machine-code contract (recommended), or amend the
   design/docs and accept the compatibility change.
5. **View override scope:** secure constrained asset policy now, or markup-only v3
   promise with broader styling deferred.
6. **SDK unavailable taxonomy:** authorize the cross-SDK addition, or explicitly defer
   it as a post-v3 API cleanup.
7. **Live environments:** authorize fresh/reset pgx C-collation and libSQL databases
   for the fenced-queue and `livedelivery` gates.

LICENSE remains a separate release-owner gate and does not authorize AV3-9.8 scope.

#### Bounded remediation batches

Each batch stops on a failed regression and records exact files, tests, and any contract
adaptation before the next batch starts. Incorporate all accepted Claude-only findings
from the imported 41-row table into the closest batch; do not create a second track.

**Batch 1 — release/runbook correctness and public inventory**

- Resolve IX-01/DATA-1 first; no other release text may continue to point at an unsafe
  Option B. Add pgx and Turso legacy-row fixtures that prove the chosen migration/drain
  behavior and source-row accounting.
- Resolve IX-09/DOC-1/DOC-2 and the documentation portions of IX-03, IX-06, IX-14,
  IX-15, IX-18, IX-19, IX-22, and IX-23 after their owner decisions.
- Make `RELEASING.md`, `NOTES.md`, feature/jobs/SDK READMEs, and module/tag floors agree
  on the exact cut. Run doc guards and every runbook fixture affected by the edits.

**Batch 2 — browser and transport security/correctness**

- Resolve IX-04, IX-11, and IX-16 together because they share browser-origin/form
  behavior. Preserve native/bearer clients and JSON/form service-call parity.
- Add exact-origin sibling-host tests, logout-with-expired-session tests, and a browser
  retry drive for fragment-carried reset/passwordless tokens.
- Apply any accepted Claude SEC/HTTP/VIEW findings that touch the same routes/templates
  in this batch, deduplicated by contract.

**Batch 3 — runtime containment, lifecycle, and observability**

- Resolve IX-08/SRE-1 panic containment before IX-02 supervision so tests distinguish
  recovered job failure from an unexpected runtime-loop exit.
- Resolve IX-02, IX-10, and IX-12: lifecycle supervision/readiness, bounded terminal
  purge, and authoritative metrics. Then settle IX-03's proof-host wiring/docs choice.
- Run focused `-race -count=1` tests for panic, injected runtime exit, shutdown, retry,
  replace, purge, failed admission, duplicate admission, and readiness transitions.

**Batch 4 — module/API cleanup and remaining accepted Claude findings**

- Resolve IX-13, IX-18, IX-19, and IX-20 according to the owner decisions; run
  `GOWORK=off go mod tidy -diff` for the resulting example/integration modules.
- Apply remaining accepted Claude DATA/ARCH/SRE/VIEW/DOC findings not already absorbed
  by Batches 1–3, smallest-risk changes first. Every security/concurrency fix receives
  a regression; documentation-only hardening names the verified contract.
- Rerun all 15 layering/architecture guards after public or module-boundary changes.

**Batch 5 — fresh hermetic and live reverification**

Run, without test-cache substitution:

```sh
make generate
git diff --check
make check
make guard
POSTGRES_TEST_DSN='<redacted C-collation DSN>' \
  TURSO_DATABASE_URL='<redacted libSQL URL>' \
  TURSO_AUTH_TOKEN='<redacted>' make test-stores
```

Then run all fenced pgx/Turso conformance and proof-host `livedelivery` tests with
their live tags and `-count=1`, plus the affected JSON + HTML/browser proof-host legs.
Fresh/reset databases are mandatory; loud skips leave AV3-9.8 open. Redact endpoints,
tokens, codes, payloads, and key material in retained evidence. Stop all proof-host
processes and release ports afterward.

### 2026-07-13 — AV3-9.8 owner gate resolved: canonical table + all seven decisions

The owner resolved the canonicalization precondition and every owner gate. Remediation
is now authorized under the bounded-batch plan above with these bindings:

- **Canonical table.** The integrated **IX-01..IX-23 table above is adopted as the
  canonical disposition record.** It already merges/supersedes all four Claude high
  findings (IX-01⊃DATA-1, IX-05=BE-3+SRE escalation, IX-08=SRE-1, IX-09=DOC-1+DOC-2)
  and the cross-cutting escalation. The 37 unmerged Claude medium/low rows are
  represented by their per-dimension verdict summaries in the AV3-9.7 entry; the
  standalone 41-row export was never checked in and is not recoverable verbatim.
  Batch language reading "accepted Claude-only findings from the imported 41-row
  table" therefore resolves to: apply the per-dimension summarized intents where they
  are identifiable from the AV3-9.7 entry, and otherwise the IX table governs.
- **Gate 1 (Option B / IX-01): delete Option B; drain-only is normative.** No opaque
  SQL copy survives anywhere in release text. Fixtures prove drain verification and
  source-row accounting.
- **Gate 2 (proof-host promise / IX-03): document honestly as non-durable.** The
  auth-cms jobs mode is in-memory fenced — restart loses queued work, no cross-instance
  coordination. Wording fix only, no durable-backend example this cut.
- **Gate 3 (key rotation / IX-06): truthful disruptive runbooks.** Correct the secret
  inventory to the real key list (including identifier-HMAC and provider-token AES);
  document each key's actual rotation consequence. No key-ID/dual-read mechanisms.
- **Gate 4 (§5.8 codes / IX-15): the named machine-code contract is normative.** Add
  an auth-specific error-code mapper emitting `challenge_expired` / `challenge_invalid`
  / `too_many_attempts` etc., with JSON/form transport-parity regressions.
- **Gate 5 (view override scope / IX-18): markup-only v3 promise.** Docs narrow the
  override claim; the constrained host asset/styling policy hook is deferred post-v3.
  No CSP change.
- **Gate 6 (SDK taxonomy / IX-19): ADD the SDK unavailable/backpressure kind now**
  (owner chose the non-default). Batch 4 gains the cross-SDK kind and reclassifies the
  in-process delivery saturation/shutdown paths; HTTP mapping stays 503.
- **Gate 7 (live environments / IX-05): authorized.** Fresh/reset databases on the
  running `authv3-pg` (C-collation) and `authv3-libsql` containers for every fenced
  pgx/turso conformance and eight-proof `livedelivery` leg, `-count=1`, redacted
  evidence. No production sign-off on skips.
- LICENSE remains a separate release-owner gate outside AV3-9.8 scope (standing
  owner intent: deliberately absent).

### 2026-07-13 — AV3-9.8 Batch 1 executed (release/runbook correctness + public inventory)

Outcome: **PASS — docs + fixtures only (sole Go edits were comments); `make guard`
green (15 guards); drain fixtures green on both live dialects and torn down.**

Files: `RELEASING.md` (IX-01 Option B deleted, drain-only normative with removal
rationale + fixture-verification subsection; IX-09 `sdk/capabilities/work` NEW-module
keyed note + `features/jobs` SWP keyed note with before/after examples + tag-floor
row + IX-23 clone wording; IX-06 secret checklist rewritten to the real five keys with
true rotation stories; IX-14 construction claims reframed to real gates vs deployment
checklist), `NOTES.md` (dated Batch 1 correction entry incl. IX-18 asset-hook post-v3
deferral), `features/authentication/README.md` (IX-06 mirror, IX-18 markup-only
override promise, IX-22 Attempt==0/`retried`-counter note), `features/jobs/README.md`
(IX-23), `examples/auth-cms/README.md` + `cmd/server/main.go` comments (IX-03
non-durable honesty).

Fixture evidence: legacy `delivery_jobs` shape from git-HEAD `0014`; pgx disposable
`dr_drain_fixture` (C-collation) and libsql isolated `dr_`-prefix tables — seed 5
mixed-state rows → nonterminal count 2 → terminalize → 0 → total 5 before/after, both
dialects; fixtures dropped, standing conformance schemas untouched, containers left
running.

Contract adaptations: (1) **`features/jobs` floor is MINOR, not breaking** — no
exported signature changed incompatibly (`job.Status = work.Status` alias; fenced
surface opt-in); recorded with the new `sdk/capabilities/work` dependency. (2) The
real five keys are `AUTH_JWT_SECRET`, `AUTH_CHALLENGE_PEPPER` (only
rotation-continuity key via `HMACKeyRing`), `AUTH_DELIVERY_ENCRYPTER_KEY`,
`AUTH_TOKEN_ENCRYPTER_KEY`, `AUTH_IDENTIFIER_KEY` — the doc's "magic-link/reset" and
"CSRF" key materials were invented (ride the pepper / per-render random) and the
identifier-HMAC + provider-token AES keys were the omissions. (3) `auth.NewService`
confirmed to have no AllowedOrigins gate and cannot observe router proxy wiring —
RELEASING.md alone needed the IX-14 fix. (4) `sdk/README.md` already accurate for the
`work` surface — untouched.

### 2026-07-13 — AV3-9.8 Batch 2 executed (browser/transport security + §5.8 mapper)

Outcome: **PASS — IX-04, IX-16, IX-15, IX-11 all fixed with regressions; full
feature module + views/templ green `-race -count=1` (independently re-verified);
`make generate` idempotent, `git diff --check` clean, `make guard` green.**

Fixes: (IX-04) `browserOriginAllowed` auto-allows only `same-origin`; `same-site`
now requires the exact Origin allowlist (empty Origin rejected); native no-header
path unchanged. (IX-16) `/auth/logout` gated with `requireBrowserSafeOrigin` —
origin-only by design so expired-session logout keeps working; bearer/native
unaffected. (IX-15) new `errors.go` mapper `challengeErrorFor`/`respondDomainError`
emits `challenge_expired`(410)/`challenge_invalid`(400)/`too_many_attempts`(403)
from the stable sentinels via `errors.Is`, wired at verify/step-up/identifier-confirm/
remove-password/OAuth-unlink JSON arms and the form arm (`formFailure`) for parity;
statuses were already correct — only codes were generic. (IX-11) reset error-rerender
echoes the submitted token into a hidden `ResetPage.Token` field (round-trips the
POST body value only — no new exposure class, not logged; nonced script only
overwrites when a fragment is present); corrected retry now succeeds.

Key regressions: `TestCredentialEstablishmentRejectsSiblingOrigin`,
`TestMutationRejectsSiblingOrigin`, `TestRequireBrowserSafeOriginSameSite`,
`TestLogoutRejectsSiblingOrigin`, `TestLogoutSameOriginExpiredSessionSucceeds`,
`TestLogoutBearerUnaffectedByOriginGate`, `TestRespondDomainErrorNamedChallengeCodes`,
`TestVerifyJSON…{Invalid,LockedOut,Expired}Code`, `TestVerifyFormChallengeParityNoCodeLeak`,
`TestResetFormRetainsTokenAcrossErrorRerender`, `TestReset_ErrorRerenderRetainsToken`.

Contract adaptations: (1) no single transport error writer exists — the named codes
are an auth-local seam over `web.RespondJSONDomainError`, sdk mapper untouched.
(2) Passwordless and password-reset deliberately keep their collapsed generic
outcomes (`ErrPasswordlessLogin` 401 / `ErrPasswordResetInvalid`) — they never
surface the challenge sentinels; README documents this. (3) README error-code
section + `challenge.go` sentinel comment updated to shipped behavior.

Known gate note: `make check`'s templ-drift step diffs `*_templ.go` against HEAD;
the IX-11 `recovery_templ.go` regeneration is a legitimately dirty generated file in
this uncommitted milestone (generation idempotent, `.go` matches `.templ`) and will
clear when `.templ` + generated file are committed together. Batch 5 must account
for this when running `make check`.

### 2026-07-13 — AV3-9.8 Batch 3 executed (runtime containment, lifecycle, observability)

Outcome: **PASS — IX-08, IX-02, IX-10, IX-12 fixed with regressions; both modules
green `-race -count=1` (panic + supervision legs independently re-verified);
`make guard` 15/15.** IX-08 landed before IX-02 per the batch order.

Fixes: (IX-08) narrow recover boundary (`handleOnce`) in
`internal/logic/delivery/inprocess.go` wrapping exactly the per-job
`processor.Handle`; a panicking provider/engine dead-letters the job as a
PERMANENT failure (no re-panic on retry) with sanitized evidence — `runtime.Error`
messages surface verbatim, any other panic value reduces to its Go type so a
decrypted payload/destination can never leak; worker keeps running, slot released,
clean shutdown proven. (IX-02) `supervisor.go` `deliverySupervisor` — `run` wraps
the signal ctx in a cancelable `hostCtx`; an unexpected delivery-runtime exit
(error OR nil while deliveryCtx uncanceled) cancels the host so web.Run drains
through the documented shutdown order and main exits nonzero; normal shutdown
stays quiet. (IX-10) host-owned purge scheduler (`purge.go`, jobs-mode only):
`DELIVERY_PURGE_INTERVAL`=1h / `DELIVERY_PURGE_RETENTION`=24h /
`DELIVERY_PURGE_BATCH`=500, WARN+default on bad values, lifecycle wired like the
poller, purged count flows to the existing `purged` health counter; in_process is
ephemeral/self-bounding (max-entries + TTL) with no durable purge surface — nothing
accumulates. (IX-12) honest-rename path: no cheap authoritative nonterminal count
exists on the fenced surface (no Count/Depth/Stats; adding one = interface + three
stores, out of scope), so `in_flight`→`outstanding` reframed as derived request
accounting; `admitted` now counts only ACCEPTED Submit/Replace, superseded added to
the terminal subtraction; the authoritative backlog remains in_process's live
`queued/capacity/saturated`.

Key regressions: `TestInProcessRuntimeProviderPanicDeadLettersAndContinues`,
`TestInProcessRuntimeEngineStepPanicDeadLetters`, `TestInProcessRuntimePanicCleanShutdown`,
`TestSanitizePanicSurfacesRuntimeErrorsHidesArbitraryValues`,
`TestSuperviseDeliveryUnexpectedErrorCancelsHost`, `…UnexpectedCleanExitCancelsHost`,
`…NormalShutdownIsQuiet`, `TestDeliveryPurgeBoundedBatch`,
`TestDeliveryPurgeLoopContinuesAfterError`, `…CleanShutdown`,
`TestAdmittedCountsOnlyAcceptedRequests`, `TestOutstandingSubtractsSupersededTerminal`,
`TestOutstandingGaugeClampsAtZero`.

Contract adaptations: (1) supervision extracted behind a `deliveryHealthMarker`
interface for unit-testability without booting `run()`. (2) `.env.example` still
carried the stale "jobs = durable/survives restart" claim Batch 1 fixed elsewhere —
corrected under the IX-03 gate-2 decision, plus the three purge knobs added.
(3) README/main comments re-synced to the shipped lifecycle (supervision, purge,
`outstanding` semantics).

### 2026-07-13 — AV3-9.8 Batch 4 executed (module/API cleanup + SDK unavailable kind)

Outcome: **PASS — IX-19, IX-13, IX-20 fixed with regressions; IX-18 residue confirmed
(no code); sdk/features/authentication/features/jobs/examples/auth-cms green
`-race -count=1`; `GOWORK=off go mod tidy -diff` clean; all 15 layering guards green;
repo-wide `make build` green (37 modules).**

Fixes:

- **IX-19 (owner gate 6 — ADD the SDK kind).** New kernel sentinel
  `sdk.ErrUnavailable` ("unavailable") in `sdk/errors.go` with a doc comment defining
  backpressure/shutdown/degraded-dependency semantics (retry unchanged) explicitly
  distinct from `ErrConflict` (state contention, retry may differ); added to
  `expectedErrors` (so `IsExpected` covers it). Mapped in `sdk/foundation/web`
  `ErrFromDomain` → `ErrUnavailable(...)` = **503** code string **`unavailable`** (the
  pre-existing 503 constructor; no new writer machinery, no Retry-After — the writer
  has no header seam). Reclassified the two in-process delivery admission rejections
  `ErrDeliveryCapacity` / `ErrDeliveryClosed` (features/authentication
  `internal/logic/delivery/inprocess.go`) from `sdk.ErrConflict` → `sdk.ErrUnavailable`;
  `ErrDeliverySuperseded` and `ErrInProcessAlreadyRunning` deliberately KEEP
  `sdk.ErrConflict` (genuine state contention, not backpressure). HTTP special-cases
  simplified: the `passwordless` start and the HTML `formFailure` arms dropped their
  `deliveryUnavailable(err)` branch — the domain-error writer now yields 503 by kind;
  the two enumeration-safe forgot paths (JSON + form, which deliberately default to 500)
  keep the `deliveryUnavailable` helper (still a precise two-sentinel check, no import
  churn). Other consumers of the conflict-wrap checked: `storetest`, `invitationsvc`,
  `authsvc` CAS paths, and the stores all wrap `ErrConflict` for optimistic-lock/state
  reasons unrelated to delivery — none touched. Where produced/mapped now:
  produced by the two delivery sentinels; mapped to 503 by `web.ErrFromDomain` (and via
  `RespondJSONDomainError` / `formFailure`). No non-test HTTP behavior changed (still 503).

- **IX-13 (example module graph self-contained).** Chose **Option B** (self-contained
  example `go.mod`), following the repo's established precedent: `examples/cms/go.mod`
  already requires its datastore/store modules directly with local replaces, and there
  is NO integration-test-module precedent anywhere in the tree. Moving the harness into a
  new module (Option A) was infeasible surgically — `jobs_delivery_live_test.go` is
  `package main` and reuses the untagged hermetic helpers (`payloadRecord`, host
  composition), so a split would force a large shared-helper extraction. Added four
  direct requires + local replaces to `examples/auth-cms/go.mod` (features/jobs/stores/
  {pgx,turso}, integrations/datastores/{pgxdb,turso}); `GOWORK=off go mod tidy` populated
  the pgx/libsql indirect block. No new module → `go.work` unchanged (all four already in
  it), `make check` module discovery unaffected.

- **IX-20 (dead mailer plumbing).** Removed the unused `Service.mailer`/`mailFrom` fields
  and their wiring in `authsvc/service.go`; the fields had zero non-test reads (delivery
  ownership is the `delivery.Router`, which carries its own `r.mailer`). Also removed the
  now-orphaned internal `authsvc.Deps.Mailer`/`MailFrom` entries (nothing read them — the
  production router is built from `cfg.Mailer` in `authentication.go`, and tests wire
  delivery via `wireSyncDelivery`) and dropped the orphaned `sdk/capabilities/email`
  import + the `d.Mailer`/`d.MailFrom` populators in `authentication.go` and ~30 test
  literals across the authsvc + inbound packages. Fixed the stale `Deps.Deliver` comment
  ("Wired whenever the Mailer is"). **Public surface unchanged: `auth.Config.Mailer`/
  `MailFrom` still accepted and still flow to the delivery router** (authentication.go
  `delivery.NewRouter(delivery.Deps{Mailer: cfg.Mailer, ...})`).

- **IX-18 residue (gate 5).** Confirmed the markup-only override promise + hard-coded-CSP
  note is present in `features/authentication/README.md` (§ Views/override, "The v3
  override promise is markup-only"); no Batch 4 change touches CSP or the override claim.
  No code work (asset-policy hook is post-v3 deferred).

New regressions: `sdk/foundation/web` `TestErrFromDomain_Kinds` (Unavailable→503,
Conflict stays 409, etc.); delivery `inprocess_test.go` updated to assert
`ErrDeliveryCapacity`/`ErrDeliveryClosed` wrap `sdk.ErrUnavailable` and NOT
`sdk.ErrConflict`; inbound `TestFormFailureDeliveryUnavailableMapsTo503` (capacity/closed/
raw-unavailable → 503, conflict → 409). The pre-existing HTTP saturation suite
(`TestForgotPasswordSaturationReturns503NotAccepted`,
`TestPasswordlessStartSaturationReturns503NotAccepted`) stays green — 503 now via the new
kind.

Premise adaptations: (1) **No IX-19 "kind pending" note existed to update.** The task
premise said Batch 1's `RELEASING.md` IX-19 note flagged the kind as pending; grep of
`RELEASING.md`/`NOTES.md`/READMEs found no such note and the delivery README section never
named the saturation error kind — so there was no stale conflict-semantics doc to correct
(Batch 1 deferred IX-19 entirely to Batch 4). (2) **IX-13 = Option B, not a new module**,
per the examples/cms precedent + the harness's package-main coupling (rationale above).
(3) IX-20 removed the internal `authsvc.Deps` mailer entries too (not just the Service
fields) because they were fully orphaned once the Service fields went; the public
`auth.Config` surface is untouched.

Verification: `sdk` `go build && go vet && go test -race -count=1 ./...` → ok;
`features/authentication` `go test -race -count=1 ./...` → all ok; `features/jobs`
build/vet/test → ok; `examples/auth-cms` build/vet/`go test -race -count=1 ./...` → ok;
`GOWORK=off go mod tidy -diff` (and normal) → clean, EXIT 0; tagged compile
`go vet -tags='livedelivery integration' ./cmd/server` → EXIT 0 both `GOWORK=off` and
normally; `make guard` → EXIT 0, **15 guards**; `make build` → EXIT 0, all 37 modules.
No PR/tag/push; no pre-existing uncommitted change reset. The Batch 2 `recovery_templ.go`
templ-drift caveat is the known accepted state (not touched by Batch 4).

### 2026-07-13/14 — AV3-9.8 Batch 5 gate run #1 (INTERIM — two defects found, fix round in flight)

Verifier ran the full gate sequence. Results:

- `make generate` ×2 → byte-idempotent; `git diff --check` clean. **PASS.**
- templ drift → only the known Batch-2 `recovery_templ.go` exception. **PASS.**
- per-module vet+build+test (36 modules) → 35 clean; **FAIL: sdk module —
  `sdk/foundation/cryptids` `TestHS256TamperedSignatureRejected` FLAKY (3/20
  isolated `-count=1` runs fail).** Security-relevant: must be proven a
  test-construction bug, not a verifier accepting tampered signatures.
- integration-tag vet (5 turso modules) → **PASS.**
- `make guard` → **PASS** 15/15.
- fresh dual-store `make test-stores` → **PASS 10/10 legs**, all `-count=1`,
  fresh DBs (pg `authv3_cconf` recreated C-collation; libsql fully wiped — the
  wipe removed a stale pre-AV3D bespoke `delivery_jobs` table, proving the reset
  was load-bearing).
- fenced-queue live conformance (`-race -count=1`, first-ever real-DB run) →
  **PASS 41/41 subtests BOTH dialects** (pgx ok 17.3s, turso ok 13.8s). The
  IX-05 fenced-store leg is green.
- eight-proof livedelivery harness (`-tags='livedelivery integration' -race
  -count=1`, both DSNs, zero skips) → **FAIL, FLAKY**: different subtests fail
  across runs. pgx run A: KnownUnknownOpaqueAdmissionParity ("delivered 2, want
  exactly 1"), ProviderTimeoutAndRetryOffRequestPath ("running, want completed"),
  RestartAfterCheckpointResendsSameSecret (new secret minted on retry),
  RestartAfterProviderAcceptanceResendsSameSecret (resend was an UNRELATED
  earlier message — Verify email in a Reset proof), ResendConvergesToLatestGeneration
  ("delivered 2, want 1"). Run B: different subset. turso: two proofs each failed
  once/passed once; ProviderTimeoutAndRetryOffRequestPath failed once WITHOUT
  `-race`. Suspected mechanisms: harness margins tuned for in-memory speed
  (lease TTL vs `-race`-slowed handle cycle → legitimate at-least-once resend),
  poll deadlines, and cross-proof state bleed (the unrelated-message failure) —
  but a real fence defect is NOT yet excluded and would be a milestone stop
  condition.
- post-run sweep → containers running, no leftover processes/ports, git tree
  unchanged by the gate run. **PASS.**

Two parallel fix/diagnosis rounds dispatched (cryptids flake; livedelivery
harness), each under the explicit discipline: prove test/harness bug and fix it
without weakening any semantic assertion, or prove a product defect and STOP
(stop condition — owner decision). Batch 5 remains OPEN; no completion claim.

#### AV3-9.8 completion record

The PR-ready handoff must contain one canonical severity-ranked table with every Claude
and independent-review row marked fixed, rejected-with-evidence, or deferred-out-of-v3-
scope; owner decisions; files changed; regression names; hermetic and fresh live evidence;
runbook fixture results; remaining release gates (including LICENSE); and confirmation
that no PR, tag, or push occurred without separate authorization. Do not mark AV3-9.8 or
the milestone complete while the canonical Claude table is absent, a live leg skips, or
IX-01's unsafe migration path remains published.

### 2026-07-14 — AV3-9.8 Batch 5 completed + PR-ready completion record

Outcome: **PASS — AV3-9.8 COMPLETE.** The gate-run #1 defects were both
root-caused as test/harness bugs (no product defect), fixed, and re-proven;
every gate then passed with zero skips; the IX-11 real-browser drive passed
9/9 checks. No PR, tag, push, or commit was made — that remains the owner's
workflow.

**Gate-run defect resolutions (both World B — no product code touched):**

- `TestHS256TamperedSignatureRejected` flake = TEST bug: the tamper flipped the
  token's final base64url character (`'A'`→`'B'`), which differs only in the two
  discarded padding bits of a 43-char RawURL signature — ~1/16 runs the
  "tampered" token decoded to byte-identical MAC bytes and correctly verified.
  The product verifier (constant-time `hmac.Equal` over decoded bytes) is
  CORRECT. Fix: decode, flip a bit in `sig[0]` (full data byte), re-encode,
  assert difference pre-verify. Stability: `-count=500` green + full sdk module
  `-race` green + independent `-count=200` re-check.
- livedelivery eight-proof flakes = HARNESS margins: (1) setup drains called
  `stopRuntime` in the window between a successful `Send` and the `Complete`
  commit, leaving the registration-verification job reclaimable; its 300ms lease
  lapsed and a later worker legitimately resent it into the next proof's
  observation window (the "unrelated Verify email", "delivered 2 want 1", and
  "new secret minted" failures — all the same stray); (2) `Complete` racing the
  final `stopRuntime` left jobs "running" at assert time; (3) a poll loop broke
  on `Retries>=2` before the cross-goroutine `hang.count()` caught up. The fence
  behaved exactly to its at-least-once contract in every case. Fixes (harness
  file only, `jobs_delivery_live_test.go`): `waitLiveJobTerminal` /
  `waitLiveQueueDrained` helpers so setup and observation phases reach durable
  terminal state before `stopRuntime`; poll loop requires BOTH counters;
  `ProcessTimeout` 60ms→400ms (still ≪ the 3s lease). NO semantic assertion
  weakened (exactly-1, same-secret, completed, retries≥2 all still enforced).
  Acceptance: 3 consecutive full `-race -count=1` runs green both dialects +
  no-race run + 8/8 turso stability + one independent confirmation run
  (`ok 20.141s`). `make guard` re-run green (exit 0).

**IX-11 real-browser drive (the Batch 2 deferred leg) — 9/9 PASS** (headless
Chromium via Playwright against a live `go run ./cmd/server` boot on :8082, dev
mode, console transports, worker live): reset landing loads; fragment scrubbed
from URL/history; hidden field carries the token after the bare-fragment read;
valid-token + short-password → error rerender RETAINS the token (the IX-11 fix,
live); raw token appears exactly once in the DOM (the hidden field); corrected
retry PRGs to `/auth/login`; login with the NEW password succeeds; the OLD
password is rejected. Token egress: exactly 1 occurrence in the host log — the
console mailer's email body (development-only transport, production-rejected by
construction) — and ZERO in any handler/service log line. Drive-harness note:
the bundled landing's contract is a BARE fragment (`#<token>`); a first drive
attempt using `#token=<value>` failed by harness error, consistent with the
recorded IX-21 `#token=` link-builder deferral. Host stopped, port 8082
released, containers left running.

**Canonical disposition table — final outcomes** (canonical per the owner's
2026-07-13 adoption: the IX table governs; unmerged Claude medium/low rows are
represented by the AV3-9.7 per-dimension summaries and were absorbed into the
batches where identifiable):

| ID | Sev | Outcome |
|---|---|---|
| IX-01 | Critical | **FIXED** (B1): Option B deleted, drain-only normative; drain fixtures green both dialects (seed 5 → nonterminal 2 → terminalize → 0; accounting exact; fixtures dropped) |
| IX-02 | High | **FIXED** (B3): delivery supervisor cancels host on unexpected runtime exit; injected-error + quiet-shutdown regressions |
| IX-03 | High | **FIXED-docs** (gate 2, B1+B3): proof host documented honestly as in-memory fenced/non-durable (README, main.go, .env.example) |
| IX-04 | High | **FIXED** (B2): `same-site` no longer auto-passes — exact Origin allowlist required; sibling-origin 403 regressions JSON+form, both gates |
| IX-05 | High gate | **CLOSED** (B5): fenced live conformance 41/41 both dialects `-race`; eight-proof livedelivery green 3× consecutive `-race` + no-race + independent run, fresh DBs, zero skips; harness-margin fixes only — verdict: no fence defect |
| IX-06 | High | **FIXED-docs** (gate 3, B1): truthful five-key inventory (JWT_SECRET, CHALLENGE_PEPPER, DELIVERY_ENCRYPTER_KEY, TOKEN_ENCRYPTER_KEY, IDENTIFIER_KEY) + real per-key rotation consequences; invented keys removed |
| IX-07 | High gate | **DEFERRED out of v3**: LICENSE remains the owner's standing release stop |
| IX-08 | High | **FIXED** (B3): narrow recover at per-job execution; panic → sanitized dead-letter, worker survives, clean shutdown under `-race` |
| IX-09 | High | **FIXED-docs** (B1): SWP inventory in RELEASING.md + NOTES; adaptation — `features/jobs` floor is MINOR (source-compatible alias), `sdk/capabilities/work` NEW module |
| IX-10 | Med | **FIXED** (B3): host purge scheduler (interval/retention/batch env knobs), bounded-batch + error-continue + shutdown regressions |
| IX-11 | Med | **FIXED** (B2+B5): error rerender retains token via hidden-field echo; httptest + templ regressions + 9/9 real-browser drive; token never logged |
| IX-12 | Med | **FIXED** (B3): honest rename `in_flight`→`outstanding` (request accounting); accepted-only admission counting; superseded subtracted |
| IX-13 | Med | **FIXED** (B4): example go.mod self-contained (requires+replaces per examples/cms precedent); `GOWORK=off go mod tidy -diff` clean + tagged vet clean |
| IX-14 | Med | **FIXED-docs** (B1): construction claims reframed truthfully (NewService has no AllowedOrigins gate; proxy wiring is a host deployment check) |
| IX-15 | Med | **FIXED** (gate 4, B2): named machine codes shipped via auth-local mapper (`challenge_expired` 410 / `challenge_invalid` 400 / `too_many_attempts` 403), JSON+form parity regressions; passwordless/reset deliberately stay collapsed-generic |
| IX-16 | Med | **FIXED** (B2): `/auth/logout` origin-gated (origin-only by design — expired-session logout preserved); sibling/expired/bearer regressions |
| IX-17 | Med | **DEFERRED out of v3**: pgx C-collation deployment prerequisite stands (documented); shared datastore fix later |
| IX-18 | Low | **FIXED-docs** (gate 5, B1): markup-only override promise; asset-policy hook recorded as post-v3 deferral |
| IX-19 | Low | **FIXED** (gate 6 non-default, B4): `sdk.ErrUnavailable` added, mapped 503/`unavailable`; delivery capacity/closed reclassified from ErrConflict; redundant transport special-cases removed; 503s regression-pinned |
| IX-20 | Low | **FIXED** (B4): dead `mailer`/`mailFrom` Service plumbing removed; public `auth.Config` unchanged (mailer still flows to the delivery router) |
| IX-21 | Info | **DEFERRED upheld**: MFA, real SMS, `CompleteStepUpWithOAuth`, `#token=` link builder, PII-audit retention lifecycle |
| IX-22 | Info | **REJECTED w/ evidence** + doc note (B1): `Attempt==0` intentional; operational counts come from lifecycle/`retried` |
| IX-23 | Info | **REJECTED w/ evidence** + wording (B1): central `bytes.Clone` kept; snapshot semantics documented |

Plus two gate-discovered defects, both **FIXED** as test/harness bugs (above);
the HS256 one belongs to the JWT-refresh workstream's code but rode this
verification.

**Final verification state:** `make generate` idempotent; `git diff --check`
clean; per-module vet+build+test green 36/36 (after the HS256 test fix); `make
guard` 15/15; fresh dual-store `make test-stores` 10/10 legs `-count=1` on
recreated C-collation pgx + fully wiped libsql; fenced live conformance 41/41
×2 dialects `-race`; livedelivery 8-proof green ×4 runs; proof-host browser
drive 9/9. Sole known `make check` caveat: the templ-drift step flags
`recovery_templ.go` as dirty vs HEAD — expected for an uncommitted generated
file whose `.templ` changed with it (generation idempotent); clears when both
are committed together.

**Remaining release gates (outside AV3-9.8):** LICENSE (owner's standing stop —
blocks first public tags); turso CI secrets upload; committing the milestone
tree and opening the PR (owner's normal workflow); tagging per the RELEASING.md
floors (feature + both auth store modules = major, `views/templ` + `work` =
first tags, `features/jobs` = minor, turso datastore = patch, sdk capabilities
= minor).

Confirmation: **no PR, tag, push, or commit was created at any point in
AV3-9.8.** Reviewer wave ran once (AV3-9.7) and was not restarted; the only
post-remediation review activity was the targeted verification runs above.
