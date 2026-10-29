# S4c-2 — Native `all` and the enrollment grace, with the #282 boot panic (SOC 2 readiness, slice 4c part 2)

**Status:** EXECUTED 2026-10-29 (tag: authentication v0.38.0 @ 564d7185, #285, with the #282 fix from #284 @ f3c1db75; examples + docs #287 @ efd0c3d7, no tags; PR-3 dropped under R-1 (a); record PR-5). See **As built** at the end; the tag table, MVS sets, upgrade notes, rollout/rollback/break-glass and deviations are in `RELEASING.md` "Native `all` and the enrollment grace (SOC 2 slice S4c-2)"; audit entry AUDIT-062. Was RATIFIED 2026-10-28, AMENDED 2026-10-29 (owner: R-6 (a), R-7 compute-after-commit; see "Rulings at execution"). Ratified with (owner: "all recs": R-1..R-5 all (a); no per-user marker and no migration, #282 is the first PR in the v0.38.0 train, plain 401 / recover=1 for claimless tokens, grace 7 d default / 30 d max, native all leaves the single-factor refusal with one WARN). Was: DRAFT 2026-10-28 (planner; the two-reviewer gate — lead-backend-engineer BE-1..BE-14, platform-sre
SRE-1..SRE-14 — run on the sketch and folded under "Decided by gate"). Awaiting owner rulings R-1..R-5. Closes the
control-map row 17 residual "native `all` + enrollment grace [S4c-2]" and issue #282.

- **Size:** S–M under R-1 (a): four PRs, ONE tag (authentication v0.38.0); no sdk, views, store or workshop tag, no
  migration. Under R-1 (b) add one store train (pgx v0.20.0 / turso v0.19.0, migration 0028, ledger 0037). Every
  behaviour change is reachable only through wiring that boot REFUSES today (`MFAConfig.Required: all` beside
  `WithPasskeys`/`WithTOTP`), so no existing host changes on the wire except the #282 fix (PR-1, named below).
- **Anchors (`main` @ ba897434).** sdk v0.24.0; authentication v0.37.0 (go.mod sdk v0.24.0); stores pgx v0.19.0 /
  turso v0.18.0 (core v0.37.0); views/goth v0.9.0 (pins core v0.31.0 — untouched); firebase v0.3.0; workshop v0.10.2;
  auth-cms, production-baseline and external-identity pin core v0.37.0 + sdk v0.24.0. Both stores' migrations end at
  0030 with 0028 free (reserved for this slice); the production-baseline ledger ends at 0036. Sources: `s4c-totp.md`
  (R-1 (a) split, Notes "S4c-2 handoff"), `s4a-mfa-posture.rev1.md:126,146-154` (`all` = external-token users; grace
  deferred), `roadmap.md:181,317`, `control-map.md:17`, `NEXT.md:64,313-314`, `gh issue 282`.
- **Owner constraints (binding).** Plan-first; conformance in a core tag before stores use it; examples repin only via
  explicit `GOWORK=off go get`; never `go work sync`; additive migrations only, with the ledger copy in the store PR
  (#223/#280 precedent); authentication/authorization naming; no local paths in tracked files; secrets never in logs;
  accept interfaces, return structs; vars/consts first, then interfaces; a stricter default is opt-in or has a grace
  path. Real host wiring is out of scope.

## Context
**What "native `all`" means today — from the code.** `MFARequiredAll` "requires AAL2 of every external-token user:
New sets `ExternalProvider.RequireMFA` on a copy of every External provider" (`pockets/authentication/config.go:47-49`;
`constructor.go:786-795` `requireExternalMFA`; the boot line prints `gates=external_tokens`, `constructor.go:811-812`).
There is NO runtime branch for native (first-party) sessions: `validateMFA` refuses `all` beside a native AAL2 source —
"`MFAConfig.Required all is not available beside WithPasskeys or WithTOTP: native all is the follow-on s4c2-native-all;
use admins meanwhile`" (`configuration.go:116-118`) — and refuses `all` while any native single-factor login is on
(password, passwordless, native OAuth; `configuration.go:125-138`). The same is in the `MFAConfig` doc
(`config.go:1185-1188`) and `ErrMFAUnsatisfiable` (`security.go:117`). So `all` is reachable only by an IdP-only host.
**What "enrollment grace" means today:** nothing in the code (no `EnrollmentGrace`, no `mfa_enrollment` symbol anywhere
under `pockets/authentication`); it is the s4c R-1 definition — "the enrollment grace (`max(EnrollmentStart,
user.CreatedAt) + EnrollmentGrace`; 403 `mfa_enrollment_required`; refresh re-checks; the admin-reopened per-user
marker)" (`s4c-totp.md:433-436`, Notes `:468-473`), deferred from S4a (`s4a-mfa-posture.rev1.md:63,154`): the window in
which a user without a second factor, under an `all` policy, may still use the app at AAL1 before every route but the
enrollment surface refuses them.

**What exists to build on.** The one enrollment predicate `holdsAAL2Factor` (passkey AAL2-capable or confirmed TOTP,
`Methods`-aware; `logic/authentication/passkey_ceremony.go:635-658`); `mfaRefusal`, the one AAL2 predicate every gate
applies (`inbound/http/authenticator.go:141-155`); `RequirePrincipal`'s order admits → assure → RequireLive
(`inbound/http/principal.go:67-108`); the access JWT's `aal`/`methods` claims signed in `signAccessToken`
(`logic/authentication/service.go:1339-1363`) and read in `parseAccessClaims` (`token.go:114-150`, unknown claims
ignored); the four mint sites (`service.go:1230`, `refresh.go:130,162`, `provisioning.go:125`); the bundled route
groups and the shared `live`/`stateless` floors (`inbound/http/routes.go:225-270`, `route_policy.go:90-115`); the
denial shapes `denyPrincipal`/`denyBrowserMFA` (`principal.go:215-262`); the `admins` gate `adminPrincipal` →
`admitMFA` (`useradmin.go:227-250`), set only for `admins` (`constructor.go:720-722`); `user.User.CreatedAt`
(`user/user.go:36`).

**#282.** `mountOAuth2`'s authorize set is `RequirePrincipal(FirstParty(), Transports(TransportCookie), Live(),
Browser())` (`inbound/http/oauth2.go:29`), and `mountSessionManagement`'s is the same (`session_management.go:15`).
`FirstParty()` sets only `set.firstParty` (`principal_options.go:129-131`); it never clears `set.externalToken`, which
`defaultSet` sets whenever `ExternalEnabled()` (`:349-360`). So `resolves(CredentialExternalToken)` is true and
`resolveSet` panics without an explicit `WithBrowserLoginPath` (`:366-369`). Fix (a) is correct: `Accept(CredentialAccessToken)`
clears the external kind (`:138-160`), which is exactly the `BrowserAccount` default already does (`routes.go:243-249`).

## Goal
A host that wires a native second factor can set `MFAConfig.Required: all`: every first-party session below AAL2 is
refused on every route but the enrollment surface once its holder's grace window closes — and a host with `WithOAuth2`
plus `WithExternal` boots without naming a browser login path — with no behaviour change for any host wired today.

## Definition of Done
- authentication v0.38.0: PR-1's #282 fix; `MFAConfig.EnrollmentStart`/`EnrollmentGrace`; boot admits native `all`
  (refusals at `configuration.go:116-118,125-138` lifted under the new rules); the `mfa_enroll_by` claim; the
  first-party floor in `RequirePrincipal` with the bundled allowlist and `AllowMFAEnrollment()`; the admin and machine
  gates and the OAuth2 authorize page at AAL2 under native `all`; `/auth/me` + `/auth/methods` report the window;
  `mfa_required_denied gate=enrollment` once per session.
- Under R-1 (b) only: stores pgx v0.20.0 / turso v0.19.0 with migration 0028, the optional marker repository, the
  core-shipped runner, the admin re-open route and ledger 0037.
- auth-cms drives the whole flow in a browser (Verification §1); examples repinned; README, docs site, record (RELEASING,
  SECURITY, AUDIT-062, control-map row 17 → DEFAULT/OPT-IN with no residual, roadmap, NEXT); every tag cold-verified.

## Scope and non-goals
**In scope:** the three carries (native `all`, the grace, #282), their tests, the examples, docs, record.
**Non-goals (over-reach flagged):** a views/goth change of any kind (a "set up two-step verification by <date>" banner
is a follow-up; views pins core v0.31.0 and would need its own repin train); an sdk change; a workshop/`guard --posture`
rule (SRE-12: P5 still prints `ack` for an inert `SingleFactorAcknowledged` — follow-up); TOTP/passkey as sign-in
methods; re-consent or revocation of API keys and grants minted during grace (BE-6: closed by gating their minting
instead); the per-user marker + migration 0028 unless R-1 = (b); host wiring (gps-three-sixty, battleboardhq,
gps-360-go, segovia/v2 — notes only).

## Decided by gate
Reviews 2026-10-28 on the sketch: lead-backend-engineer (BE, ship with edits), platform-sre (SRE, ship with edits).
Folded unless marked.
- **BE-1 (HIGH, lost sessions):** the refresh grace lane runs `ConsumeGrace` before signing (`refresh.go:150-162`); an
  I/O failure after that CAS makes the client's retry hit `PreviousUsed` → `reuse()` → the session is revoked.
  → The enrollment snapshot is computed BEFORE the CAS in `rotate` and `grace`, before `createSession` in
  `mintSessionUntil`, and before the provisioning commit; `signAccessToken` stays I/O-free and gains the value as a
  parameter (§2).
- **BE-2 + SRE-8 (HIGH, R-1 (b) only):** erasure tombstones `users` with an UPDATE and deletes children by hand
  (`stores/pgx/erasure.go:103,129-214`); no shipped migration uses `REFERENCES`. → No FK; explicit `DELETE FROM
  mfa_enrollment_grace` in both stores' `Erase` (the TOTP precedent `:162`), an `EraseCounts.MFAEnrollmentGrace` count,
  storetest coverage; 0028 copies 0029's `lock_timeout` header and REQUIRED-only-when text.
- **BE-3 (HIGH, allowlist):** `withDefaults` wraps every group as `strategy(floor(next))` and the shared `live`/
  `stateless` floors are `RequirePrincipal` sets (`routes.go:252-268`) that run admits → assure → the new floor on their
  OWN set; marking only the strategies still 403s at the floor, and the floors are shared with UserAdministration and
  MachineLifecycle. → Separate allowlisted floor instances for the six groups; the option is also appended inside
  `RoutePrincipalStrategy.middleware` (`route_policy.go:90-95`) so a host override inherits it (§3).
- **BE-4 (HIGH):** nothing reads `cfg.MFARequired` at request time; the adapter gets only `AdministrationMFA`
  (`constructor.go:720` → `options.go:149` → `adapter.go:196`). → New inbound option `WithMFAEnrollmentFloor(...)` and
  `adapterConfig` field; new logic-side `Dependencies` fields (§2, §3).
- **BE-5 + SRE-5 + BE-6 (MED→HIGH together, grace must not mint durable credentials):** `WithAdministrationMFA` is set
  only for `admins` (`constructor.go:720-722`), so an in-grace AAL1 admin would reach `/auth/admin/users*`, the machine
  lifecycle (`machineMFA` follows it, `routes.go:299`) and the OAuth2 authorize page, minting API keys and grants that
  outlive the deadline. → Under native `all`: `AdministrationMFA` is on (condition `admins || (all && nativeMFA)`;
  `logMFAPosture` says so), and the OAuth2 authorize set gains `RequireMFA()`. Grace satisfies ONLY the plain
  first-party floor — never `RequireMFA`, the admin, machine or authorize gates (answers both leads' question).
  External-only `all` is unchanged.
- **BE-7 + SRE-5 + SRE-12 (MED, posture overclaim):** → under native `all` the native sources leave the production
  REFUSAL (`singleFactorCauses`, `security.go:704-722`) but boot logs one WARN `auth: mfa_required=all admits
  single-factor sign-in for at most <grace> per user until enrollment`; `SingleFactorAcknowledged` set beside native
  `all` logs an "inert acknowledgement" WARN rather than refusing (the flip must not fail boot). No P5 change (follow-up).
- **BE-8 + SRE-4 (MED, the no-claim dead end):** a pre-upgrade token, a token minted before the flip, or one from a
  host-implemented `CredentialService` carries no claim; sending an unenrolled holder to the step-up chooser is a dead
  end for up to `AccessTokenTTL`. → No claim + first-party + below AAL2 under the floor → the plain JSON 401 (no RFC 9470
  challenge, the byte-stable body — clients refresh on 401) and, under `Browser()`, `redirectToBrowserLogin(recover=true)`
  so the login page's session recovery refreshes and mints the claim. Named in the upgrade notes.
- **BE-9 + SRE-10 (#282 verified, with corrections):** status codes are unchanged EXCEPT one case: today a
  provider-cookie-only browser on those two pages has its external token resolved (a provider outage → 503), then
  refused by `admits` → 303; after the change the cookie is not read → 303 with `recover=1` (`providerCookieOnly` is
  false for a set that does not admit external, `principal.go:278-287`), and the side effects of resolving it (JIT
  provisioning, external events) stop. A host that panics at mount today boots. The PR and record say exactly this.
- **BE-10 (LOW):** the enrollment redirect target is re-rooted with `pagePath` (`authenticator.go:301-316`) and chosen
  the way `adapter.go:202-207` chooses the step-up path — never a hard-coded `/auth/account/totp/enroll`.
- **BE-11 + SRE-11 (LOW):** `/auth/sessions` (HTML revoke/revoke-all) is session security → allowlisted.
- **BE-12 (LOW):** the snapshot runs only under native `all` and only when the minted assurance is below AAL2 (a
  passkey step-up through `mintSessionUntil` skips it); at most three reads (`passkeys.ListByUser`, `totp.Get`,
  `users.Get`) per sub-AAL2 mint.
- **BE-13 + SRE-3 (INFO/HIGH, lost factor):** `ResetUserTOTP`/`ResetUserPasskeys` revoke at the revision fence
  (`totp_management.go:184-212`, `passkey_management.go:150`); after a reset the next login mints a claim from the
  ORIGINAL deadline, so a past-deadline user is walled to the enrollment surface — which they can complete at AAL1
  (`secondFactorPolicy` bootstrap, `passkey_ceremony.go:660-672`; CredentialManagement is allowlisted). That is the
  recovery path for a lost device: admin reset → self-enrol; no marker needed. → R-1.
- **SRE-1 (HIGH, rollback):** a v0.37.0 binary refuses native `all` at boot; rollback is "flip `Required` to `admins`,
  THEN repin" — the claim itself is harmless to v0.37.0 (`parseAccessClaims` ignores unknown claims; it refuses only
  `client_id`/`origin_client_id`/`act`, `token.go:41`). In the record.
- **SRE-2 (HIGH, the wrong date):** `EnrollmentStart` stays a code literal (never env; the auth-cms hatch is
  development-gated like `main.go:436-446`); boot WARNs when `now > EnrollmentStart + EnrollmentGrace` ("enrollment
  window closed for every user created before <start>: unenrolled first-party sessions are refused outside the
  enrollment surface"); the fleet lever is "bump `EnrollmentStart`, redeploy" — a deliberate action, named in the record.
- **SRE-3 (HIGH, break-glass):** never "redeploy with `MFARequiredNone`" in production (it then needs
  `SingleFactorAcknowledged` and is itself refused). The record names: (1) the sole admin self-enrols (allowlisted);
  (2) bump `EnrollmentStart`; (3) redeploy with `admins`.
- **SRE-6 (MED):** a factor-read error fails the mint as `sdk.ErrUnavailable` naming only `error_kind` (the S4d4 SRE-9
  log rule); never a silent pass. (Under R-1 (b): a marker-read error falls back to the computed deadline — stricter.)
- **SRE-7 (MED, audit flood):** `mfa_required_denied gate=enrollment` is recorded once per session id (the
  `authext_age_denied` once-per-token dedupe precedent), not per request.
- **SRE-9 (MED, R-1 (b) only):** P9 goes red for every guard-pinned host the moment it bumps to pgx v0.20.0 / turso
  v0.19.0; the ledger copy 0037 lands IN the stores PR and the upgrade note says "copy 0028 when you bump the store".
- **SRE-13 + SRE-14 (INFO):** the patch tag → R-2; deadlines, user ids and the boot fields are fine to log; no
  operator free text in events; the Turso leg runs locally (no CI secrets) — the record says which runners ran.
- **BE-14 (INFO, checks out):** `Credential.MFAEnrollBy`, the new gate/reason values and the config fields are
  additive; `user.UserRepository` is unchanged; a zero `CreatedAt` falls back to `EnrollmentStart`.

## Design

### 1. #282: the two OAuth2-mounted browser sets (PR-1)
`mountOAuth2` (`oauth2.go:29`) and `mountSessionManagement` (`session_management.go:15`) become
`RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Transports(TransportCookie), Live(), Browser())` — the
`BrowserAccount` default verbatim (`routes.go:243-249`); `route_policy.go:82`'s comment follows. Tests: an adapter
constructed with OAuth2 + external wired and NO `WithBrowserLoginPath` mounts (today: panic); the authorize page with a
provider cookie only → 303 with `recover=1` and no provider call (BE-9). The auth-cms explicit `BrowserLoginPath:
"/auth/login"` (`main.go:1153`) stays — it is a legitimate setting; only its comment loses the "refuses the implicit
default" sentence. The OAuth2 README gains one line. Under R-2 (a) this is the first PR of the v0.38.0 tag.

### 2. Config, boot, the claim (authentication v0.38.0, logic side)
```go
// MFAConfig (config.go) — two additive fields; wiring literals, never env.
EnrollmentStart time.Time     // required (non-zero) when Required is all beside WithPasskeys or WithTOTP
EnrollmentGrace time.Duration // zero → DefaultMFAEnrollmentGrace (7 days); above MaxMFAEnrollmentGrace (30 days) or negative → ErrMFAEnrollmentGraceInvalid
```
**Boot (`validateMFA`, every mode).** Native `all` = `Required == all && nativeMFA(cfg)`. Under it: the refusals at
`configuration.go:116-118` and `:125-138` do not apply; `EnrollmentStart.IsZero()` → `ErrMFAEnrollmentStartRequired`;
the grace bounds above; `all` without any AAL2 source stays `ErrMFAUnsatisfiable`; external-only `all` is byte-for-byte
today's. `constructor.go:720`: `AdministrationMFA` on for `admins || native all` (BE-5). `singleFactorCauses` drops the
three native causes under native `all` (BE-7) and `checkPosture` logs the two WARNs of BE-7/SRE-12 and SRE-2's
window-closed WARN. `logMFAPosture`: `gates=first_party_routes,/auth/admin/users*,machine_lifecycle,oauth2_authorize,
external_tokens enrollment_start=<RFC 3339 date> enrollment_grace=<duration>`. `MFARequiredAll`'s doc, `MFAConfig`'s
doc and `ErrMFAUnsatisfiable`'s doc drop "native all is the follow-on".

**The deadline and the claim.** `Service` gains `mfaFloorAll bool`, `mfaEnrollmentStart time.Time`,
`mfaEnrollmentGrace time.Duration` (from `Dependencies`). One method:
```go
// enrollmentDeadline returns when userID's grace window closes under native all, or zero when no
// claim applies: the floor is off, auth is already AAL2, or the user holds an AAL2 factor
// (holdsAAL2Factor). It runs BEFORE any session write (BE-1); a read error is sdk.ErrUnavailable.
func (s *Service) enrollmentDeadline(ctx context.Context, userID string, auth session.AuthenticationMetadata) (time.Time, error)
// deadline = max(EnrollmentStart, user.CreatedAt) + EnrollmentGrace   (zero CreatedAt → EnrollmentStart)
```
`signAccessToken(userID, sessionID, auth, enrollBy time.Time)` signs `tokenClaimMFAEnrollBy = "mfa_enroll_by"` (unix
seconds) when non-zero; it stays I/O-free. The four callers compute the deadline first: `mintSessionUntil` before
`createSession`; `rotate` and `grace` before their CAS (`refresh.go:117,153`); `provisioning.go` before `Redeem`.
`parseAccessClaims` reads it on `ProfileFirstParty` only (a non-numeric value refuses the token; absent → zero) into
`Credential.MFAEnrollBy time.Time` (additive, doc'd beside `Methods`). A delegated token never carries it.
`refreshByHash` therefore "re-checks" by construction: every refresh re-evaluates enrolment and the window.

### 3. The floor in the HTTP adapter (authentication v0.38.0, inbound side)
New inbound option `WithMFAEnrollmentFloor(EnrollmentFloor{Enabled bool})` (set by the constructor under native `all`)
→ `Adapter.enrollmentFloor bool` and `Adapter.mfaEnrollmentPath` (resolved like `mfaStepUpPath` at `adapter.go:202-207`:
the TOTP enrol page when TOTP views are wired, else the passkeys page when passkey views are, else empty → JSON;
re-rooted with `pagePath`, BE-10). `principalSet` gains `allowUnenrolled bool`; two options:
```go
// AllowMFAEnrollment marks a route a user may reach while enrolling a second factor under
// MFAConfig.Required all: the first-party enrollment floor does not run on it. The bundled
// enrollment surface carries it; a host adds it to its own enrollment or SPA-shell routes.
// It never relaxes RequireMFA, the admin, machine or OAuth2 authorize gates.
func AllowMFAEnrollment() PrincipalOption
```
(and the unexported twin the groups use). **In `RequirePrincipal`** (`principal.go:85`, after `admits`, before
`assure`), when `s.enrollmentFloor && cred.Kind == CredentialAccessToken && cred.Profile == ProfileFirstParty &&
!cred.Assurance.AtLeast(AAL2) && !set.allowUnenrolled`:
- `cred.MFAEnrollBy.IsZero()` → BE-8: JSON 401 (byte-stable, no challenge); `Browser()` + cookie →
  `redirectToBrowserLogin(w, r, true)`;
- `now < MFAEnrollBy` → pass (the grace; explicit gates still deny);
- otherwise → record `mfa_required_denied` `gate=enrollment reason=enrollment` once per `cred.SessionID` (SRE-7);
  `Browser()` + cookie + a page → 303 `mfaEnrollmentPath?return_to=<validated>`; else JSON **403**
  `mfa_enrollment_required` with `enroll_by` (RFC 3339) in the body.
`assurance.go`: `MFAGateEnrollment = "enrollment"`, `MFADenialEnrollment = "enrollment"`; `mfaDenialEvent`'s bounded
enums and `securityevent.go:275-282` gain them. **Allowlist (BE-3):** `withDefaults` builds `liveEnroll`/`statelessEnroll`
floors (the shared floors + the unexported option) for CredentialManagement, SessionSecurityReads, SessionHydration,
BrowserAccount, OAuthLinkStart, CSRFBootstrap; `RoutePrincipalStrategy.middleware` appends it for those six so a host
`BundledRouteAuth` override inherits it; `mountSessionManagement`'s set carries it (BE-11). NOT allowlisted:
UserAdministration, MachineLifecycle, Invitations, the OAuth2 authorize set (which gains `RequireMFA()` under native
`all` via a `mountDeps` flag — BE-6). `/auth/refresh` and `/auth/logout` are credential-driven, not gated.
**JSON views:** `/auth/me` (`me.go:27`) and `/auth/methods` (`methods.go:81`) gain
`"mfa_enrollment": {"required": true, "enroll_by": "<RFC 3339>"}` (`omitempty`, from the credential's claim).

### 4. The per-user marker — R-1 (b) only (stores pgx v0.20.0 / turso v0.19.0)
Migration `0028_mfa_enrollment_grace.sql` (both stores; 0029's header; REQUIRED only under native `all`): `CREATE TABLE
IF NOT EXISTS mfa_enrollment_grace (user_id TEXT PRIMARY KEY, deadline <TIMESTAMPTZ|TEXT>, granted_by TEXT NOT NULL,
granted_at <...> NOT NULL)` — no FK (BE-2). Port in `logic/authentication/mfaenrollment/repository.go`:
`Repository{Get(ctx, userID) (Grace, error); Set(ctx, Grace) error; ProbeSchema(ctx) error}` on a new
`Repositories.MFAEnrollmentGrace` field; `enrollmentDeadline` takes `max(computed, marker.Deadline)`; `Erase` deletes the
row and counts it; `storetest.RunMFAEnrollmentGrace` (+ a schema-probe runner) ships in the CORE tag; boot probes the
schema under native `all` (`ErrMFAEnrollmentGraceRequires` / `...SchemaMissing` naming 0028; the 0029 shape). Route
`POST /auth/admin/users/{id}/mfa/enrollment-grace {}` through `adminPrincipal` (AAL2 under `all`) → deadline =
`now + EnrollmentGrace`; event `mfa_enrollment_grace_reopened` (actor, target, deadline). Ledger
`examples/production-baseline/workshop/migrations/primary/0037_mfa_enrollment_grace.sql` = pgx 0028 byte for byte, in
the same PR (SRE-9). authmem adopts.

## Upgrade notes
> **Read first:** `grep -rnE 'MFAConfig\{|MFARequiredAll|WithOAuth2\(|WithExternal\(|RequirePrincipal\(\)' --include='*.go'`.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | #282: the authorize and sessions-page sets stop admitting external tokens | boot no longer needs `WithBrowserLoginPath` beside `WithOAuth2`+`WithExternal`; a provider-cookie-only browser gets `recover=1` (303 either way; a provider outage there was 503, is 303) | — | authentication@v0.38.0 |
| 2 | `MFAConfig.EnrollmentStart`/`EnrollmentGrace` | additive; read only under native `all` | — | authentication@v0.38.0 |
| 3 | native `all` admitted at boot | opt-in; under it every first-party route but the enrollment surface refuses sub-AAL2 sessions after the window; admin, machine and authorize gates at AAL2 with no grace; native sources leave the production single-factor refusal (two WARNs) | `admins` | authentication@v0.38.0 |
| 4 | `mfa_enroll_by` claim; `Credential.MFAEnrollBy` | signed only under native `all`; v0.37.0 verifiers ignore it | — | authentication@v0.38.0 |
| 5 | `AllowMFAEnrollment()`; `mfa_enrollment_required` 403; `/auth/me` + `/auth/methods` `mfa_enrollment` | additive; an SPA shell under `RequirePrincipal()` must carry the option or be served unauthenticated, and must route 403 `mfa_enrollment_required` → its enrollment UI, 401 + RFC 9470 → step-up, plain 401 → refresh | — | authentication@v0.38.0 |
| 6 | (R-1 (b)) migration 0028, `Repositories.MFAEnrollmentGrace`, the re-open route | additive; REQUIRED only under native `all`; a `--posture` host copies 0028 into its ledger when the store pin moves (P9) | — | stores pgx@v0.20.0 / turso@v0.19.0 |

**Rollout for native `all`:** (1) wire `WithPasskeys`/`WithTOTP` (0025/0026 already required when wired); (2) ship the
client handling of row 5; (3) set `Required: all`, `EnrollmentStart = today`, the grace; (4) watch the boot line and
`mfa_required_denied gate=enrollment`. **Rollback (SRE-1):** set `Required: admins` FIRST, then repin to v0.37.0; a
v0.37.0 binary with `all` beside a native factor refuses to boot. **Break-glass (SRE-3):** the walled sole admin
self-enrols (the surface is allowlisted); the fleet lever is "bump `EnrollmentStart`, redeploy"; never `MFARequiredNone`
in production (it needs `SingleFactorAcknowledged` and is refused). **Window flip on a live fleet:** tokens minted
before the flip carry no claim → plain 401 / login-page recovery until their first refresh (≤ `AccessTokenTTL`, BE-8).

**By host shape:**
- **gps-three-sixty / battleboardhq** (native Google OAuth sign-in → first-party AAL1 sessions; pgx; React SPAs; no
  external IdP, no OAuth2 server): rows 1–2 and 4 at repin change nothing on the wire. To adopt native `all` they wire
  a native factor first (passkeys and/or TOTP + their migrations), then rows 3 and 5 — the SPA work in row 5 is the
  real cost; the shell route needs `AllowMFAEnrollment()` or anonymous serving. Not in this slice.
- **gps-360-go** (`WithOAuth2`, no `WithExternal`; old pins, no compatibility promise): row 1 is inert for it (no
  external); under native `all` its authorize page would require AAL2. Repin only.
- **segovia/v2:** deferred (owner).
- **external-IdP hosts** (production-baseline shape): nothing — external-only `all` is unchanged; the baseline stays
  `admins`. (R-1 (b): it takes the 0037 ledger copy with the store bump.)
- **examples:** auth-cms (the development hatches + authmem under (b)); production-baseline and external-identity
  (repin only, plus the ledger under (b)).

## Risks
(1) A wrong `EnrollmentStart` walls every unenrolled user at once (SRE-2); controls: literal-only, the window-closed
boot WARN, the enrollment surface stays reachable, the lever is one redeploy. (2) The floor sits on `RequirePrincipal`
only — custom host middleware that verifies tokens itself is outside it (BE-7); control: README + SECURITY say so.
(3) The no-claim window on a live flip (BE-8); control: plain 401 + recovery, documented. (4) Rollback is a config
change plus a repin (SRE-1); control: the record's order. (5) Under (b), a Turso leg proven locally only (SRE-14).

## Tasks as PRs
Worktrees `../gopernicus-s4c2-<pr>`; the owner merges; Claude cuts each tag after a green `live-stores` dispatch on
`main`, confirms it on origin, cold-verifies; every PR runs with model **opus**; `make guard && make check` in every
verify line; live legs on a free port (`lsof -i :8096` first; never 8082 or 55432); no local paths in tracked files.

- **PR-1 — #282 (`authentication/282-oauth2-browser-sets`).** Depends on nothing. **Tag:** none alone under R-2 (a)
  (first PR of v0.38.0); `pockets/authentication/v0.37.1` under R-2 (b). **Files:** `pockets/authentication/inbound/http/
  {oauth2.go (:29), session_management.go (:15), route_policy.go (:82 comment), adapter_test.go or oauth2_test.go
  (mounts without WithBrowserLoginPath beside external; provider-cookie-only → 303 recover=1, no provider call)}`,
  `pockets/authentication/README.md` (OAuth2 section, one line), `examples/auth-cms/cmd/server/main.go` (:1148-1152
  comment only). **Verify:** `cd pockets/authentication && go build ./... && go test -race ./... && go vet ./...`;
  `cd examples/auth-cms && AUTH_OAUTH2_DEMO=1 AUTH_EXTERNAL_DEMO=1 go test ./...`; `make guard && make check`; run-and-look:
  auth-cms with both demos and the `BrowserLoginPath` line temporarily removed boots, `/auth/oauth2/authorize` signed
  out → 303 to `/auth/login`.
- **PR-2 — core: native `all` + the grace (`authentication/s4c2-core`).** Depends on PR-1 merged. **Tag:**
  `pockets/authentication/v0.38.0`. **Files** (under `pockets/authentication/`): `config.go` (MFAConfig fields + docs,
  `DefaultMFAEnrollmentGrace`, `MaxMFAEnrollmentGrace`, `MFARequiredAll` doc), `configuration.go` (`validateMFA` rules,
  `singleFactorCauses` interplay), `configuration_errors.go` (`ErrMFAEnrollmentStartRequired`,
  `ErrMFAEnrollmentGraceInvalid`), `constructor.go` (`:720` admin gate condition; `WithMFAEnrollmentFloor`; the OAuth2
  authorize flag; `logMFAPosture`; the three WARNs), `security.go` (`singleFactorCauses`, the inert-acknowledgement
  WARN, `ErrMFAUnsatisfiable` doc), `logic/authentication/{options.go (Dependencies), service.go (fields;
  enrollmentDeadline; signAccessToken signature; mintSessionUntil order), refresh.go (snapshot before both CAS lanes),
  provisioning.go, token.go (claim const; parseAccessClaims first-party only), credential.go (MFAEnrollBy),
  assurance.go (gate + reason), securityevent/securityevent.go (:275-282), enrollment_test.go (new: deadline math incl.
  zero CreatedAt; no claim at AAL2 / enrolled / floor off; read error → ErrUnavailable; refresh re-evaluates; grace
  lane never revokes on a failed snapshot), token_test.go (claim parse/refuse)}`, `inbound/http/{options.go
  (WithMFAEnrollmentFloor), adapter.go (fields; mfaEnrollmentPath), authenticator.go (fields), principal_options.go
  (AllowMFAEnrollment + unexported twin; allowUnenrolled), principal.go (the floor; denials; the once-per-session
  recorder), routes.go (allowlisted floors; the six groups), route_policy.go (middleware appends; docs),
  session_management.go (allowlist), oauth2.go (RequireMFA under the flag), me.go + methods.go (mfa_enrollment),
  enrollment_floor_test.go (new: pass in grace; 403 past with enroll_by; Browser → enrol page / passkeys page / JSON;
  no-claim → plain 401 / recover redirect; allowlist on every bundled group incl. a host BundledRouteAuth override and
  /auth/sessions; admin, machine and authorize deny in grace; API key / delegated / external untouched; one event per
  session)}`, `README.md` (an "MFAConfig.Required all (native)" subsection after `:2672`; the Assurance model `:2189`;
  the route table), `go.mod` unchanged (sdk v0.24.0). **Verify:** `cd pockets/authentication && go build ./... && go test
  -race ./... && go vet ./...`; the module dropped from `go.work` → `GOWORK=off go build ./... && go vet ./...`;
  `cd examples/auth-cms && go test ./...` (go.work); `make guard && make check`; `live-stores` dispatch on `main` after
  merge (the released stores beside core v0.38.0 — under (a) nothing store-side changes).
- **PR-3 — stores: 0028 + the marker (R-1 (b) ONLY; `authentication-stores/mfa-enrollment-grace`).** Depends on tag
  v0.38.0 (explicit `GOWORK=off go get .../pockets/authentication@v0.38.0` in both go.mod). **Tags:**
  `pockets/authentication/stores/pgx/v0.20.0`, `.../turso/v0.19.0`. **Files:** `pockets/authentication/stores/pgx/
  {migrations/0028_mfa_enrollment_grace.sql, mfa_enrollment_grace.go (new), erasure.go (DELETE + count),
  mfa_enrollment_grace_test.go (ErrNotFound on an unmigrated schema; the runner under the live leg), README.md (0028 row),
  go.mod, go.sum}`, the turso twin, `examples/production-baseline/workshop/migrations/primary/0037_mfa_enrollment_grace.sql`
  (byte-identical to pgx 0028; SRE-9) — plus, moved here from PR-2 under (b): `logic/authentication/mfaenrollment/`,
  `repositories.go`, the probe in `constructor.go`, the admin route in `inbound/http/useradmin.go` + `routes.go`,
  `stores/storetest/mfa_enrollment_grace.go`, `logic/authentication/user/erase.go` (count) — those ship in PR-2 so the
  runner is in the core tag first. **Verify:** per store `go build ./... && go test ./... && go vet ./...`; the pgx live
  leg with the new runner; turso `go test -tags=integration ./...` against local libsql; `GOWORK=off go build ./...`
  per module; `cd workshop/gopernicus && go run . guard --posture ../../examples/production-baseline` (P9 green with
  0037); `make guard && make check`; `live-stores` dispatch on `main` after merge.
- **PR-4 — examples + docs (`docs/s4c2-examples`).** Depends on tag v0.38.0 (and PR-3's tags under (b)); explicit
  `GOWORK=off go get` repins: auth-cms, production-baseline, external-identity → core v0.38.0 (+ pgx v0.20.0 for the
  baseline under (b)). **Files:** `examples/auth-cms/cmd/server/{main.go (development-only hatches
  `AUTH_DEMO_MFA_REQUIRED=all`, `AUTH_DEMO_MFA_ENROLLMENT_GRACE`, `AUTH_DEMO_MFA_ENROLLMENT_START` defaulting to boot
  time — refused outside development like `:436-446`), demo.go (a plain `RequirePrincipal()` JSON route and a `Browser()`
  HTML route as the floor probes)}`, `examples/auth-cms/{.env.example, go.mod, go.sum}` (+ `pockets/authentication/
  outbound/authmem/mfa_enrollment_grace.go` under (b)); `examples/production-baseline/{go.mod, go.sum, .env.example
  (a comment: `all` here is the IdP floor)}`; `examples/external-identity/{go.mod, go.sum}`;
  `workshop/documentation/docs/{pockets/authentication.md ("Added by S4c-2"), guides/production-posture.md (native
  `all`, the two WARNs, rollback order), guides/operations-runbooks.md ("Turn on MFA for everyone": rollout, the
  window-closed WARN, break-glass)}`. **Verify:** per example `GOWORK=off go build ./... && go test ./...`; `pnpm build`
  in `workshop/documentation`; `make guard && make check`; Verification §1–§2.
- **PR-5 — the record (`docs/s4c2-release-record`).** Depends on PR-4 merged and §3. **Files:** `RELEASING.md` (ONE
  section "Native `all` and the enrollment grace (SOC 2 slice S4c-2)": tag table, upgrade notes by host shape, rollout/
  rollback/break-glass, the #282 behaviour note, review + verification records, as-built deviations), `SECURITY.md`
  (version rows; "the floor covers RequirePrincipal routes; grace never mints keys or grants"), `AUDIT.md` (AUDIT-062),
  `.claude/plans/soc2-readiness/{control-map.md (row 17: residual closed; row 45), roadmap.md (:181 + a "S4c-2 RELEASED"
  paragraph), NEXT.md}`, `plans/s4c2-native-all-enrollment-grace.md` (executed copy); close #282 from the PR body.
  **Verify:** `make check`; `make guard`.

## Sequencing
```
PR-1 #282 ── PR-2 core ──[live-stores]──[tag auth v0.38.0]──┬─(R-1 (b)) PR-3 stores ──[live-stores]──[tags pgx v0.20.0 / turso v0.19.0]─┐
                                                           └─────────────────────────────────────────────────────────────────────────┴── PR-4 ── PR-5
```
PR-1 first so the #282 test pins the sets before PR-2 adds `RequireMFA()` to the authorize one. Under R-1 (a) there is
one tag and PR-4 follows it directly.

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 0 (R-2 (b) only) | `pockets/authentication/v0.37.1` | PR-1 merge | core alone beside released stores and views; auth-cms shape builds |
| 1 | `pockets/authentication/v0.38.0` | PR-2 merge + green `live-stores` on `main` | core v0.38.0 beside RELEASED pgx v0.19.0 / turso v0.18.0 and views v0.9.0 selects sdk v0.24.0 and builds; the gps-three-sixty, battleboardhq and gps-360-go shapes build |
| 2 (R-1 (b) only) | `.../stores/pgx/v0.20.0`, `.../stores/turso/v0.19.0` | PR-3 merge + green `live-stores` | each beside core v0.38.0; the baseline shape with 0037 passes `guard --posture` |

Annotated, unsigned tags, one per command, cut at the squash commit, confirmed on origin, THEN polled on
`proxy.golang.org`; cold-verify with `GOWORK=off`, an empty `GOMODCACHE`, `go mod verify`, `go build ./...` on
`go 1.26.10`. Never tidy before a tag resolves; never `go work sync`. **Not tagged:** sdk, views/goth, `pockets`,
workshop, ui/goth, pgxdb, turso (integration), the examples.

## Verification
Real binaries in DEVELOPMENT on a FREE port. `$B` = a scratch auth-cms binary: `PORT=8096 AUTH_RUNTIME_MODE=development
AUTH_DEBUG=1 AUTH_ACCESS_TOKEN_TTL=1m AUTH_DEMO_MFA_REQUIRED=all AUTH_DEMO_MFA_ENROLLMENT_GRACE=3m go run ./cmd/server`
(authmem, console mailer; passkeys + TOTP wired as today).
- **§1 — the grace flow in a browser (PR-4; PR-2 proves the same in-process).** (1) Boot line shows `mfa_required=all
  gates=first_party_routes,... enrollment_grace=3m` and the single-factor WARN; no refusal. (2) Register + verify + password
  login (AAL1) → `/auth/account` **200**; the demo HTML route **200** (in grace); `GET /auth/me` carries
  `mfa_enrollment.enroll_by` ≈ now+3m; the JWT payload carries `mfa_enroll_by`. (3) `/demo/mfa` (`RequireMFA()`) →
  **401** + RFC 9470 (grace never satisfies it); `/auth/admin/users` as a demo admin in grace → **401** challenge.
  (4) Wait past the window, refresh (the 1 m token) → the demo HTML route → **303** to `/auth/account/totp/enroll`
  with `return_to`; the demo JSON route → **403** `mfa_enrollment_required` with `enroll_by`; exactly ONE
  `mfa_required_denied gate=enrollment` row after three retries; `/auth/account`, `/auth/account/totp*`,
  `/auth/account/passkeys`, `/auth/step-up/*`, `/auth/methods`, `/auth/me`, `/auth/sessions` → **200**. (5) Enrol TOTP
  (Google Authenticator or the dev secret) → step up → the demo routes **200**, the token carries `aal2` and no
  `mfa_enroll_by`. (6) Admin TOTP reset of that user → their session is gone; next login → walled to enrolment at once
  (BE-13) and enrolment completes at AAL1. (7) A passkey sign-in with UV → AAL2, no claim, **200** everywhere.
  (8) No log line, response or event carries a secret or token.
- **§2 — boot and posture (PR-2).** `Required: all` + native factor without `EnrollmentStart` → refuses in every mode;
  grace 31 d → refuses; `all` beside `SingleFactorAcknowledged` → boots with the inert WARN; production mode with
  native `all` and no acknowledgement → boots (no single-factor refusal), two WARNs; `EnrollmentStart` a year ago →
  the window-closed WARN; external-only `all` → today's boot line byte for byte; `WithOAuth2`+`WithExternal` without
  `WithBrowserLoginPath` → boots (PR-1). A token minted by v0.37.0 (no claim) under the floor → plain **401** on JSON,
  **303** `recover=1` under `Browser()`; after `/auth/refresh` it carries the claim.
- **§3 — from the tags (PR-5).** §1 (2)–(5) against a `GOWORK=off` auth-cms binary (`go version -m`: core v0.38.0); the
  gps-three-sixty/battleboardhq cold shapes build; (R-1 (b)) `RunMFAEnrollmentGrace` green on local Postgres and local
  libsql, P9 green on the baseline with 0037; the docs site builds; the record's tag table matches
  `git ls-remote --tags origin`.

## Open rulings — YOUR CALL
- **R-1 — the per-user marker (admin re-open) and migration 0028.** (a) NOT in this slice: a past-deadline unenrolled
  user is walled to the allowlisted enrollment surface and can complete enrolment at AAL1 immediately; a lost device is
  "admin reset → self-enrol" (BE-13); the fleet lever is `EnrollmentStart`; no store train, no migration, no ledger copy,
  no new Repositories field — one tag. (b) The s4c handoff as written: 0028 `mfa_enrollment_grace`, the optional
  repository, `POST /auth/admin/users/{id}/mfa/enrollment-grace`, ledger 0037, stores pgx v0.20.0 / turso v0.19.0,
  authmem. **Recommend (a).** The marker buys only "use the app for another week before enrolling", not access to
  enrolment, which never closes; (b) costs a store train, a hot-path read on every sub-AAL2 refresh, an erasure path and
  a P9 red for every guard-pinned host that bumps the store. Ship (b) later if a host asks. 0028 stays reserved.
- **R-2 — where #282 ships.** (a) Its own PR, first in the v0.38.0 train, no patch tag. (b) Patch tag
  `authentication v0.37.1` before the minor. **Recommend (a):** no named host is blocked (gps-360-go has no
  `WithExternal`; auth-cms is worked around), RELEASING says every module ships MINOR under v0 (SRE-13), and the
  minor follows within days. Take (b) only if a host reports the panic before PR-2 merges.
- **R-3 — the no-claim denial (BE-8/SRE-4).** (a) Plain JSON 401 (no challenge) and, under `Browser()`, the login
  redirect with `recover=1` — clients refresh and get the claim. (b) One live `holdsAAL2Factor` read per request for
  claim-less tokens, through a new `CredentialService` method (breaks host-implemented services). **Recommend (a).**
- **R-4 — grace length bounds.** (a) Default 7 days, maximum 30 days, every mode. (b) Default 14 days, maximum 90.
  **Recommend (a):** the s4c handoff's numbers; `EnrollmentStart` can always be bumped.
- **R-5 — production posture under native `all` (BE-7/SRE-12).** (a) The native sources leave the production
  single-factor REFUSAL; one WARN names the bounded window; `SingleFactorAcknowledged` beside it WARNs as inert.
  (b) Keep the refusal: native `all` still needs `SingleFactorAcknowledged` until a future P5 change. **Recommend
  (a):** (b) makes the strictest posture need a waiver flag, which is the inert-acknowledgement shape the repo
  refuses elsewhere (`configuration.go:94-96`).

## Rulings at execution (2026-10-29, owner) — these override §2/§3 where they conflict
Found by the PR-2 implementer: an ENROLLED user's password sign-in mints AAL1; `enrollmentDeadline` returns zero for a
factor holder, so the token is claimless, R-3 answers claimless with 401/recover, the refresh re-mints claimless → an
infinite loop.
- **R-6 = (a).** A second claim `mfa_enrolled: true` (`tokenClaimMFAEnrolled`), signed when the floor is on, auth is
  below AAL2 and the user holds an AAL2 factor (`enrollmentDeadline` becomes a small result: `{EnrollBy time.Time;
  Enrolled bool}`; at most one of the two is set). `parseAccessClaims` reads it on `ProfileFirstParty` only (a non-bool
  refuses the token) into `Credential.MFAEnrolled bool`. In the floor, checked BEFORE the claimless branch:
  `cred.MFAEnrolled` → the step-up answer exactly as `RequireMFA` gives it (the RFC 9470 401 with
  `insufficient_user_authentication` + `acr_values`; under `Browser()` + cookie the `mfaStepUpPath` redirect), and
  `mfa_required_denied gate=enrollment reason=assurance` once per `cred.SessionID`. Allowlisted routes still pass
  (an enrolled user can manage factors/sessions at AAL1 as today). Tests: an enrolled user's AAL1 session gets the
  step-up, NOT a 401-refresh loop (refresh re-mints `mfa_enrolled`, the floor answers step-up again, never 401
  recover); steps up → AAL2 → 200; the claim never appears at AAL2, floor off, or on delegated tokens.
- **R-7 = compute after commit.** `provisioning.go` (magic link) cannot read factors before `Redeem` creates the user;
  compute the claim result after the commit, before signing; a factor-read error → `sdk.ErrUnavailable` (the
  session row exists; the client retries/refreshes, refresh computes it again). The other three callers keep the
  BE-1 "before any session write" order.
- **Build notes (implementer, accepted as stated):** admin MFA = `admins || (nativeAll && UserAdminCheck != nil)`; the
  machine gate is AAL2 regardless; logic option `authlogic.WithMFAEnrollment(MFAEnrollmentConfig{Start, Grace})`; NO
  unexported twin of `AllowMFAEnrollment` (the groups use the exported option); `mfaEnrollmentPath` respects
  `MFAConfig.Methods` (no TOTP page when TOTP is not an allowed method); the once-per-session dedupe is per process via
  `cacher.Memory` (10k entries, TTL `min(SessionLifetime, 24h)`, floor 1m). PR-1 (#284) is merged, so PR-2 branches
  from f3c1db75 with no conflict.
- **Real run (PR-2) gains:** an enrolled user signs in with a password → step-up answer (JSON 401 challenge, and the
  browser redirect to the step-up page) → completes TOTP/passkey → 200 on a gated route.

## Open questions
- gps-three-sixty / battleboardhq: do their SPAs already branch on 401 + `WWW-Authenticate` for step-up? It decides
  how much of upgrade-note row 5 is new client work for them (not this slice, but it sizes the hand-off).

## Recommended reviews
**product-manager:** R-1 and R-3 before ratification; the "by host shape" paragraph as the adopter contract. The
two-reviewer gate (BE, SRE) has run and is folded above; re-reads at execution: **lead-backend** on PR-2's
`principal.go` + `refresh.go` diff (BE-1 order, BE-3 floors); **lead-frontend-engineer** on PR-2's `Browser()` denial
paths and the enrol-page redirect (no templ change expected); **platform-sre** on PR-5's rollout/rollback/break-glass
text; **data-integration-reviewer** on PR-3 only under R-1 (b).

## Notes
- **Carries closed here:** s4c R-1 (a)'s "native `all` + the enrollment grace" and its Notes handoff (the allowlist,
  the deadline formula, `holdsAAL2Factor` as the enrolment test, 403 `mfa_enrollment_required`, refresh re-check); the
  S4d4 follow-up #282. **Deviations from the handoff:** the per-user marker → R-1; the denial event carries
  `reason=enrollment` beside `gate=enrollment`; `/auth/sessions` joins the allowlist.
- **Follow-ups:** a views/goth account-page banner with the deadline (needs a views repin train); a P5 rule noticing an
  inert `SingleFactorAcknowledged` and an `EnrollmentStart`-literal check (workshop tag); `DelegatedAssuranceMaxAge`
  and `FreshAuth` asserted mode (from S4d4) are unaffected; re-sealing TOTP secrets on ring rotation (S4c).
- **Cut for the cap:** per-test case lists beyond the suites; the turso SQL twin beyond its encoding rule (R-1 (b));
  the runbook prose (PR-4 writes it from §Upgrade notes); the boot-line attribute order (PR-2 pins it in
  `configuration_test.go`).

## As built

| PR | Merged | Tag | `live-stores` |
|---|---|---|---|
| PR-1 | #284 (`f3c1db75`), #282 fix; closes #282 | none (R-2 (a): ships in v0.38.0) | — |
| PR-2 | #285 (`564d7185`) | `pockets/authentication/v0.38.0` (annotated `362e5a46`) | 40910848620 |
| PR-3 | dropped under R-1 (a); 0028 stays reserved | — | — |
| PR-4 | #287 (`efd0c3d7`) | none (examples + docs) | — |
| PR-5 | the record PR | none | — |

Cold sets: authentication v0.38.0 at tag time (MVS selects sdk v0.24.0); **set C** (#287: auth-cms, production-baseline, external-identity at `GOWORK=off`).
The gps-three-sixty, battleboardhq and views shapes were cold-verified by PR-5 at the final
versions; PR-5 also drove §1 (2)–(5) on a `GOWORK=off` auth-cms binary built from the tag.

Deviations from the task text:

- **PR-2:** R-6 (a) `mfa_enrolled` → the step-up answer, `reason=assurance`; R-7 the magic link
  computes after the commit and fails closed on a factor-read error (owner accepted; enhancement
  #286, low priority); native `all` refuses boot when `Methods` admits neither passkey nor
  totp; the logic option `authlogic.WithMFAEnrollment` validates; the boot-line gates follow
  the wiring; no unexported `AllowMFAEnrollment` twin; `mfaEnrollmentPath` respects `Methods`;
  the once-per-session dedupe is per process (`cacher.Memory`). The real run used temporary
  local `WithMFA` wiring, reverted.
- **PR-4:** the floor's TOTP redirect lands on an "expired setup" 410 and drops `return_to`
  (#288, low priority; documented, no fix in this slice). The owner drove auth-cms by hand in
  a browser 2026-10-29: "works!".
- **PR-5:** fixed two remaining "the only native AAL2 factor" sentences (the README Passkeys
  section and the docs site Passkeys section). §3's event count was not re-read (no admin
  wired in that run; #287 showed one row per session). §1 (7) (passkey with UV) was not shown
  live.

Follow-ups: #286; #288; a views/goth deadline banner; a P5 rule for an inert
`SingleFactorAcknowledged` and an `EnrollmentStart`-literal check; the per-user marker (R-1
(b)) if a host asks.
