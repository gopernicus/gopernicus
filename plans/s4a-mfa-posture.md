# S4a — MFA posture: provider-asserted assurance made real (SOC 2 readiness, slice 4a)

**Status:** EXECUTED 2026-10-24 — RELEASED. Two code PRs squash-merged on `main`: #188 PR-1 sdk @ 544bc781 · #189 PR-2 authentication @ 467b834b; PR-3 = the record (`docs/s4a-release-record`). TWO tags: sdk v0.19.0 @ 544bc781; pockets/authentication v0.25.0 @ 467b834b (after the auth pgx bare+schema+turso legs and the three IdP-related examples on merged main). No store, adapter or workshop tags. Build-time deviations under "Build-time deviations" below. Former status: RATIFIED 2026-10-24 (owner: "ratified"; rev 2 as reviewed; R-1 strict amr default with the MFAMethods hatch; R-2a IdP hosts with Required none under the widened SingleFactorAcknowledged with a `causes` attr; R-2b native OAuth counts as single-factor; R-3 ErrMFAUnsatisfiable boot refusal; R-4 bundled admin MFA default stays none until S4b; R-5 machine lifecycle out until S4b; the three planner placements accepted). Build started from `main` @ 14b0a994: PR-1 sdk first; PR-2 authentication after the sdk tag; PR-3 record last.

**Size:** S–M, two trains (PR-1 sdk, PR-2 authentication, PR-3 the record; two tags). Rev 1: `s4a-mfa-posture.rev1.md`.

**Anchors.** Read on `main` @ 14b0a994 (S1, S2, S3, S5, S6 RELEASED). Tags verified local = remote: sdk v0.18.0,
authentication v0.24.0, authentication `stores/pgx` v0.11.0 / `stores/turso` v0.10.0, go-oidc v0.2.0,
firebase v0.2.0, auth0 v0.1.0, workshop v0.9.0.
Sources: `RELEASING.md:1-60` and `:313-316`; parent `roadmap.md:181-185`, `:245-246`, `:307` (the trimmed process; no
store change here, so no branch dispatch); evidence `passes/01-auth-core.md:185-197` (FAC-2). Control-map rows:
`control-map.md:17` (CC6.1 MFA), `:44` (CC6.3 admin surfaces) and `:99` (CC7.2 authentication events).

**Owner rulings, 2026-10-24:** Passkeys first, TOTP as the fallback, recovery codes always; authenticator libraries are
adapters behind sdk ports; S4 is four plans: S4a (this one), S4b passkeys, S4c TOTP + key ring, S4d the rest; breaking
the examples' route tiers is acceptable.

**What the code already does — the brief, corrected against `main`.** `RequireMFA()` already works for external
tokens. It reads `Identity.MFA` (`pockets/authentication/inbound/http/principal_options.go:197-208`): the sdk sets the
field from `amr` or a flag path (`sdk/capabilities/authentication/oidc.go:106-112`, `profile.go:11-13,27-29,58-61`);
Firebase sets it from `sign_in_second_factor` (`integrations/authentication/firebase/firebase.go:248-250`); auth-cms's
`/demo/external/mfa` route exercises it (`examples/auth-cms/cmd/server/demo.go:345-346`). The claim mapping is already
exported, per-provider, struct-input config — `sdkauth.Profile.Claims` (`profile.go:19-33`) — so S4a tightens it.

**Gaps:** the default amr set counts one factor — `["otp"]`, `["sms"]`, `["hwk"]`, `["swk"]`, `["pop"]` — as MFA
(`profile.go:13`, `oidc.go:107-112`); `Identity.MFA` has no documented meaning on the port (`authentication.go:124`);
assurance is a bool, not a `session.AssuranceLevel`; `RequireMFA()` skips non-external kinds
(`principal_options.go:353-356`); the standalone `NewExternalAuthenticator` (`logic/authentication/external.go:149-171`)
runs no posture check; there is no policy, no denial row and no production check.

**Not fixable from claims:** `MinLoginAssurance` floors credential MUTATIONS over a local method set
(`logic/authentication/credential/policy.go:54-56,102-104`; OAuth links are AAL1 by descriptor, `session/method.go:63`);
`RecentAuthPolicy.MinAssurance` reads a native session's login (`logic/authentication/stepup.go:49-69,155-157`). An
IdP-delegated host has neither; both become meaningful when S4b/S4c add native AAL2 methods.

## Goal

A host on an identity provider can require MFA, for its administrators or for every user. The framework enforces it
from what the provider actually asserted: amr or flag evidence maps to `session.AssuranceAAL2` under a strict default;
unknown is AAL1, never higher; `RequireMFA()`, `ExternalProvider.RequireMFA` and `MFAConfig` deny anything below AAL2
with the RFC 9470 challenge and a bounded `mfa_required_denied` row; production refuses to boot, through either
constructor, without a declared MFA posture or the acknowledgement.

## Scope and non-goals

**In scope:** 1. the strict amr rule and `Identity.MFA`'s meaning at the sdk port, plus pocket credential assurance;
2. `MFAConfig{Required}`; 3. production posture, root and standalone; 4. events; 5. docs; 6. examples + run-and-look.

**Non-goals:** passkeys/WebAuthn (S4b, `integrations/cryptids/go-webauthn`); TOTP, recovery codes and the key ring
(S4c, `integrations/cryptids/pquerna-otp`); idle timeout, registration mode, revoke-all, API key TTL/disable and the
breach checker (S4d); S7 items. **acr is cut to a follow-up** (no verified consumer): `ClaimMapping.ACR`/`MFAACRValues`,
`Identity.ACR`, the devissuer field. `MFAConfig.Methods`/`MFAMethod` go to S4b. Native sessions get no assurance (S4b
stamps access tokens); native OAuth does not reach AAL2 (`oauth.IDTokenClaims` carries no amr,
`sdk/capabilities/oauth/oauth.go:114-122`). Also out: enrollment grace (S4b), AAL3 from external claims, `acr_values`
in the challenge, per-claim attribution in events, and API keys under MFA gates (they pass — an S4d residual).

## Decided by gate

Reviews: lead-backend-engineer (B1–B11, one question) and product-manager (P1–P15, one question), 2026-10-24. Where
they differ, the lead's ruling decides.

- B1 (HIGH, the standalone engine is never posture-checked) → `ExternalConfig.SingleFactorAcknowledged`; production
  `NewExternalAuthenticator` refuses unless every provider sets `RequireMFA` or it is set; P5 matches by name (Design 3).
- B2 + P1 (HIGH, `admins` clears the check gating nothing) → `admins` without `UserAdminCheck` = `ErrMFAUnsatisfiable`;
  boot line `mfa_required=admins`; one "does NOT gate" sentence in README + control map; the name `admins` is KEPT.
- B3 + P2 (HIGH, the panic flip) → panic KEPT; `RequireMFA` reads `cred.Assurance`; S4b lifts the panic with access-token
  stamping; rev-1 row 4, Risk 2 and the flipped-panic test dropped.
- B4 + P12 (API-key contradiction) → `RequireMFA` and `adminPrincipal` skip `CredentialAPIKey`; residual in the
  `control-map.md:17` cell, handed to S4d.
- B5 (rank unexported, duplicated) → `session.AssuranceLevel.AtLeast(min)`; both private `assuranceRank` copies folded.
- B6 + P5 (`Methods` slot) → CUT to S4b; `MFAConfig{Required}` ships; an unknown `Required` → `sdk.ErrInvalidInput`.
- B7 (the sdk reason; `pop`) → `Identity.MFA` is the port's output, documented on the port; the table is "the IANA amr
  registry, factor-categorised"; the hatch stays `MFAMethods` (setting it restores any-of exactly).
- B8 (`CredentialService`; rev-1 rows 9–10 not BREAKING) → Upgrade row 3; `admins`/`all` (rows 7–8) are opt-in.
- B9 (provider-gate `UserID`; slice mutation) → empty `UserID` stated (the budget is per client IP); `all` clones.
- B10 + P7 (one waiver hides its sources; two host groups) → bounded `causes` attr on waiver and refusal; R-2a/R-2b.
- B11 + P15 (citations) → `event_type`/`event_status`; `main.go:392-393`; the `u2` token minted; no dispatch claim.
- B question (does a downstream host implement `CredentialService`?) → none known; the row stays anyway.
- P3 + P4 (not one day; acr has no consumer) → sized "S–M, two trains"; acr CUT to a follow-up; the sdk train stays.
- P6 (fails closed unseen) → greps in Read-first; row 1 names Auth0/Firebase unaffected; self-check + passkey caveat.
- P8 + P9 (walk-throughs; echo stand-in) → README lines in PR-2's list, `amr` tokens keep the S2 step, AAL1 → 401
  added; the stand-in dropped for `RequireMFA: true` on external-identity's `dev` providers; the memstore line is the look.
- P10 + P11 (re-auth loop; §2 overclaims) → the re-auth recipe in "Assurance model"; §2 matches §1; residuals in the cell.
- P13 + P14 (checklist; table TODOs) → a by-host-shape Read-first with greps and the development WARN; every IdP cell
  a linked fact with a checked date, or **unknown** + the self-check, and no advice on unknown cells.
- P question (is the sdk train needed without acr?) → yes: `Identity.MFA` is the port's output, so Firebase's flag path
  and any future `Provider` must mean the same thing; a pocket rule over `Identity.Methods` would leave the port loose.

## Design

### 1. Provider evidence → `session.AssuranceAAL2`

**sdk — `sdk/capabilities/authentication`, v0.19.0.** `Identity.MFA` is the port's output (`authentication.go:124`).
The pocket does see amr (`Identity.Methods`, `:123`), but a rule there would bind one consumer and leave Firebase's
`Provider` (`firebase.go:248-250`) and any future provider free to mean something else. So the meaning lives on the
port, as `Identity.MFA`'s doc comment: "the provider asserted authentication with at least two factor categories, or
an explicit MFA claim or flag; a one-factor sign-in leaves it false". Firebase's `sign_in_second_factor` meets it.

The rule (`oidc.go:106-112`) stays `flag || amrMFA(Methods)`. With `MFAMethods` EMPTY (the default) `amrMFA` is strict
over the IANA amr registry, factor-categorised: `mfa`, or values from at least two categories — knowledge (`pwd` `pin`
`kba`), possession (`otp` `sms` `tel` `hwk` `swk` `sc` `pop`), inherence (`fpt` `face` `iris` `retina` `vbm`); any
other value (`user`, `geo`, `rba`, `wia`, `mca`, vendor values) is no factor. `pop` is IANA-registered from OpenID EAP
ACR Values 1.0, not RFC 8176. With `MFAMethods` SET, any listed value counts — today's semantics exactly: the hatch.
`defaultMFAMethods` (`profile.go:11-13`) becomes the category table (R-1); the `MFAMethods` comment (`:28`) says so.
No adapter changes, no adapter tags: Firebase keeps its flag; `auth0.Profile` (`auth0/auth0.go:63-72`) keeps the
defaults, and Auth0 emits `["mfa"]` after a second factor (`:55-58`, asserted at `auth0_test.go:94-104`); go-oidc is a
verifier only (`integrations/cryptids/go-oidc/gooidc.go:200`).

**Pocket — `pockets/authentication`, v0.25.0. One comparator:** `func (a AssuranceLevel) AtLeast(min AssuranceLevel)
bool` in `logic/authentication/session/method.go` (unknown < aal1 < aal2 < aal3; an unrecognised level ranks as
unknown). The private copies at `credential/policy.go:126-139` and `authgrant/requirement.go:38-49` fold into it,
behaviour-preserving (`assuranceRank(x) == 0` becomes `!x.AtLeast(AssuranceAAL1)`).

**New `Credential.Assurance session.AssuranceLevel`** (`logic/authentication/credential.go:63-92`).
`ExternalAuthenticator.Verify` (`external.go:247-284`) is the ONE seam where verified provider evidence meets a local
principal; at `:283` it stamps `AssuranceAAL2` when `identity.MFA` is set, else `AssuranceAAL1` (`externalAssurance`,
new `logic/authentication/assurance.go`); access tokens and API keys stay `AssuranceUnknown`. Nothing produces AAL3.

**New `ExternalProvider.RequireMFA bool`** (`external_config.go:43-65`, beside `RequireVerifiedEmail`) is the provider
floor. `Verify` denies a token below AAL2 after the email check (`external.go:275-277`) and before `resolveSubject`, so
a denied token never provisions a user; it records `mfa_required_denied` (gate `provider`) and returns the new
`authlogic.ErrMFARequired` (wraps `sdk.ErrUnauthorized`; re-exported at the root); `denyPrincipal`
(`inbound/http/principal.go:194-215`) maps it to the RFC 9470 challenge (`principal_options.go:48-68`).

**`RequireMFA()`** (`principal_options.go:197-208`) reads `cred.Assurance` and requires `AtLeast(AssuranceAAL2)`. The
wiring-time panic beside access tokens (`:330-332`) STAYS: no access token can reach AAL2 in S4a, and S4b lifts it in
the PR that stamps them. `assure` (`:353-356`) already passes every non-external kind, so API keys skip it.

**Closes:** FAC-2 for IdP hosts. CC6.1 "MFA for users and admins" (`control-map.md:17`) moves from GAP to OPT-IN for
IdP hosts (IdP-asserted); the native half stays GAP until S4b/S4c.

### 2. `MFAConfig` (root package `pockets/authentication`, `config.go`)

The brief's "MFAPolicy", named to the `XConfig` + `WithX` convention (`config.go:157-989`). No env tag: like
`SingleFactorAcknowledged` (`config.go:209-216`), it is a wiring literal.

```go
type MFARequirement string
const (
	MFARequiredNone   MFARequirement = ""       // zero: no floor (v0.24.0 behaviour)
	MFARequiredAdmins MFARequirement = "admins" // /auth/admin/users* for principals the UserAdminCheck admits
	MFARequiredAll    MFARequirement = "all"    // every external-token user
)
type MFAConfig struct{ Required MFARequirement }
func WithMFA(value MFAConfig) Option
```

**`admins`.** Authentication never knows who an admin is (`inbound/http/host_policy.go:130-147`); the coordinate that
exists is the host's `UserAdminCheck`. `adminPrincipal` (`inbound/http/useradmin.go:121-137`) is the one helper every
`/auth/admin/users*` handler runs, erase included (`routes.go:369-374`). After `checkUserAdmin` passes it reads the
current credential: `CredentialAPIKey` skips (the bundled set admits API keys, `routes.go:166`, and a challenge an API
key can never satisfy helps nobody); anything else below AAL2 gets the RFC 9470 401 and a `mfa_required_denied` row
(gate `admin`). A non-admin still gets the host's 403, so the ask never reveals who is an admin. New handler option
`authenticationhttp.WithAdministrationMFA()`, beside `WithUserAdminCheck` (`constructor.go:636`). Machine lifecycle
(`routes.go:162-164,393`) stays out (R-5). **`admins` does NOT gate role routes, cms admin routes or host admin
pages** — a host adds `RequireMFA()` to those itself; the pocket README and the `control-map.md:44` cell say so.

**`all`.** `New` clones `cfg.External.Providers` and sets `RequireMFA` on each clone, so the host's slice is never
mutated; a standalone `NewExternalAuthenticator` host sets `RequireMFA` on its providers itself (its form of `all`).

**Validation, every mode:** an unknown `Required` → `sdk.ErrInvalidInput`; any `Required` other than none with no
`External` wired → the new `ErrMFAUnsatisfiable` (`security.go` var block; S4a has no other AAL2 source); `admins`
without `AdministrationConfig.UserAdminCheck` → `ErrMFAUnsatisfiable` (nothing would be gated); `all` while native
single-factor login is on (`!PasswordFlowsDisabled`, any `Passwordless` kind, or any OAuth `Providers`) →
`ErrMFAUnsatisfiable` (R-3). Each error names the field to change. **Boot line:** `New` logs one Info line beside the
maintenance line (`constructor.go:680`): `auth: mfa posture mfa_required=admins gates=/auth/admin/users*` (`none` →
`gates=none`; `all` → `gates=external_tokens`).

**Closes:** CC6.1 MFA (`control-map.md:17`): OPT-IN for IdP hosts, GAP for native until S4b/S4c. CC6.3 "Admin surfaces
gated" (`:44`) gains "AAL2 for admitted administrators on `/auth/admin/users*` (opt-in)".

### 3. Production posture

**Verified today:** `checkPosture` (`security.go:526-561`) refuses native single-factor login in production
(`!PasswordFlowsDisabled || len(Passwordless) > 0`, `:538`) without `IdentityConfig.SingleFactorAcknowledged`
(`config.go:209-216`), with `ErrSingleFactorUnacknowledged` (`security.go:89-95`); an honoured acknowledgement logs
`posture_waiver`; development WARNs; native OAuth does not count; `guard --posture` P5 lists `SingleFactorAcknowledged`
and prints `ack` (`workshop/gopernicus/internal/commands/posture_rules.go:31,64-67`) — confirmed.

**Causes.** The `:538` condition becomes "this host admits single-factor users", computed as a bounded cause list:
`native_password` (`!PasswordFlowsDisabled`), `passwordless` (any kind), `native_oauth` (any OAuth `Providers`, R-2b)
and `external_no_mfa` (`External` wired and `Required` none, R-2a). A cause is a login source that cannot, or is not
asked to, produce AAL2: native sources cannot in S4a, so they count under any `Required`; the IdP source counts only
under none. That settles `admins` (B2): with its `UserAdminCheck` rule it always gates something, so it clears
`external_no_mfa`; beside native login the native causes remain and the acknowledgement is still needed.

The causes ride the condition's `attrs` slot (`security.go:546,554`): the waiver reads
`posture_waiver=SingleFactorAcknowledged causes=[native_password external_no_mfa]`; the development WARN names the
acknowledgement and the causes (the pre-deploy signal); the production refusal wraps `ErrSingleFactorUnacknowledged`
with the tripped causes. Same acknowledgement, same P5 name: no new P rule, no workshop tag.

**Standalone (B1).** New `ExternalConfig.SingleFactorAcknowledged bool`. `NewExternalAuthenticator` (`external.go:167`,
after `validateExternal`) in production refuses unless every provider sets `RequireMFA` or the acknowledgement is set;
honoured → one `posture_waiver=SingleFactorAcknowledged causes=[external_no_mfa]` WARN; development WARNs once, naming
it. `ErrSingleFactorUnacknowledged` moves into logic and is re-exported at the root under the same name (the
`ErrDevelopmentOnlyProvider` pattern, `security.go:96-102`), so one `errors.Is` target serves both constructors. P5
matches acknowledgements by name (`posture_rules.go:64-80`): `ack` for the new field, no workshop change. Root `New`
refuses `External.SingleFactorAcknowledged` with `sdk.ErrInvalidInput` naming `IdentityConfig.SingleFactorAcknowledged`,
so neither field is ever inert.

**Closes:** CC6.1's "production check" target (`control-map.md:17`), for both constructors.

### 4. Security events (`logic/authentication/securityevent`)

**New `TypeMFARequiredDenied = "mfa_required_denied"`** (vocabulary at `securityevent.go:236-260`): `StatusBlocked`;
`UserID` is the principal for gates `route` and `admin`, and EMPTY for gate `provider` (the denial precedes
`resolveSubject`, `external.go:275-278`); `Budgeted: true`, keyed per client IP (`recorder.go:60-71,133-141`), because
an AAL1 holder can drive it. Details are bounded enums that pass `boundDetails` (`recorder.go:337-360`): `credential`
(`external_token`, `access_token`), `provider` (`ExternalProvider.Name`, external only), `assurance` (`aal1`,
`unknown`), `gate` (`route`, `admin`, `provider`). No amr values, no claims, no token.

**`external_login`** (`external.go:291-303`) Details gain `assurance` (`aal1` | `aal2`): the "assurance asserted"
record, once per token per login-record cache; no `assurance_asserted` type (it would duplicate a deduped row).

**Recording seam.** The adapter decides the `RequireMFA` denial and logic records it, through a new optional interface
beside `CredentialService` (`inbound/http/authenticator.go:120-131`): `MFADenialRecorder { RecordMFADenied(ctx,
authlogic.MFADenial) }`. `CredentialService` itself is unchanged (exported, host-implemented); `*authlogic.Service` and
`*authlogic.ExternalAuthenticator` implement it over their one recorder; `NewAuthenticator` asserts it once (`:133`);
the provider and admin gates record in logic directly. **Closes:** CC7.2 (`control-map.md:99`) gains MFA denials.

### 5. Docs

**Pocket `README.md`:** the assurance row and signature (`:1237`, `:1247`); RFC 9470 (`:1314`); `external_login`
Details (`:1941`); the posture row and its causes (`:2120`); the "v3 ships none of these" passage (`:4136-4142`).

**New "Assurance model" subsection:** what `Identity.MFA` means; the strict rule and the `MFAMethods` hatch; what
`admins` does NOT gate; API keys pass every MFA gate; and three host aids:
- **Self-check:** sign in with MFA and confirm the `external_login` row reads `assurance=aal2`.
- **Passkey caveat:** an IdP passkey sign-in emitting only `hwk`/`swk` reads AAL1; list it in `MFAMethods` only if the
  IdP enforces user verification.
- **Re-auth recipe:** on `insufficient_user_authentication`, re-authorize at the IdP forcing a second factor (the
  per-IdP parameter, or `prompt=login`); `RequireMFA()` beside `Browser()` needs a login path that does this, or a live
  IdP session answers AAL1 again and the browser loops.

**Elsewhere:** `sdk/README.md:118` (the strict rule, `Identity.MFA`'s meaning); docs site
`workshop/documentation/docs/pockets/authentication.md:238-243,349`, `.../sdk/authentication.md:16,48-49`,
`.../guides/production-posture.md:56` (the causes), plus an "Added by S4a" block.

**IdP table.** Every shipped cell is a linked vendor fact with its checked date, or **unknown** plus the self-check; an
unknown cell carries no mapping advice; no "(verify)" survives to merge. PR-2 resolves the cells.

| IdP | What it emits | Mapping |
|---|---|---|
| Auth0 | `amr: ["mfa"]` after a second factor (`auth0.go:55-58`; PR-2 adds the vendor link + checked date) | `auth0.Profile` defaults |
| Firebase | `firebase.sign_in_second_factor` when MFA was used (`firebase.go:248-250`); no amr | `firebase.New` flag, or `firebase.Profile` `MFAFlag` (`firebase/profile.go:28-34`) |
| Google, Okta, Entra ID | **unknown** unless PR-2 links a vendor fact with a checked date (one row each); run the self-check | — |

### 6. Examples (wiring only; the `examples/README.md` H-rules are unchanged)

**production-baseline:** `WithMFA(MFAConfig{Required: MFARequiredAdmins})` beside `WithAdministration`
(`cmd/server/main.go:348-366`); `RequireMFA()` on the role-routes `authenticate` (`:392-393`, already
`Accept(CredentialExternalToken)`, so no panic). README walk: the boot paragraph (`README.md:142-149`) gains the
`mfa posture` line; `TOKEN` (`:159`) mints `"amr":["pwd","mfa"]`, so the S2 `authorization_denied` step (`:172-181`)
survives; a new AAL1 step beside it (no amr → 401 + challenge on `POST /authorization/roles`).

**external-identity:** `RequireMFA: true` on both `dev` entries (`cmd/server/main.go:107-108`): the standalone form of
`all`, which also meets the standalone production posture. README tokens and the session (`README.md:74-96`) mint
`"amr":["mfa"]` (the example maps with `auth0.Profile`); a new step: a no-amr token → 401 + challenge, and the log line
`memstore: security event type=mfa_required_denied status=blocked user_id=` (`memstore.go:328`). `/api/echo` is untouched.

**auth-cms:** unchanged (`external_demo_test.go:161` sends `["pwd","mfa"]`; its production tests already acknowledge).

## Upgrade notes

> **Read first — by host shape.** Development logs one WARN naming `SingleFactorAcknowledged` and its causes for every
> shape production would refuse: that WARN is the pre-deploy signal.
> - **IdP-only via `New`** (`WithExternal(`, native login off) → rows 1 and 10: declare `WithMFA(` or acknowledge.
> - **Native password with the acknowledgement** → none. **Native OAuth** (`WithOAuth(`) without it → row 11.
> - **Direct `NewExternalAuthenticator`** → rows 1 and 12. **A host-implemented `CredentialService`** → row 3.
>
> Greps: `grep -rnE 'RequireMFA\(|MFAMethods|NewOIDC\(' --include='*.go'` (row 1); `grep -rn 'WithOAuth('
> --include='*.go'` (row 11); `grep -rnE 'WithExternal\(|NewExternalAuthenticator\(|WithMFA\(|SingleFactorAcknowledged'
> --include='*.go'` (rows 10, 12).

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | default amr rule strict (R-1) | one-factor amr (`["otp"]`, `["sms"]`, `["hwk"]`, `["swk"]`, `["pop"]`) leaves `MFA` false: `RequireMFA` 401 + challenge; no compile error. Auth0 (`["mfa"]`) and Firebase (flag) hosts are unaffected | `MFAMethods: []string{"mfa","otp","hwk","sms","swk","pop"}` (any-of: the v0.18.0 default exactly) | sdk@v0.19.0 |
| 2 | `Credential.Assurance`; `session.AssuranceLevel.AtLeast` | additive: external AAL1/AAL2; access token and API key `unknown` | none | authentication@v0.25.0 |
| 3 | `RequireMFA()` reads `Assurance` (the access-token panic stays) | a host's own `CredentialService` that never stamps `Assurance` gets its external MFA tokens denied; no compile error | stamp `AssuranceAAL2` when `Identity.MFA`, else `AssuranceAAL1` | authentication@v0.25.0 |
| 4 | `mfa_required_denied` rows (budgeted); `external_login` Details `assurance` | new audit rows; additive row content | none | authentication@v0.25.0 |
| 5 | `ExternalProvider.RequireMFA` | additive; a below-AAL2 token gets 401 + challenge on every route and a row (gate `provider`) | leave false | authentication@v0.25.0 |
| 6 | `WithMFA(MFAConfig{Required})`, zero = none | additive; an unknown value → `sdk.ErrInvalidInput` | none | authentication@v0.25.0 |
| 7 | opt-in `Required: MFARequiredAdmins` | `/auth/admin/users*` answers 401 + challenge to an ADMITTED principal below AAL2 (every native session in S4a); API keys pass; non-admins keep 403; no `UserAdminCheck` → `ErrMFAUnsatisfiable` | stay none until S4b on a native-admin host | authentication@v0.25.0 |
| 8 | opt-in `Required: MFARequiredAll` | every provider gets `RequireMFA` (cloned); native login or no `External` → `ErrMFAUnsatisfiable` in every mode | native login off, or `admins` | authentication@v0.25.0 |
| 9 | `ErrMFARequired`, `ErrMFAUnsatisfiable` new; `ErrSingleFactorUnacknowledged` re-exported from logic (same `errors.Is` target); `posture_waiver` gains `causes` | new `errors.Is` targets; log content | none | authentication@v0.25.0 |
| 10 | production `New`: `External` + `Required` none is cause `external_no_mfa` (R-2a) | BOOT FAILURE `ErrSingleFactorUnacknowledged` naming the cause | `WithMFA(…)` or `SingleFactorAcknowledged: true` | authentication@v0.25.0 |
| 11 | production `New`: OAuth `Providers` are cause `native_oauth` (R-2b) | BOOT FAILURE without the acknowledgement | `SingleFactorAcknowledged: true` | authentication@v0.25.0 |
| 12 | production `NewExternalAuthenticator` without `RequireMFA` on every provider | BOOT FAILURE `ErrSingleFactorUnacknowledged` | `RequireMFA` on each provider, or `ExternalConfig.SingleFactorAcknowledged: true` | authentication@v0.25.0 |
| 13 | examples wired (production-baseline admins + role routes; external-identity provider floor) | example behaviour; README walks updated | — | examples (untagged) |

There are no compile-time breaks. Rows 10–12 fail boot; rows 1 and 3 change decisions silently; rows 7–8 are opt-in.
Both modules ship MINOR under the v0 convention; the tag messages name rows 1, 3 and 10–12.

## Risks

1. **R-1:** an IdP whose passkey or push sign-in emits one possession value without `mfa` drops to AAL1. The hatch is
   per provider (row 1); the docs carry the self-check and the passkey caveat.
2. **Rows 10–12** are production boot failures with no compile signal. Read-first, the development WARN naming the
   acknowledgement and its causes, and the RELEASING handoff are the warning.
3. **API keys pass every MFA gate**, and an AAL1 native session can mint API keys: a host whose `UserAdminCheck` admits
   service accounts allows administration without MFA. Named in the `control-map.md:17` cell; S4d owns it.

## Tasks as PRs

### PR-1 — sdk: strict amr rule, `Identity.MFA` meaning (`sdk/strict-amr`)

- **depends_on:** [] · **model:** opus
- **files:** `sdk/capabilities/authentication/{profile,oidc,authentication,doc}.go`; `oidc_test.go` as a rule table:
  `["otp"]` false, `["pwd","otp"]` true, `["mfa"]` true, `["hwk"]` false, `["hwk","pin"]` true, `["user","hwk"]` false,
  `["pop","fpt"]` true; explicit `MFAMethods` + `["otp"]` true; the flag path true; `TestOIDCMethodsClone` and
  `TestNoVendorNames` still green; `authenticationtest/provider.go`, if it builds identities; `sdk/README.md:118`.
  Carried for the `go.work` CI gate: any test relying on a single-factor amr counting as MFA (a grep finds none today;
  `oidc_test.go:241` sends `["pwd","otp"]`, two categories).
- **BREAKING:** row 1 (behaviour only). **Hatch:** an explicit `MFAMethods`.
- **verify:** `cd sdk && go build ./... && go test -race ./... && go vet ./...`; `make guard`; `make check`.
  Run-and-look: Verification §1.
- **tag:** `sdk/v0.19.0` (MINOR; the tag message names the amr rule change).

### PR-2 — authentication: assurance, `MFAConfig`, events, posture; examples wired (`pockets/authentication-mfa-posture`)

- **depends_on:** PR-1 tagged. The pocket's `go.mod` moves sdk v0.17.0 → v0.19.0 by explicit `GOWORK=off go get`, so a
  host upgrading the pocket gets the strict rule by MVS (a semantic floor: no new sdk symbol is referenced).
  · **model:** opus
- **files** (under `pockets/authentication/`): **logic** (`logic/authentication/`): `credential.go`,
  `external_config.go` (`RequireMFA`, `SingleFactorAcknowledged`), `external.go` (`:167`, `:275-283`, `:297`); new
  `assurance.go` (`ErrMFARequired`, the moved `ErrSingleFactorUnacknowledged`, `MFADenial`, `RecordMFADenied` on both
  engines, `externalAssurance`, the standalone posture check); `securityevent/securityevent.go`; `session/method.go`
  (`AtLeast`, docs); `credential/policy.go` and `authgrant/requirement.go` (the fold); **inbound** (`inbound/http/`):
  `principal_options.go` (`:197-208` only); `principal.go` (`:194-215`); `authenticator.go`, `useradmin.go`
  (`:121-137`), `options.go`; **root:** `config.go`; `constructor.go` (the `all` clone near `:534`; `:636`; the boot
  line beside `:680`); `security.go` (`ErrMFAUnsatisfiable`, the re-exports, the causes at `:526-561`);
  `configuration.go` (validation; the `External.SingleFactorAcknowledged` refusal); **tests:** new
  `mfa_posture_test.go` (every refusal and cause in both modes, the waiver and WARN lines, the `all` clone, `admins`
  without a check, an unknown `Required`); new `logic/authentication/external_assurance_test.go` (stamping, the
  provider floor before provisioning, the empty `UserID`, the standalone posture, bounded Details, the budget); new
  `inbound/http/mfa_test.go` (reads `Assurance`, the access-token panic still fires, an API key passes `RequireMFA` and
  the admin gate, nested narrowing, the recorder called once, admin 403 before 401); an `AtLeast` table; the
  denial-coverage test gains the type; `README.md` (Design 5). **Examples carried for `go.work` CI:**
  `examples/production-baseline/`: `cmd/server/main.go` + `main_test.go`, `README.md:142-209`;
  `examples/external-identity/`: `cmd/server/{main,main_test}.go`, `README.md:74-107`.
- **BREAKING:** rows 3 and 10–12 (row 1 arrives by MVS). **Hatch:** per row.
- **verify:** `cd pockets/authentication && go build ./... && go test -race ./... && go vet ./...`; `go test ./...` in
  both examples; `make guard`; `make -C examples/production-baseline guard` (`gopernicus guard` + `--posture`);
  `make check`; run-and-look: Verification §2–§4.
- **tag:** `pockets/authentication/v0.25.0`, only after the live legs.

### PR-3 — record (`docs/s4a-release-record`)

- **depends_on:** PR-2 tagged · **model:** sonnet (mechanical)
- **files:** `RELEASING.md` section "MFA posture (SOC 2 slice S4a)": the tag table, the Upgrade notes (Read-first block
  and table verbatim), per-module handoff notes, and the verification record with leg counts; every `examples/*/go.mod`
  pinning authentication or sdk, moved to v0.25.0 / v0.19.0 by explicit `GOWORK=off go get` (never `go work sync`); the
  docs site (Design 5); `control-map.md:17` (OPT-IN for IdP hosts, GAP for native; residuals: API keys pass MFA gates,
  an AAL1 native session can mint API keys, `admins` gates only `/auth/admin/users*`), `:44` and `:99`; the
  `roadmap.md` S4 status line; the executed copy at `plans/s4a-mfa-posture.md`.
- **BREAKING:** none.
- **verify:** `make check`; `make guard`; `GOWORK=off go build ./... && go test ./...` in each repinned example;
  `pnpm build` in `workshop/documentation`.
- **tag:** none.

## Sequencing

PR-1 → step 1 → PR-2 (repins sdk) merges → live legs on merged `main` → step 2 → PR-3. Two code trains is the floor:
the strict rule lives on the port (the P question) and the pocket's sdk pin needs the sdk tag on the proxy. Two
rulings change that: **R-1 = (b):** PR-1 vanishes and the pocket keeps its pin (one train); **R-2a = (b):** add PR-2b —
one `postureAcknowledgements` entry (`posture_rules.go:64-80`) → `workshop/gopernicus/v0.10.0`, before PR-3.

## Release

| Step | Tag | After | Cold verification |
|---|---|---|---|
| 1 | `sdk/v0.19.0` | PR-1 merge | scratch consumer builds `sdk/...`; §1 program prints the expected decisions |
| 2 | `pockets/authentication/v0.25.0` | PR-2 merge + authentication live legs on merged `main` | builds; MVS set: v0.25.0 beside `stores/pgx` v0.11.0 and `stores/turso` v0.10.0 on sdk v0.19.0 — `go.mod` selects all three, `go mod verify`, `go build` |

**Two tags.** **Not tagged:** every store module (no code, no store-repin PR; MVS lifts their pins); `views/goth`,
`ui/goth`, `pockets`; go-oidc, firebase, auth0 (no code); workshop (unless R-2a = (b)). No migration: the event type is
a string with no CHECK. Nothing retracts. **Tag rules:** annotated, not signed, cut at the squash-merge commit; one
merge/tag/push per command (classifier rule).

**Live legs before step 2**, on merged `main`, counted with `go test -json`. Use a fresh scratch PostgreSQL 17 (`lsof`
the port first; never 55432) and a fresh local libSQL server: `cd pockets/authentication/stores/pgx &&
POSTGRES_TEST_DSN=… go test -json ./...`, bare and with `POSTGRES_TEST_SCHEMA=s4a` (S6 baseline 432 / 0 / 1 each); `cd
pockets/authentication/stores/turso && TURSO_DATABASE_URL=… go test -json ./...` (baseline 737 passed).

**Cold-verify:** `GOWORK=off`, an empty module cache, `proxy.golang.org` + `sum.golang.org`, `go mod verify`,
`go build <module>/...`; poll the `.info` URL before any `go mod tidy`; stop the scratch containers afterwards.

## Verification

Real binaries run in DEVELOPMENT mode. A production boot needs TLS Postgres and an https issuer (the S5 lesson), so
production refusals are proven in-process (`mfa_posture_test.go`) and by the §4 program. `$B` = `http://localhost:8080`.

### §1 — sdk scratch program

Built in the scratchpad, never the repo, from three pieces: `oidctest.Start()`
(`integrations/cryptids/go-oidc/oidctest/issuer.go:121,171`); `gooidc.New` pinned to its JWKS; `sdkauth.NewOIDC` with
`Profile{}`. Mint each token and print `MFA`. Expected: `["pwd","mfa"]` true, `["otp"]` false, `["pwd","otp"]` true,
`["hwk"]` false, `["hwk","pin"]` true, `["user","hwk"]` false; the old six `MFAMethods` + `["otp"]` true (the hatch).

### §2 — production-baseline

Set up per `examples/production-baseline/README.md:130-140`: docker Postgres 17 on 55491, `.env` edits, `make run`.
The boot log carries `auth: mfa posture mfa_required=admins gates=/auth/admin/users*`.

1. Mint four tokens:
   ```
   MFA=$(curl -s -XPOST $B/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["pwd","mfa"]}' | jq -r .token)
   ONE=$(curl -s -XPOST $B/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["pwd"]}' | jq -r .token)
   OTP=$(curl -s -XPOST $B/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true,"amr":["otp"]}' | jq -r .token)
   U2=$(curl -s -XPOST $B/dev/token -d '{"sub":"u2","email":"b@x.test","email_verified":true,"amr":["pwd"]}' | jq -r .token)
   ```
2. `curl -s $B/auth/me -H "Authorization: Bearer $MFA"` provisions `u1`. Get its id via README `:192`, then run
   `make bootstrap SUBJECT_ID=<id>`.
3. `curl -i $B/auth/admin/users`: `$MFA` → **200**; `$ONE` → **401** with `WWW-Authenticate: Bearer
   error="insufficient_user_authentication", error_description="multi-factor authentication is required"`; `$OTP` →
   **401** (strict rule); `$U2` (a non-admin) → **403** and no MFA row. `POST /authorization/roles` (README `:160`'s
   `$ASSIGN`): `$ONE` → **401** + challenge; `$MFA` → **200** `{"outcome":"applied"}`.
4. `docker exec baseline-pg psql -U postgres -Atc "select event_type,event_status,details from security_events order by
   created_at desc limit 8"` shows
   `mfa_required_denied|blocked|{"assurance":"aal1","credential":"external_token","gate":"admin","provider":"idp"}`,
   one row with gate `route`, `external_login` rows with `"assurance":"aal2"` and `"aal1"`, and no token or amr value.

### §3 — external-identity

Run `AUTH_RUNTIME_MODE=development go run ./cmd/server` (README `:71`). With an amr `["mfa"]` token, `GET /api/me` and
the cookie `POST /api/echo` return **200** as before. A no-amr token for a fresh `sub` (`u9`) gets **401** + the
challenge on `GET /api/me`; the log shows `memstore: security event type=mfa_required_denied status=blocked user_id=`
(empty: the provider gate precedes provisioning) and no `external_subject_provisioned` line for `u9`.

### §4 — posture scratch program

A real binary over the pocket's in-memory test repositories. Development: `Required: all` with password flows on →
`ErrMFAUnsatisfiable`; `admins` without a `UserAdminCheck` → `ErrMFAUnsatisfiable`; `Required: "most"` →
`sdk.ErrInvalidInput`. Production, root `New`: external-only with `Required` none → `ErrSingleFactorUnacknowledged`
naming `external_no_mfa`; the same with `admins` and a check → boots; OAuth `Providers` without the acknowledgement →
refused naming `native_oauth`; with it → one `posture_waiver=SingleFactorAcknowledged causes=[native_oauth]` WARN;
`External.SingleFactorAcknowledged` → `sdk.ErrInvalidInput`. Production, standalone: no `RequireMFA` → refused;
`RequireMFA` on every provider → boots with no WARN; the acknowledgement instead → one waiver WARN.

### Build-time deviations (lead rulings during execution; PRs #188–#189)

1. PR-1 sdk (#188): `amrMFA` is a method on `ClaimMapping` in `profile.go` beside the category table (plan :262/:307); matching stays case-sensitive (`MFA`/`PWD` unknown) as before; an empty non-nil `MFAMethods` means strict, same as nil (the old `len == 0` test); no exported symbols added or removed — the change is behaviour only (row 1). Docs-site `workshop/documentation/docs/sdk/authentication.md:48-49` lists the old six values until PR-3.

2. PR-2 authentication (#189): native ACCESS TOKENS stay `unknown` assurance (the plan's rule; the lead brief's "native sessions AAL1" was wrong and withdrawn) — S4b stamps them; `external_no_mfa` counts PER PROVIDER (any provider without `RequireMFA` while `Required` is none), so a root host whose every provider sets `RequireMFA` boots without the acknowledgement — matching the standalone path (plan :191 said "External wired and Required none"); event Details carry `required` (plan §2's sample row lacked it); `WithAdministrationMFA()` without `WithUserAdminCheck` on `authenticationhttp.New` → `sdk.ErrInvalidInput` (the plan was silent; the root path returns `ErrMFAUnsatisfiable` first); `validateMFA` runs right after `validateOAuthConfig`, before `authlogic.New`, so a production dev-only external provider without an MFA posture reports the single-factor refusal first (two tests set the acknowledgement); slog renders multi-cause values quoted (`causes="[native_password external_no_mfa]"`) and an empty user id as `user_id=""`; the IdP table's Auth0 and Firebase cells cite this repository's code ("checked 2026-10-24") because the build had no web access — Google, Okta, Entra **unknown**; a logic comment was reworded to keep G28 (which greps `UserAdminCheck`) green. New exported symbols beyond the plan: `ErrMFARequired` (wraps `sdk.ErrUnauthorized`), `MFAGate` + three consts, `MFADenial`, `(*Service).RecordMFADenied`, `(*ExternalAuthenticator).RecordMFADenied`, `MFARequirement` + three consts.

## Open rulings — YOUR CALL

**R-1 — default amr rule.** (a) Strict over the IANA amr registry, factor-categorised, with an explicit `MFAMethods`
as the hatch (setting it restores any-of exactly); (b) Keep the six-value any-of default; (c) Strict, and drop any-of.
**Recommend (a).** "Never higher" rules out (b); (c) removes the only hatch. It fails closed only on gates the host
opted into, and Auth0/Firebase hosts are untouched.

**R-2a — IdP hosts with `Required: none` in production.** (a) Covered by the widened `SingleFactorAcknowledged` (cause
`external_no_mfa`, both constructors); no new acknowledgement, no workshop tag; (b) A new `MFANotRequiredAcknowledged`,
plus a P5 list edit and workshop v0.10.0; (c) WARN only; the "production check" stays unmet.
**Recommend (a):** one acknowledgement meaning "this host admits AAL1 users", kept auditable by the `causes` attr.

**R-2b — native OAuth as single-factor login.** (a) Include: OAuth `Providers` are cause `native_oauth` under the same
acknowledgement (this reverses today, where native OAuth never trips `:538`; row 11); (b) Exclude until S4b: an
OAuth-only production host keeps booting with no acknowledgement.
**Recommend (a).** An OAuth link is AAL1 by descriptor (`session/method.go:63`); excluding it leaves a single-factor
source unacknowledged, and the `causes` attr shows which source a waiver covers.

**R-3 — password users under `Required: all`.** (a) Boot refusal `ErrMFAUnsatisfiable` in every mode; (b) Boot, then
deny at login with 403 `mfa_required` plus a row; (c) Admit at AAL1 with a WARN.
**Recommend (a).** S4a has no native second factor, so a password user under `all` is a wiring contradiction. (b)
strands every password user at runtime; (c) makes the policy lie. S4b relaxes this to "deny at login unless
enrolled", with grace.

**R-4 — `RequireMFA` default on bundled admin/machine routes: now or in S4b?** (a) Keep the default at none; S4b flips
it to `admins` once native factors exist; (b) Default to `admins` now; every native-admin host then gets 401 on
`/auth/admin/users*` until S4b. **Recommend (a).**

**R-5 — machine lifecycle under `admins`.** (a) Out in S4a, in for S4b; (b) In now. Those routes admit first-party
access tokens only (`routes.go:162-164`), so they would be unreachable on every host until S4b.
**Recommend (a).** The residual — an AAL1 native session can mint API keys — is stated in the `control-map.md:17`
cell and the README.

## Recommended reviews

The two-reviewer gate is done (lead-backend-engineer, product-manager; folded above). product-manager re-reads rev 2
against P1–P15 if the owner wants a confirmation pass; platform-sre is optional, on rows 10–12 and the release steps.

## Notes

**Handoff to S4b:** stamp `Credential.Assurance` for access tokens, from a signed `aal` claim at mint or from the Live
session row that `credentialSessionLive` reads (`logic/authentication/machine.go:407-430`), and lift the `RequireMFA()`
panic beside access tokens (`principal_options.go:330-332`) in that same PR; add `MFAConfig.Methods` (over
`session.MethodKind`, `method.go:21-24`) where it first selects between sources, and define enrollment grace; take
R-4/R-5 to `admins`, machine lifecycle included; `MinLoginAssurance` becomes meaningful once an AAL2 method joins the
MethodSet. **Handoff to S4c:** TOTP, recovery codes (always) and the key ring. **Handoff to S4d:** API keys under MFA
gates and the AAL1-session-mints-API-keys residual.

**Follow-up (acr):** `ClaimMapping.ACR`/`MFAACRValues`, `Identity.ACR` and the devissuer field, once one vendor's MFA
acr value is verified against its docs and an example uses that value.

**Libraries ruling.** `go-webauthn/webauthn` and `pquerna/otp` become adapters behind `sdk/capabilities/…` ports, named
for the package whose API they use: `integrations/cryptids/go-webauthn` (S4b), `.../pquerna-otp` (S4c); not in S4a.

**Cut for the 500-line cap** (detail, not scope): per-test case lists beyond the rule table; the handoff-note prose
(PR-3 writes it); a per-file diff sketch of `principal_options.go`; IdP vendor links (PR-2); a lead pre-consultation.

**Placements decided here, open to review:** root `New` refusing `External.SingleFactorAcknowledged` (rather than
honouring it); the boot line's `gates=` vocabulary; `ErrSingleFactorUnacknowledged` moving into logic; the pocket's
sdk repin, which is semantic rather than compile-forced.
