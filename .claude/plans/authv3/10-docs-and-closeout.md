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
