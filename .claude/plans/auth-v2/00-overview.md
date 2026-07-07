# auth-v2 — milestone overview

Status: **RATIFIED** — phases cut 2026-07-07 from the ratified design at
`.claude/plans/roadmap/auth-v2-feature-design.md` (the DESIGN DOC —
ratified 2026-07-07, all AV defaults, NOTES.md entry is the record; every
phase references it by section; **do not re-decide anything it decides**).
Milestone: `auth-v2` — the identity remainder inside `features/auth` + its
two store modules: v1 debts, OAuth flows, API keys + service accounts, JWT
bearer mode, the security-event audit rail, and ReBAC-decoupled
invitations. **Plan-cut gate run 2026-07-07**
(architecture-steward / lead-backend-engineer / platform-sre /
data-integration-reviewer), **4× ratify-with-amendments, amendments
applied** across these files (fold-in logged in the execution log; one
conscious design amendment — ChangePassword's delete-all+remint — landed
in the design doc's §7.2 + status header). **CUT-RATIFIED 2026-07-07
(jrazmi)** — including explicit confirmation of the §7.2
delete-all+remint amendment. Execution-ready; queued behind repo-hardening
phases 1–3 per the ratified order. First leg: A1 (`01-debts.md`).

## Inherited law

The constitution (`restructure/00-overview.md`, rules 1–8), the roadmap
rulings (R1–R10, `roadmap/00-intersections.md`), the trio layout
(`logic/<domain>` public rims; `internal/logic/authsvc` +
`internal/inbound/http` interior; `stores/` as sibling modules), store
posture C with the supported set **{turso, pgx}** (store modules named for
the driver package, R-KV2/R-KV3 — `stores/pgx`, never `stores/postgres`),
R3 (memstore placement — auth's reference stays in `storetest` +
`examples/auth-cms/internal/authmem`; no in-core memstore, no
`stores/memory`), R4 (`storetest` naming, port-set sub-runners), and the
charter (`features/README.md`, checklist items 1–12) all apply unchanged.

The **2026-07-06 authorization ruling** governs every seam here: ReBAC
supported, never required — consumer-declared narrow ports, structural
satisfaction, documented nil semantics, deny-by-absence route
registration. Naming rule stands: authorization/authorizer, never
authz/authn.

**The ratified AV table (design §12, all eleven — executors must not
relitigate):** AV1 (authorization is its own module `features/authorization`
— NOT this milestone), AV2 (consumer seams are Check-only), AV3 (two
milestones — this is the first), AV4 (invitations decoupled via the
grant-on-accept `Granter` port, deny-by-absence), AV5 (no principals
registry table — actor references are `(subject_type, subject_id)` string
pairs; `auth.Principal` is a value type), AV6 (JWT = stateless short-TTL
*user* tokens, no refresh; machine clients authenticate via API keys), AV7
(OAuth mobile flow + code-gated unlink OUT), AV8 (`RequireVerifiedEmail`
defaults false), AV9 (`Repositories.SecurityEvents` optional), **AV10
(the durable security-event rail is DEFERRED — struck A8, disposition
below)**, **AV11 (auth-v2 is DECOUPLED from events-v1 — zero events
dependency in this milestone)**.

- **Executor model policy (jrazmi): implementation phases on
  `model: opus`; design/doc-judgment phases on `model: fable` (A10 is the
  fable phase). Never sonnet.**

## Phases (design §13 numbering kept — A8 is STRUCK per ratified AV10)

| Phase | File | What | Executor model |
|---|---|---|---|
| A1 | `01-debts.md` | v1 debts: service-side session hashing, ChangePassword + `DeleteByUser`, `RequireVerifiedEmail` knob (design §7) | opus |
| A2 | `02-oauth.md` | OAuth: `logic/oauthaccount` + `logic/oauthstate`, flow orchestration, Config + routes (design §3) | opus |
| A3 | `03-machine-identity.md` | API keys + service accounts + principal resolution + middleware (design §4.1–§4.3) | opus |
| A4 | `04-jwt-bearer.md` | JWT bearer mode: `Config.TokenSigner`, `POST /auth/token`, bearer verification (design §4.4) | opus |
| A5 | `05-security-events.md` | Security events — the synchronous audit rail ONLY (design §5.1; §5.2 is deferred) | opus |
| A6 | `06-invitations.md` | Invitations: `logic/invitation`, Granter/MemberCheck seams, routes, resolve-on-registration (design §6) | opus |
| A7a | `07a-store-turso.md` | `stores/turso` extension: migrations 0006–0011, six new repositories, live leg (design §10, §9) | opus |
| A7b | `07b-store-pgx.md` | `stores/pgx` extension: identical version filename set, live leg (design §10, §9) | opus |
| ~~A8~~ | — (no file) | **STRUCK (ratified AV10)** — the durable rail; disposition below | — |
| A9 | `09-proof-host.md` | `examples/auth-cms` extension + the full proof protocol (design §13 A9, as amended) | opus |
| A10 | `10-docs-sync.md` | READMEs, nil-semantics tables, the session-hashing UPGRADE NOTE, wiring page, capability-map rows, records, fresh-eyes | fable |

Dependencies: A2 and A3 need A1 (both touch `authsvc`; they are
parallelizable with each other). A4 needs A3. A5 needs A2+A3 (it wires
audit writes across their ops). A6 needs A1+A5. A7a needs A2–A6; A7b needs
A7a (identical version-filename set is authored once, in A7a). A9 needs
A2–A6 (A4 included). A10 needs everything.

**Deferred — the durable security-event rail (struck A8, ratified AV10).**
Recorded disposition, NOT a phase of this milestone: trigger = the first
real durable consumer (webhooks/alerting). Scope, governing law
(design §5.2: the events-contract re-check gate, the combined-guarantee
statement, the no-`features/events`-import acceptance grep), and
dependencies-when-cut (A5, A7a/A7b, events-v1 shipped) live in the design
doc's §5.2 + §13 deferred-disposition block. It is the only
auth-v2-designed work with an events-v1 dependency (AV11).

**authorization-v1 (Z1–Z5) is NOT cut here.** Its phase files are cut from
design §13 when that milestone's window opens; nothing in this milestone
imports or anticipates `features/authorization` (the toy Granter in A9 is
the point: invitations provably work without it).

## Sequencing & coordination notes

- **repo-hardening phases 1–3 land first** — code enters git before this
  milestone executes; hygiene gates before any push.
- **This milestone adds ZERO new modules.** Everything folds into
  `features/auth` + `features/auth/stores/{turso,pgx}` +
  `examples/auth-cms`. The Makefile MODULES / STORE_MODULES / go.work sets
  are untouched (the +3 module count belongs to authorization-v1).
- **No G5 guard yet**: the feature→feature import guard ships in
  authorization-v1's Z5. Until then the **manual rule-6 greps** in each
  phase's acceptance stand in for it.
- The session-hashing forced-logout **UPGRADE NOTE (README + RELEASING) is
  an A10 deliverable** (design §7.3), not only a NOTES artifact.
- `features/auth/go.mod` must still require **exactly** `sdk` at milestone
  close (charter item 2) — no new dependency enters the core; the JWT
  integration is host-wired only (A4 tests use an in-package fake signer).

## Loop protocol

Same as auth-v1/jobs-v1: one phase per leg; read this overview + the phase
file + THE DESIGN DOC fully; preconditions → work items in order →
acceptance → real-interaction check → dated execution-log entry → stop.
Surgical diffs; goimports; premise-false → closest correct thing + log
divergence; constitution/ratified-decision conflict → STOP and flag.

**Standing real-interaction check (a) — every phase:** `make check` green
(the current 26-module set + all four guards), boot `examples/minimal`
(:8081), `GET /` and `GET /products/widget-3000` → 200s, kill, port free.

**Auth-flow check (b) — phases that touch auth behavior (A1–A6, A9):**
the auth-cms cookie-jar flow (read `examples/auth-cms/cmd/server/main.go`
for the port; plans assume :8082): 401 → register 201 → login 200+cookie →
admin 200 → logout 200 → 401. From A9 on, the host runs with
`RequireVerifiedEmail=true`, so the flow gains a verify step (register →
code from the console-mailer log → `POST /auth/verify` → login) and a
login-before-verify → 403 assertion; A1–A6 run it against the
then-current host config. Report exact codes.

**A9 proof protocol:** `09-proof-host.md` carries the full protocol from
design §13 A9 as amended — the OAuth fake-provider leg, the API-key
machine call, the JWT-bearer leg (incl. expired-token and absent-signer
paths), the toy-Granter invitation flow, audit rows visible, curl
transcripts. Green tests alone do not close A9.

**Live-store gates (A7a/A7b):** turso leg `-tags=integration` + `TURSO_*`
— the ONLY authorized database is
`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`
(verify the env URL matches before ANY run; the .env may point elsewhere);
pgx leg env-gated on `POSTGRES_TEST_DSN` (docker, postgres:17). Loud skips
mid-milestone are fine; milestone close requires one recorded live
conformance run per store as dated NOTES.md artifacts — never a hermetic
green. **Live legs run manually/locally by the loop executor** —
consistent with RH4's manual-dispatch CI posture; the playground token
never enters CI logs.

## Acceptance for the milestone as a whole

- All 10 phases' execution logs green; `make check` green across the
  unchanged 26-module set + guards.
- Recorded live conformance runs per store (turso playground + pgx docker)
  as dated NOTES.md artifacts, covering every new storetest sub-runner.
- The A9 proof protocol passes end to end (all legs, exact codes
  recorded).
- Rule 6 clean both directions (import-anchored — plain-text greps
  false-fail on doc comments, plan-cut gate finding):
  `grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
  → empty, and the reverses (same form, swapped roots) → empty.
- **Deferred-rail absence, enforced at close** (the only enforcement —
  A5 runs it at phase time but A7a/A7b add store code after):
  `grep -rn --include='*.go' '"github.com/gopernicus/gopernicus/\(sdk/events\|features/events\)' features/auth/`
  → empty.
- Charter item-12 nil-semantics rows shipped in the README for **every**
  new `Config`/`Repositories` port (Providers, TokenEncrypter,
  OAuthCallbackBase/RedirectAllowlist, TokenSigner, Granter, MemberCheck,
  and the six new repository ports), including the deny-by-absence
  couplings and the loud partial-wiring errors.
- `features/auth/go.mod` requires exactly `sdk`.

## Execution log

(planning-leg and cross-phase entries here; per-phase logs in each file)

### 2026-07-07 — planning leg: phase files cut

Cut `00-overview.md` + phases A1–A7b, A9, A10 from the ratified design's
§13 (A8 struck per ratified AV10 — recorded as the deferred disposition
above, no phase file; authorization-v1's Z1–Z5 deliberately not cut —
their window has not opened). No code touched. **Cut-time refinements**
(operationalizations of details the design left unpinned — logged per the
jobs-v1 precedent, none is a design change):

1. `SessionRepository.DeleteByUser` semantics pinned (A1): bulk +
   idempotent — deletes all sessions for the user, returns nil when zero
   rows exist (bulk deletes never `ErrNotFound`); storetest case shape
   specified in the phase file.
2. ChangePassword session policy pinned (A1): delete ALL of the user's
   sessions via `DeleteByUser`, then mint a fresh session for the caller
   and set the new cookie in the response — simpler and strictly safer
   than delete-all-but-current.
3. `APIKeyRepository.GetByHash` sentinels pinned (A3): unknown or revoked
   → `errs.ErrNotFound`; present-but-expired → `errs.ErrExpired`
   (mirrors the session-port precedent).
4. Machine lifecycle route paths pinned (A3) — the design specified
   "minimal session-gated JSON lifecycle routes" inside `/auth/*` without
   paths; the phase file fixes them.
5. Machine partial-wiring rule pinned (A3): `Repositories.APIKeys` and
   `Repositories.ServiceAccounts` are both-or-neither; one without the
   other is a loud construction error.
6. Client-info capture pinned (A5): IP/User-Agent ride a feature-internal
   context carrier set by the feature's own HTTP layer (the
   identity-in-context precedent — unexported, no sdk change).
7. `Granter`/`MemberCheck` placement pinned (A6): `Config` fields on
   `auth.Config` (collaborators, not repositories).
8. Migration filename order pinned (A7a, authored once): 0006_oauth_accounts,
   0007_oauth_states, 0008_service_accounts, 0009_api_keys,
   0010_security_events, 0011_invitations.
9. A9 protocol operationalized: the standing (b) flow gains the verify
   step (the host runs `RequireVerifiedEmail=true`); the absent-signer JWT
   path is a second boot variant (env-flagged); audit-row visibility via a
   dev-only host-local `GET /debug/security-events` route (host code, not
   feature surface).
10. JWT unit-testing pinned (A4): in-package fake `JWTSigner` (G2 keeps
    golang-jwt out of the feature core); the real
    `integrations/cryptids/golang-jwt` is exercised host-side in A9.

Next: the plan-cut review gate (tier-review question + platform-sre +
data-integration-reviewer), then leg 1 = A1 (`01-debts.md`, opus).

### 2026-07-07 — plan-cut gate fold-in (4× ratify-with-amendments applied)

Gate ran (architecture-steward, lead-backend-engineer, platform-sre,
data-integration-reviewer); consolidated amendments folded into every
phase file. Highlights, and supersessions of the planning-leg entry above:

- **Refinement 2 RECLASSIFIED as a conscious design amendment** (steward):
  delete-ALL-sessions+remint contradicted design §7.2's "other sessions"
  wording — the design was amended in place (§7.2 + status header,
  2026-07-07) rather than the plan silently diverging; flagged to jrazmi
  at cut-ratification.
- **Refinement 3 SUPERSEDED — the pinned GetByHash contract** (backend +
  data, reconciled): the store selects by `key_hash` ALONE and returns the
  record for ANY present row (revoked and expired included; NULL
  `expires_at` = never expires); `errs.ErrNotFound` only for
  genuinely-unknown hashes; `ErrExpired` is REMOVED from the port —
  revocation/expiry are SERVICE-layer branches in `AuthenticateAPIKey`
  (revoked → `blocked` audit with service-account attribution + deny;
  expired → failure audit + deny; valid → proceed). Four storetest cases
  incl. valid-with-NULL-expiry. Stated identically in A3/A5/A7a/A7b.
- **Refinement 6 SUPERSEDED** (backend): the client-info carrier is an
  EXPORTED `authsvc.WithClientInfo(ctx, ip, ua)` setter populated at ONE
  blanket point (feature middleware over ALL routes in `http.Mount`,
  unauthenticated routes included); the carrier is the single source of
  truth for IP (login's rate-limit key reads it too).
- **Refinement 9 amended** (SRE): the debug route is env-gated DEFAULT-OFF
  (`AUTH_DEBUG=1`) AND session-gated; `.env.example` gains secret-free
  placeholders; absent JWT secret → EPHEMERAL random key at boot, never a
  hardcoded constant.
- All rule-6/boundary greps switched to the import-anchored form (the
  plain-text forms false-fail today on doc comments — steward ran them).
- Store phases: explicit pagination/nullable templates (cms entries.go,
  jobs queue.go), `ORDER BY <field> DESC, id DESC` tiebreak pinned in
  every paginated port + collision conformance cases, named secondary
  indexes, no-upsert-on-oauth_accounts, `DELETE … RETURNING` Consume
  (delete-regardless-of-expiry), JSON empty-value round-trip rule,
  authTables truncate-slice additions (child-before-parent), playground
  reset story + single-executor caution.
- Docs phase: upgrade note gains deploy-mode guidance
  (single-cutover/drain; rollback = second mass logout; long-TTL sessions
  included); migration-source language corrected (connectors dedupe by
  FULL FILENAME under source "default" — "per-source ledger" is
  aspirational; hosts must NOT renumber scaffolded files);
  RequireVerifiedEmail=true requires a working Mailer (lockout warning).

Files stay pending jrazmi cut-ratification; then leg 1 = A1.
