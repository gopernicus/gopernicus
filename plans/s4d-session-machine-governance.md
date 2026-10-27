# S4d part 1 — Session and machine governance: global revocation, registration mode, API-key lifecycle, keys under MFA gates (SOC 2 readiness, slice 4d)

**Status:** EXECUTED 2026-10-26 (tags: authentication v0.31.0 #214; views/goth v0.9.0 #215; stores pgx v0.15.0 / turso v0.14.0 #216; record #217). Ratified 2026-10-26: R-1–R-5 all (a); H1 owner ruling (revoke-all admits external tokens). PR-1 and PR-2 each passed a lead-backend review (ship-with-edits, fixed before merge). Owner waived the Turso libSQL-server leg and the owner drill; §3 from the tags is the acceptance evidence. As-built deviation: §3's external admin keeps a live IdP session after revoke-all (200, corrected below). Release record: `RELEASING.md` "Session and machine governance".

**Progress 2026-10-26:** tag 1 `pockets/authentication/v0.31.0` @ dfe104b6 (live-stores green on main; set A cold-verified). PR-3 #215 merged → tag 3 `views/goth/v0.9.0` @ b10c1df8 cold-verified (views + core + ui/goth v0.2.1 + rsc-qr + go-webauthn; full set C after tag 2); PR-3 also guarded the auth-cms login override and moved auth-cms core to v0.31.0. PR-2 (60b6dc34) review ship-with-edits, edits in progress. **Owner ruling:** the Turso libSQL-server leg is waived for this release — the local-file driver run (`-tags=integration`, 823/0/0) is accepted as Turso coverage (CI skips Turso: empty token); record in PR-4.

- **Size:** M — four PRs, three trains, four tags (authentication core; stores pgx + turso; views/goth); one migration
  (0027, conditional); no sdk change. Part 2/3 of S4d (M4, M7, M8, S4c-2) are separate plans.
- **Anchors.** `main` @ 32f22c0d (S1–S3, S5, S6, S7, S4a–S4c RELEASED): sdk v0.20.0; authentication v0.30.0; stores pgx v0.14.0 /
  turso v0.13.0 (both pin core v0.29.0); views/goth v0.8.1 (pins core v0.28.0); examples pin core v0.30.0, the baseline
  pins pgx v0.14.0; pgxdb v0.14.0, turso v0.9.0, workshop v0.9.0; baseline ledger at 0033. Sources: `roadmap.md:181-199`,
  `review.md:274-283` (M5, M6), `review.md:369` (the rotation claim), `control-map.md:14,17,19,32,37,116`,
  `s4a-mfa-posture.md:50-55,300,491`, `s4c-totp.md:41,266,474`, `s7-authorization-governance.md` (route and opt-in precedents).
- **Owner rulings (binding).** Host opt-in everywhere; a migration ships but is REQUIRED ONLY WHEN THE FEATURE IS WIRED
  (unwired + unmigrated boots; wired + unmigrated refuses, naming the file — the S4c 0025/0026 rule); canonical sets ship
  final schema only; production WARNs over refusals unless argued; JSON contracts first-class (goldens on every new route);
  per-module annotated tags, explicit `GOWORK=off go get` repins, never `go work sync`; authentication/authorization naming.
- **What `main` already does.** `RevokeAllUserSessions` is per user (`logic/authentication/session_management.go:35-44`) over
  `ManagementRepository.RevokeAllForUser` (lock user → bump `auth_revision` → delete sessions, grants, `password_reset`
  challenges; `stores/pgx/oauth2.go:384-394`, `credential_writes.go:30-43`); nothing bundled revokes globally — the README says
  "JWT-secret rotation does not end sessions" (`README.md:2820-2823`) and refresh never consults the signer. Registration is
  open whenever password flows are on (`service.go:505`); accounts are also created by passwordless provision-on-redeem
  (intent captured in the binding AT ISSUE, `provisioning.go:58-67`), OAuth first sign-in (`oauth.go:425,597`) and external
  JIT provisioning (`external.go:689-697`). API keys never expire by default (`machine.go:120`, `apikey.go:17`), the list has
  no filter (`apikey/repository.go:22`), and a service account cannot be disabled (`serviceaccount/repository.go`; S3 shipped
  `DeleteWithKeys`). API keys pass every MFA gate: `admitMFA` returns true for `CredentialAPIKey` before the predicate runs
  (`inbound/http/authenticator.go:154-157`), `principalSet.assure` returns nil for every kind but access and external tokens
  (`principal_options.go:383-389`), and `machineMFA` is conditional on a native factor (`routes.go:262`). `UserAdministration`
  defaults to `RequirePrincipal(Live())`, which admits API keys (`routes.go:203`). Delegated tokens refuse `aal`/`methods`
  (`token.go:125-150`) — part 2.

## Goal
An operator ends every session in the fleet with one audited call (and, opt-in, one admin route) and knows exactly what
survives it; a host chooses who may create an account (open, by invitation, nobody) and the choice holds on every
account-creating path; machine credentials have a bounded life, a list an operator can filter, and a reversible pause that
cannot be undone by a rollback; and no API key passes an MFA gate.

## Scope and non-goals
**In scope:** `RevokeAllSessions` + `all_sessions_revoked_global` + the opt-in route (CC7.4, FAC-12); `IdentityConfig.
Registration` on every native and external account-creating path (M5/FAC-8); `APIKeyMaxTTL`, the list filter, the
`status` field, `DisableServiceAccount`/`EnableServiceAccount` + 0027 (M6/FAC-9, FAS-3); API keys denied at the MFA gates
with a hatch; the goth login link; docs and the record. **Non-goals (part 2/3):** `SessionsConfig.IdleTimeout` /
`last_rotated_at`, `AccessTokenTTL` ceiling, Live-tier defaults on bundled reads, `ExternalProvider.MaxTokenLifetime` and
`RevocationFailOpen` (M4); the key-ring JWT signer with `kid`, `KeyedEncrypter` for delivery/provider-token keys, `iss`/`aud`
on first-party tokens (M7); the breach checker and escalating lockout (M8); native `all` + enrollment grace (S4c-2);
`aal`/`methods` on delegated tokens (stated residual; part 2); API-key scopes; pre-expiry notifications for keys (Notes);
a cross-account "every key expiring before X" route (a SQL recipe ships instead); an sdk change of any kind.

## Decided by gate
Reviews 2026-10-26: lead-backend-engineer (BE), platform-sre (SRE), lead-frontend-engineer (FE). Folded unless marked.
- BE2 + SRE3 (HIGH, a one-transaction full-table `users` UPDATE holds row locks on every user: every `CreateForActiveUser`
  `FOR SHARE`, `lockOwner`, refresh rotation and credential mutation queues behind it; a pool-wide `DB_STATEMENT_TIMEOUT`
  kills the incident button; turso `BEGIN IMMEDIATE` takes the whole-database write lock) → chunked keyset batches (§1), per
  chunk the existing per-user order (users first, then sessions, grants, challenges); pgx `SET LOCAL lock_timeout`/`statement_
  timeout` inside each chunk; the shape is R-1.
- BE3 (HIGH, evidence written after the store returns can be lost) → two rows: `all_sessions_revoked_global` status `started`
  BEFORE the first chunk, then `success`/`failure` with counts; an audit write failure after commit logs ERROR with
  `error_kind`, never swallowed. BE4 → the bump is scoped to `status = 'active'` users; erased tombstones and deactivated
  rows are untouched (reactivation bumps anyway, `user_admin.go:149-151`).
- BE5 + FE2 (HIGH, "one predicate" does not hold — three bypass sites; a key sent the RFC 9470 challenge loops) → edits at
  `authenticator.go:155`, `principal_options.go:385-389` (apply ONLY the `mfa` option to keys, never the external checks) and
  `assurance.go:163-166` (the `boundedEnum` on `reason`; reason named `api_key`, since `credential` is already a Details key);
  the denial is **403 `mfa_credential_not_eligible` with NO `WWW-Authenticate` challenge** (a key cannot step up); under a
  `Browser()` set a key arrives by header, so the denial is JSON, never the login 303; doc comments at `useradmin.go:194-195`,
  `machine.go:157-158`, `authenticator.go:152-153`, `principal_options.go:222-223`, `passkeys.go:340-342` corrected.
- SRE12 (default deny vs admin-gate-only) → R-2; FE1 (HIGH, an admin API key could call revoke-all without MFA) → the
  revoke-all authenticator is access-token-only + `FirstParty()` + `Live()` REGARDLESS of R-2 and of the hatch (§1).
- BE6 + SRE6 (HIGH, disable-without-revoke lets keys resurrect when the feature is un-wired or the binary rolls back) →
  **disable = status `disabled` AND every unrevoked key revoked in ONE transaction; enable restores minting only** (keys stay
  revoked); README: "a compromised account → revoke its keys/delete; disable is a reversible pause of future mints".
- BE7 + SRE1 (BLOCKER, changing `ListByServiceAccount` forces store lockstep, breaks set A, auth-cms `authmem` and every host
  store) → **no existing port gains or changes a method (a release invariant under set A)**: optional `apikey.FilteredLister`
  (the `CascadeDeleter` precedent), NEW `Repositories` slots for `GlobalSessions` and `ServiceAccountStatus` (nil = unwired),
  storetest sub-suites skip when nil.
- SRE4 ("wired" must be an explicit host switch, not "repo non-nil": store constructors return complete bundles) →
  `AdministrationConfig.ServiceAccountStatus bool` + the slot; flag without slot → `ErrServiceAccountStatusRequires`; 0027 joins
  the "never probed blanket" list beside 0025/0026 (`stores/pgx/postgres.go:96-100`); the probe runs under a 5 s deadline and
  maps ONLY `sdk.ErrNotFound` to `ErrServiceAccountStatusSchemaMissing` (naming 0027), anything else to `sdk.ErrUnavailable`
  (the existing probes' unbounded `context.Background()` and blanket wrap, `constructor.go:260-286`, are named as a follow-up).
- SRE8 (a status read failing must be 503, not a bare 401 that reads as "bad key") → `verifyAPIKeyCredential` returns
  `sdk.ErrUnavailable` through `Authenticate`; act-as-user keys of a disabled account are blocked too.
- BE1 (BLOCKER, `s4c-totp.md:436,473` reserve 0027 for the S4c-2 grace marker) → S4d part 1 CLAIMS 0027 (`service_account_
  status`); the S4c-2 plan, not yet drafted, takes 0028 — the drafter of `s4c2-native-all.md` renumbers (this plan edits no
  other plan file). Ledger copy: `0034_service_account_status.sql`, byte-identical, in PR-2 (G34 reads the working tree, SRE5).
- BE (placement) + SRE11 → `IdentityConfig.Registration` (a DELIBERATE deviation from `roadmap.md:190`'s `PasswordConfig`:
  the mode means nothing on a `PasswordFlowsDisabled` host and must govern OAuth, passwordless and external provisioning);
  `ExternalConfig.Registration` DROPPED — root `New` hands the mode to the engine; `closed` beside `ProvisionOnFirstVerification`
  is a construction error; an unknown value refuses boot in EVERY mode (a typo never means `open`); boot line
  `registration=<mode> source=config|default`; **no WARN for `open`** (BE, SRE: a legitimate B2C posture).
- FE7 + BE11 + SRE10 (the invitation 403 is an oracle for "an invitation is pending for this address") → the invitation check
  runs FIRST (an existing account without an invitation gets the 403, not the 409); the oracle is a named posture (register
  already leaks existence through its 409, `service.go:504`); the IP budget (`routes.go:272`) is the mitigation; SRE's "decide at
  verify" NOT taken (it would mint unverifiable half-accounts). The lookup is an optional interface asserted on the existing
  resolver (`pendingInvitationResolver` precedent, `service.go:166`), reusing the resolve iteration and predicate (BE).
- FE3 (HIGH, the machine listing has no strict parsing: `q.Get`, unknown keys tolerated, repeated keys take the first) →
  `status`/`never_expires` go through `singleValued` + closed vocabularies with named 400s; contradictory filters → 400;
  unknown keys stay tolerated (compat); a derived `status` joins every `apiKeyResponse`; the moving-set caveat is documented.
- FE4 + SRE ("what pages at 2am": CI/CD mints with no `expires_at`, every key dies on the same day a year later) →
  `api_key_ttl_exceeded` carries `max_ttl_seconds` + `max_expires_at`; `api_key_minted` gains `expires_at` and
  `expires_at_defaulted`; a PAST `expires_at` → 400 (unvalidated today, `machine.go:239-243`); the DTO doc is rewritten; the
  fill-vs-refuse choice is R-4. SRE: 365 d default, docs recommend 90 d → R-3. The waiver is a bool
  (`APIKeyUnlimitedTTLAcknowledged`, BE), not a negative duration.
- FE5 → revoke-all answers **200** (synchronous counts), `ClearSessionCookies` on success for every transport
  (`passkeys.go:336`), no confirmation token (erase takes `{}`), `revoked_at` UTC RFC 3339 nanos. FE6 → disable/enable return
  `{service_account, changed}` (`userStatusResponse` shape), `requireJSON` + strict `{}`; `status` ALWAYS present (`active`
  when unwired). FE (seam) → `revokeAPIKey`'s missing `requireJSON` is pre-existing, named, not spread.
- FE (OAuth) → a registration refusal in the browser callback clears the flow cookie and 302s to the flow's validated
  destination with `?auth=registration_closed|registration_invitation_required` (the `pendingLinkRedirect` shape, `oauth.go:
  246-257`), which needs the service to return `RedirectTo` with the error (§2); the native-complete JSON route keeps the coded
  403; the pre-existing JSON-on-navigation for invalid state is out of scope.
- FE8 + BE8 (the goth login renders "Create an account" unconditionally beside password flows, `credential.templ:45-48`) →
  `LoginPage.RegistrationClosed` set by BOTH builders (`html.go:206-214`, `forms.go:175-180`), hidden only under `closed`
  (invitees need the link under `invitation_only`); a register-form case before `formFailure` (`forms.go:196-197`) for the
  invitation 403 with actionable copy; views/goth v0.9.0 (repin v0.28.0 → v0.31.0).
- BE9 (machine-MFA dead end) → `machineMFA` stays conditional (`routes.go:262`); `New` WARNs once when `admins` is on, no
  native AAL2 source is wired and the machine routes mount ("native sessions mint API keys at AAL1; wire a native factor or
  override `BundledRouteAuth.MachineLifecycle`"). BE10 → every new field has its three mirrors (`constructorConfig`, the group
  struct, the `WithX` copy) and a `config_env_test.go` row. SRE13 (P5) → `guard --posture` is NOT extended (the S7
  `InvitationTokenInQuery` precedent; the constructor WARN is the check; no workshop tag). SRE (pins) → examples start at
  core v0.30.0, not v0.29.0; PR-4 moves them to v0.31.0.

## Design
### 1. Global session revocation (authentication v0.31.0; stores v0.15.0 / v0.14.0; CC7.4)
**Port (new slot).** `Repositories.GlobalSessions session.GlobalRevoker`:
```go
type GlobalRevoker interface { // every active user's revision bumped and every session, step-up grant and password_reset challenge gone
	RevokeAllBatch(ctx context.Context, afterUserID string, limit int, now time.Time) (GlobalRevocationBatch, error) // keyset page over users WHERE status='active' AND id > afterUserID ORDER BY id; ONE tx per batch
	SweepOrphans(ctx context.Context, now time.Time) (sessions, grants int, err error)                                // deletes sessions/grants whose user is not active (deactivated users' rows too); the final pass
}
type GlobalRevocationBatch struct{ Users, Sessions, Grants int; LastUserID string; Done bool }
```
Per batch, in one transaction and in the per-user lock order (`UPDATE users SET auth_revision = auth_revision + 1, updated_at
= now WHERE id IN (page)` first, then `DELETE FROM sessions / authentication_grants / challenges(password_reset) WHERE user_id
IN (page)`; pgx adds `SET LOCAL lock_timeout = '5s'` and `SET LOCAL statement_timeout = '60s'`). Chunk size 500 (constant;
R-1). The race with the fenced mint is safe under READ COMMITTED (BE): a mint holding `FOR SHARE` delays the batch, the later
DELETE sees its row; a mint after the bump gets `sdk.ErrConflict`. Delegated OAuth2 connections are rows in `sessions`, so
connected apps are disconnected too (stated). pgx and turso `OAuth2Store` implement it beside `RevokeAllForUser`; the stores
fill the slot unconditionally (no schema change); auth-cms `authmem` implements it.
**Service.** `authlogic.Service.RevokeAllSessions(ctx, actor Principal) (GlobalRevocation, error)` — nil slot →
`ErrGlobalRevocationUnsupported`; records `all_sessions_revoked_global` **status `started`** (Actor = `actor`, Details
`{batch_size}`), loops `RevokeAllBatch` until `Done`, runs `SweepOrphans`, records `success` with `{users, sessions, grants,
batches, duration_ms}` — or `failure` with the partial counts and `error_kind`, returning the partial `GlobalRevocation` beside
the error (idempotent, re-runnable: a re-run bumps again, which is harmless). Uses `s.now()` (the per-user twin's bare
`time.Now()` at `session_management.go:39` is an adjacent fix in the same PR). Non-HTTP entry: the method is the API; the README
shows a host CLI recipe (an IdP or admin-sign-in compromise makes the route unreachable — SRE).
**Route (R-5; opt-in).** `AdministrationConfig.GlobalSessionRevocationRoute bool` → `POST /auth/admin/sessions/revoke-all`;
set without the slot or without `UserAdminCheck` → `ErrGlobalSessionRevocationRouteRequires`. Gates, in order: a dedicated
authenticator `RequirePrincipal(Accept(CredentialAccessToken), FirstParty(), Live())` (API keys refused UNCONDITIONALLY, FE1/SRE
— a key survives the revoke and could fire it in a loop), `browserSafe`, `adminPrincipal(UserAdminRevokeAllSessions, "")` (new
action; the `admins` MFA gate applies), a per-actor budget `global_revoke:` 1/min. Body strict `{}`; 200 `{users, sessions,
grants, batches, revoked_at}` (UTC RFC 3339 nanos); `ClearSessionCookies` on success (the caller's own session is gone; a bearer
SPA gets 401 next); the HTML form lane is NOT added (JSON only; the self route `/auth/sessions/revoke-all` is unrelated and the
README names both). Under `MFAConfig.Required: none` the route is AAL1-reachable — the host's gate decides (Risks).
**Revocation SLA (README + `production-posture.md:433-448`, replacing line 448):**

| Credential or proof | After `RevokeAllSessions` |
|---|---|
| refresh token, `Live()` routes, step-up grants, pending magic links / OTPs / OAuth codes / passkey+TOTP pending rows | dead at once (row gone or revision fenced) |
| first-party access JWT on `LivenessOnMutation` safe reads and on `Stateless()` routes | valid ≤ `SessionsConfig.AccessTokenTTL` (15 m default); rotate `AUTH_JWT_SECRET` for immediate death |
| delegated OAuth2 connections | disconnected (same table) |
| API keys, act-as-user keys included | UNTOUCHED — a separate rail; pair with `DisableServiceAccount`/key revocation (§3) |
| external IdP tokens | the provider's session; local kill switch is deactivation (`requireActive`) |
| invitation tokens, deactivated/erased users' rows | untouched / swept (no bump) |

### 2. Registration mode (`IdentityConfig.Registration`; M5)
`type RegistrationMode string`; `RegistrationOpen` (zero, `""` or `"open"`), `RegistrationInvitationOnly`, `RegistrationClosed`;
`IdentityConfig.Registration RegistrationMode` `env:"AUTH_REGISTRATION"`; unknown → `sdk.ErrInvalidInput` at construction in
every mode; `invitation_only` without a Granter → `ErrRegistrationModeRequiresInvitations`; `closed` beside any
`ExternalProvider.ProvisionOnFirstVerification` → `ErrRegistrationClosedWithProvisioning`. Boot line `registration=<mode>
source=config|default`. **Lookup port:** `invitationLookup{ HasPendingInvitation(ctx, kind, value string) (bool, error) }`
asserted on the resolver; `invitations.Service` implements it over the resolve iteration (`service.go:898-945`): pending or
accepting, unexpired, any `AutoAccept`. **Per-path table (the contract; README):**

| Path | `open` | `invitation_only` | `closed` |
|---|---|---|---|
| `POST /auth/register` (`service.go:505`) | as today | lookup FIRST → 403 `registration_invitation_required`; then Provision (409 keeps its meaning) | routes unmounted (POST + GET page); `/auth/verify`, resend stay for existing unverified accounts |
| passwordless provision-on-redeem (`provisioning.go:58-67`) | binding `ProvisionIfAbsent` as today | `ProvisionIfAbsent = lookup(kind, value)` AT ISSUE; redeem of an unknown address stays the generic 401 | `ProvisionIfAbsent = false` |
| OAuth first sign-in (`oauth.go:425`) | as today | lookup on the provider-verified email → refusal | refusal |
| external JIT (`external.go:689-697`) | `ProvisionOnFirstVerification` | lookup on the verified email/phone before `provision`; unverified → deny | construction error (above) |
| invitation accept | session-gated; creates no account | — | — |

Refusals: `ErrRegistrationClosed` / `ErrRegistrationInvitationRequired` (both `sdk.ErrForbidden`, `SafeDomainError` codes
`registration_closed` / `registration_invitation_required`); OAuth records `oauth_register failure reason=registration_closed|
invitation_required` and the browser callback clears the flow cookie and 302s to the validated destination with `?auth=<code>`
— `OAuthCallback` returns the validated `RedirectTo` beside the error through `authlogic.RegistrationRefusal{Code, RedirectTo}`
(FE); external records `external_subject_unknown reason=registration_closed|invitation_required`. Residuals: a passwordless link
issued under `invitation_only` provisions even if the invitation is revoked before redeem (the binding decides, by design);
registrations pending when the mode flips are not re-checked at verify (the account exists). **Views:** `LoginPage.
RegistrationClosed bool` (zero keeps today), both builders; goth hides the link (PR-3). **gps-360-go (Google OAuth only,
`PasswordFlowsDisabled`):** the mode governs its first Google sign-in — `invitation_only` makes onboarding invitation-driven
(an invitation to the Google-verified address, resolved by S7 on provisioning), `closed` freezes the roster; nothing else in
this slice touches it unless it wires machine repositories.

### 3. API-key lifecycle: max TTL, filter, status, disable (M6)
- **`AdministrationConfig.APIKeyMaxTTL time.Duration`** (`env:"AUTH_API_KEY_MAX_TTL"`): resolved once at construction —
  explicit > 0 stands; zero → **production default (R-3)**, development zero = unlimited (today); `APIKeyUnlimitedTTLAcknowledged
  bool` keeps unlimited in production with one `posture_waiver=APIKeyUnlimitedTTLAcknowledged` WARN (the `checkPosture` shape,
  `security.go:616-655`). `MintAPIKey`: `expires_at` beyond `now + max` → `ErrAPIKeyTTLExceeded` (400 `api_key_ttl_exceeded`,
  body `max_ttl_seconds`, `max_expires_at`); a past `expires_at` → 400 `api_key_expiry_past`; omitted → R-4. `api_key_minted`
  Details gain `expires_at` (or `never`) and `expires_at_defaulted`. Existing never-expiring keys are untouched; the runbook ships
  the SQL recipe (`SELECT … FROM api_keys WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at < now() + interval '30
  days')`) because the listing is per account (SRE). Boot line `api_key_max_ttl=<d>|unlimited source=…`.
- **Filter (optional interface, no port change).** `apikey.Filter{Status: ""|active|expired|revoked; NeverExpires bool; Now
  time.Time}`; `apikey.FilteredLister{ ListByServiceAccountFiltered(ctx, saID, Filter, list.Request) }` asserted on
  `Repositories.APIKeys`; SQL matches `APIKey.Expired` exactly (at-or-before, `apikey.go:68-69`) with revoked taking precedence
  (`machine.go:276-281`). HTTP: `GET /auth/service-accounts/{id}/keys?status=…&never_expires=true` — `singleValued`, closed
  vocabulary, `status=expired&never_expires=true` → 400 `filter_contradiction`, a store without the lister → 400
  `filter_unsupported`; `apiKeyResponse` gains derived `status` on every row; cursors do not bind the clock (a key that expires
  between pages moves; README). Never filter in memory over a page.
- **Status (new slot, conditional schema).** `Repositories.ServiceAccountStatus serviceaccount.StatusRepository`:
```go
type Status string // StatusActive (default; an absent row), StatusDisabled
type StatusRepository interface {
	GetStatus(ctx, id string) (Status, error)                                  // absent row → StatusActive
	Statuses(ctx, ids []string) (map[string]Status, error)                     // the list page, one query
	Disable(ctx, id, changedBy string, now time.Time) (StatusChange, error)    // ONE tx: upsert status row + UPDATE api_keys SET revoked_at = now WHERE service_account_id = id AND revoked_at IS NULL; Changed=false when already disabled
	Enable(ctx, id, changedBy string, now time.Time) (StatusChange, error)
	Delete(ctx, id string) error                                               // called by DeleteServiceAccount when wired (no orphan)
	ProbeSchema(ctx) error                                                     // sdk.ErrNotFound = 0027 absent
}
type StatusChange struct{ Changed bool; KeysRevoked int }
```
  pgx `0027_service_account_status.sql`: `SET LOCAL lock_timeout = '5s'`; `CREATE TABLE IF NOT EXISTS service_account_status
  (service_account_id TEXT COLLATE "C" PRIMARY KEY, status TEXT NOT NULL CHECK (status IN ('active','disabled')), changed_at
  TIMESTAMPTZ NOT NULL, changed_by TEXT NOT NULL DEFAULT '')`; turso twin (TEXT timestamps); no FK; NOT in the blanket probe.
  **Wired** = `AdministrationConfig.ServiceAccountStatus: true` (`env:"AUTH_SERVICE_ACCOUNT_STATUS"`): flag without slot →
  `ErrServiceAccountStatusRequires`; wired + 0027 absent → `ErrServiceAccountStatusSchemaMissing` naming 0027 (probe: 5 s
  deadline, `ErrNotFound` only); unwired → the table is never read, boots. **Service:** `DisableServiceAccount(ctx, actor, id)`
  / `EnableServiceAccount` (via `adminPrincipal`-style actor; unwired → `ErrServiceAccountStatusUnavailable`); `MintAPIKey`
  refuses a disabled account (`ErrServiceAccountDisabled`, 409 `service_account_disabled`); `verifyAPIKeyCredential` reads
  `GetStatus` when wired — disabled → deny + `apikey_auth blocked reason=service_account_disabled` (act-as-user keys included; a
  new bounded `reason` on `recordAPIKeyAuth`), a read error → `sdk.ErrUnavailable` (503). `DeleteServiceAccount` deletes the
  status row when wired. **Routes** (under the machine gate + `requireMachineMFA`, only when wired): `POST
  /auth/service-accounts/{id}/disable|enable` → `{service_account, changed, keys_revoked}`; `serviceAccountResponse.status`
  always present. Events `service_account_disabled {keys_revoked}` / `service_account_enabled`. **Rollback caveat (README):**
  un-wiring or rolling back stops READING the table — new mints on a disabled account resume, but the revoked keys stay
  revoked (why disable revokes). `changed_by` is an admin id, kept like security-event actors (erasure leaves it).

### 4. API keys under MFA gates (the S4a residual; R-2)
`mfaRefusal` is reached by keys at all three sites (BE5): `admitMFA` drops its early return; `assure` runs the `mfa` option
(and only that) for `CredentialAPIKey`; the predicate's first case `cred.Kind == CredentialAPIKey && !s.apiKeysExempt` →
reason `api_key`. The denial writes **403 `mfa_credential_not_eligible`, no challenge** (`writeMFARequired` branches on the
reason), records `mfa_required_denied {gate, reason: api_key, credential: api_key}` (the `boundedEnum` at `assurance.go:166`
admits `api_key`). Hatch `MFAConfig.APIKeysExempt bool` restores today's pass at every gate; production logs
`posture_waiver=APIKeysExempt`, development a plain WARN. The revoke-all route refuses keys regardless (§1). `machineMFA`
stays conditional with the BE9 boot WARN. Boot line gains `api_keys=denied|exempt`. Delegated-token `aal` → part 2.

### 5. Events, config mirrors, docs
New types in `Types()` (`securityevent.go:549-568`): `all_sessions_revoked_global` (started/success/failure), `service_
account_disabled`, `service_account_enabled`; extended Details: `api_key_minted` (`expires_at`, `expires_at_defaulted`),
`apikey_auth` (`reason`), `mfa_required_denied` (`reason=api_key`), `oauth_register`/`external_subject_unknown` (`reason`).
New `UserAdminAction`: `UserAdminRevokeAllSessions = "revoke-all-sessions"`. Config: `IdentityConfig.Registration`;
`AdministrationConfig.{GlobalSessionRevocationRoute, ServiceAccountStatus, APIKeyMaxTTL, APIKeyUnlimitedTTLAcknowledged}`;
`MFAConfig.APIKeysExempt`; each mirrored in `constructorConfig` and its `WithX` (`config.go:989-996`), each env field in
`config_env_test.go` and both examples' `.env.example`. Docs: README (incident recipe + CLI, SLA table, registration per-path
table, TTL + filter + disable, the gate change), `production-posture.md` ("Added by S4d" refusals/waivers; the SLA rows;
line 448 removed), `keys-and-rotation.md:60-70` (the manual `DELETE FROM sessions` recipe superseded — it never bumped
revisions), `operations-runbooks.md` ("Revoke every session", "Disable a service account", "Find never-expiring keys"),
`SECURITY.md` versions, `control-map.md:17,19,32,37,116` (+ the event row), the docs-site pocket page.

## Upgrade notes
> **Read first:** `grep -rnE 'RequireMFA\(|Accept\(.*CredentialAPIKey|UserAdminCheck|AdministrationConfig\{|MFAConfig\{|
> IdentityConfig\{|apikey\.APIKeyRepository|session\.ManagementRepository' --include='*.go'`. **By host shape:** every host →
> rows 1, 2; hosts admitting API keys on `RequireMFA()` routes or through `UserAdminCheck` → row 3 (the break); machine hosts in
> production → rows 4–6; store hosts → rows 7–8; goth hosts → row 9; OAuth-only hosts (gps-360-go) → rows 1, 2 only.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `IdentityConfig.Registration` (`AUTH_REGISTRATION`); unknown value refuses boot; `closed` + `ProvisionOnFirstVerification` refuses | zero = `open`, today's behaviour; boot line names the mode | — | authentication@v0.31.0 |
| 2 | `RevokeAllSessions`, `Repositories.GlobalSessions`, the opt-in route, `UserAdminRevokeAllSessions`, three event types | additive; a host `UserAdminCheck` ignoring `Action` admits the new action — but the route mounts only under the flag | leave false | authentication@v0.31.0 |
| 3 | **BREAKING:** API keys denied at `RequireMFA()`, the admin and the machine MFA gates (403 `mfa_credential_not_eligible`, no challenge); admin passkey/TOTP reset and every admin route included | a service account administering users or calling an `Accept(…APIKey)` + `RequireMFA()` route is refused | `MFAConfig.APIKeysExempt` (production WARN) | authentication@v0.31.0 |
| 4 | `APIKeyMaxTTL`: production zero → the R-3 default; `expires_at` beyond it → 400; past → 400; omitted → R-4 | **behaviour change in production:** keys minted without `expires_at` now expire (or 400); `api_key_minted` names it | `APIKeyUnlimitedTTLAcknowledged` (production WARN); set `APIKeyMaxTTL` explicitly | authentication@v0.31.0 |
| 5 | `apikey.FilteredLister`; `?status=`/`?never_expires=`; `apiKeyResponse.status`; strict parsing of the two new params | additive; a store without the lister → 400 `filter_unsupported` on a filtered call only | — | authentication@v0.31.0 |
| 6 | `ServiceAccountStatus` + `Repositories.ServiceAccountStatus`; disable/enable routes; `serviceAccountResponse.status`; `apikey_auth reason` | opt-in; wired + 0027 absent → BOOT FAILURE naming 0027; unwired never reads the table | leave false | authentication@v0.31.0 |
| 7 | **migration 0027** (`service_account_status`), ledger 0034; `GlobalRevoker` + `StatusRepository` + `FilteredLister` filled | apply 0027 before setting row 6's flag; otherwise optional | — | stores pgx@v0.15.0, turso@v0.14.0 |
| 8 | stores repin core v0.29.0 → v0.31.0 | MVS lifts a store host's core | — | stores pgx@v0.15.0, turso@v0.14.0 |
| 9 | `LoginPage.RegistrationClosed`; the goth login hides "Create an account" under `closed`; the register-form 403 copy | a `closed` host on views v0.8.1 still renders the link (it answers 404) | — | views/goth@v0.9.0 |
| 10 | the BE9 WARN (admins + no native factor + machine routes); `machineMFA` unchanged | one WARN line | wire a factor / override `MachineLifecycle` | authentication@v0.31.0 |
| 11 | auth-cms wires rows 2, 6 (and `invitation_only` behind an env), `authmem` implements the three ports; baseline stays machine-unwired; both `.env.example` | example behaviour | — | examples (untagged) |

Row 3 is the behaviour break; row 4 is a production-only behaviour change; row 6/7 is the conditional boot failure. Every
module ships MINOR under v0; tag messages name rows 3, 4, 6, 8. **Not touched:** sdk, `pockets`, workshop (no P-rule), ui/goth,
any existing port's method set (the set-A invariant).

## Risks
(1) Row 3 breaks machine administration silently for a host whose `UserAdminCheck` admits service accounts; the 403 code and
the Read-first grep are the warning; the hatch is one field. (2) The chunked revoke is not globally atomic: a failure leaves a
partial fleet revoked, reported in counts and re-runnable; during the run logins on in-flight chunks stall briefly (R-1).
(3) A stateless access JWT outlives the revoke by ≤ `AccessTokenTTL`; API keys and IdP sessions are untouched — the SLA table
and the runbook pairing (JWT secret rotation, key review) are the controls. (4) Production keys minted without `expires_at`
now expire a year (R-3) after mint with no pre-expiry signal; the event, the filter, the recipe and the hatch are the
mitigations; a notification is a follow-up. (5) `invitation_only` is a pending-invitation oracle at `POST /auth/register`
(FE7), budgeted per IP. (6) Un-wiring `ServiceAccountStatus` resumes mints on disabled accounts (keys stay revoked). (7) Under
`Required: none` revoke-all is AAL1-reachable behind the host gate. (8) turso: each chunk is a whole-database write
transaction; the PR-2 live leg measures a 10k-user run on the playground before the record claims it.

## Tasks as PRs
Worktrees `../gopernicus-s4d-<pr>`; one merge/tag/push per command; every PR runs with model **opus**; live legs never touch
ports 55432 or 8082; `make check` in every verify line.

- **PR-1 — authentication core + `authmem` + storetest (`authentication/s4d-core`).** **Depends on** nothing (no sdk pin
  move). **BREAKING:** rows 3, 4. **Tag:** `pockets/authentication/v0.31.0` after a live-stores dispatch on `main`. **Files**
  (under `pockets/authentication/`): `logic/authentication/session/{global.go (new: GlobalRevoker, batch types), management.go
  (doc)}`; `logic/authentication/{session_management.go (RevokeAllSessions, s.now), useradmin.go (actions), machine.go
  (max TTL, status read, disable/enable, filter, reasons), service.go (registration mode, invitationLookup, Register order),
  provisioning.go + passwordless.go:526-560 (ProvisionIfAbsent from the mode), oauth.go:380-430,597 (refusal + RedirectTo),
  external.go:689-697 + external_config.go (the mode handed in; the construction error), assurance.go (reason api_key),
  securityevent/securityevent.go (+Types()), apikey/{filter.go (new), repository.go (FilteredLister)}, serviceaccount/{status.go
  (new), repository.go}, errors}`; `logic/invitations/service.go` (`HasPendingInvitation` over the resolve iteration);
  `inbound/http/{authenticator.go (admitMFA), principal_options.go (assure; docs), principal.go (writeMFARequired 403 branch),
  useradmin.go (revoke-all route + action), machine.go (filters via singleValued, TTL 400 body, disable/enable, status DTOs),
  routes.go (flags, mounts, the BE9 WARN inputs), html.go + forms.go (RegistrationClosed in both builders; the register 403
  case), oauth.go (refusal redirect + flow cookie), views.go (LoginPage field), host_policy.go, errors.go}`; `{config.go,
  configuration.go (resolve mode, TTL; the WARNs), constructor.go (slots, probe with deadline, ErrNotFound-only mapping),
  configuration_errors.go, security.go (two waivers), repositories.go (two slots), README.md}`; `stores/storetest/{global_
  revocation.go, service_account_status.go, apikey_filter.go (new; each skips when nil), storetest.go, reference_test.go}`;
  tests: `inbound/http/{useradmin_test.go, machine_test.go, mfa_test.go, oauth_test.go, forms_test.go, no_store_test.go (fixture
  wires admin + machine + the opt-ins, FE)}`, `logic/authentication/{global_revocation_test.go, registration_mode_test.go,
  machine_test.go}`, `config_env_test.go`, `security_test.go`, `mfa_posture_test.go`. **auth-cms (go.work CI):** `examples/
  auth-cms/pockets/authentication/outbound/authmem/{global_sessions.go, service_account_status.go (new), api_keys filter,
  authmem.go}`; `examples/auth-cms/cmd/server/{main.go (GlobalSessionRevocationRoute, ServiceAccountStatus, AUTH_REGISTRATION
  from env), authentication_host_api_test.go, machine_test.go}`; `examples/auth-cms/.env.example`. **Verify:** `cd
  pockets/authentication && go build ./... && go test -race ./... && go vet ./...`; `cd examples/auth-cms && go test ./...`;
  `make guard`; `make check` (stores still build: no port method changed); Verification §1.
- **PR-2 — stores: 0027, the three ports, repin (`authentication/s4d-stores`).** **Depends on** tag 1. **BREAKING:** rows 7–8.
  **Tag:** `pockets/authentication/stores/pgx/v0.15.0`, `…/turso/v0.14.0`. **Files:** `pockets/authentication/stores/{pgx,
  turso}/{migrations/0027_service_account_status.sql, global_sessions.go (new: RevokeAllBatch, SweepOrphans, SET LOCAL
  timeouts on pgx), service_account_status.go (new), api_keys.go (FilteredLister), service_accounts.go (status Delete hook),
  postgres.go|turso.go (slots; 0027 excluded from the blanket probe; README), go.mod, go.sum (core → v0.31.0 by explicit
  `GOWORK=off go get`, then `GOWORK=off go mod tidy`), conformance tests}`; `examples/production-baseline/workshop/migrations/
  primary/0034_service_account_status.sql` (G34, byte-identical). **Verify:** `make check`; `make guard` (G34, G35); live legs on
  the branch — pgx bare + `POSTGRES_TEST_SCHEMA=s4d` on a fresh PG17 (`lsof` first), the turso gate on a fresh libSQL, a
  `live-stores` dispatch; the concurrency leg (logins racing a global revoke → zero sessions below the new revision) and the
  10k-user duration measurement on both backends recorded in the PR; Verification §2.
- **PR-3 — views/goth: the login link and the register copy (`authentication/s4d-views`).** **Depends on** tag 1 (repin
  v0.28.0 → v0.31.0, forced by the page model). **Tag:** `pockets/authentication/views/goth/v0.9.0`. **Files:** `pockets/
  authentication/views/goth/{credential.templ (the `!m.RegistrationClosed` guard at :45-48; the invitation copy), credential_
  templ.go (from `make generate` ONLY), views_test.go, go.mod, go.sum}`; `examples/auth-cms/go.{mod,sum}`. **Verify:** `make
  generate` leaves no diff; `go test ./...` in the module and auth-cms; `make check`; **run and look:** auth-cms with
  `AUTH_REGISTRATION=closed` → `/auth/login` shows no "Create an account", `/auth/register` 404; `invitation_only` → the form
  re-renders with the invitation copy and the email kept.
- **PR-4 — the record (`docs/s4d-release-record`).** **Depends on** tags 1–4 cold-verified and §3. **Files:** `RELEASING.md`
  (ONE section "Session and machine governance (SOC 2 slice S4d part 1)": tags, the Upgrade notes by host shape, the 0027
  rule, the measured revoke durations, the verification record); every `examples/*/go.mod` pinning a moved module (explicit
  `GOWORK=off go get`: auth-cms core v0.31.0 + views v0.9.0; baseline core v0.31.0 + pgx v0.15.0); `examples/production-
  baseline/.env.example`; `SECURITY.md`; `workshop/documentation/docs/{pockets/authentication.md ("Added by S4d"),
  guides/production-posture.md, guides/keys-and-rotation.md, guides/operations-runbooks.md}`; `control-map.md`; `roadmap.md` S4
  line; `plans/s4d-session-machine-governance.md` (executed copy). **Verify:** `make check`; `make guard`; `GOWORK=off go build
  ./... && go test ./...` per repinned example; `pnpm build` in `workshop/documentation`.

## Sequencing
```
PR-1 ─[live-stores dispatch on main]─[tag 1: authentication v0.31.0]─┬─ PR-2 (repin + 0027 + ports) ─[live legs]─[tag 2: pgx v0.15.0, turso v0.14.0]
                                                                      └─ PR-3 (views repin) ─[run-and-look]─[tag 3: views/goth v0.9.0]
                                      tags 1–3 cold-verified + §3 ─ PR-4 (record + example repins)
```
PR-1 first (the ports and page model live in core); PR-2 and PR-3 in parallel off tag 1. Three trains is the floor: set A
holds because no existing port changes (BE7/SRE1).

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 1 | `pockets/authentication/v0.31.0` | PR-1 merge + dispatch | **set A:** core v0.31.0 beside RELEASED pgx v0.14.0 / turso v0.13.0 AND views v0.8.1 selects sdk v0.20.0 and builds (no lockstep; the new slots stay nil) |
| 2 | `…/stores/pgx/v0.15.0`, `…/stores/turso/v0.14.0` | PR-2 + live legs on `main` | **set B:** both stores + core v0.31.0 + pgxdb v0.14.0 + turso v0.9.0 select sdk v0.20.0 and build; a gps-360-go-shaped `main` (core + pgx store + goredis v0.6.0) builds |
| 3 | `…/views/goth/v0.9.0` | PR-3 + run-and-look | **set C:** views v0.9.0 + core v0.31.0 + ui/goth v0.2.1 + rsc-qr v0.1.0 + go-webauthn v0.1.0 + set B build an auth-cms-shaped `main` |

**Tagging.** Four annotated, unsigned tags, each cut at its squash commit, pushed, THEN polled on `.info`; cold-verify with
`GOWORK=off`, an empty `GOMODCACHE`, `proxy.golang.org` + `sum.golang.org`, `go mod verify`, `go build <module>/...`. Never tidy
before a tag resolves; never `go work sync`. **Not tagged:** sdk, `pockets`, workshop, ui/goth, the examples.

## Verification
Real binaries in DEVELOPMENT on a FREE port (`lsof -i :8094` first; never 8082 or 55432). `$B` = a scratch auth-cms binary
(`PORT=8094 AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 go run ./cmd/server`, authmem, console mailer); `$P` = production-
baseline on a fresh PG17 with the baseline ledger; events read from `GET /auth/admin/security-events` as a platform admin.
- **§1 — PR-1, the real binary over HTTP.** (1) Boot line `registration=open source=default api_key_max_ttl=unlimited
  api_keys=denied`; a second binary with `AUTH_REGISTRATION=bogus` refuses to start naming the field. (2) Two users signed in
  (cookie + bearer), one delegated OAuth2 connection, one pending magic link; as a non-admin `POST /auth/admin/sessions/
  revoke-all` → **403**; as an AAL1 admin → **401** + challenge (`admins`); after a TOTP step-up → **200** `{users:n,
  sessions, grants, batches, revoked_at}` with `Set-Cookie` clearing both cookies; the next `/auth/me` **401**; the bearer
  user's refresh → **401**; the magic link → the generic **401**; the connection is gone; the rail shows `started` then
  `success` with counts; an API key on the route → **401** regardless of `UserAdminCheck`; the 2nd call within a minute → **429**;
  a binary without the flag → **404**. (3) `AUTH_REGISTRATION=invitation_only` (a second binary): register `x@y` with no
  invitation → **403** `registration_invitation_required`; invite `x@y`, register → **201**, verify → the invitation grants
  (S7); a passwordless start for an uninvited unknown address → redeem **401** generic; Google-shaped OAuth first sign-in for
  an uninvited verified email → 302 to the destination with `?auth=registration_invitation_required`, flow cookie cleared,
  `oauth_register failure reason=invitation_required`; `closed` → `POST /auth/register` **404**, `GET /auth/register` **404**,
  `/auth/verify` still answers. (4) Machine: mint with `expires_at` past → **400**; `AUTH_RUNTIME_MODE=production`-shaped
  constructor test: zero TTL resolves to the default, omitted `expires_at` → R-4's outcome, beyond max → **400** with
  `max_ttl_seconds`; `GET …/keys?status=expired` lists only expired, `?status=expired&never_expires=true` → **400**
  `filter_contradiction` (revoked+never_expires is valid — corrected after the PR-1 review, L8), `?status=bogus` → **400**, repeated `status` → **400**; every row carries `status`. (5)
  `AUTH_SERVICE_ACCOUNT_STATUS=1`: disable → **200** `{changed:true, keys_revoked:2}`, the account's key → **401** + `apikey_auth
  blocked reason=service_account_disabled`, mint → **409**, disable again → `changed:false`, enable → mint **201**, the old keys
  stay revoked; unwired binary → routes **404**, `status: "active"` on every DTO. (6) An API key on `/demo/mfa`
  (`RequireMFA()`) → **403** `mfa_credential_not_eligible`, NO `WWW-Authenticate`, `mfa_required_denied reason=api_key`; with
  `APIKeysExempt` → **200** + the WARN; an admin API key on `/auth/admin/users` → **403**. (7) No response, row or log line carries
  a key, token or address.
- **§2 — PR-2, the store legs.** storetest `GlobalRevocation` (batches, `Done`, idempotent re-run, active-only bump, sweep of a
  deactivated user's rows), `ServiceAccountStatus` (disable revokes in one tx — a failing revoke leaves no status row; enable;
  Delete; probe `ErrNotFound` on an unmigrated schema), `APIKeyFilter` (the expiry boundary at-or-before, revoked precedence,
  turso precision) on pgx, turso, `TestReference`, authmem; the concurrency leg; the 10k-user timing on PG17 and the Turso
  playground (recorded, with `busy_timeout` behaviour); `$P`-shaped boot: ledger 0034 applied, with the flag and `service_
  account_status` dropped → refuses naming 0027; unwired without the table → boots.
- **§3 — from the tags (PR-4).** §1(2) and §1(5) against a `GOWORK=off` auth-cms binary (`go version -m`: core v0.31.0, views
  v0.9.0); `$P` boots from the tags with ledger 0034 and `GlobalSessionRevocationRoute` on; one revoke-all as its admin
  (external token at AAL2) → **200**, the admin's next request **200** — the IdP session survives (the SLA table); the local kill switch is deactivation: a deactivated external user's next request **401**. *(Corrected at execution; the draft said 401.)*

## Open rulings — YOUR CALL
- **R-1 — the global revoke shape.** (a) Chunked keyset batches (500 users per transaction, active users only, re-runnable,
  `started`/`success`/`failure` rows with partial counts on failure). (b) One transaction (atomic, but a full-table lock: every
  login queues for its duration, a pool `DB_STATEMENT_TIMEOUT` can kill it, turso holds the whole-database write lock).
  **Recommend (a):** "everything minted before T is dead" holds either way; (a) survives the incident it is for.
- **R-2 — API keys under MFA gates.** (a) Deny by default at ALL three gates (`RequireMFA()`, admin, machine) with 403
  `mfa_credential_not_eligible` and the `MFAConfig.APIKeysExempt` hatch (production WARN). (b) Deny by default only at the
  bundled admin gate; `RequireMFA()` keeps passing keys unless a host adds a `HumanOnly()` option. **Recommend (a):** the
  residual the roadmap names is "API keys pass EVERY MFA gate"; a route that says `RequireMFA()` and admits keys is a
  contradiction the host should resolve in code once, and the 403 cannot loop a machine client; cost: row 3 is BREAKING.
- **R-3 — `APIKeyMaxTTL` production default.** (a) 365 d, docs recommend 90 d. (b) 90 d. **Recommend (a):** the objective is
  "no unbounded credential"; a 90 d default with no pre-expiry signal turns every host without automated rotation into a
  quarterly outage; one env sets 90 d.
- **R-4 — an omitted `expires_at` in production.** (a) Fill with `now + max`, echo it in the 201, `expires_at_defaulted: true`
  on the event. (b) 400 `api_key_expiry_required`. **Recommend (a):** WARNs over refusals; existing automation keeps working
  and the event names the default.
- **R-5 — the revoke-all transport.** (a) Service method + the opt-in route (`GlobalSessionRevocationRoute`). (b) Service
  method only; hosts write their own transport. **Recommend (a):** the owner's React admin wants an API, the gate chain is
  pinned (access-token-only, Live, admin check, admins MFA, budget), and the flag keeps it off every host that does not ask.

## Open questions
_None from the code._ Host facts at adoption: whether any host implements `apikey.APIKeyRepository`, `session.
ManagementRepository` or a `UserAdminCheck` admitting service accounts outside this repo (none here beyond `authmem`); whether
gps-360-go wants `invitation_only` or `closed` for its Google roster.

## Recommended reviews
**product-manager:** R-2 (a vs b) and R-3/R-4 before ratification; the per-path registration table as the user-facing
contract. **platform-sre:** re-read the drafted §1 (chunking, timeouts, the turso measurement) and §3's conditional 0027.
**data-integration-reviewer:** `RevokeAllBatch` keyset paging and lock order on both dialects; the filter's expiry boundary;
`Disable`'s single transaction. **architecture-steward:** the two new `Repositories` slots vs optional interfaces, and
`IdentityConfig.Registration` reaching the external engine through root `New`.

## Notes
- **Part 2 handoff (M4, M7):** `IdleTimeout` (`last_rotated_at`, a store train), `AccessTokenTTL` ceiling, Live-tier defaults on
  `/auth/me`, `/auth/methods`, `/auth/sessions` and the admin/machine listings (BREAKING for `BundledRouteAuth` overrides),
  `ExternalProvider.MaxTokenLifetime` + `RevocationFailOpen`; key-ring JWT signer with `kid`, `KeyedEncrypter` for delivery and
  provider-token keys, `iss`/`aud`; `aal`/`methods` on delegated tokens. **Part 3 (M8):** breach checker + production WARN,
  escalating lockout. **S4c-2:** takes migration 0028 (this plan claims 0027).
- **Follow-ups:** a pre-expiry notification for API keys (a maintenance job reading `expires_at`); bounded deadlines and
  `ErrNotFound`-only mapping for the EXISTING 0025/0026 probes (`constructor.go:260-286`); `requireJSON` on `revokeAPIKey`;
  a cross-account key listing; a `HumanOnly()` principal option if R-2 = (b); the pre-existing JSON-on-navigation for an
  invalid OAuth state.
- **Cut for the cap:** per-test case lists beyond the suites; DTO tables beyond §1/§3 (the goldens are the contract); the
  handoff prose (PR-4 writes it); the SQL of the turso twin (TEXT timestamps, same columns).
