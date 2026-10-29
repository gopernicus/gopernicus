# S4d part 4 — Delegated `aal`/`methods` and `Identity.AuthTimeAsserted` (SOC 2 readiness, slice 4d part 4)

**Status:** EXECUTED 2026-10-28 (tags: sdk v0.24.0 @ 21f21f4f, #277; firebase v0.3.0 @ eefcc693, #278; authentication v0.37.0 @ 2fe347f5, #279; stores pgx v0.19.0 / turso v0.18.0 @ 118340a7, #280; examples + docs + runbook #281 @ ddab754a, no tags; record PR-6). See **As built** at the end; the tag table, MVS sets, upgrade notes, rollout/rollback and deviations are in `RELEASING.md` "Delegated assurance and asserted authentication time (SOC 2 slice S4d part 4)"; audit entry AUDIT-061. Build-time owner rulings (a) and (A) are the late section below. Was RATIFIED 2026-10-28 (owner: "all recs" on R-1..R-6, all (a); open questions answered: gps-360-go probably has no other verifier process; the demo rides AUTH_OAUTH2_DEMO). Was: DRAFT 2026-10-28 (planner; the two-reviewer gate — lead-backend-engineer BE-1..BE-12, platform-sre
SRE-1..SRE-13 — run on the sketch and folded under "Decided by gate"). Awaiting owner rulings R-1..R-6. Closes the two
residuals the control map carries to "S4d part 4": row 17 ("residual: `aal` on delegated tokens [S4d part 4, carried by
R-4]") and row 138 ("an `iat`-only provider gets a token-age bound only (`Identity.AuthTimeAsserted`, R-5) [S4d part 4]").

- **Size:** S–M — six PRs, five tags on five modules (sdk; firebase; authentication core; auth stores pgx + turso); ONE
  additive migration (0030, on `oauth_authorization_codes`); no views tag, no workshop module tag. Every behaviour change
  is host opt-in (`OAuth2Config.DelegatedAssurance`, `ExternalProvider.RequireAuthenticationTime`); the one unconditional
  change is verifier-side and admits a claim shape nobody mints until the flag flips.
- **Anchors (`main` @ be21b62c, #270).** sdk v0.23.0; authentication v0.36.0 (go.mod pins sdk v0.21.0); stores pgx
  v0.18.0 / turso v0.17.0 (both pin core v0.35.0, sdk v0.21.0); firebase v0.2.0 (pins sdk v0.18.0); go-oidc v0.2.0;
  auth0 v0.1.0; views/goth v0.9.0; workshop v0.10.2; examples auth-cms + production-baseline pin sdk v0.23.0 + core
  v0.36.0, external-identity sdk v0.22.0 + core v0.36.0. Migrations: both stores end at 0029 (0028 reserved for S4c-2;
  0030 is free — `database-default-ids.md` edits CREATE files, it claims no number); production-baseline ledger ends at
  `0035_session_activity.sql`. Last audit entry AUDIT-060. Sources: `s4d2-session-lifetime.md` ("Decided by gate" BE10 +
  SRE6, R-4/R-5, Notes "Part 3 handoff"), `s4d3-keys-passwords.md` §Scope ("deferred to a part 4") and Notes "Part 4
  handoff", `control-map.md:17,138`, `roadmap.md:181`, `NEXT.md` (2026-10-28 "Carries for S4d part 4").
- **Owner constraints (binding).** Plan-first; conformance in a core tag before stores use it; examples repin only via
  explicit `GOWORK=off go get`; never `go work sync`; additive migrations only; authentication/authorization naming;
  no local paths in tracked files; secrets never in logs; accept interfaces, return structs; vars/consts first, then
  interfaces. Real host wiring is out of scope (new hand-offs after the sprint).

## Context
S4d part 2 shipped `ExternalProvider.MaxAuthenticationAge` and left two residuals the gate would not let it close in
place: delegated OAuth2 tokens refuse `aal`/`methods` (`logic/authentication/token.go:161`), so a delegated credential
is `AssuranceUnknown` and every `RequireMFA` gate denies it; and `Identity.AuthenticatedAt` is `auth_time` else `iat`
with no way for the core to tell which, so a bound on an `iat`-only provider is a token-age bound `exp` already gives.
Part 3 kept to keys and passwords and handed both here. The code today: `oauth2/tokens.go:38` mints the delegated
session with empty `Authentication`; the `sessions` columns `authenticated_at`/`authentication_methods`/
`assurance_level` already exist (0003) and `sessionArgs`/`insertSession` already write them, so the DELEGATED ROW needs
no migration; `oauth_authorization_codes` (0019) has no such columns, and the code is the only thing that survives from
approval to redemption; pgx `CreateCode` already locks the approving session row with the full `sessionColumns`
(`stores/pgx/oauth2.go:103`, turso `oauthSession`), which is the atomic copy BE10 asked for.

**The "three adapters" corrected from the code.** Only three places produce `AuthenticatedAt`: the sdk's generic
`OIDC` provider (`sdk/capabilities/authentication/oidc.go:98`), the firebase provider
(`integrations/authentication/firebase/firebase.go:240`), and the conformance suite that pins the contract
(`authenticationtest/provider.go:118`). go-oidc is a `cryptids.JWTVerifier` and auth0 is a `Profile` preset; neither
touches the field, so neither changes nor tags (the s4d2 note "go-oidc / firebase / auth0" was a guess).

## Goal
A host that opts in gets delegated access tokens carrying the approving session's `aal`/`methods` (so an MCP grant
approved at AAL2 passes `RequireMFA`), and a host with a `MaxAuthenticationAge` bound can refuse `iat`-only identities
— with no behaviour change for a host that opts into neither.

## Definition of Done
- sdk v0.24.0: `Identity.AuthTimeAsserted`, set by `OIDC` and asserted by `RunProvider`; firebase v0.3.0 sets it.
- authentication v0.37.0: `OAuth2Config.DelegatedAssurance` (opt-in, boot-probed), delegated mints sign `aal`/`methods`,
  verifiers admit them, `Introspection` reports them; `ExternalProvider.RequireAuthenticationTime` (opt-in) denies
  `authenticated_at_unasserted`; the storetest runner `RunOAuth2CodeAuthentication` ships in this tag.
- stores pgx v0.19.0 / turso v0.18.0: migration 0030 + `oauth2.CodeAuthenticationRepository`, runner green on both.
- auth-cms demonstrates the delegated path end to end (authmem adopts the interface); production-baseline carries the
  0036 ledger copy and `IDP_REQUIRE_AUTHENTICATION_TIME`; docs and the record (RELEASING, SECURITY, AUDIT-061,
  control-map rows 17 + 138 → DEFAULT/OPT-IN, roadmap, NEXT) land; every tag cold-verified.

## Scope and non-goals
**In scope:** the two carries above, their tests, the migration + ledger copy, the examples, the docs site, the record.
**Non-goals (over-reach flagged):** an age bound on delegated assurance (R-4: documented residual); `FreshAuth` honouring
the asserted flag (R-6); flipping `RequireAuthenticationTime` on by default (R-5); re-consent tooling for grants
approved before the flag; exchange tokens raising the level (they copy the subject session's, never more); any views,
workshop-module or scaffold change; a `guard --posture` rule; host wiring (gps-360-go is the only host with `WithOAuth2`;
gps-three-sixty and battleboardhq wire native Google OAuth sign-in, no external IdP and no OAuth2 server — untouched).

## Decided by gate
Reviews 2026-10-28 on the sketch: lead-backend-engineer (BE, ship with edits), platform-sre (SRE, ship with edits).
Folded unless marked.
- **BE-1 (HIGH) kills the row-stamping alternative.** `RequirePrincipal` runs `assure(... mfaRefusal)` BEFORE
  `RequireLive` (`inbound/http/principal.go:89` vs `:104`), so an `Assurance` stamped from the live delegated row in
  `credentialSessionLive` would be invisible to `RequireMFA`. The claims design (§1) is the design; this order is the
  recorded reason.
- **BE-2 (HIGH):** the admin and machine gates are guarded by the same `admitMFA`. Checked: the bundled admin and
  machine groups set no `Audience`/`DelegatedAudience`, so `admits` refuses `ProfileDelegated` before any MFA check
  (`principal_options.go:422`). PR-3 pins it with a test (a delegated AAL2 token → 401 on `/auth/admin/users` and the
  machine lifecycle) and the README/SECURITY say "an MCP grant never reaches admin".
- **BE-3 (MED):** the store's `RedeemCode` re-reads the code with `oauthCodeColumns` (no 0030 columns) and
  `sameOAuthCode` ignores `Authentication`, so the persisted level is whatever the core read through
  `GetCodeWithAuthentication`. The runner asserts the persisted delegated session's `Authentication` equals the code's;
  the interface doc says the core is trusted to carry it (codes are single-use and immutable).
- **BE-4 (MED):** with the flag on `RedeemCode` (`tokens.go:28`) must switch `GetCode` → `GetCodeWithAuthentication`;
  missing it makes every grant silently `AssuranceUnknown`. Its own task + service test (flag on, AAL2 approver → `aal2`).
- **BE-5 + SRE-1 (HIGH, rollback):** `signAccess` stamping whenever the row records a level means flag-off does not stop
  stamping on issued grants, and a v0.36.0 binary refuses them. → R-2 (recommend: stamping is gated on the flag too), and
  a rollback section in the record: flag off → wait `AccessTTL` → downgrade; rolling back with the flag on is a known
  401 window of at most `AccessTTL`.
- **SRE-2 (HIGH):** "deploy the fleet" means EVERY process that verifies delegated tokens (a separate resource server,
  a sidecar) runs ≥ v0.37.0 before the flip. Upgrade notes step 1 says so.
- **SRE-3 (HIGH) + BE-10:** the tag order starts at sdk (core needs v0.24.0); the stores pin core v0.35.0 so PR-4 does an
  explicit `GOWORK=off go get .../pockets/authentication@v0.37.0`; each module proves its lifted sdk with the module
  dropped from `go.work` and `GOWORK=off go build/vet/test` before its tag (sdk is pre-1.0; v0.22/v0.23 must have broken
  nothing core, the stores or firebase call).
- **SRE-4 (MED):** guard P9 wants a byte-identical ledger copy per store migration — the baseline's 0036 is a verbatim
  copy of pgx 0030; a `--posture` host that bumps to pgx v0.19.0 copies 0030 into its ledger even with the flag off
  (upgrade note).
- **SRE-5 (MED):** AAL2 on a delegated grant never expires on refresh (the level is fixed at approval, bounded only by
  `OAuth2Config.SessionTTL`), and exchange can never raise it → R-4 + a SECURITY.md line.
- **SRE-6 (MED):** approval's transaction holds the approving `sessions` row lock against refresh `RotateAt`. The copy
  reads the row CreateCode ALREADY locks (no new lock, no new statement order); the runner adds a concurrent
  rotate-during-approve case on pgx (no deadlock, both complete); the transaction is bounded by the request context.
- **SRE-7 (MED):** turso legs skip in CI (no secrets). PR-4's verify runs `RunOAuth2CodeAuthentication` against local
  libsql before the turso tag; the record says "turso live leg: local only".
- **SRE-8 (MED):** no example drives the store path end to end (production-baseline has no `WithOAuth2`). Verification
  §1 drives authorize → approve → redeem → `RequireMFA` in auth-cms (authmem), and PR-4 adds a pgx integration case
  under the store's existing live leg; green storetest alone does not close the delegated task.
- **SRE-9 (MED):** `DelegatedAssurance` in auth-cms rides `AUTH_OAUTH2_DEMO` (development-only, authmem, no migration);
  production-baseline documents the flag beside `IDP_REQUIRE_AUTHENTICATION_TIME` in `.env.example` though it wires no
  OAuth2 server. The probe error names only "migration 0030 / oauth_authorization_codes" and never wraps a driver error
  that could echo a DSN or Turso URL (the 0029 `ProbeSchema` text is the template).
- **SRE-10 (LOW):** during a rolling flip a code approved flag-on and redeemed flag-off (or the reverse) is
  `AssuranceUnknown` — fails closed; one line in RELEASING.
- **SRE-11 (LOW):** host-written providers leave `AuthTimeAsserted` false, so `RequireAuthenticationTime` denies them
  (fails closed), and the new field breaks unkeyed `Identity{...}` literals — both in the sdk upgrade notes. The denial
  logs `reason=authenticated_at_unasserted provider=<name>`, never the token.
- **SRE-12 (LOW):** pgx 0030 copies 0029's header (`SET LOCAL lock_timeout = '5s'`, "on lock timeout re-run
  `make migrate`") and uses `ADD COLUMN IF NOT EXISTS`; turso applies one file in one transaction (plain ADD COLUMN).
- **BE-6 (MED):** `FreshAuth` (`principal_options.go:239`) reads `AuthenticatedAt` and accepts the `iat` fallback → R-6.
- **BE-7 (LOW):** `RequireAuthenticationTime` with `MaxAuthenticationAge == 0` refuses at config time
  (`sdk.ErrInvalidInput`, beside the negative-age check at `external_config.go:270`) — never a silent no-op.
- **BE-8 (LOW):** the new reason joins the `authext_age_denied` once-per-token dedupe, the `MaxAuthenticationAge` doc
  comment, the README table and `securityevent.go:263`.
- **BE-9 (LOW):** `OIDC.identity` sets the flag only when the mapped path is present AND parses to a valid instant;
  present-but-garbage falls back to `iat` with the flag false — a `RunProvider` case.
- **BE-11 (LOW):** inverted assertions to rewrite: `token_test.go:292` ("a delegated token carrying aal was admitted"),
  `README.md:2178`, `:2645`, `credential.go:98,105`, docs site `pockets/authentication.md:455,460,1065`.
- **SRE-13:** every PR ends `make guard && make check`; tags only after a green `live-stores` dispatch on `main` that
  includes the new runner. **Correction taken:** only auth-cms and production-baseline pin sdk v0.23.0; external-identity
  (v0.22.0) repins because it takes core v0.37.0; cms, minimal, jobs-minimal, goth-showcase are not touched.
- **BE-12 (INFO, checks out):** the approving row's level is read inside the transaction that inserts the code (pgx
  `lockSession`, turso `oauthSession`); the session insert already writes the three columns; delegated credentials never
  take the stateless shortcut; 0028 is S4c-2's; the go-oidc/auth0 correction stands.

## Design

### 1. Delegated `aal`/`methods` (authentication v0.37.0; stores pgx v0.19.0 / turso v0.18.0)
**Migration 0030 (`0030_oauth_code_authentication.sql`, both stores; additive).** Three nullable columns on
`oauth_authorization_codes`, the `sessions` encodings verbatim: `authenticated_at` (pgx `TIMESTAMPTZ`, turso `TEXT` in
`created_at`'s encoding), `authentication_methods` (`TEXT`, the JSON array `encodeMethods` writes), `assurance_level`
(`TEXT`). NULL on every row a pre-flag approval wrote. REQUIRED only when `OAuth2Config.DelegatedAssurance` is set; the
header comment says so, as 0029's does. No index; the table is tiny and pruned.

**Port (`logic/authentication/oauth2/repository.go`).** `Code` gains `Authentication session.AuthenticationMetadata`
(a copy of the approving session's; zero when the store did not record it). `sameOAuthCode` (both stores) keeps
comparing the binding fields only — `Authentication` is a carried copy, not a binding (BE-3). An OPTIONAL interface,
asserted on `Repositories.OAuth2` only when the flag is set (the `session.ActivityRepository` precedent):
```go
// CodeAuthenticationRepository is the OPTIONAL delegated-assurance capability (SOC 2 S4d part 4),
// backed by three nullable columns on oauth_authorization_codes (migration 0030).
type CodeAuthenticationRepository interface {
	// CreateCodeWithAuthentication is CreateCode's contract plus: inside the same transaction, the
	// LOCKED approving session's Authentication is copied into the code row. code.Authentication is ignored.
	CreateCodeWithAuthentication(ctx context.Context, code Code, approvingSessionID string) error
	// GetCodeWithAuthentication is GetCode's contract plus the recorded Authentication (zero when NULL).
	GetCodeWithAuthentication(ctx context.Context, hash string) (Code, error)
	// ProbeSchema reports sdk.ErrNotFound when migration 0030 is absent.
	ProbeSchema(ctx context.Context) error
}
```
`RedeemCode` is unchanged: `proposed.Authentication` is persisted by the existing session insert; the core is trusted to
carry it from `GetCodeWithAuthentication` to `proposed` (the doc says so). The wired-only column list
(`oauthCodeAuthenticationColumns = oauthCodeColumns + ", authenticated_at, authentication_methods, assurance_level"`)
is named only by the two WithAuthentication methods, so an unwired host never reads a column it has not migrated.

**Config and boot.** `OAuth2Config.DelegatedAssurance bool` (opt-in, default off), threaded to `oauth2.Config`
(`DelegatedAssurance bool`). `validateOAuth2` (constructor): when set, `Repositories.OAuth2` must implement
`CodeAuthenticationRepository` (`ErrDelegatedAssuranceRequires`) and a bounded `ProbeSchema` must pass — `ErrNotFound`
→ `ErrDelegatedAssuranceSchemaMissing` naming 0030, any other probe error → `sdk.ErrUnavailable` (the
`validateIdleTimeout` shape, `constructor.go:370`). The `auth: oauth2` boot line (or the existing OAuth2 line) gains
`delegated_assurance=on|off`.

**Service (`oauth2/`).** With the flag on: `Approve` calls `CreateCodeWithAuthentication`; `RedeemCode` calls
`GetCodeWithAuthentication` and sets `proposed.Authentication = code.Authentication` (BE-4); `signAccess` adds
`tokenClaimAssurance`/`tokenClaimMethods` from `sess.Authentication` when `Recorded()` and the level is `aal1`/`aal2` —
on redeem, refresh and exchange alike (all hold the session); under R-2 (a) the stamping is ALSO gated on the flag.
With the flag off: today's paths, byte for byte. Grants approved before the flag have NULL columns → the session row
records nothing → no claim → `AssuranceUnknown` (documented; re-consent is the upgrade). Exchange copies the subject
session's level and can never raise it.

**Verifier (`token.go`, unconditional).** `parseAccessClaims` admits and validates `aal`/`methods` on
`ProfileDelegated` exactly as on first-party (unknown value → refuse; absent → `AssuranceUnknown`/no methods). The
delegated shape rules (`client_id`, single `aud`, `iss`) are unchanged. `Credential.Assurance`/`Methods` doc comments
drop "delegated tokens leave it unknown/empty" (BE-11). `mfaRefusal`, `assure`, `MFASources` are untouched: a delegated
AAL2 credential whose approving session's methods pass `MFAConfig.Methods` now satisfies `RequireMFA` on a route that
admits it (`Audience`/`DelegatedAudience`); the bundled admin and machine groups still refuse the profile at admission
(BE-2, pinned).

**Introspection (R-3).** `Introspection` gains `Assurance string json:"aal,omitempty"` and
`Methods []string json:"methods,omitempty"`, filled only when recorded (never `"unknown"`), read from the live session
`liveAccess` already loads. Additive JSON; the auth-cms MCP demo prints them.

**Conformance (core tag first).** `storetest.RunOAuth2CodeAuthentication(t, newRepos)` — a SEPARATE runner (the
`RunIDs`/`RunPasswordHashUpgrade` precedent; skips when the store does not implement the interface): a code created
WithAuthentication from an AAL2 approver reads back the same `Authentication`; from a row recording nothing reads back
zero; `GetCode` (plain) still works on the same row; `CreateCode` (plain) then `GetCodeWithAuthentication` → zero (NULL);
the redeemed delegated session row persists the code's `Authentication` (BE-3); the plain `RunOAuth2` cases still pass
over the WithAuthentication methods; a concurrent `RotateAt` on the approving session during approval completes without
deadlock (SRE-6); `ProbeSchema` → nil on a migrated store (the stores' own tests prove `ErrNotFound` on an unmigrated
schema, as 0029 did).

### 2. `Identity.AuthTimeAsserted` (sdk v0.24.0; firebase v0.3.0; core v0.37.0)
**sdk.** `Identity.AuthTimeAsserted bool // AuthenticatedAt came from auth_time (or the provider's own sign-in instant),
not the iat fallback`. `OIDC.identity`: true when `lookupTime(claims, m.AuthTime)` succeeds (present and a valid
instant, BE-9), false on the `iat` fallback. `Clone` needs nothing (a bool). `authenticationtest.RunProvider` gains
`AuthTimeAsserted`: the valid claims (with `auth_time`) → true; the same claims without `auth_time` → `AuthenticatedAt`
≈ `iat`, false; `auth_time: "garbage"` → `iat`, false. The `Identity` doc on the port and `sdk/README.md` +
`workshop/documentation/docs/sdk/authentication.md` row 16 say what the flag means. **Upgrade note (SRE-11):** a
host-written `Provider` that does not set it reads false; unkeyed `Identity{...}` literals stop compiling.

**firebase.** `id.AuthTimeAsserted = token.AuthTime > 0` beside `firebase.go:240`; go.mod sdk → v0.24.0 (explicit
`GOWORK=off go get`, BE-10 proof). The suite's new case runs through the existing `unsignedMinter` (it already mints
`auth_time`). **go-oidc and auth0:** no code, no tag; their suites pick up the case under `go.work` (`make check`).

**core.** `ExternalProvider.RequireAuthenticationTime bool` (opt-in): under a positive `MaxAuthenticationAge`, an
identity with `!AuthTimeAsserted` is denied before the subject resolves with the new reason
`externalReasonAuthTimeUnasserted = "authenticated_at_unasserted"`, through `denyAuthenticationAge` (once-per-token
dedupe, BE-8); the order in `authenticationAgeRefusal` is: zero bound → ""; zero `AuthenticatedAt` → `missing`;
unasserted under the flag → `unasserted`; over age → `exceeded`. Set without a bound → `sdk.ErrInvalidInput` at config
(BE-7). The provider lifetime boot line gains `authentication_time=asserted_required|any`. The README table
("External authentication age") gains a column "with `RequireAuthenticationTime`" (Firebase: unchanged — always
asserted; go-oidc/auth0: denied unless the IdP sends `auth_time` — request it with `max_age`/`prompt`); the "core cannot
tell" paragraph is replaced. Default off (R-5): a flipped default would 401 every Google-ID-token host with a bound.
`FreshAuth` is untouched (R-6).

### 3. Examples, docs, record
- **auth-cms:** authmem implements `CodeAuthenticationRepository` (map copy from the approving session; `ProbeSchema`
  nil) and runs the new runner; `AUTH_OAUTH2_DEMO=1` sets `DelegatedAssurance: true`; the MCP demo page prints
  `aal`/`methods` from introspection; a demo route admitting the MCP audience under `RequireMFA()` (beside
  `demo.go:55`) is the real-behaviour probe for §1.
- **production-baseline:** `IDP_REQUIRE_AUTHENTICATION_TIME` (default false, `.env.example` comment beside
  `IDP_MAX_AUTHENTICATION_AGE`; the dev issuer and Firebase both assert `auth_time`, so the production test can run with
  it on and still log zero WARNs); ledger `workshop/migrations/primary/0036_oauth_code_authentication.sql` = pgx 0030
  byte for byte (guard P9/G34); `.env.example` documents `DelegatedAssurance` as the flag a host with `WithOAuth2` sets.
- **external-identity:** repin core v0.37.0 + sdk v0.24.0; its comment at `main.go:107` mentions
  `RequireAuthenticationTime`.
- **Docs site:** `docs/pockets/authentication.md` "Added by S4d part 4"; rewrite lines 455/460/1065; `docs/sdk/
  authentication.md` row 16; `docs/integrations/firebase.md`, `auth0.md` (one sentence each); `docs/guides/
  production-posture.md` (the two flags); operations runbooks: "Enable delegated assurance" (deploy every verifier →
  apply 0030 → flip → re-consent; rollback = flip off → wait `AccessTTL` → downgrade).
- **Record:** `RELEASING.md` ONE section "Delegated assurance and asserted authentication time (SOC 2 slice S4d part
  4)" (tag table, upgrade notes by host shape, the rollout + rollback order, SRE-10's flip-window line, review +
  verification records, as-built deviations); `SECURITY.md` version rows (sdk, firebase, core, both stores) + the
  "delegated AAL2 is fixed at approval; an MCP grant never reaches admin" lines; `AUDIT.md` AUDIT-061; control-map rows
  17 and 138; `roadmap.md:181` + a "S4d part 4 RELEASED" paragraph; `NEXT.md`; executed copy in `plans/`.

## Upgrade notes
> **Read first:** `grep -rnE 'WithOAuth2\(|MaxAuthenticationAge|authentication\.Identity\{|RequireMFA\(\)|/auth/oauth2/introspect' --include='*.go'`.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `Identity.AuthTimeAsserted` | additive; unkeyed `Identity{}` literals stop compiling; host-written providers read false | key the literal; set the field | sdk@v0.24.0 |
| 2 | firebase sets the flag | nothing until a core bound uses it | — | firebase@v0.3.0 |
| 3 | verifiers admit `aal`/`methods` on delegated tokens | nothing on the wire until a flag-on issuer exists; a v0.36.0 verifier beside a v0.37.0 flag-on issuer 401s delegated tokens | deploy every verifier first | authentication@v0.37.0 |
| 4 | `OAuth2Config.DelegatedAssurance` | opt-in; boot refuses without the store interface or 0030; new grants carry the approving level; old grants stay unknown until re-consent; delegated AAL2 now passes `RequireMFA` on routes that admit the audience | leave off | authentication@v0.37.0 + stores |
| 5 | `Introspection.aal`/`methods` | two additive JSON fields, only when recorded | — | authentication@v0.37.0 |
| 6 | `ExternalProvider.RequireAuthenticationTime` | opt-in; set without a bound refuses boot; `iat`-only tokens denied `authenticated_at_unasserted` | leave off | authentication@v0.37.0 |
| 7 | migration 0030 | additive, NULL on existing rows; a `--posture` host that bumps the store pin copies it into its ledger (P9) even with the flag off | — | stores pgx@v0.19.0 / turso@v0.18.0 |
| 8 | core and stores lift sdk to v0.24.0; stores pin core v0.37.0 | MVS; proven `GOWORK=off` per module before each tag | — | all |

Row 3 is the only unconditional change and it admits, never refuses. **Rollout for a host that wants row 4:** (1) every
process that verifies delegated tokens on ≥ v0.37.0 (SRE-2); (2) apply 0030; (3) set the flag; (4) users re-consent for
AAL2 grants. **Rollback:** flag off → wait one `AccessTTL` (under R-2 (a) no new token is stamped) → downgrade; rolling
back with the flag on is a 401 window of at most `AccessTTL` on delegated routes (BE-5/SRE-1). During a rolling flip a
code approved flag-on and redeemed flag-off (or the reverse) is `AssuranceUnknown` — fails closed (SRE-10).

**By host shape:**
- **gps-three-sixty / battleboardhq** (native Google OAuth sign-in → first-party sessions; no external IdP, no OAuth2
  server; pgx): rows 1, 3, 7–8 at repin — nothing changes on the wire; copy 0030 into the ledger when the store pin
  moves to v0.19.0; no flag applies.
- **gps-360-go** (`WithOAuth2`, no `RequireMFA` on delegated routes; old pins, no compatibility promise): rows 3–5 and
  7 when it repins; it is the one host for which row 4's rollout order matters, and every MCP resource server it runs
  is a "verifier" for step (1).
- **segovia/v2:** deferred (owner).
- **external-IdP hosts** (production-baseline shape): rows 1–2 and 6; a Google-ID-token issuer under a bound must NOT
  set row 6 unless it requests `auth_time`; Firebase hosts may set it at once.
- **examples:** auth-cms (demo flag on, authmem adopts); production-baseline (env + ledger 0036); external-identity
  (repin only).

## Risks
(1) The verifier/issuer order: a flag-on issuer beside an older verifier 401s delegated routes; controls: the flag, the
rollout order in the record, R-2 (a), the upgrade-note grep. (2) Delegated AAL2 is fixed at approval and lives as long as
the refresh grant (SRE-5); control: documented, `SessionTTL` bounds it, R-4 names the follow-up. (3) A `--posture` host
goes guard-red on the store bump until it copies 0030 (SRE-4); control: upgrade note row 7. (4) `RequireAuthenticationTime`
on an `iat`-only issuer is a self-inflicted outage; controls: opt-in, the README table, the boot line. (5) Turso is proven
locally only (SRE-7).

## Tasks as PRs
Worktrees `../gopernicus-s4d4-<pr>`; the owner merges; Claude cuts each tag after a green `live-stores` dispatch on
`main`, confirms it on origin, cold-verifies; every PR runs with model **opus**; live legs on a free port (`lsof -i
:8096` first; never 8082 or 55432); `make guard && make check` in every verify line; no local paths in tracked files.

- **PR-1 — sdk: `Identity.AuthTimeAsserted` (`sdk/auth-time-asserted`).** Depends on nothing. **Tag:** `sdk/v0.24.0`.
  **Files:** `sdk/capabilities/authentication/{authentication.go (the field + doc), oidc.go (identity: set the flag,
  BE-9), oidc_test.go, authenticationtest/provider.go (the AuthTimeAsserted case: asserted / iat fallback / garbage)}`,
  `sdk/README.md`, `workshop/documentation/docs/sdk/authentication.md` (row 16). **Verify:** `cd sdk && go build ./... &&
  go test -race ./... && go vet ./...`; `make guard`; `make check` (go-oidc, auth0 and firebase suites run the new case
  under go.work).
- **PR-2 — firebase sets the flag (`firebase/auth-time-asserted`).** Depends on tag 1. **Tag:**
  `integrations/authentication/firebase/v0.3.0`. **Files:** `integrations/authentication/firebase/{firebase.go,
  firebase_test.go, README.md, go.mod, go.sum (sdk → v0.24.0 via explicit GOWORK=off go get)}`,
  `workshop/documentation/docs/integrations/firebase.md`. **Verify:** `cd integrations/authentication/firebase && go
  build ./... && go test -race ./... && go vet ./...`; the module dropped from `go.work` → `GOWORK=off go build ./... &&
  go test ./...` (BE-10); `make guard`; `make check`.
- **PR-3 — authentication core (`authentication/s4d4-core`).** Depends on tag 1 (go.mod sdk → v0.24.0, explicit).
  **Tag:** `pockets/authentication/v0.37.0`. **Files** (under `pockets/authentication/`): `oauth2.go`
  (`OAuth2Config.DelegatedAssurance`), `constructor.go` (`validateOAuth2`: interface + bounded probe),
  `configuration_errors.go` (`ErrDelegatedAssuranceRequires`, `ErrDelegatedAssuranceSchemaMissing`), `configuration.go`
  (boot lines), `logic/authentication/oauth2/{repository.go (Code.Authentication, CodeAuthenticationRepository),
  service_types.go (Config.DelegatedAssurance, Introspection aal/methods), authorization.go (Approve →
  WithAuthentication), tokens.go (RedeemCode → GetCodeWithAuthentication + proposed.Authentication, BE-4; Introspect),
  service.go (signAccess stamps; R-2 gate), service_test.go (flag on + AAL2 approver → aal2 + methods on redeem, refresh,
  exchange; flag off → no claim; pre-flag grant → none; introspection)}`, `logic/authentication/{token.go
  (parseAccessClaims admits on delegated), token_test.go (invert :292, BE-11), credential.go (doc), delegated_test.go
  (delegated AAL2 under RequireMFA: pass; aal1: assurance denial; Methods restriction), external_config.go
  (RequireAuthenticationTime + the no-bound refusal, BE-7; boot line), external.go (the reason + order), external_age_test.go
  (unasserted denied once per token; asserted passes; flag without bound refuses), securityevent/securityevent.go (:263)}`,
  `inbound/http/delegated_test.go` (delegated AAL2 → 401 on the admin and machine groups, BE-2), `stores/storetest/
  oauth2_authentication.go` (new: `RunOAuth2CodeAuthentication`, §1 conformance incl. SRE-6 and BE-3), `README.md`
  (Assurance model :2178, Methods :2645, External authentication age table + paragraph, an "OAuth2 delegated assurance"
  subsection), `go.mod`, `go.sum`. **Verify:** `cd pockets/authentication && go build ./... && go test -race ./... && go
  vet ./...`; the module dropped from `go.work` → `GOWORK=off go build ./... && go vet ./...` (BE-10); `cd examples/auth-cms
  && go test ./...` (go.work; authmem not yet adopted → the runner skips, `RunOAuth2` still green); `make guard`; `make
  check`; `live-stores` dispatch on `main` after merge (core beside the released stores: the runner skips there).
- **PR-4 — stores: 0030 + the optional interface (`authentication-stores/delegated-assurance`).** Depends on tag 3
  (both go.mod: core → v0.37.0 explicit `GOWORK=off go get`; sdk lifts to v0.24.0). **Tags:**
  `pockets/authentication/stores/pgx/v0.19.0`, `pockets/authentication/stores/turso/v0.18.0`. **Files:**
  `pockets/authentication/stores/pgx/{migrations/0030_oauth_code_authentication.sql, oauth2_authentication.go (new:
  the wired-only column list, the two methods, ProbeSchema with the 0029 probe SQL and error text — SRE-9), oauth2.go
  (Code → row mapping only if shared), oauth2_authentication_test.go (ErrNotFound on an unmigrated schema; the runner
  under the live leg; a concurrent RotateAt case), go.mod, go.sum}`, the turso twin `{migrations/0030_..., oauth2_authentication.go,
  oauth2_authentication_test.go (-tags=integration), go.mod, go.sum}`, both READMEs (migration table rows). **Verify:**
  per store `go build ./... && go test ./... && go vet ./...`; pgx live leg (`make test-stores` pgx leg or the Makefile's
  `pgx-leg`) with the new runner; turso `go test -tags=integration ./...` against local libsql (SRE-7); the module
  dropped from `go.work` → `GOWORK=off go build ./...` (BE-10); `make guard`; `make check`; `live-stores` dispatch on
  `main` after merge.
- **PR-5 — examples + docs (`docs/s4d4-examples`).** Depends on tags 1–5 (explicit `GOWORK=off go get` repins: auth-cms
  sdk v0.24.0 + core v0.37.0; production-baseline sdk v0.24.0 + core v0.37.0 + pgx v0.19.0; external-identity sdk v0.24.0
  + core v0.37.0). **Files:** `examples/auth-cms/pockets/authentication/outbound/authmem/{oauth2.go (the interface),
  authmem_test.go or ports_test (the runner)}`, `examples/auth-cms/cmd/server/{main.go (DelegatedAssurance under
  AUTH_OAUTH2_DEMO), demo.go (the RequireMFA MCP-audience probe route)}`, `examples/auth-cms/pockets/authentication/
  inbound/http/oauth2demo/demo.go` (print aal/methods), `examples/auth-cms/{.env.example, go.mod, go.sum}`;
  `examples/production-baseline/cmd/server/{main.go (IDP_REQUIRE_AUTHENTICATION_TIME), production_test.go}`,
  `examples/production-baseline/workshop/migrations/primary/0036_oauth_code_authentication.sql` (byte-identical to pgx
  0030), `examples/production-baseline/{.env.example, go.mod, go.sum}`; `examples/external-identity/{cmd/server/main.go
  (comment), go.mod, go.sum}`; `workshop/documentation/docs/{pockets/authentication.md, integrations/auth0.md,
  guides/production-posture.md, guides/operations-runbooks.md}`. **Verify:** per example `GOWORK=off go build ./... &&
  go test ./...`; `cd workshop/gopernicus && go run . guard --posture` (or the Makefile target) against production-baseline
  (P9/G34 green with 0036); `pnpm build` in `workshop/documentation`; `make guard`; `make check`; Verification §1–§2.
- **PR-6 — the record (`docs/s4d4-release-record`).** Depends on PR-5 merged and §3. **Files:** `RELEASING.md` (ONE
  section), `SECURITY.md`, `AUDIT.md` (AUDIT-061), `.claude/plans/soc2-readiness/{control-map.md, roadmap.md, NEXT.md}`,
  `plans/s4d4-delegated-assurance-auth-time.md` (executed copy). **Verify:** `make check`; `make guard`.

## Sequencing
```
PR-1 sdk ──[tag sdk v0.24.0]──┬── PR-2 firebase ──[tag firebase v0.3.0]────────────────────────────┐
                              └── PR-3 core ──[live-stores]──[tag auth v0.37.0]── PR-4 stores ──[live-stores]──[tags pgx v0.19.0 / turso v0.18.0]──┼── PR-5 ── PR-6
```
PR-2 and PR-3 run in parallel worktrees after tag 1. PR-4 waits for tag 3 (the runner lives in core; conformance ships
in the core tag first). PR-5 waits for all five tags; PR-6 for PR-5 and §3.

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 1 | `sdk/v0.24.0` | PR-1 merge | sdk alone; a scratch consumer on core v0.36.0 + sdk v0.24.0 builds |
| 2 | `integrations/authentication/firebase/v0.3.0` | PR-2 merge | firebase selects sdk v0.24.0 and builds; the production-baseline shape (core v0.36.0 + firebase v0.3.0) builds |
| 3 | `pockets/authentication/v0.37.0` | PR-3 merge + green `live-stores` on `main` | **set A:** core v0.37.0 beside RELEASED pgx v0.18.0 / turso v0.17.0 and views v0.9.0 selects sdk v0.24.0 and builds; the gps-three-sixty and battleboardhq shapes build |
| 4 | `pockets/authentication/stores/pgx/v0.19.0`, `.../turso/v0.18.0` | PR-4 merge + green `live-stores` on `main` | each beside core v0.37.0; the gps-three-sixty shape (core + pgx v0.19.0 + goredis) builds; the full S4d4 cold set |

Annotated, unsigned tags, one per command, cut at the squash commit, confirmed on origin, THEN polled on
`proxy.golang.org/<mod>/@v/<ver>.info`; cold-verify with `GOWORK=off`, an empty `GOMODCACHE`, `go mod verify`, `go build
./...` on `go 1.26.10`. Never tidy before a tag resolves; never `go work sync`. **Not tagged:** go-oidc, auth0,
views/goth, `pockets`, workshop, ui/goth, pgxdb, the examples.

## Verification
Real binaries in DEVELOPMENT on a FREE port (`lsof -i :8096` first). `$B` = a scratch auth-cms binary (`PORT=8096
AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 AUTH_OAUTH2_DEMO=1 AUTH_EXTERNAL_DEMO=1 AUTH_ACCESS_TOKEN_TTL=1m go run
./cmd/server`, authmem, console mailer).
- **§1 — delegated assurance on `$B` (PR-5; PR-3 proves the same in-process with fakes).** (1) Password login (AAL1) →
  `/demo/oauth2` connect → consent → redeem: the access token payload (`cut -d. -f2 | base64 -d`) carries
  `"aal":"aal1"` and `"methods":["password"]`; `/auth/oauth2/introspect` shows the same; the MCP demo page prints them.
  (2) Enrol a passkey or TOTP, step up (AAL2) → a NEW connection: the token carries `aal2` and the step-up method; the
  demo route `GET /demo/oauth2/mfa` (MCP audience + `RequireMFA()`) → **200**; the AAL1 token from (1) → **401** with
  the RFC 9470 challenge and one `mfa_required_denied reason=assurance` row. (3) `POST /auth/oauth2/token` refresh on
  the AAL2 grant → the new token still carries `aal2` (fixed at approval); exchange → the API token carries it too.
  (4) The AAL2 delegated token on `/auth/admin/users` and the machine lifecycle → **401** (admission, BE-2). (5) Restart
  with `DelegatedAssurance` off (a demo env hatch) → new grants carry no claim; a token minted under (2) still verifies
  (admission is unconditional). (6) No log line, response or event carries a token or code.
- **§2 — asserted authentication time on `$B` + the baseline test (PR-5).** (1) `AUTH_EXTERNAL_DEMO` provider with
  `MaxAuthenticationAge=12h` + `RequireAuthenticationTime`: the dev issuer's token (has `auth_time`) → **200**; a token
  minted by the test helper WITHOUT `auth_time` → **401** + ONE `external_subject_denied reason=authenticated_at_unasserted
  provider=<name>` row across three retries; with the flag off the same token → **200** (token-age bound only). (2)
  `RequireAuthenticationTime` with a zero bound → boot refuses with `sdk.ErrInvalidInput`. (3) The baseline production
  test with `IDP_REQUIRE_AUTHENTICATION_TIME=true` logs zero WARNs and the provider lifetime line says
  `authentication_time=asserted_required`.
- **§3 — from the tags (PR-6).** §1(1)–(2) against a `GOWORK=off` auth-cms binary (`go version -m`: core v0.37.0, sdk
  v0.24.0); `RunOAuth2CodeAuthentication` green on local Postgres (pgx v0.19.0) and local libsql (turso v0.18.0,
  recorded "local only"); guard P9/G34 green on production-baseline with 0036; the docs site builds; the record's tag
  table matches `git ls-remote --tags origin`.

## Open rulings — YOUR CALL
- **R-1 — how a host turns delegated assurance on.** (a) `OAuth2Config.DelegatedAssurance bool`, default off; boot
  refuses when the store lacks the interface or 0030. (b) Auto-detect: on when the store implements the interface and
  the probe passes, else one WARN. **Recommend (a):** the 0029 precedent, the rollout order needs an explicit step
  (SRE-2), and (b) makes a store repin silently change what `RequireMFA` admits.
- **R-2 — is stamping gated on the flag too?** (a) Yes: `signAccess` signs `aal`/`methods` only while the flag is on, so
  flag-off is a clean rollback lever (wait `AccessTTL`, downgrade). (b) No: sign whenever the row records a level; the
  flag only gates the copy at approval. **Recommend (a):** BE-5/SRE-1 — without it rollback has no safe sequence.
- **R-3 — `Introspection` reports `aal`/`methods`.** (a) Yes, additive, only when recorded. (b) Omit; resource servers
  read the token. **Recommend (a):** the MCP resource server in the demo (and gps-360-go's) validates by introspection,
  not by parsing the token; two `omitempty` fields.
- **R-4 — an age bound on delegated AAL2 (SRE-5).** (a) Document: the level is fixed at approval, lives for
  `OAuth2Config.SessionTTL`, exchange never raises it; a SECURITY.md line; follow-up `OAuth2Config.DelegatedAssuranceMaxAge`.
  (b) Build the bound now: at mint, a copied `AuthenticatedAt` older than the bound signs `aal1`. **Recommend (a):**
  first-party AAL2 is also login-time and session-long; (b) changes a token's level mid-grant with no signal to the
  client and is a second knob nobody asked for yet.
- **R-5 — `RequireAuthenticationTime` default.** (a) Off; production-baseline exposes `IDP_REQUIRE_AUTHENTICATION_TIME`
  (default false). (b) On by default under a bound (the s4d2 R-5 (b) wording "deny `iat`-only identities"). **Recommend
  (a):** (b) is a boot-to-outage for any Google-ID-token host that set a bound in part 2; the README table and boot
  line make the posture visible either way.
- **R-6 — `FreshAuth` and the `iat` fallback (BE-6).** (a) Document the gap in `FreshAuth`'s doc comment; follow-up.
  (b) `FreshAuth` denies an unasserted identity when the route's provider has `RequireAuthenticationTime`. **Recommend
  (a):** `FreshAuth` is a per-route option with no provider in scope; (b) threads provider config into the option set.

## Open questions
- Does gps-360-go run any process that verifies delegated tokens outside the issuing binary (an MCP resource server
  parsing tokens rather than introspecting)? It decides whether its step (1) is one deploy or several.
- Should the auth-cms demo flag ride `AUTH_OAUTH2_DEMO` (no new env) or get `AUTH_OAUTH2_DELEGATED_ASSURANCE` so §1(5)
  can flip it without code? The plan assumes a demo-only env hatch.

## Recommended reviews
**product-manager:** R-2, R-4, R-5 before ratification; the "by host shape" paragraph as the adopter contract. The
two-reviewer gate (BE, SRE) has run and is folded above; re-reads at execution: **lead-backend** on PR-3's
`oauth2/service.go` + `token.go` diff, **data-integration-reviewer** on PR-4 (both dialects' `oauth2_authentication.go`,
the NULL mapping, the probe SQL), **platform-sre** on PR-6's rollout/rollback text, **architecture-steward** only if the
optional interface's home (`oauth2/repository.go`) is questioned.

## Notes
- **Carries closed here:** s4d2 R-4 (all four constraints: atomic copy over the locked row — pgx `lockSession`/turso
  `oauthSession`; verifiers before issuers — the unconditional verifier change + the flag; `assure` newly admits — BE-2
  pinned; stale level — R-4) and R-5 (the sdk field + the opt-in denial).
- **Follow-ups:** `OAuth2Config.DelegatedAssuranceMaxAge` (R-4); `FreshAuth` asserted mode (R-6); a `guard --posture`
  line for `DelegatedAssurance`; re-consent tooling (revoke pre-flag grants in bulk); go-oidc/auth0 README sentences
  on `max_age` the next time either module tags.
- **Cut for the cap:** per-test case lists beyond the runner; the SQL of the turso twin beyond its encoding rule; the
  runbook prose (PR-5 writes it from §Upgrade notes); the boot-line attribute order (PR-3 pins it in
  `configuration_test.go`).

## Owner answers to the open questions (2026-10-28)
1. **Does gps-360-go verify delegated tokens in a process other than the issuing binary?** Owner: "i don't think so." So rollout step 1 (every verifier on ≥ v0.37.0) is the issuing binary for gps-360-go. The record's gps-360-go note should still say to confirm this before flipping the flag.
2. **The auth-cms demo flag:** it rides `AUTH_OAUTH2_DEMO` (owner: "ride is fine"). No new env hatch.

## Owner ruling during execution (2026-10-28): the RunProvider Garbage case
Firebase's Admin SDK rejects a non-integer `auth_time` rather than falling back to `iat`. Owner: (a). The shared `AuthTimeAsserted/Garbage` case accepts EITHER a fallback (success, `AuthTimeAsserted=false`, `AuthenticatedAt` ≈ `iat`) OR a rejection (`ErrInvalidCredential`, zero Identity). Anything else fails. The merge order (fold firebase's one-line fix into PR-1 vs land PR-1 red until PR-2) is still open.

## As built

Executed 2026-10-28. Every PR passed `make guard && make check`; a green `live-stores`
dispatch on `main` preceded each tag; every tag is confirmed on origin and cold-verified.

| PR | Merged as | Tag(s) | `live-stores` |
|---|---|---|---|
| PR-1 | #277 (`21f21f4f`), with the firebase flag line (ruling (A)) | `sdk/v0.24.0` | 40797996927 |
| PR-2 | #278 (`eefcc693`), repin only (sdk v0.18.0 → v0.24.0) | `integrations/authentication/firebase/v0.3.0` | 40806875519 (at `2fe347f5`, after #278 and #279) |
| PR-3 | #279 (`2fe347f5`) | `pockets/authentication/v0.37.0` | 40806875519 |
| PR-4 | #280 (`118340a7`), with the production-baseline ledger copy 0036 | `pockets/authentication/stores/pgx/v0.19.0`, `pockets/authentication/stores/turso/v0.18.0` | 40810755387 |
| PR-5 | #281 (`ddab754a`) | none (examples + docs + runbook) | — |
| PR-6 | the record PR | none | — |

Cold sets: sdk v0.24.0 + go-oidc v0.2.0; **set A** (authentication v0.37.0 + the released
stores pgx v0.18.0 / turso v0.17.0 + firebase v0.3.0 + go-oidc v0.2.0 + sdk v0.24.0); **the
full set** (authentication v0.37.0 + pgx v0.19.0 + turso v0.18.0 + firebase v0.3.0 + go-oidc
v0.2.0 + golang-jwt v0.4.0 + pgxdb v0.17.0 + sdk v0.24.0); **set C** (#281: auth-cms,
production-baseline, external-identity). The release table's host shapes (gps-three-sixty,
battleboardhq, views/goth v0.9.0 beside the new core and stores, a firebase host) were
cold-verified by PR-6 against the final versions.

Deviations from the task text:

- **PR-1:** the `RunProvider` Garbage case accepts an `iat` fallback (flag false) OR
  `ErrInvalidCredential` with a zero `Identity` (ruling (a)): the firebase Admin SDK refuses a
  non-integer `auth_time`. The firebase line that sets the flag landed in PR-1 so `main` stayed
  green under `go.work` (ruling (A)); firebase did not build at `GOWORK=off` between #277 and
  #278. PR-2 became the repin and the tag.
- **PR-3:** Introspection is gated on the flag, following R-2 (lead decision); the interface
  check and the bounded probe live in `oauth2.New`, with the sentinels re-exported from the
  pocket root (lead decision); the R-6 `FreshAuth` doc comment was added here, since no PR's
  file list had it.
- **PR-4:** production-baseline's ledger copy 0036 was folded in (the #223 precedent), so guard
  stayed green before PR-5. #272 did not reproduce on a fresh database. The concurrent-rotate
  case catches a second lock and a stall, not a lock-order inversion (`RotateAt` locks only the
  session row). The real check ran at the service level; the AAL2 session was seeded through
  the repositories.
- **PR-5:** auth-cms with `AUTH_OAUTH2_DEMO` + `AUTH_EXTERNAL_DEMO` panicked at boot
  (pre-existing); fixed host-side with an explicit `BrowserLoginPath`; the core fix is #282.
  Verification §1(5) was not shown (authmem does not persist; production-baseline runs no
  OAuth2 server); #280's real check showed a token and refresh surviving a restart on pgx.
  Exchange was proven indirectly.
- **PR-6:** `NEXT.md` is the lead's, not this PR's.

Follow-ups: `OAuth2Config.DelegatedAssuranceMaxAge` (R-4); `FreshAuth` threading the asserted
flag (R-6); #282; a runner case that can detect a lock-order inversion; the Notes' list above.
