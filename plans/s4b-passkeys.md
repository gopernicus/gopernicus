# S4b — Passkeys: native AAL2 through WebAuthn (SOC 2 readiness, slice 4b)

**Status:** EXECUTED 2026-10-25 (tags: sdk v0.20.0, go-webauthn v0.1.0, authentication v0.26.0 → v0.27.0 → v0.27.1 via the device-check addendum `s4b-device-check-fixes.md`, stores pgx v0.12.0 / turso v0.11.0, views/goth v0.7.0; owner Safari + Touch ID device check PASS 2026-10-25). RATIFIED 2026-10-24 (owner: "ratified"; rev 2 as reviewed; B-1 recovery codes + native `all` + Methods + grace → S4c; B-2 zero Required resolves to `admins` when passkeys + UserAdminCheck are wired, `MFARequiredNone` = "none" opts out; B-3 machine lifecycle gated under admins when passkeys are wired; B-4 attestation none; B-5 residentKey required, usernameless only; B-6 the script in views/goth; OPEN QUESTION ruled YES: a password/code step-up grant inside the 5-minute window bootstraps the first passkey beside a fresh login). Build started from `main` @ 6f05b685: PR-1 sdk first; PR-2 adapter + PR-3a core after tag 1; PR-3b stacked on 3a; tag 3 after 3a+3b merge and the live legs; PR-3c + PR-4 in parallel; the owner's Safari/Touch ID check gates PR-5.

- **Size:** L — seven PRs, five trains, six tags (one for a NEW module); an owner-run device check gates the record PR. No
  split (Notes). Rev 1: `s4b-passkeys.rev1.md`.
- **Anchors.** `main` @ 6f05b685 (S1–S3, S5, S6, S4a released): sdk v0.19.0, authentication v0.25.0, stores pgx v0.11.0 /
  turso v0.10.0, views/goth v0.6.0, ui/goth v0.2.0, pgxdb v0.14.0, turso v0.9.0, workshop v0.9.0. Sources:
  `plans/s4a-mfa-posture.md` (Notes), `roadmap.md:181,313`, `RELEASING.md:3-268`, `control-map.md:17,44,99`. Library:
  go-webauthn v0.18.2, the latest (proxy, 2026-09-19; `go 1.26.0`).
- **Owner rulings 2026-10-24 (binding).** Passkeys first, TOTP the S4c fallback; recovery codes ship regardless (B-1 picks
  the slice); libraries go behind sdk ports, in adapters named for the package whose API they use; example tiers may break.
- **What `main` already does.** Native methods are AAL1 and the access JWT carries no assurance (`session/method.go:72-84`,
  `token.go:18-26`); `RequireMFA()` is external-only and panics beside access tokens (`principal_options.go:204,334`);
  `RequireRecentAuthentication` consumes its grant before returning (`stepup.go:45-58,123-170`).

## Goal
Native users enroll a passkey and sign in (usernameless) or step up with it. A user-verified passkey is AAL2 end to end: the
session row, a signed `aal` claim, `RequireMFA()` and the `admins` gates. Every ceremony is audited and backed by pgx, turso
and memory stores, proven in real Chrome with a virtual authenticator and once on a real Mac.

## Scope and non-goals
**In scope:** port and adapter (PR-1, PR-2); domain, ceremonies, assurance, events, notices, posture, JSON + HTML seams
(PR-3a); stores (PR-3b; repin PR-3c); pages, the script, the auth-cms login markup, the browser leg (PR-4); the record
(PR-5). **Non-goals:** TOTP, recovery codes, native `all`, `MFAConfig.Methods`, the enrollment grace (S4c, B-1);
username-first sign-in (follow-up); attestation and AAGUID allow-lists (B-4); autofill, Related Origin Requests, native-app
origins, passkey-only sign-up; `aal` on delegated tokens, API keys under MFA gates (S4d); AAL3; a committed Playwright spec.

## Decided by gate
Reviews 2026-10-24: lead-backend-engineer (BE), lead-frontend-engineer (FE), product-manager (PM), each with one question.
Where they differ, the lead's ruling decides. Folded unless marked.
- BE1 + PM1 (HIGH, a password alone enrolls the AAL2 factor) → AAL2 to add/remove once a UV passkey is held; fresh-login bootstrap; notices; residual; break-glass (§3.2, §7).
- BE2 (HIGH, flags read from the stored record) → UV, backup state and count from THIS ceremony's authData; `CreateCredential`; a suite case (§1, §2).
- BE3 (`Allow` breaks a two-passkey step-up) → `AssertionStart.Allow` DROPPED; discoverable + a server-side owner check (`user_mismatch`); suite pins two.
- BE4 (the grant is consumed at Begin) → consumed at Begin; the session-bound ceremony row carries it; Finish checks user + live session (§3.2).
- BE5 (zero `Required` weakens the posture) → resolved ONCE before every consumer; IdP-host test; `source=default` (§3.4). Its `Methods` clause MOOT.
- BE6 (AAL2 freshness) → AAL2 = a UV passkey only; the re-mint keeps `ExpiresAt`; Risk 4. The in-window password option DECLINED.
- BE7 (TTL drift) → `Timeout` ≤ 5m; the adapter sets `Enforce: true`. Feeding `Ceremony.ExpiresAt` to the rail DECLINED (one TTL).
- BE8 (`Context` is a "pure validator") → the Go docs amended in PR-3a; the APPLIED `0011` SQL comment stays (immutable; G34 copies).
- BE9 (one handle per user) → `Create` under `lockCredentialUser` refuses a differing `UserHandle` (`sdk.ErrConflict`); `RunPasskeys` pins it.
- BE10 (proof/revision order) → spelled out and pinned (§3.2); "removed between lookup and mint → denied"; the step-up fenced alike.
- BE11 (`aal` edges) → a row with `assurance_level=''` omits `aal`; the delegated branch REFUSES an `aal` claim (§3.3).
- BE12 (CI, paths, erasure) → `RunPasskeys` separate, like `RunOAuth2`; authmem `ports_v3.go`; the `erase.go` list; rows asserted gone (§4).
- BE13 (docs; namespaces) → an ARCHITECTURE sentence (PR-1); production refuses `HasHTTPNamespace()`, read via `RelyingParty.Config()` (§3.5).
- BE14 + PM9 + PM12 (line budget) → the grace and its ruling to the S4c handoff; username-first CUT; the methods-claim hard rule (Notes).
- BE question (unsolicited extension outputs) → assertions IGNORE them, registration rejects; Risk 5; the device check probes it.
- FE1 (HIGH, auth-cms overrides Login) → PR-4 adds `authpages.{go,_test.go}`; that markup is the custom-view contract (§5).
- FE2 (HIGH, no wire contract) → the §5 wire table; `credential` as `json.RawMessage`; step-up answers as `POST /auth/login`; goldens.
- FE3 + PM6 (HIGH, step-up dead end; empty page) → finish returns `assurance`; navigate on `aal2` only; `HasPasskeys`/`CanReachAAL2` (§5).
- FE4 (§3 not runnable) → `{options:{…}}`; presence off before a second; a 401 allowlist; `/debug/security-events`; spawn; main checkout.
- FE5 (fallback unexercised; no a11y) → a §3 `addInitScript` pass; axe on the login, manage and step-up pages.
- FE6 + PM11 (script contract; U2F copy) → copy table incl. U2F-only; in-flight guard; empty live region; click-only fetch; JSON type (§5).
- FE7 (orphan device credentials) → the cap pre-checked at Begin; a finish never fails on its name (server default) (§3.2).
- FE8 (`PasskeyViews` wiring) → `PasskeyConfig.Views` (nil = JSON only); invariant: the `Views` interface gains no method (§3.5).
- FE9 (nonce under a nil `HTMLPolicy`) → an un-nonced tag (as `fragmentScriptTag`); godoc; a Permissions-Policy test (§5).
- FE10 (hard-coded redirect) → derived like the login redirect; `return_to` on GET/HEAD only; `PasskeyLinks` carries every URL (§3.3).
- FE11 (route precedent) → form `POST /auth/passkeys/{id}` `action=rename|remove`; step-up beside `/auth/step-up/*`; row ids; confirm copy.
- FE12 (lockstep unproven) → views v0.6.0 + core v0.26.0 in cold-verify set A; the views `go.mod`/`go.sum` in PR-4.
- FE notes → username-first as an SPA JSON option DECLINED (cut entirely); SPAs read `/auth/methods` → README; the question MOOT (BE6).
- PM2 (HIGH, the admin reset unproven) → Verification §2(11) + the README operator path. PM question → YES: API keys reach it (`routes.go:139-140,169-170`, `useradmin.go:145`).
- PM3 (HIGH, no real-device check) → the owner-run Safari + Touch ID check GATES PR-5; iOS, Android, Windows Hello named unverified.
- PM4 (cut-line) → "considered and rejected", with the reason (Notes). PM5 (`Methods`) → S4c with the claim; its row and paragraph cut.
- PM7 (auth-cms teaches `localhost`) → RP ID + `Origins` from its origin; a production test on a real domain; the README recipe (§7).
- PM8 (no out-of-band signal) → `passkey_change_notice` on the identifier-notice path (§3.2). PM10 → a by-host-shape index (Upgrade notes).
- PM B-5 wording → B-4 reads "AAGUID recorded when the client reports one".

## Design
### 1. The port: `sdk/capabilities/authentication/webauthn` (sdk v0.20.0)
Imports root `sdk` and `sdk/pkg/web` only (no capability, its parent included: G12c, `Makefile:319-323`); stdlib-only. PR-1
adds an `ARCHITECTURE.md:106-108` sentence for a subsystem subpackage that imports nothing from its parent. S4c's TOTP port
lands beside it. No default: the library sits behind the port (the `oauth`/`work` precedent).
```go
type Requirement string // "required" | "preferred" | "discouraged"
type Config struct {
	RPID, RPName string           // RPID: the effective domain ("example.com", "localhost"); lowercase, no scheme/port/path
	Origins      web.OriginPolicy // admits the client-data origin; every exact host and namespace suffix sits within RPID
	Timeout      time.Duration    // zero → 5m; above 5m refused (the rail TTL)
}
func (c Config) Validate() error // sdk.ErrInvalidInput; ErrOriginOutsideRPID names the host
type RelyingParty interface {
	Config() Config // validated; the core reads RPID and Origins for its production refusals
	BeginRegistration(ctx context.Context, in RegistrationStart) (Ceremony, error)
	FinishRegistration(ctx context.Context, in RegistrationFinish) (Credential, error)
	BeginAssertion(ctx context.Context, in AssertionStart) (Ceremony, error) // always discoverable: empty allowCredentials
	ReadAssertion(response []byte) (AssertionHandle, error)                // unverified lookup keys only
	FinishAssertion(ctx context.Context, in AssertionFinish) (Assertion, error)
}
type User struct{ Handle []byte; Name, DisplayName string } // Handle: 32 random bytes, never the user id
type RegistrationStart struct{ User User; Exclude [][]byte; UserVerification Requirement } // zero → preferred
type AssertionStart struct{ UserVerification Requirement }
type Ceremony struct{ Options json.RawMessage; State []byte; ExpiresAt time.Time } // Options: the inner publicKey JSON
type RegistrationFinish struct{ State, Response []byte }                            // Response: RegistrationResponseJSON
type AssertionHandle struct{ CredentialID, UserHandle []byte }
type AssertionFinish struct{ State, Response []byte; Credential Credential; UserHandle []byte } // stored record + owner
type Credential struct{ ID, PublicKey, AAGUID []byte; SignCount uint32; Transports []string; UserVerified, BackupEligible, BackupState bool }
type Assertion struct{ SignCount uint32; UserVerified, BackupEligible, BackupState, CounterRegressed bool }
var ErrCeremonyRejected, ErrUserVerificationRequired, ErrOriginOutsideRPID error // first two wrap sdk.ErrUnauthorized
```
- **Port rules.** Attestation `"none"` (B-4), `residentKey: "required"` (B-5), ES256/EdDSA/RS256; `PublicKey` is a COSE_Key.
  **Ceremony truth:** flags and `SignCount` come from THIS ceremony's authenticator data, never the stored record.
  `ErrCeremonyRejected` covers every failure (signature, challenge, origin, `rpIdHash`, type, expired or foreign state,
  handle mismatch, malformed input). Registration rejects unsolicited extension outputs; assertions ignore them.
  `CounterRegressed`: either count non-zero and presented ≤ stored (0/0 never), with a nil error; the caller decides.
- **`webauthntest`.** `NewAuthenticator(AuthenticatorConfig{RPID, Origin; UserVerifying})`: a stdlib ES256, `none`,
  discoverable authenticator (`crypto/ecdsa` + minimal CBOR) with `SetUserVerified`, `SetSignCount`, `SetOrigin`,
  `SetExtensionOutputs`. `Run(t, factory)` pins registration ± UV and its options, assertions ± UV under `preferred` and
  `required`, **register WITH UV then assert WITHOUT under `preferred` → `UserVerified=false`**, two credentials of one
  user, origin admission, every rejection class (`Enforce`-backed expiry included), extension outputs, regression vs 0/0,
  and each `Validate` refusal. **`sdk/pkg/web`:** additive `OriginPolicy.Allows(origin) bool` and `Domains() []string`.

### 2. The adapter: `integrations/cryptids/go-webauthn` (NEW module, v0.1.0)
Package `gowebauthn`. `New(cfg)` runs `Validate()` first, so an RP ID outside the origin policy fails boot in EVERY mode.
Finish requires `cfg.Origins.Allows(clientDataJSON.origin)`, then verifies via a per-ceremony `webauthn.Config{RPOrigins:
[]string{origin}, …}` with `Timeouts.{Login,Registration}.Enforce: true` (assertions: unsolicited extensions ignored).
Registration finish is `CreateCredential` (not the `*http.Request` `FinishRegistration`); `ReadAssertion` is
`protocol.ParseCredentialRequestResponseBytes`; `FinishAssertion` is `ValidatePasskeyLogin` (one-credential handler).
**Ceremony truth:** flags and count map from `parsed.Response.AuthenticatorData` (`Flags.HasUserVerified()`, `.Counter`),
never the returned `*Credential` (stored UV latched; stored count kept on `CloneWarning`). Requires go-webauthn v0.18.2
exactly + sdk v0.20.0, no other gopernicus module (G13); joins `Makefile` MODULES, `go.work`, `ARCHITECTURE.md:12-16`.

### 3. The pocket core (`pockets/authentication` v0.26.0)
**3.1 Domain: `logic/authentication/passkey`.**
```go
type Passkey struct {
	ID, UserID, Name string; CredentialID, PublicKey, AAGUID, UserHandle []byte // Name: 1–64 runes, no control chars
	SignCount uint32; Transports []string; UserVerified, BackupEligible, BackupState bool; CreatedAt time.Time; LastUsedAt *time.Time
}
type Repository interface {
	Create(ctx context.Context, p Passkey, maxPerUser int) (Passkey, error) // locks the user row
	GetByCredentialID(ctx context.Context, credentialID []byte) (Passkey, error)
	ListByUser(ctx context.Context, userID string) ([]Passkey, error)
	Rename(ctx context.Context, id, userID, name string) error // another user's id → sdk.ErrNotFound
	RecordUse(ctx context.Context, id string, expected, count uint32, backupState bool, at time.Time) error // CAS
}
```
`Create`: a duplicate credential id → `sdk.ErrAlreadyExists`; the cap → `ErrPasskeyLimit`; a differing `UserHandle` →
`sdk.ErrConflict`. `RecordUse`: a CAS miss or a missing row → `sdk.ErrConflict`. **Removal goes through `Apply`:**
`credential.RemovePasskey{PasskeyID}` and `RemovePasskeys{}` (the admin reset) join the sealed `Mutation` set; `Snapshot`
fills `MethodSet.Passkeys []PasskeyMethod{ID; UserVerified}`, so passkeys count as login methods and a removal revokes every
session; enrollment bumps nothing. Erasure deletes the rows (`EraseCounts.Passkeys`; listed in `user/erase.go:20-40`).

- **3.2 Ceremonies** (`passkey_ceremony.go`, `passkey_management.go`). **The challenge rail is reused:** purposes
  `passkey_registration`, `passkey_assertion`, `passkey_step_up` (5 min) join `challengeSpecs`; the handle is the rail's
  256-bit token, single-use by digest (expired → `ErrChallengeExpired`). `Context` holds `{state, session_id}`, and PR-3a
  amends the "pure validator" Go docs (`challenge.go:123`): the state holds no secret. `SubjectKey` is the session id
  (registration, step-up) or the token digest (sign-in, IP-budgeted).
- **Enroll.** `BeginPasskeyRegistration(ctx, sessionID, userID)` / `FinishPasskeyRegistration(ctx,
  PasskeyRegistrationInput{SessionID, UserID, Ceremony, Response, Name})`. **Gate:** a UV-passkey holder needs
  `RecentAuthPolicy{MinAssurance: AAL2}`; anyone else (the FIRST passkey included) gets an AAL1 bootstrap from a FRESH
  primary login (the recent-login shortcut; Open questions). **Begin** pre-checks `MaxPerUser` (no device orphan) and
  consumes the grant, after which the single-use, session-bound row carries the authorization. **Finish** consumes it,
  checks its user and `session_id` against the caller's live session, excludes the user's credentials, reuses or mints the
  handle, applies a valid `Name` (else "Passkey N"; never a failure), calls `Create`, records `passkey_registered` and sends
  the notice.
- **Sign in (usernameless only).** `BeginPasskeySignIn(ctx)` / `FinishPasskeySignIn(ctx, PasskeySignInInput{Ceremony,
  Response}) (TokenPair, error)`, order pinned: consume → `ReadAssertion` → `GetByCredentialID` → `users.Get` (active;
  revision R) → `FinishAssertion` → regression check → `RecordUse` (`ErrConflict` → denied) → `mintSession(R)` →
  `passkey_login`; the reference suite pins "removed between lookup and mint → denied". Failures: the generic
  `ErrPasskeyRejected` (401) + `passkey_failed`.
- **Step up.** `{Begin,Finish}PasskeyStepUp` (`PasskeyStepUpInput{SessionID, UserID, Ceremony, Response}` → `StepUpResult`):
  discoverable, the same order, plus an owner check (another user's credential → `reason=user_mismatch`) and the
  live-session check. Success re-mints the session (+ the passkey; `AuthenticatedAt` now; the OLD `ExpiresAt`), fenced on R,
  deletes the old one, and returns the token pair and the resulting `assurance` (`aal1` for presence-only).
- **Manage.** `ListPasskeys`, `RenamePasskey`, `RemovePasskey` (a `PurposeRemovePasskey` grant under the enrollment gate's
  policy) and `ResetUserPasskeys(ctx, actor, userID)` (via `adminPrincipal`). **Notices:** registration, removal and reset
  enqueue a `passkey_change_notice` (a new delivery purpose + core template) to the verified channels on the
  identifier-change path (`identifier_management.go:485-540`; best-effort). **Clone:** `CounterRegressed` denies, keeps the
  stored count and records `passkey_sign_count_regressed`.
- **3.3 Assurance.** `MethodPasskey = "passkey"` (UV) is AAL2, `MethodPasskeyUnverified = "passkey_unverified"` AAL1, both
  phishing- and replay-resistant: a UV passkey is the ONLY native AAL2 source. Every mint site signs `aal` (`aal1`|`aal2`)
  from the session row (a legacy `assurance_level=''` row omits it); `parseAccessClaims` maps it to `Credential.Assurance`
  (missing → `unknown`; an unknown value → rejected); the delegated branch REFUSES an `aal` claim. **The panic lift** (same
  PR): `RequireMFA()` becomes an `AtLeast(AAL2)` check on access and external tokens (API keys still pass, S4d);
  `FreshAuth`/`VerifiedEmail`/`Tenant`/`Check` keep the `:334` panic, its message naming them. **`Browser()` +
  `RequireMFA()`:** a cookie below AAL2 gets a 303 to the passkey step-up page when passkeys and `Views` are wired (the path
  derived like the login redirect, `authenticator.go:20`; `return_to` on GET/HEAD only), else the JSON 401 + RFC 9470
  challenge.
- **3.4 `MFAConfig`.** **B-2 (recommended):** a NEW zero value `MFARequiredDefault = ""`; `MFARequiredNone` becomes
  `"none"`. Configuration resolves `Required` ONCE, before `validateMFA`, the posture check (`security.go:607`) and
  `requireExternalMFA` (`constructor.go:696-718`): zero → `admins` when `WithPasskeys` and `UserAdminCheck` are both wired,
  else `none`. A test pins an IdP host at zero still tripping `external_no_mfa`; the boot line prints `mfa_required=admins
  source=default`. Satisfiability becomes "needs WithExternal or WithPasskeys"; native `all` stays `ErrMFAUnsatisfiable`,
  saying it waits for recovery codes in S4c — use `admins` meanwhile. **B-3 (recommended):** with passkeys, `admins` also
  gates the machine lifecycle after `MachineRoutesGate`. `Methods` and the grace go to S4c.
- **3.5 Config and posture.** `WithPasskeys(PasskeyConfig{RelyingParty; UserVerification; MaxPerUser; Views PasskeyViews})`:
  a nil RP → off; UV zero → `preferred`; `MaxPerUser` zero → 10, max 25; `Views` nil → JSON only (HTML routes mount from
  this field alone; the `Views` interface gains no method). `Repositories.Passkeys`, `Challenges` and `CredentialMutations`
  are required (`ErrPasskeyReposRequired`). **Production** refuses an RP whose `Config().RPID` is `localhost` or an IP, or
  whose `Config().Origins.HasHTTPNamespace()`: `ErrPasskeyDevelopmentRelyingParty`. The single-factor posture is unchanged.

### 4. Stores (pgx v0.12.0, turso v0.11.0; the S3/S5 migration rules)
pgx `0025_passkey_credentials.sql`: `SET LOCAL lock_timeout = '5s'`; `CREATE TABLE IF NOT EXISTS passkey_credentials`
(§3.1's fields; `id TEXT COLLATE "C"` key; BYTEA ids/key/handle; `sign_count BIGINT CHECK (0..4294967295)`; `transports`
JSON text; no foreign key), UNIQUE `credential_id` and `(user_id, created_at, id)` indexes, all `IF NOT EXISTS`; turso
`0025` is the twin. **Required:** the `Repositories` probe refuses its absence in every mode, naming 0025 (empty table: no
CONCURRENTLY, no `index_missing` WARN). Per dialect: `passkeys.go`, `Snapshot`/`Apply` (both variants, user-row lock),
erasure, `Create` via `lockCredentialUser`. **`storetest.RunPasskeys`** is separate, like `RunOAuth2`
(`stores/storetest/oauth2.go:21`), so `Run` is unchanged and PR-3a passes go.work CI alone (the stores' `default:` arm fails
closed). It pins the `Create` errors, inactive users, `Rename` scoping, the `RecordUse` CAS, `Apply` revoking sessions, a
concurrent `RemovePassword` + `RemovePasskey` never leaving zero methods, and erasure counts AND rows gone. `TestReference`
and auth-cms `authmem` (`examples/auth-cms/pockets/authentication/outbound/authmem/ports_v3.go:473-482`) implement it. G34:
a byte-identical ledger `0032_passkey_credentials.sql`.

### 5. HTTP, views and the script asset
Every route sits under `/auth` (S5 `no-store`; the 64 KiB bounded decoder).

| Route | Tier and gates |
|---|---|
| `POST /auth/passkeys/registration/options` · `POST /auth/passkeys/registration` | CredentialManagement + browserSafe; the §3.2 gate |
| `POST /auth/passkeys/assertion/options` · `POST /auth/passkeys/assertion` | public; `PublicRouteLimits.Passkey` (zero → 30/min); S5 JSON-arm origin gate |
| `POST /auth/step-up/passkey/options` · `POST /auth/step-up/passkey` | CredentialManagement + browserSafe, beside `/auth/step-up/{password,code}` |
| `GET /auth/passkeys` · `PATCH`/`DELETE /auth/passkeys/{id}` (delete takes a grant) | SessionSecurityReads · CredentialManagement + browserSafe |
| `POST /auth/admin/users/{id}/passkeys/reset` | UserAdministration + browserSafe + `adminPrincipal` (`admins` applies; an admin API key passes) |

**Wire contract** (golden tests in core's `inbound/http/passkeys_test.go`; `passkey.js` and SPAs share it):

| Call | Request | Success | Errors |
|---|---|---|---|
| each `…/options` | `{}` | 200 `{ceremony, public_key}` (the inner options JSON `parse*OptionsFromJSON` takes) | 403 step-up required; 409 `passkey_limit` |
| registration finish | `{ceremony, credential, name?}` | 201: the list item | 410 `challenge_expired`; 401 `passkey_rejected`; 409 `passkey_exists`/`passkey_limit` |
| sign-in finish | `{ceremony, credential}` | exactly as `POST /auth/login` | 410 `challenge_expired`; 401 `passkey_rejected` |
| step-up finish | `{ceremony, credential}` | as `POST /auth/login` (bearer SPAs get the new pair), plus `assurance` | 410 `challenge_expired`; 401 `passkey_rejected` |

- `credential` is a `json.RawMessage` (so `strictJSONBody` tolerates the extra `toJSON()` members); `challenge_expired` and
  the 403 reuse existing mappings (`errors.go:24-34`, `ErrStepUpRequired`). List items: `{id, name, created_at,
  last_used_at, user_verified, backup_eligible, backup_state, transports}`; no response carries a key, credential id, handle
  or state.
- **Views.** `PasskeyViews{Passkeys(PasskeysPage); PasskeyStepUp(PasskeyStepUpPage)}` follows `OAuthViews`; `LoginPage`,
  `StepUpPage` and `AccountSecurityPage` gain `Passkeys *PasskeyLinks` (every URL the templates and script use).
  `PasskeyStepUpPage` carries `HasPasskeys` and `CanReachAAL2`: none → "Add a passkey" to
  `/auth/account/passkeys?return_to=…`; presence-only keys only → copy that they cannot confirm a PIN or biometric; no
  `PublicKeyCredential` → a text explanation. **HTML routes** (with `Views`): `GET /auth/account/passkeys`; form `POST
  /auth/passkeys/{id}` `action=rename|remove` (BrowserAccount + the form lane; per-row ids; removal confirms "this signs you
  out on every device"); `GET /auth/step-up/passkey`. **views/goth v0.7.0:** `passkey.templ` plus buttons in `credential`,
  `stepup` and `account`; `_templ.go` via `make generate`. **auth-cms** overrides Login
  (`examples/auth-cms/pockets/authentication/inbound/authpages/authpages.go:66,93`); PR-4 renders the button, its data
  attributes and the `<script src>` from `LoginPage.Passkeys`: the contract for custom-view hosts.
- **The asset (B-6).** `assets/passkey.js` behind `PasskeyScript()`, `PasskeyScriptHandler()`, `DefaultPasskeyScriptPath =
  "/assets/auth/passkey.js"` and `WithPasskeyScriptPath`, mounted like `FragmentScriptHandler`. No inline script, `eval`,
  inline handlers or `innerHTML`; the tag carries NO nonce (as `fragmentScriptTag`), so `HTMLPolicy`'s `script-src 'self'` +
  `connect-src 'self'` (StylesOnly included) load it, while a nil-`HTMLPolicy` host's nonce-only CSP blocks it and the
  controls stay hidden (the godoc says so); no `runtime.js`, Alpine or HTMX. Controls stay `hidden` without
  `window.PublicKeyCredential`; options are fetched on click only (the 30/min budget); `Content-Type: application/json` +
  the CSRF double-submit header; `parse*OptionsFromJSON`/`toJSON()` with a base64url fallback; a step-up navigates only on
  `aal2`; an in-flight guard (`aria-busy`); an EMPTY template-rendered live region via `textContent`, `role="alert"` for
  errors. **Copy:** NotAllowedError (cancelled, timed out, or a U2F-only key: "This security key can't store a passkey; use
  your device or a FIDO2 key with a PIN"), InvalidStateError ("This device already has a passkey for your account"),
  SecurityError, each wire code. ≤ 200 lines, no build step; tests pin the forbidden tokens, the copy, the headers, and a
  Permissions-Policy never denying `publickey-credentials-*`.

### 6. Security events (S2 rules: bounded enums, opaque ids, small integers)
No row carries a public key, credential id, handle, name, origin or raw response. `mfa_required_denied` gains
`credential=access_token` and gate `machine`.

| Type | Status | UserID | Details | Budgeted |
|---|---|---|---|---|
| `passkey_registered` | success | owner | `passkey_id`, `user_verified`, `backup_eligible`, `aaguid` (32 hex) | no |
| `passkey_removed` | success | owner | `passkey_id`, `remaining` | no |
| `passkeys_reset` | success | target | `removed`; the actor, as `user_erased` records it | no |
| `passkey_login` | success | owner | `ceremony` (`sign_in`/`step_up`), `assurance`, `backup_state` | no |
| `passkey_failed` | failure | empty (sign-in) / owner (step-up) | `ceremony`, `reason` ∈ {`unknown_credential`, `user_mismatch`, `rejected`, `uv_required`, `expired`, `inactive`, `conflict`} | yes, per IP |
| `passkey_sign_count_regressed` | blocked | owner | `passkey_id`, `stored`, `presented` | yes, per IP |

### 7. Workshop, the control map and the README
**Workshop:** untouched and untagged (neither new refusal carries a waiver). **Control map (PR-5):** `:17` becomes "OPT-IN
(IdP; native passkeys) / DEFAULT (native admin surfaces with passkeys, B-2)" with residuals: the first-passkey AAL1
bootstrap (the notice is the detective control), API keys (S4d), native `all` (S4c), delegated tokens; `:44` gains AAL2 by
default on `/auth/admin/users*` and the machine lifecycle; `:99` the six types. **README** (PR-3a; the markup in PR-4):
"Turn on passkeys" (0025, `gowebauthn.New`, `WithPasskeys`, `Views` + the script, the B-2 default; the RP ID is PERMANENT,
and changing it orphans every passkey); the sole-admin break-glass (`Required: MFARequiredNone` + redeploy); "admins enroll
two passkeys"; the reset operator path (a cookie + CSRF `fetch` from an AAL2 session, or an admin API key the host's
`UserAdminCheck` admits); U2F-only keys cannot enroll; SPAs read `/auth/methods` on the admin 401; the custom-view markup.

## Upgrade notes
> **Read first:** `grep -rnE 'MFARequiredNone|MutationRepository|RequireMFA\(' --include='*.go'`; apply 0025 before
> repinning a store. **By host shape** (PR-5 expands each):
> - **Every host that repins stores:** row 12 (apply 0025 first), rows 3 and 4; row 8 with a custom `MutationRepository`.
> - **IdP-only hosts** (the production-baseline shape): row 12 only; 0025 is required with passkeys unwired.
> - **Hosts turning passkeys on:** rows 2, 5–7, 9–11, 13 and the README "Turn on passkeys" recipe.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `webauthn` port, `webauthntest`, `OriginPolicy.Allows`/`Domains` | additive | none | sdk@v0.20.0 |
| 2 | new adapter `gowebauthn.New(webauthn.Config)` | `Validate` failures refuse boot in every mode | fix RPID/Origins | go-webauthn@v0.1.0 |
| 3 | access tokens carry `aal`; native `Credential.Assurance` is aal1/aal2 | pre-upgrade tokens read `unknown` until refresh; a delegated token carrying `aal` is refused | none | authentication@v0.26.0 |
| 4 | `RequireMFA()` beside access tokens no longer panics | AAL1 tokens get 401 + challenge; `FreshAuth`/`VerifiedEmail`/`Tenant`/`Check` still panic | `Accept(...)` | authentication@v0.26.0 |
| 5 | `WithPasskeys(PasskeyConfig{…, Views})`, `Repositories.Passkeys`, the JSON routes, `PublicRouteLimits.Passkey`, `/auth/methods` `passkey`; `admins` satisfiable by passkeys | opt-in; production refuses a `localhost`/IP RP or an HTTP namespace; S4a's "needs WithExternal" refusal now boots | leave unwired | authentication@v0.26.0 |
| 6 | **BREAKING value:** `MFARequiredNone` = `"none"`; zero resolves once (B-2) | passkeys + `UserAdminCheck` → `admins`; boot line `source=default` | `Required: MFARequiredNone` | authentication@v0.26.0 |
| 7 | `admins` gates the machine lifecycle with passkeys (B-3) | AAL1 key minting → 401 after `MachineRoutesGate` | `MFARequiredNone` (drops the admin gate too) | authentication@v0.26.0 |
| 8 | `MethodSet.Passkeys`; `RemovePasskey`/`RemovePasskeys`; erasure deletes passkeys (`EraseRepository`, `EraseCounts`) | a host `MutationRepository` must snapshot and apply them, or it undercounts login methods (conservative) and cannot remove passkeys | implement per `RunPasskeys` | authentication@v0.26.0 |
| 9 | removing a passkey revokes its user's sessions; a UV-passkey holder adds/removes only at AAL2 | sign-out everywhere, as a password removal does | none | authentication@v0.26.0 |
| 10 | six event types; `mfa_required_denied` gains `access_token`/`machine`; `passkey_change_notice` | new audit rows and notices | override the template (`AppTemplates`) | authentication@v0.26.0 |
| 11 | `PasskeyViews`; page-model `Passkeys`; `Browser()`+`RequireMFA()` redirect | additive; the `Views` interface is unchanged | leave `PasskeyConfig.Views` nil | authentication@v0.26.0 |
| 12 | **REQUIRED migration 0025** | BOOT FAILURE without `passkey_credentials`, naming 0025, every mode | apply 0025; ledger copy (P9) | stores pgx@v0.12.0, turso@v0.11.0 |
| 13 | passkey pages; `PasskeyScriptHandler` | mount the script, or the passkey controls stay hidden | do not mount | views/goth@v0.7.0 |
| 14 | auth-cms wires passkeys (admins by default; RP from its origin; login markup); baseline ledger 0032 | example behaviour | — | examples (untagged) |

Two compile-adjacent breaks: row 6 (a constant's value changes) and row 8 (host stores must cover the new variants). Row 12
is a boot failure. Every module ships MINOR under v0; tag messages name rows 3, 4, 6, 8 and 12.

## Risks
(1) The first passkey is an AAL1 bootstrap: a stolen password can enroll it and reach AAL2; the notice and the residual
answer it. (2) A host `MutationRepository` ignoring passkeys undercounts login methods (row 8). (3) 0025 refuses boot on
repinned stores in every mode (row 12). (4) AAL2 lasts the refresh lifetime (a step-up keeps `ExpiresAt`); `FreshAuth` stays
external-only. (5) Assertions ignore unsolicited extension outputs (password managers); the device check probes it. (6) CDP
authenticators are Chrome-only (iOS, Android, Windows Hello unverified); synced passkeys trust the platform account (B-4).

## Tasks as PRs
Worktrees `../gopernicus-s4b-<pr>`; one merge/tag/push per command; retarget stacked children to `main` first. Every PR runs
with model **opus**.

- **PR-1 — sdk: the WebAuthn port, `webauthntest`, the `OriginPolicy` accessors (`sdk/webauthn-port`).** **Depends on**
  nothing. **BREAKING:** none. **Tag:** `sdk/v0.20.0`. **Files:**
  `sdk/capabilities/authentication/webauthn/{webauthn,config,doc}.go` + tests;
  `sdk/capabilities/authentication/webauthn/webauthntest/{authenticator,cbor,suite}.go` + a self-test;
  `sdk/pkg/web/origin_policy.go` + test; `sdk/README.md`; `ARCHITECTURE.md:106-108,192`. **Verify:** `cd sdk && go build
  ./... && go test -race ./... && go vet ./...`; `make guard` (G12c, sdk-stdlib); `make check`.
- **PR-2 — the go-webauthn adapter module (`integrations/go-webauthn`).** **Depends on** PR-1 tagged. **BREAKING:** none
  (new module). **Tag:** `integrations/cryptids/go-webauthn/v0.1.0`. **Files:**
  `integrations/cryptids/go-webauthn/{go.mod,go.sum,gowebauthn.go,doc.go,README.md,conformance_test.go,gowebauthn_test.go}`;
  `go.work`; `Makefile` (MODULES); `ARCHITECTURE.md:12-16`. **Verify:** in the module, `GOWORK=off go build ./... && go test
  -race ./... && go vet ./...` (the conformance run) and `govulncheck ./...` (recorded); `make guard` (G13); `make check`.
- **PR-3a — authentication core: passkeys, assurance, events, notices, the JSON + HTML seams
  (`authentication/passkeys-core`).** **Depends on** PR-1 tagged; merges after PR-2's tag (auth-cms requires the adapter);
  the sdk pin moves to v0.20.0 by explicit `GOWORK=off go get`. **BREAKING:** rows 3, 4, 6, 7, 8, 9, each with its hatch.
  **Tag:** none (step 3 follows PR-3b). **Files** (under `pockets/authentication/`):
  `logic/authentication/{passkey/{passkey,repository}.go, passkey_ceremony.go, passkey_management.go, challenge.go,
  challenge/{challenge,repository}.go, authgrant/{authgrant,requirement}.go, credential/{credential,repository}.go,
  session/method.go, service.go, token.go, refresh.go, provisioning.go, securityevent/securityevent.go, user/erase.go,
  methods.go}`; `logic/delivery/{router.go, templates/passkey_change_notice.{html,txt}}`; `inbound/http/{passkeys.go,
  passkey_html.go (both new), principal_options.go, principal.go, routes.go, html.go, views.go, public_rate_limit.go,
  useradmin.go, machine.go, methods.go, errors.go}`; `{config,configuration,constructor,repositories,security}.go`;
  `README.md`; `stores/storetest/{passkeys.go,reference_test.go}`; tests `passkey_test.go`, `passkey_ceremony_test.go` (RP
  stub), `inbound/http/passkeys_test.go` (tiers, budgets, origin gate, wire goldens, no key material, redirect, the gate's
  403), `mfa_test.go` (the lift; zero-value resolution; an IdP host at zero trips `external_no_mfa`). **auth-cms** (go.work
  CI): `examples/auth-cms/go.{mod,sum}`; `examples/auth-cms/pockets/authentication/outbound/authmem/{passkeys.go
  (new),ports_v3.go,credential_writes.go,erasure.go}`; `examples/auth-cms/cmd/server/{main,authentication,demo}.go`
  (`WithPasskeys` via `gowebauthn.New`, RP ID + `Origins` from its origin/`AllowedOrigins` at `main.go:1070`, `Views` nil
  until PR-4; `GET /demo/mfa` with `RequireMFA()`, `GET /demo/mfa/page` with `Browser()` + `RequireMFA()`);
  `examples/auth-cms/cmd/server/{passkey_test.go,production_test.go}` (real adapter + `webauthntest`; a real domain
  constructs, `localhost` refused); user-admin tests step up via a helper. **Verify:** `cd pockets/authentication && go
  build ./... && go test -race ./... && go vet ./...`; `cd examples/auth-cms && go test ./...`; `make guard`; `make check`;
  Verification §2.
- **PR-3b — authentication stores: 0025, the passkey stores, Snapshot/Apply/erasure (`authentication/passkeys-stores`).**
  **Depends on** PR-3a (stacked). **BREAKING:** row 12 (hatch: apply 0025). **Files:**
  `pockets/authentication/stores/{pgx,turso}/{migrations/0025_passkey_credentials.sql, passkeys.go, credential_mutations.go,
  credential_writes.go, erasure.go, postgres.go|turso.go (the probe), README.md, conformance tests with RunPasskeys}`;
  `examples/production-baseline/workshop/migrations/primary/0032_passkey_credentials.sql` (G34). **Verify:** `make check`;
  `make guard` (G34); the branch live legs: pgx bare and `POSTGRES_TEST_SCHEMA=s4b` on a fresh PG17 (`lsof` the port; never
  55432), the S3 turso gate verbatim on a fresh libSQL, a Linux-container `make test-stores`, a `live-stores` dispatch.
  **Tag:** none; after 3a and 3b merge, re-run the legs on `main`, then cut step 3 at PR-3b's merge commit.
- **PR-3c — stores repin (`authentication/stores-repin-s4b`).** **Depends on** step 3 resolving. **BREAKING:** nothing
  beyond row 12. **Files:** both stores' `go.mod`/`go.sum` (core → v0.26.0 via `GOWORK=off go get`, then `GOWORK=off go mod
  tidy`). **Verify:** `make check`; `make test-stores`; a `live-stores` dispatch on `main`. **Tag:** step 4.
- **PR-4 — views/goth: pages, `passkey.js`, the auth-cms login markup (`authentication/passkeys-views`).** **Depends on**
  step 3 resolving; the views `go.mod` moves authentication v0.14.0 → v0.26.0 (forced). **BREAKING:** none. **Tag:** step 5.
  **Files:** `pockets/authentication/views/goth/{passkey,credential,stepup,account,layout}.templ` (`_templ.go` from `make
  generate` only), `pockets/authentication/views/goth/{assets/passkey.js, assets.go, views.go, passkey_test.go,
  views_test.go, go.mod, go.sum}`; `pockets/authentication/README.md` (the markup contract);
  `examples/auth-cms/pockets/authentication/inbound/authpages/{authpages.go,authpages_test.go}`;
  `examples/auth-cms/cmd/server/{main.go,goth_proof_test.go}` (mount the script; `PasskeyConfig.Views: authViews`).
  **Verify:** `make generate` leaves no `_templ.go` diff; `go test ./...` in the views module and `examples/auth-cms`; `make
  check`; **the browser leg (§3) on this branch**: run and look at the manage, login and step-up pages.
- **PR-5 — the record (`docs/s4b-release-record`).** **Depends on** steps 1–5 cold-verified AND the owner's device check
  (Verification §5) recorded; a failure there becomes a views/goth patch, tagged first. It runs the browser leg from the
  tags. **BREAKING:** none. **Tag:** none. **Files:** `RELEASING.md` (ONE section, "Passkeys (SOC 2 slice S4b)": the tags,
  the Upgrade notes expanded by host shape, the migration note, the device-check result and unverified platforms, the
  verification record); every `examples/*/go.mod` pinning a moved module (explicit `GOWORK=off go get`); `SECURITY.md`;
  `control-map.md:17,44,99`; the roadmap S4 line; the docs site; `plans/s4b-passkeys.md` (executed copy). **Verify:** `make
  check`; `make guard`; `GOWORK=off go build ./... && go test ./...` per repinned example; `pnpm build` (docs); §4.

## Sequencing
```
PR-1 ─[tag 1]─┬─ PR-2 ─[tag 2]──────────────┐
              └─ PR-3a ── PR-3b (stacked) ──┴─ merge 3a, 3b ─[live legs on main]─[tag 3]─┬─ PR-3c ─[dispatch]─[tag 4]
                                                                                         └─ PR-4 ─[browser leg]─[tag 5]
                                     tags 1–5 cold-verified + the owner's device check ─ PR-5 (record)
```
Five trains is the floor (the adapter and pocket need the sdk tag; stores and views need the pocket tag). PR-3c and PR-4 run
in parallel worktrees; the device check may run on the PR-4 branch or from the tags.

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 1 | `sdk/v0.20.0` | PR-1 merge | builds `sdk/...`; the `webauthntest` self-test passes from the cache |
| 2 | `integrations/cryptids/go-webauthn/v0.1.0` (NEW module, FIRST tag) | PR-2 merge | adapter + sdk v0.20.0 + go-webauthn v0.18.2: `go mod verify`, build, `go test` from the cache |
| 3 | `pockets/authentication/v0.26.0` | PR-3a + 3b merged; live legs on `main` | **set A:** core v0.26.0 beside the RELEASED stores pgx v0.11.0 / turso v0.10.0 AND views v0.6.0 builds (passkeys unwired; no lockstep) |
| 4 | `…/stores/pgx/v0.12.0`, `…/stores/turso/v0.11.0` | PR-3c + dispatch | **set B:** core v0.26.0 + both stores + pgxdb v0.14.0 + turso v0.9.0 select sdk v0.20.0 and build |
| 5 | `…/views/goth/v0.7.0` | PR-4 + browser leg | **set C:** views v0.7.0 + core v0.26.0 + ui/goth v0.2.0 + adapter v0.1.0 + set B build an auth-cms-shaped `main` |

**Tagging.** Six annotated, unsigned tags, each cut at its squash commit, pushed, and only THEN polled on `.info`;
cold-verify with `GOWORK=off`, an empty `GOMODCACHE`, `proxy.golang.org` + `sum.golang.org`, `go mod verify`, `go build
<module>/...`. Never tidy before a tag resolves; never `go work sync`. **Not tagged:** ui/goth, `pockets`, workshop, the
examples, every other module. Live legs before steps 3 and 4 are counted with `go test -json` against the S4a baselines
(432/0/1 per pgx mode, 737/0/0 turso); stop the scratch containers afterwards.

## Verification
Real binaries in DEVELOPMENT. `$B` = `http://localhost:8082` (auth-cms, authmem, console mailer, RP ID `localhost` from its
origin), run with `AUTH_DEBUG=1`; events are read from `GET /debug/security-events` as a platform admin.

- **§1 — PR-2.** The suite over `webauthntest` bytes; a scratch program prints the `Validate` refusals (RPID `example.com`
  against `https://*.other.test`; zero `Origins`; a 6m Timeout).
- **§2 — PR-3a, the real binary over HTTP** (`go run ./cmd/server`; a scratch Go client on `webauthntest.Authenticator`).
  (1) Boot line `mfa_required=admins source=default`; register, verify (code from STDERR), password login; `GET /demo/mfa` →
  **401** `insufficient_user_authentication`. (2) Enroll within 5 min → **201**; the console mailer prints a
  `passkey_change_notice`. (3) Usernameless sign-in → `/demo/mfa` **200** `aal2`. (4) A fresh password login of this holder
  → registration options and a removal each **403**; after a passkey step-up both pass. (5) Step-up after a password login →
  a new cookie, `assurance: aal2`, the old refresh token refused, `ExpiresAt` unchanged. (6) A second UV passkey steps up →
  **200**; another user's passkey → **401** `reason=user_mismatch`. (7) Register WITH UV, then `SetUserVerified(false)`:
  sign-in **200**, `/demo/mfa` **401**, `passkey_login assurance=aal1`. (8) `SetSignCount(0)` after use → **401**,
  `passkey_sign_count_regressed`. (9) `DELETE /auth/passkeys/{id}` at AAL2 → every session revoked, a notice; the credential
  then fails `unknown_credential`. (10) `/auth/admin/users` as an AAL1 platform admin → **401** + challenge (B-2); **200**
  after a passkey step-up. (11) An AAL2 admin resets user X → **200**, a `passkeys_reset` row, X's sessions revoked, a
  notice; X re-enrolls at AAL1 after a password login → **201**; the same reset with an admin API key → **200**. (12) No log
  line, row or notice holds a key, credential id, handle or name.
- **§3 — the browser leg (PR-4 branch; from the tags in §4).** A scratch CommonJS script (never committed) requires
  `@playwright/test` 1.60.0 and `@axe-core/playwright` 4.12.1 by absolute path from the MAIN checkout's
  `examples/goth-showcase/e2e/node_modules` (absent from worktrees), spawns the auth-cms binary
  (`AUTH_RUNTIME_MODE=development AUTH_DEBUG=1`), reads codes from its stderr, drives Chromium at `$B`, and listens on
  `console` and `securitypolicyviolation`. CDP: `WebAuthn.enable`;
  `WebAuthn.addVirtualAuthenticator({options:{protocol:'ctap2', ctap2Version:'ctap2_1', transport:'internal',
  hasResidentKey:true, hasUserVerification:true, isUserVerified:true, automaticPresenceSimulation:true}})`. **Steps →
  expected:** (1) Register, verify, password sign-in; `/demo/mfa/page` BEFORE enrolling → the step-up page offers "Add a
  passkey" to `/auth/account/passkeys?return_to=…`. (2) Add a passkey: listed, one `isResidentCredential:true` in
  `WebAuthn.getCredentials`, `passkey_registered`; back on the step-up page, "Use a passkey" → **200** at `/demo/mfa/page`.
  (3) Sign out; "Sign in with a passkey" on the auth-cms `/auth/login`, typing nothing; `fetch('/demo/mfa')` → **200**
  `aal2`. (4) `setAutomaticPresenceSimulation(false)` on the first; add and enroll a presence-only one
  (`hasUserVerification:false, transport:'usb'`); save the first's credential; sign out; remove the first authenticator. A
  passkey sign-in → `/demo/mfa` **401**; on the step-up page, "Use a passkey" answers `aal1` and the script shows the
  presence-only copy and stays put (no loop). (5) A fresh UV authenticator + `WebAuthn.addCredential` of the saved
  credential at `signCount` = stored − 1 → the generic refusal, `passkey_sign_count_regressed`. (6) Password sign-in; remove
  the presence-only key on the manage page ("this signs you out on every device"); its step-up is met by the saved
  credential re-added at stored + 1; the browser lands on sign-in, every session revoked; the removed key is refused
  (`unknown_credential`). (7) `page.addInitScript` deleting `parseCreationOptionsFromJSON`, `parseRequestOptionsFromJSON`
  and `PublicKeyCredential.prototype.toJSON`: a new user enrolls and signs in (the base64url fallback). (8) Without
  `window.PublicKeyCredential`: login shows the password form, the step-up page its text explanation. (9) CSP `script-src
  'self'`, no `unsafe-inline`, zero violations; console errors only on the allowlisted 401/403 URLs of steps 4–6;
  `passkey.js` as `application/javascript` + `nosniff`; Permissions-Policy admits `publickey-credentials-*`; axe on login,
  manage and step-up: zero serious/critical. Screenshots go into the record.
- **§4 — from the tags (PR-5).** §3 against a `GOWORK=off` auth-cms binary (`go version -m`: sdk v0.20.0, authentication
  v0.26.0, views/goth v0.7.0, go-webauthn v0.1.0). production-baseline boots from the tags on a fresh PG17 with ledger 0032
  and, with `passkey_credentials` dropped, refuses to boot naming 0025.
- **§5 — the owner-run real-device check (PRECONDITION of PR-5).** On the Mac at `$B`, Safari with Touch ID: enroll on
  `/auth/account/passkeys`, sign out, sign in usernameless on `/auth/login`, open `/demo/mfa/page` and step up. Expected: no
  console error, and `/demo/mfa` answers `aal2`. Pass/fail goes into the RELEASING section; a failure becomes a views/goth
  patch first. iOS, Android and Windows Hello are named there as unverified (no HTTPS RP ID yet).

## Open rulings — YOUR CALL
- **B-1 — recovery codes and native `Required: all`: S4b or S4c?** (a) S4c with TOTP; native `all`, `MFAConfig.Methods` and
  the grace ship with it. (b) S4b, as a PR-3d: recovery codes, a per-user grace marker, native `all`. **Recommend (a)**, on
  the conditions now folded: a UV-passkey holder adds or removes only at AAL2 (§3.2), and the admin reset is proven with a
  documented operator path (Verification §2(11), §7). Under none or admins a lost passkey costs nobody their AAL1 login; if
  the owner will not carry an admin reset per non-admin who loses their only passkey, that is the case for (b).
- **B-2 — the bundled admin MFA default (S4a R-4).** (a) Keep none; WARN when passkeys and a `UserAdminCheck` are wired. (b)
  Zero resolves to `admins` when both are wired; `MFARequiredNone` (now `"none"`) opts out (row 6). (c) Flip
  unconditionally. **Recommend (b)**, with §3.4's single resolution, §5's no-passkey page and the README break-glass. No
  released host wires passkeys, so nothing flips silently; (c) would 401 IdP hosts asserting no MFA.
- **B-3 — the machine lifecycle under `admins` (S4a R-5).** (a) In when passkeys are wired, after `MachineRoutesGate`. (b)
  Out until S4d. **Recommend (a).** These routes admit access tokens only, which can now satisfy it; minting a key is
  administrator-grade, and (a) stops an AAL1 session minting a key that then passes `admins`. Note: row 7's hatch
  (`MFARequiredNone`) also drops the admin gate; there is no machine-only opt-out.
- **B-4 — attestation.** (a) `none` only; the AAGUID is recorded when the client reports one, untrusted. (b) Opt-in `direct`
  with an AAGUID allow-list over the FIDO metadata service. **Recommend (a):** AAL2 needs no attestation; that trust chain
  is its own milestone.
- **B-5 — discoverable credentials, usernameless only.** (a) `residentKey: required`; usernameless sign-in only,
  username-first a follow-up. (b) `preferred`, which needs username-first with `allowCredentials`, leaking credential ids
  per address. **Recommend (a).** No credential id leaves the server before authentication, and the browser's account
  chooser covers account choice. Cost: U2F-only keys cannot enroll; `passkey.js` and the README say so.
- **B-6 — where the script lives.** (a) `views/goth`, beside `fragment.js`, plus `PasskeyScript()` bytes for custom-view
  hosts. (b) `ui/goth`: a tag, and pocket behaviour in a module that owns no domain or routes. (c) The pocket core:
  presentation in a templ-free core. **Recommend (a):** precedent, profile-independent, no ui/goth tag; a core wire change
  forces a views retag, and the core's wire goldens keep that visible.

## Open questions
- The first-passkey bootstrap: should a password/code step-up grant count beside a fresh login? Rev 2 says no (the lead's
  "fresh primary login"); yes would spare a long session a re-sign-in at the same factor and 5-min bound.

## Recommended reviews
**product-manager:** the rulings and the device gate, before ratification. **platform-sre:** 0025's boot failure, a new
module's first tag, the live legs. **architecture-steward:** the subpackage sentence and `RelyingParty.Config()`.

## Notes
- **S4c handoff.** TOTP (`authentication/totp`, `integrations/cryptids/pquerna-otp`, `MFAMethodTOTP`), recovery codes, the
  key ring. **Hard rule:** `MFAConfig.Methods` (`passkey`, `idp`, `totp`) lands with the methods claim in the SAME PR as
  TOTP, or `MFAMethodPasskey` would admit a TOTP AAL2 token. **Native `all` with the enrollment grace:** deadline
  `max(EnrollmentStart, user.CreatedAt) + EnrollmentGrace` (`EnrollmentStart` a required literal date; rev 1 recommended a
  7-day default and a 30-day maximum, ruled there); before it, a no-passkey user signs in at AAL1 and MFA gates still deny;
  after it, a 403 `mfa_enrollment_required` + `mfa_required_denied` (gate `enrollment`); enrolled users must use the factor,
  UV forced to `required`; refresh re-checks; an admin reset re-opens grace via a per-user marker. **S4d handoff.** API keys
  under MFA gates; `aal` on delegated tokens.
- **Follow-ups.** Username-first sign-in (a server-side hint, identical responses for unknown addresses); a committed
  Playwright spec; autofill and Related Origin Requests; an AAGUID → model-name table; iOS, Android and Windows Hello.
  **Cut-line: considered and rejected.** Holding PR-4 for a second record saves no train (it runs beside PR-3c off tag 3),
  breaks the one-section RELEASING ruling, and would ship the B-2 default with auth-cms wired but no page to enroll on and
  no browser proof of the Goal. **Cut for the cap:** per-test case lists beyond the suite; the column list; a `passkey.js`
  sketch; the handoff prose (PR-5 writes it); a `principal_options.go` diff.
