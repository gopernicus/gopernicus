# S4c — TOTP fallback and recovery codes: the native second factor beside passkeys (SOC 2 readiness, slice 4c)

**Status:** EXECUTED 2026-10-26 (tags: rsc-qr v0.1.0 #200; authentication v0.28.0 #201+#202 → v0.29.0 #205 (0025 conditional); stores pgx v0.13.0 / turso v0.12.0 #203 → pgx v0.14.0 / turso v0.13.0 #207; views/goth v0.8.0 #204 → v0.8.1 + ui/goth v0.2.1 #206 (follow-ups); owner device check: Google Authenticator PASS (scan, enroll, aal2), Apple Passwords inconclusive (attaches codes to a saved login) — deferred by owner). RATIFIED 2026-10-25 (owner: "ratified"; R-1–R-5 at the recommendations: (a) split — native `all` + grace to
`s4c2-native-all.md`; (a) rsc-qr adapter; (a) TOTP/recovery step-up is AAL2 on any live session; (a) stdlib RFC 6238; (a)
disabling TOTP revokes every session). Owner note: every factor is host opt-in (an OAuth-only host like gps-360-go wires
none). **Owner ruling 2026-10-25 (amends BE12 / row 10):** 0026 still ships (hosts may apply the empty tables ahead of
time), but the schema is REQUIRED ONLY WHEN TOTP IS WIRED — not wired + not migrated boots; wired + not migrated refuses,
naming 0026. The pocket probes the TOTP repository's schema through an optional interface only under `WithTOTP`; erasure and
the admin reset skip TOTP when it is not wired. Tag 1 `integrations/encoding/rsc-qr/v0.1.0` cut 2026-10-25 (#200). **Owner ruling 2026-10-25 (second):** the same conditional rule applies to S4b's 0025 (`passkey_credentials`): required only when `WithPasskeys` is wired, probed through an optional `passkey.SchemaProber`; erasure skips passkeys when unwired. Ships as PR-2d (core + stores, one PR) → authentication v0.29.0, stores pgx v0.14.0 / turso v0.13.0 (repin PR-2e), BEFORE the PR-4 record so examples repin once. Rev 1 after the
two-reviewer gate: lead-backend-engineer BE1–BE13, lead-frontend-engineer FE1–FE9, folded below.

- **Size:** M–L — six PRs, four trains, five tags (one for a NEW module, none for sdk); an owner-run authenticator-app check
  gates the record PR. Native `all` + the enrollment grace are proposed for a follow-on plan (R-1).
- **Anchors.** `main` @ 0fe93f39 (S1–S3, S5, S6, S4a, S4b RELEASED): sdk v0.20.0, authentication v0.27.1, stores pgx v0.12.0 /
  turso v0.11.0, views/goth v0.7.0, go-webauthn v0.1.0, ui/goth v0.2.0, pgxdb v0.14.0, turso v0.9.0, workshop v0.9.0; baseline
  ledger at 0032. Sources: `plans/s4b-passkeys.md` (Notes "S4c handoff", B-1), `s4b-device-check-fixes.md`, `RELEASING.md:3-268`,
  `roadmap.md:181,307,315`, `control-map.md:17`. Libraries checked on the proxy 2026-10-25: pquerna/otp v1.5.0 (2024-12-31,
  `go 1.12`, requires boombuler/barcode + testify); rsc.io/qr v0.2.0 (2018, BSD-3, zero deps, 2.3k lines; QR is a frozen spec).
- **Owner rulings (binding).** Passkeys are the primary native factor, TOTP the fallback; recovery codes ship regardless (B-1);
  libraries behind sdk ports in adapters named for the package; the JSON contract is first-class for React SPAs (embedded or
  decoupled); views/goth is the reference UI; a real-device check is a precondition of the record; `passkey.js` has no headroom.
- **What `main` already does.** AAL2 = a UV passkey or IdP evidence (`session/method.go:72-84`); the access JWT carries `aal`
  (`token.go:18-26`); `RequireMFA()` is an `AtLeast(AAL2)` check (`principal_options.go:215`); passkey step-up re-mints the
  session (`passkey_ceremony.go:400-451`); the enrollment gate is `passkeyEnrollmentPolicy` (`:619-626`); `MFAConfig{Required}`
  resolves once (`configuration.go:74-123`); five keys in `keys.go`; `cryptids.KeyedEncrypter`/`AESGCMRing` exist
  (`sdk/pkg/cryptids/keyring.go:39,107`); the challenge protector digests codes under a rotating pepper ring
  (`protection/hmac.go:31-40,163-190`); the stores probe 0025 in every mode (`stores/pgx/postgres.go:85-89`).

## Goal
A native user enrolls an authenticator app by scanning a QR code (or typing the key), receives ten single-use recovery codes
once, and steps up with a code to AAL2 end to end — the session row, the signed `aal` and `methods` claims, `RequireMFA()` and
the `admins` gates — while `MFAConfig.Methods` lets a host refuse TOTP where only passkeys or IdP MFA will do. Every change is
audited, noticed, backed by pgx, turso and memory stores, and proven in Chromium, WebKit and once with a real authenticator app.

## Scope and non-goals
**In scope:** the rsc.io/qr adapter (PR-1); TOTP + recovery codes domain, the second-factor aggregate port, the AAL2-factor
predicate, `MFAConfig.Methods` + the `methods` claim, events, notices, keys, JSON + HTML seams (PR-2a); stores and 0026 (PR-2b;
repin PR-2c); pages and the auth-cms wiring (PR-3); the record (PR-4). **Non-goals (R-1):** native `Required: all`, the
enrollment grace, the per-user grace marker and refresh re-check (follow-on `s4c2-native-all.md`); TOTP as a sign-in method
(it is a second factor only); SHA-256/8-digit TOTP; push/SMS second factors; re-sealing TOTP secrets on ring rotation (a
maintenance follow-up; `KeyIDOf` makes it possible); API keys under MFA gates and `aal` on delegated tokens (S4d); raising the
AAL1 `set_password`/identifier-change policies (pre-existing, BE2); a committed Playwright spec; an sdk change of any kind.

## Decided by gate
Reviews 2026-10-25: lead-backend-engineer (BE), lead-frontend-engineer (FE), each with one question. Folded unless marked.
- BE1 + BE5 (HIGH, removal skips the revision fence; two ports break atomicity) → ONE aggregate port `totp.Repository`; `Confirm`,
  `Remove` and the admin reset run in one transaction under the users row lock and bump `auth_revision`; recovery codes exist
  only beside a confirmed TOTP; `Mutation` unchanged (§2). BE question → the aggregate owns the fenced delete; no host
  `MutationRepository` break.
- BE2 (HIGH, the passkey gate ignores TOTP) → one predicate `holdsAAL2Factor` on BOTH gates (§3); the AAL1 password/identifier
  policies named out of scope.
- BE3 (HIGH, lockout lost update) → `RecordFailure` is one atomic increment `RETURNING`; the lock reads the row it writes; a
  concurrent `RunTOTP` case (§2, §5).
- BE4 (HIGH, pepper retirement kills recovery codes) → keyed under the pepper ring with `key_id` stored; `RecoveryCodeKeyIDs` + a
  boot WARN naming orphaned key ids; regeneration is the documented rotation step; `recovery_code` reserved as a purpose (§2, §7).
- BE6 (assurance allowlist; never lower) → R-3. **Amended by lead after the PR-2a code review (M2):** a step-up that proves less than the session holds LOWERS assurance (refreshing AuthenticatedAt on a weaker proof must not extend AAL2); `mergeMethods` drops carried AAL2 methods below AAL2.
- BE7 (ciphertext not bound to its user) → the sealed envelope is `{u: userID, s: secret}` and decrypt checks `u` (§2).
- BE8 (sdk placement) → `totp` is an algorithm with one consumer: pocket `logic/authentication/totp`; NO sdk port, NO sdk tag;
  QR is view work: `authgoth.WithQR(func(string) ([][]bool, error))`, the adapter module stays (R-2). `qr_svg` dropped from JSON.
- BE9 (re-mint implications) → stated in §3: one code is a recent login for every gate for 5 minutes; `recovery_code_used`.
- BE10 → `Confirm` stores `LastUsedStep` = the confirmed step. BE11 → lock order pinned (§5). BE12 → 0026 required in every mode;
  SECURITY.md names the `RunTOTP` obligation. BE13 → the kinds list, named `methods`, refused on delegated tokens (§4).
- FE1 (HIGH, `aal1` answers lock users out) + BE6 → R-3 (recommend (a): no dead end, one predicate). FE question → under (a) a
  TOTP holder reaches AAL2 with TOTP whatever the primary; under (b) the predicate is conditional and `/auth/methods` says so.
- FE2 (HIGH, the redirect picker cannot live in the middleware) → one static target computed at `New`: the chooser page
  `GET /auth/step-up/mfa` when `TOTPConfig.Views` is wired, else S4b's passkey page; the chooser handler reads the inventory (§6).
- FE3 (HIGH, POST-rendered secret; typo restarts; regenerate re-POST) → the pending enrollment is a ROW (one per user), so
  `GET /auth/account/totp/enroll` re-reads it (PRG) and a wrong code re-renders the same secret; regenerate takes an
  `expected_generated_at` precondition → 409 `recovery_codes_stale` (§2, §6). Back/forward cache noted (§6).
- FE4 (QR) → modules rendered by the goth view as one `<path>`, presentation attributes only, dark-on-white, 4-module quiet zone,
  minimum size; no `templ.Raw`, no `data:` img (CSP `default-src 'none'`, `security.go:494`).
- FE5 (fields) → recovery-code and confirm fields get their own attrs (`autocomplete="off"`, `autocapitalize="none"`, no
  `inputmode=numeric`); server normalizes case and hyphens; `otpauth://` via `templ.SafeURL`; label percent-encoded.
- FE6 (form-arm errors) → `accountFailure` cases for the lock and refusals; the JSON 429 carries `Retry-After` (§6).
- FE7 (SPA contract) → `/auth/methods` gains the session's `assurance`, `totp` and `recovery_codes`; `DELETE /auth/totp` ends
  bearer tokens (documented). FE8 → the TOTP step-up form carries `return_to`, rotates cookies, PRGs. FE9 → Verification §2/§4.

## Design
### 1. The QR adapter: `integrations/encoding/rsc-qr` (NEW module, v0.1.0; R-2)
Package `rscqr`. `func Modules(text string) ([][]bool, error)` encodes at level M via rsc.io/qr v0.2.0 (`Encode`, `Code.Size`,
`Code.Black`); an empty or over-long text is `sdk.ErrInvalidInput`. It imports rsc.io/qr and root `sdk` only (G13), joins
`Makefile` MODULES, `go.work`, `ARCHITECTURE.md:12-16` ("a connector (rsc.io/qr; QR modules for the views/goth TOTP page)").
Its signature matches `authgoth.QRModules` by shape, so neither module imports the other. `govulncheck` recorded.

### 2. The domain: `logic/authentication/totp` (authentication v0.28.0)
**The algorithm** (`totp.go`, stdlib): `NewSecret() []byte` (20 random bytes), `Code(secret []byte, step int64) string`
(RFC 4226 HMAC-SHA1, 6 digits), `Validate(secret []byte, code string, at time.Time, window int) (step int64, ok bool)`
(period 30 s, constant-time compare over steps `now-window…now+window`), `KeyURI(issuer, account string, secret []byte) string`
(`otpauth://totp/<issuer>:<account>?secret=<base32, no padding>&issuer=…&algorithm=SHA1&digits=6&period=30`, label
percent-encoded, `:` refused in issuer), `FormatSecret` (4-char groups). Pinned to RFC 6238 Appendix B and RFC 4226 Appendix D.
```go
type Credential struct {
	UserID string; Secret string // the sealed envelope: cryptids ring ciphertext of json {"u": userID, "s": base32 secret}
	SessionID, Tier string; AuthRevision int64; ExpiresAt *time.Time // pending only; cleared by Confirm
	ConfirmedAt *time.Time // nil = pending
	LastUsedStep int64; FailedAttempts int; LockedUntil *time.Time; CreatedAt time.Time; LastUsedAt *time.Time
}
type RecoveryCode struct{ ID, UserID, KeyID, Digest string; GeneratedAt time.Time }
type Repository interface { // the second-factor aggregate: one TOTP credential and its recovery codes per user
	Begin(ctx context.Context, c Credential) error                       // upserts the PENDING row; a confirmed row → ErrAlreadyEnrolled
	Get(ctx context.Context, userID string) (Credential, error)            // pending or confirmed; sdk.ErrNotFound
	Confirm(ctx context.Context, in Confirmation) error                    // users row lock; re-checks tier + revision; sets ConfirmedAt,
	                                                                       // LastUsedStep; inserts the codes; bumps auth_revision
	RecordUse(ctx context.Context, userID string, expectedStep, step int64, at time.Time) error // CAS step > expected; miss → sdk.ErrConflict
	RecordFailure(ctx context.Context, userID string, lockAt int, lockFor time.Duration, now time.Time) (Failure, error) // atomic +1
	ReplaceRecoveryCodes(ctx context.Context, userID string, codes []RecoveryCode, expectedGeneratedAt *time.Time) error // CAS
	ConsumeRecoveryCode(ctx context.Context, userID string, candidates []protection.DigestCandidate, now time.Time) (remaining int, err error)
	CountRecoveryCodes(ctx context.Context, userID string) (int, *time.Time, error)
	Remove(ctx context.Context, userID string, expectedRevision int64) (Removed, error) // users row lock; both tables; bumps auth_revision
	RecoveryCodeKeyIDs(ctx context.Context) ([]string, error)              // distinct key_id, for the boot WARN (BE4)
}
```
- **Secrets at rest (BE7).** `TOTPConfig.Encrypter cryptids.KeyedEncrypter` seals `{u, s}`; decrypt refuses an envelope whose
  `u` is not the row's user (`ErrTOTPRejected`, reason `envelope`). The pending row holds the SAME sealed envelope — plaintext
  never rests. **Keys:** `KeyTOTPRing KeyName = "AUTH_TOTP_KEYS"`; `KeysInput.TOTPKeys` (`id:hex,…`) and `TOTPActiveKey`
  (`AUTH_TOTP_ACTIVE_KEY`); `Keys.TOTPRing cryptids.KeyRing` (parsed by `cryptids.ParseKeyRing`); development → an ephemeral
  one-key ring (`ephemeral`) + the existing WARN; production → `ErrKeyRequired` when needed. The host builds
  `cryptids.NewAESGCMRing(keys.TOTPRing)`.
- **Replay and brute force.** Window ±1 (three live codes). `RecordUse` accepts only `step > LastUsedStep` (no reuse, no
  older-window replay; a miss → `reason=reused` or `conflict`). Lockout (RFC 6238 §5.2): the 5th consecutive failure sets
  `locked_until = now + 5m` inside the same `RETURNING` statement (BE3); success resets the counter; a locked row refuses with
  `ErrTooManyAttempts` (`reason=locked`). Budgets: `AbuseBudgets.SecondFactor RequestBudget` (zero → 10/min per user, 30/min per
  IP) through `allowAuthenticationRequest("second_factor", …)`, checked BEFORE any decrypt.
- **Recovery codes (BE4).** Ten codes `xxxxx-xxxxx`, Crockford base32 lowercase (50 bits), generated by `Confirm` and
  `ReplaceRecoveryCodes`; stored as `protector.DigestCode(activeKeyID, userID, "recovery_code", code)` with `key_id`; consumed
  through `CandidateCodeDigests` (every ring key verifies); single-use (the row is deleted; `remaining` returned). Input
  normalization: lowercase, hyphens and spaces stripped. `New` runs `RecoveryCodeKeyIDs` once and WARNs
  `auth: recovery codes sealed under retired pepper keys key_ids=[…]` — those users regenerate at their next AAL2. The README's
  rotation step says: regenerate before dropping a key. `challenge.PurposeRecoveryCode = "recovery_code"` is reserved, unused.
- **Erasure.** `EraseRepository` deletes both tables; `EraseCounts.TOTP`, `EraseCounts.RecoveryCodes` (`user/erase.go:80-111`).

### 3. Flows, gates and assurance (`totp_enrollment.go`, `totp_stepup.go`, `totp_management.go`)
- **One predicate (BE2).** `holdsAAL2Factor(ctx, userID)` = any AAL2-capable passkey (`MethodSet.Passkeys`) OR a CONFIRMED
  TOTP. `passkeyEnrollmentPolicy` (`passkey_ceremony.go:619`) and the new `secondFactorPolicy` both return
  `RecentAuthPolicy{MinAssurance: AAL2}` when it holds and the AAL1 bootstrap otherwise. Row 5: a TOTP holder adds or removes a
  passkey only at AAL2, and the reverse.
- **Enroll.** `BeginTOTPEnrollment(ctx, sessionID, userID) (Enrollment, error)`: `RequireRecentAuthentication` under
  `secondFactorPolicy` (purpose `register_totp`; the grant consumed at Begin, the row carries `tier` + `auth_revision` as the
  passkey ceremony row does); a confirmed row → `ErrTOTPExists`; otherwise `Begin` upserts the pending row (`ExpiresAt` now+10m,
  a re-Begin replaces it) and returns `{Secret (plaintext, once), KeyURI, Issuer, Account (the login identifier, masked in
  logs), ExpiresAt}`. `ConfirmTOTPEnrollment(ctx, ConfirmInput{SessionID, UserID, Code})`: budget → `Get` (pending, unexpired,
  same `session_id`, live session) → decrypt → `Validate` → generate codes → `Confirm` (lock; tier re-check: bootstrap refused
  once an AAL2 factor exists, moved revision → `ErrStepUpRequired`; `LastUsedStep` = step, BE10) → `totp_enrolled`,
  `recovery_codes_generated`, a notice → returns the codes ONCE. A wrong code records `totp_failed reason=invalid` and leaves
  the pending row (FE3). TOTP never joins `MethodSet.LoginMethods` — it is not a sign-in method.
- **Step up.** `StepUpWithTOTP(ctx, StepUpCodeInput{SessionID, UserID, Code})` and `StepUpWithRecoveryCode` → `StepUpResult`
  (the passkey shape): budget → live session + active owner (revision R) → confirmed row (else `reason=not_enrolled`; a
  recovery code with no confirmed TOTP is refused, BE5) → verify (`RecordUse` CAS / `ConsumeRecoveryCode`) → re-mint fenced on
  R with `AuthenticatedAt` now and the OLD `ExpiresAt`, delete the replaced session and its grants, `Methods` = replaced +
  `totp`|`recovery_code` → `totp_step_up` (+ `recovery_code_used` {remaining}; a notice on every recovery-code use). Failures
  answer the generic `ErrTOTPRejected`/`ErrRecoveryCodeRejected` (401) with the class in the audit row only. **Implication
  (BE9):** the re-minted session satisfies `RequireRecentAuthentication`'s shortcut for 5 minutes — one code is a recent AAL2
  login for every gate, `DELETE /auth/totp` and regeneration included (NIST reauthentication; stated in the README).
- **Assurance (R-3; recommend (a)).** New kinds `MethodTOTP = "totp"`, `MethodRecoveryCode = "recovery_code"` (descriptors AAL1,
  not phishing-resistant, replay-resistant). The re-mint composes AAL2 per R-3; a weaker step-up lowers assurance (BE6 as amended).
- **Manage.** `TOTPStatus`, `RemoveTOTP` (a `remove_totp` grant under `secondFactorPolicy`; `Remove` bumps R; every session
  revoked; `totp_removed` + notice), `RegenerateRecoveryCodes(ctx, in{…, ExpectedGeneratedAt})` (the policy; CAS →
  `ErrRecoveryCodesStale`; `recovery_codes_generated trigger=regenerate` + notice), `ResetUserTOTP(ctx, actor, userID)` (via
  `adminPrincipal`; `Remove` without the credential policy — break-glass; `totp_reset` + notice).

### 4. `MFAConfig.Methods` and the `methods` claim (the S4b hard rule; same PR as TOTP)
```go
type MFAMethod string
const ( MFAMethodPasskey MFAMethod = "passkey"; MFAMethodIdP MFAMethod = "idp"; MFAMethodTOTP MFAMethod = "totp" )
type MFAConfig struct { Required MFARequirement; Methods []MFAMethod } // empty Methods = any AAL2 source; recovery_code counts under totp
```
Every mint site signs `methods` (the session row's method kinds) beside `aal`; `parseAccessClaims` fills `Credential.Methods`
(an unknown kind → rejected; the delegated branch refuses `methods` as it refuses `aal`, `token.go:138`). `ExternalAuthenticator`
stamps `Methods = [external]`→ `idp`. One helper `mfaSourceAllowed(cred, allowed)`: the AAL2-contributing kinds of the credential
(`passkey` → passkey; `totp`/`recovery_code` → totp; an external AAL2 token → idp) must intersect `Methods`; a first-party AAL2
token WITHOUT a `methods` claim (pre-upgrade) fails closed under a non-empty `Methods` (401 + challenge; refresh fixes, row 3).
Enforced in `RequireMFA()`, `adminPrincipal` (`useradmin.go:144-151`), the machine gate and the `Browser()` redirect;
`mfa_required_denied` Details gain `reason` (`assurance`|`method`) and `method`. Validation: an unknown value →
`sdk.ErrInvalidInput`; `Methods` naming a source that is not wired (`totp` without `WithTOTP`, `passkey` without `WithPasskeys`,
`idp` without `WithExternal`) → `ErrMFAUnsatisfiable`. `resolveMFARequired`: zero → `admins` when (`WithPasskeys` OR `WithTOTP`)
and `UserAdminCheck` are wired (B-2 widened, row 4); `validateMFA`: `admins` is satisfiable by TOTP; native `all` stays
`ErrMFAUnsatisfiable`, its message now naming `s4c2-native-all`. The boot line adds `sources=passkey,totp methods=any|…`.

### 5. Stores (pgx v0.13.0, turso v0.12.0; the S3/S5 migration rules)
pgx `0026_totp.sql`: `SET LOCAL lock_timeout = '5s'`; `CREATE TABLE IF NOT EXISTS totp_credentials` (`user_id TEXT COLLATE "C"
PRIMARY KEY`, `secret TEXT NOT NULL`, `session_id TEXT NOT NULL DEFAULT ''`, `tier TEXT NOT NULL DEFAULT ''`, `auth_revision
BIGINT NOT NULL DEFAULT 0`, `expires_at TIMESTAMPTZ`, `confirmed_at TIMESTAMPTZ`, `last_used_step BIGINT NOT NULL DEFAULT 0`,
`failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0)`, `locked_until TIMESTAMPTZ`, `created_at TIMESTAMPTZ
NOT NULL`, `last_used_at TIMESTAMPTZ`) and `recovery_codes` (`id TEXT COLLATE "C" PRIMARY KEY DEFAULT gen_random_uuid()::text`,
`user_id`, `key_id`, `code_digest`, `generated_at`, all NOT NULL; `UNIQUE (user_id, code_digest)`; indexes `(user_id)`,
`(key_id)`), all `IF NOT EXISTS`, no foreign keys; turso `0026` is the twin (TEXT timestamps, `lower(hex(randomblob(16)))`).
**Required (BE12):** the probe refuses both tables' absence in every mode, naming 0026 — erasure deletes from them
unconditionally. **Lock order (BE11):** `Confirm`, `Remove` and erase take the users row FIRST (`lockCredentialUser`), then the
totp rows, as passkeys do; `RecordUse` and `RecordFailure` are single statements on `totp_credentials` and never read `users`.
**`storetest.RunTOTP`** (separate from `Run`, like `RunPasskeys`): Begin upsert and the confirmed refusal; Confirm's tier and
revision guards, `LastUsedStep`, the R bump and the codes inserted atomically (a failing insert leaves no confirmed row);
RecordUse CAS (equal and lower steps refused; concurrent → one wins); **concurrent RecordFailure reaching exactly the lock**;
Replace's CAS; Consume single-use with `remaining`, a foreign user's digest refused; Remove bumping R and clearing both tables;
erasure counts AND rows gone. `TestReference` and auth-cms `authmem` implement it. G34: ledger `0033_totp.sql`, byte-identical.

### 6. HTTP, views and the chooser page
Every route sits under `/auth` (S5 `no-store`; the 64 KiB decoder; cookie callers CSRF double-submit, bearer SPAs skip it).

| Route | Tier and gates |
|---|---|
| `POST /auth/totp/enrollment` · `POST /auth/totp/enrollment/confirm` | CredentialManagement + browserSafe; the §3 gate |
| `GET /auth/totp` · `DELETE /auth/totp` (a `remove_totp` grant) | SessionSecurityReads · CredentialManagement + browserSafe |
| `GET /auth/recovery-codes` · `POST /auth/recovery-codes` | SessionSecurityReads · CredentialManagement + browserSafe; the §3 policy |
| `POST /auth/step-up/totp` · `POST /auth/step-up/recovery-code` | CredentialManagement + browserSafe, beside `/auth/step-up/{password,code,passkey}` |
| `POST /auth/admin/users/{id}/totp/reset` | UserAdministration + browserSafe + `adminPrincipal` |
| `GET /auth/account/totp` · `GET /auth/account/totp/enroll` · `GET /auth/step-up/mfa` | BrowserAccount (only with `TOTPConfig.Views`) |

**Wire contract** (goldens in `inbound/http/totp_test.go`; SPAs and the goth forms share it):

| Call | Request | Success | Errors |
|---|---|---|---|
| enrollment | `{}` | 200 `{secret, otpauth_uri, issuer, account, algorithm:"SHA1", digits:6, period:30, expires_at}` | 403 step-up required; 409 `totp_exists` |
| confirm | `{code}` | 201 `{enrolled_at, recovery_codes:[10], generated_at}` (shown ONCE) | 410 `enrollment_expired`; 401 `totp_rejected`; 409 `totp_exists`; 429 |
| status | — | `{enrolled, pending, enrolled_at?, last_used_at?, recovery_codes_remaining}` | — |
| remove | `{}` | `{status:"removed"}`; cookies cleared; EVERY session revoked (bearer tokens die) | 403 step-up required |
| recovery codes | GET · POST `{expected_generated_at?}` | `{remaining, generated_at?}` · 201 `{recovery_codes, generated_at}` | 403; 409 `recovery_codes_stale` |
| step-up totp / recovery-code | `{code}` | exactly as `POST /auth/login` (new cookies; bearer SPAs get the new pair) plus `assurance` | 401 `totp_rejected` / `recovery_code_rejected`; 429 `too_many_attempts` + `Retry-After` |
| admin reset | `{}` | `{removed: true\|false}` | 403; 401 + challenge below AAL2 |

- `GET /auth/methods` gains `assurance` (the caller's session), `totp {enrolled, last_used_at?}` and `recovery_codes
  {remaining}` (read from `totp.Repository`, not `Snapshot`), so an SPA picks its step-up on an admin 401 (FE7). No response
  carries a digest, ciphertext or another user's data; the secret appears in exactly one response and one page.
- **Views (FE2, FE3).** `TOTPViews{TOTP(TOTPPage); TOTPEnroll(TOTPEnrollPage); RecoveryCodes(RecoveryCodesPage);
  MFAStepUp(MFAStepUpPage)}` follows `PasskeyViews`; the `Views` interface gains no method. `TOTPEnrollPage{Secret (grouped),
  OTPAuthURI, Account, Issuer, ExpiresAt}` — the goth view renders the QR itself from `OTPAuthURI` through `authgoth.WithQR(fn
  QRModules)` (`type QRModules func(string) ([][]bool, error)`; nil → no QR, the key and `otpauth://` link only), as one
  `<svg><path d=…>` with presentation attributes only (`fill="#000"`, white `<rect>` background, 4-module quiet zone, 200 px
  minimum). `AccountSecurityPage`, `StepUpPage` and `PasskeyStepUpPage` gain `TOTP *TOTPLinks{Manage, Enroll, Confirm, Remove,
  RecoveryCodes, StepUp, RecoveryCodeStepUp, ChooserPage}`. **Form arms** (dispatch by Content-Type like `stepup.go:60-70`):
  enrollment POST → 303 `GET /auth/account/totp/enroll` (re-reads the pending row; a wrong code re-renders it with the error);
  confirm POST renders `RecoveryCodes` DIRECTLY (no PRG: the codes are one-time) with a "I saved these" link; its re-POST
  (refresh) hits 409/410 and the form arm maps to the manage page with "already set up; regenerate if you didn't save them";
  regenerate carries `expected_generated_at` hidden (FE3); the TOTP step-up form carries `return_to`, rotates cookies and PRGs
  to it (FE8). Back/forward cache may show a no-store page on Back on that device — accepted, written in the README.
- **The chooser (FE2).** `GET /auth/step-up/mfa` ("Confirm it's you") is the single static `Browser()` + `RequireMFA()`
  redirect target when `TOTPConfig.Views` is wired (derived like `defaultPasskeyStepUpPath`, `authenticator.go:22-25`); a
  passkey-only host keeps S4b's page. The handler renders, filtered by `MFAConfig.Methods`: the passkey control when passkeys
  are wired and an AAL2 passkey is held (first), the TOTP code form when enrolled, the recovery-code form, and otherwise
  "Add a passkey" / "Set up an authenticator app" links. `/auth/step-up/passkey` and `/auth/step-up/totp` stay as endpoints.
- **Fields and copy (FE5, FE6).** Confirm and recovery-code inputs: `autocomplete="off"`, `autocapitalize="none"`,
  `spellcheck="false"`, no `inputmode=numeric` for recovery codes (never `codeFieldAttrs`, `helpers.go:72-78`); the secret in
  `<code>` groups of four; the `otpauth://` link through `templ.SafeURL`. `accountFailure` (`account_forms.go:121-139`) maps
  `ErrTooManyAttempts` (429 + `Retry-After`), `ErrTOTPRejected`, `ErrRecoveryCodeRejected`, `ErrRecoveryCodesStale`,
  `ErrTOTPExists`, `ErrTOTPEnrollmentExpired`. **No new script**; `passkey.js` is untouched; `passkeyUnsupported` gains the
  "Use an authenticator app instead" link when TOTP is enrolled.

### 7. Security events, notices, keys docs, control map
S2 rules: bounded enums, opaque ids, small integers; no row carries a secret, code, digest, URI or ciphertext.

| Type | Status | UserID | Details | Budgeted |
|---|---|---|---|---|
| `totp_enrolled` | success | owner | `tier` (`bootstrap`/`aal2`) | no |
| `totp_removed` · `totp_reset` | success | owner · target (+ Actor) | — · `removed` | no |
| `totp_step_up` | success | owner | `method` (`totp`/`recovery_code`), `assurance` | no |
| `totp_failed` | failure | owner | `method`, `reason` ∈ {`invalid`, `reused`, `locked`, `not_enrolled`, `expired`, `envelope`, `inactive`, `conflict`} | yes, per IP |
| `totp_locked` | blocked | owner | `attempts`, `locked_for_seconds` | yes, per IP |
| `recovery_codes_generated` | success | owner | `count`, `trigger` (`enrollment`/`regenerate`) | no |
| `recovery_code_used` | success | owner | `remaining` | no |

`mfa_required_denied` gains `reason` and `method`. **Notice:** one new delivery purpose `mfa_change_notice` (core templates
`.html`/`.txt`, `change` ∈ {`totp_enrolled`, `totp_removed`, `totp_reset`, `recovery_codes_regenerated`, `recovery_code_used`})
on the identifier-notice path, best-effort. **Docs:** `keys-and-rotation.md` "The six authentication keys" (the TOTP ring;
regenerate recovery codes before dropping a pepper key); the pocket README "TOTP and recovery codes (SOC 2 S4c)" (turn-on
recipe, the gate table, the wire contract, the chooser, the custom-view markup for the forms, the 5-minute implication, the
rotation steps, the operator reset path); `docs/pockets/authentication.md` "Added by S4c"; `SECURITY.md` (the `RunTOTP` erasure
obligation). **Control map (PR-4):** `:17` adds native TOTP + recovery codes and `Methods`; residuals: R-3's composition,
native `all` (S4c-2), API keys and delegated `aal` (S4d); `:99` the seven types.

## Upgrade notes
> **Read first:** `grep -rnE 'MFAConfig\{|RequireMFA\(|EraseRepository|KeysInput\{' --include='*.go'`; apply 0026 before repinning
> a store, TOTP wired or not. **By host shape** (PR-4 expands each): every store host → rows 10, 3, 9; IdP-only → row 10
> (and row 2 if it sets `Methods`); hosts turning TOTP on → rows 1, 4–8, 11; passkey hosts → rows 3, 5, 11 (the chooser page).

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | new adapter `rscqr.Modules` | additive | none | rsc-qr@v0.1.0 |
| 2 | `MFAConfig.Methods`; `mfa_required_denied` `reason`/`method` | additive; empty = any source; an unwired source → `ErrMFAUnsatisfiable` | leave empty | authentication@v0.28.0 |
| 3 | access tokens carry `methods`; `Credential.Methods`; delegated tokens refuse it | pre-upgrade AAL2 tokens fail closed under a non-empty `Methods` until refresh | leave `Methods` empty through the rollout | authentication@v0.28.0 |
| 4 | `WithTOTP`, `Repositories.TOTP`, the routes, `/auth/methods` `assurance`/`totp`/`recovery_codes`; `admins` satisfiable by TOTP; zero `Required` → `admins` with TOTP + `UserAdminCheck` | opt-in; a host wiring TOTP beside a `UserAdminCheck` gets the `admins` default (`source=default`) | `Required: MFARequiredNone` | authentication@v0.28.0 |
| 5 | one AAL2-factor predicate on both gates | a TOTP holder adds/removes a passkey only at AAL2, and the reverse | none | authentication@v0.28.0 |
| 6 | a TOTP/recovery step-up re-mints the session at AAL2 (R-3) and is a recent login for 5 min | new AAL2 source on every `RequireMFA()` gate | `Methods: [passkey, idp]` | authentication@v0.28.0 |
| 7 | `NewKeys`: `KeysInput.TOTPKeys`/`TOTPActiveKey`, `Keys.TOTPRing`, `KeyTOTPRing` | additive; production refuses an unset ring only when `KeyTOTPRing` is needed | do not pass `KeyTOTPRing` | authentication@v0.28.0 |
| 8 | seven event types; `mfa_change_notice`; the recovery-code key WARN | new audit rows and notices | override the template | authentication@v0.28.0 |
| 9 | erasure deletes TOTP + recovery codes (`EraseCounts.TOTP`, `.RecoveryCodes`); a host `EraseRepository` must, or TOTP secrets survive erasure | host-store obligation; `RunTOTP` pins it | implement per `RunTOTP` | authentication@v0.28.0 |
| 10 | **REQUIRED migration 0026** | BOOT FAILURE without `totp_credentials` + `recovery_codes`, naming 0026, every mode | apply 0026; ledger copy (P9) | stores pgx@v0.13.0, turso@v0.12.0 |
| 11 | `TOTPViews`; `WithQR`; the chooser page is the `Browser()`+`RequireMFA()` redirect target when TOTP views are wired; `TOTP *TOTPLinks` on three page models | additive; passkey-only hosts keep `/auth/step-up/passkey` | leave `TOTPConfig.Views` nil | views/goth@v0.8.0 |
| 12 | auth-cms wires TOTP (ring from env, `rscqr.Modules`, views); baseline ledger 0033 | example behaviour | — | examples (untagged) |

No compile-time breaks. Row 10 is a boot failure; row 3 changes a decision only under a non-empty `Methods`; row 5 tightens a
gate for TOTP holders (none exist before upgrade); rows 4 and 6 are opt-in. Every module ships MINOR under v0; tag messages name
rows 3, 5, 9 and 10. **Not touched:** sdk (BE8), `Mutation`/`MethodSet` (BE1 resolved in the aggregate), `passkey.js`.

## Risks
(1) R-3(a) composes AAL2 from a passwordless or OAuth primary plus a possession code — strictly two possession factors; named as
a residual, excludable per host via `Methods`. (2) One code is a recent AAL2 login for every gate for 5 minutes (BE9): a stolen
unlocked session that reads one TOTP code can disable TOTP; the notices are the detective control. (3) Retiring a pepper key
kills recovery codes sealed under it (BE4): the boot WARN and the documented regenerate-first step. (4) 0026 refuses boot on
repinned stores in every mode (row 10). (5) Clock skew beyond ±30 s between server and phone fails every code; the ±1 window
and a README line on NTP. (6) Authenticator-app QR behaviour (dark themes, label parsing of `issuer:account`) is only proven by
the owner's device check.

## Tasks as PRs
Worktrees `../gopernicus-s4c-<pr>`; one merge/tag/push per command; retarget stacked children to `main` first; every PR runs
with model **opus**. Live legs never touch ports 55432 or 8082.

- **PR-1 — the rsc.io/qr adapter module (`integrations/rsc-qr`).** **Depends on** nothing. **BREAKING:** none (new module).
  **Tag:** `integrations/encoding/rsc-qr/v0.1.0`. **Files:** `integrations/encoding/rsc-qr/{go.mod,go.sum,rscqr.go,doc.go,
  README.md,rscqr_test.go}` (a known-text matrix is pinned: size, finder patterns, a round-trip of the quiet-zone contract);
  `go.work`; `Makefile` (MODULES); `ARCHITECTURE.md:12-16`. **Verify:** in the module `GOWORK=off go build ./... && go test
  -race ./... && go vet ./...`, `govulncheck ./...` (recorded); `make guard` (G13); `make check`.
- **PR-2a — authentication core: TOTP, recovery codes, the aggregate port, `Methods` + `methods`, events, notices, keys, the
  JSON + HTML seams (`authentication/totp-core`).** **Depends on** nothing (no sdk pin move). **BREAKING:** rows 3–6, 8, 9.
  **Tag:** none (step 2 follows PR-2b). **Files** (under `pockets/authentication/`): `logic/authentication/totp/{totp,
  credential,repository,recovery}.go` + tests (RFC vectors; the envelope; code normalization); `logic/authentication/
  {totp_enrollment,totp_stepup,totp_management}.go` (new), `{passkey_ceremony.go (the predicate; mergeMethods), stepup.go,
  service.go, token.go, refresh.go, external.go, methods.go, options.go, assurance.go, challenge/challenge.go (the reserved
  purpose), authgrant/authgrant.go (register_totp, remove_totp, regenerate_recovery_codes), session/method.go, securityevent/
  securityevent.go, user/erase.go, abuse.go}`; `logic/delivery/{router.go, templates/mfa_change_notice.{html,txt}}`;
  `inbound/http/{totp.go, totp_html.go (new), principal_options.go, principal.go, authenticator.go, adapter.go, routes.go,
  html.go, views.go, account_forms.go, useradmin.go, machine.go, methods.go, errors.go}`; `{config,configuration,constructor,
  keys,repositories,security}.go`; `README.md`; `stores/storetest/{totp.go,reference_test.go}`; tests `totp_test.go`,
  `logic/authentication/totp_*_test.go`, `inbound/http/totp_test.go` (tiers, budgets, wire goldens, Retry-After, no secret in
  any response but enrollment, the chooser redirect, form arms), `mfa_test.go` (`Methods` on every gate; a token without
  `methods` under a restriction; delegated refusal; the resolve widening), `keys_test.go`. **auth-cms** (go.work CI):
  `examples/auth-cms/go.{mod,sum}`; `examples/auth-cms/pockets/authentication/outbound/authmem/{totp.go (new),erasure.go,
  authmem.go}`; `examples/auth-cms/cmd/server/{main,authentication}.go` (`WithTOTP{Encrypter: ring, Issuer: "Gopernicus CMS"}`,
  `Views` nil until PR-3; `NewKeys` with `KeyTOTPRing`); `examples/auth-cms/cmd/server/totp_test.go` (the real binary's routes
  with codes computed by the `totp` package). **Verify:** `cd pockets/authentication && go build ./... && go test -race ./... &&
  go vet ./...`; `cd examples/auth-cms && go test ./...`; `make guard`; `make check`; Verification §1.
- **PR-2b — authentication stores: 0026, the aggregate store, erasure (`authentication/totp-stores`).** **Depends on** PR-2a
  (stacked). **BREAKING:** row 10. **Files:** `pockets/authentication/stores/{pgx,turso}/{migrations/0026_totp.sql, totp.go,
  erasure.go, postgres.go|turso.go (the probe), README.md, conformance tests with RunTOTP}`;
  `examples/production-baseline/workshop/migrations/primary/0033_totp.sql` (G34). **Verify:** `make check`; `make guard`
  (G34, G35); the live legs on the branch: pgx bare and `POSTGRES_TEST_SCHEMA=s4c` on a fresh PG17 (`lsof` the port first),
  the turso gate on a fresh libSQL, a Linux-container `make test-stores`, a `live-stores` dispatch. **Tag:** none; after 2a + 2b
  merge, re-run the legs on `main` against the S4b baselines (453 / 0 / 1 per pgx mode, 758 / 0 / 0 turso), then cut step 2.
- **PR-2c — stores repin (`authentication/stores-repin-s4c`).** **Depends on** step 2 resolving. **Files:** both stores'
  `go.mod`/`go.sum` (core → v0.28.0 by explicit `GOWORK=off go get`, then `GOWORK=off go mod tidy`). **Verify:** `make check`;
  `make test-stores`; a `live-stores` dispatch on `main`. **Tag:** step 3.
- **PR-3 — views/goth: the TOTP pages, the chooser, the QR hook, auth-cms wired (`authentication/totp-views`).** **Depends on**
  step 2 resolving; the views `go.mod` moves authentication v0.27.1 → v0.28.0 (forced: page models changed); **rebased onto
  `main` after every later core tag before the owner gets a build** (the S4b lesson). **BREAKING:** none. **Tag:** step 4.
  **Files:** `pockets/authentication/views/goth/{totp.templ (new), stepup.templ, passkey.templ, account.templ}` (`_templ.go`
  from `make generate` only), `{views.go (WithQR, TOTPViews), qr.go (the path builder), helpers.go (the new field attrs),
  totp_test.go, views_test.go, go.mod, go.sum}`; `pockets/authentication/README.md` (the custom-view markup);
  `examples/auth-cms/cmd/server/main.go` (`TOTPConfig.Views: authViews`, `authgoth.WithQR(rscqr.Modules)`),
  `examples/auth-cms/go.{mod,sum}`, `goth_proof_test.go`. **Verify:** `make generate` leaves no `_templ.go` diff; `go test ./...`
  in the views module and `examples/auth-cms`; `make check`; **the browser leg (§2) on this branch**: run and look at the
  manage, enroll, recovery-codes and chooser pages in Chromium and WebKit, light and dark theme.
- **PR-4 — the record (`docs/s4c-release-record`).** **Depends on** steps 1–4 cold-verified AND the owner's device check (§4)
  recorded; a failure becomes a views/goth or core patch, tagged first. **Files:** `RELEASING.md` (ONE section "TOTP and
  recovery codes (SOC 2 slice S4c)": tags, the Upgrade notes by host shape, the migration note, the device-check result per app
  and theme, the verification record); every `examples/*/go.mod` pinning a moved module (explicit `GOWORK=off go get`);
  `SECURITY.md`; `control-map.md:17,99`; the roadmap S4 line; the docs site (§7); `plans/s4c-totp.md` (executed copy).
  **Verify:** `make check`; `make guard`; `GOWORK=off go build ./... && go test ./...` per repinned example; `pnpm build`; §3.

## Sequencing
```
PR-1 ─[tag 1]──────────────────────────────────────────────┐
PR-2a ── PR-2b (stacked) ── merge 2a, 2b ─[live legs on main]─[tag 2]─┬─ PR-2c ─[dispatch]─[tag 3]
                                                                      └─ PR-3 (needs tag 1 too) ─[browser leg]─[tag 4]
                        tags 1–4 cold-verified + the owner's device check ─ PR-4 (record)
```
Four trains is the floor (no sdk train: BE8). PR-1 and PR-2a start in parallel worktrees; PR-2c and PR-3 run in parallel off
tag 2; PR-3 also needs tag 1 (auth-cms requires the adapter). A core patch after tag 2 forces a PR-3 rebase before any build.

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 1 | `integrations/encoding/rsc-qr/v0.1.0` (NEW module, FIRST tag) | PR-1 merge | adapter + rsc.io/qr v0.2.0 + sdk v0.20.0: `go mod verify`, build, `go test` from the cache |
| 2 | `pockets/authentication/v0.28.0` | PR-2a + 2b merged; live legs on `main` | **set A:** core v0.28.0 beside the RELEASED stores pgx v0.12.0 / turso v0.11.0 AND views v0.7.0 builds (TOTP unwired; no lockstep) |
| 3 | `…/stores/pgx/v0.13.0`, `…/stores/turso/v0.12.0` | PR-2c + dispatch | **set B:** core v0.28.0 + both stores + pgxdb v0.14.0 + turso v0.9.0 select sdk v0.20.0 and build |
| 4 | `…/views/goth/v0.8.0` (requires core v0.28.0) | PR-3 + browser leg | **set C:** views v0.8.0 + core v0.28.0 + ui/goth v0.2.0 + rsc-qr v0.1.0 + go-webauthn v0.1.0 + set B build an auth-cms-shaped `main` |

**Tagging.** Five annotated, unsigned tags, each cut at its squash commit, pushed, and only THEN polled on `.info`; cold-verify
with `GOWORK=off`, an empty `GOMODCACHE`, `proxy.golang.org` + `sum.golang.org`, `go mod verify`, `go build <module>/...`.
Never tidy before a tag resolves; never `go work sync`. **Not tagged:** sdk, ui/goth, go-webauthn, `pockets`, workshop (no new
waiver), the examples. Live legs before steps 2 and 3 are counted with `go test -json` against the S4b baselines; stop the
scratch containers afterwards.

## Verification
Real binaries in DEVELOPMENT. `$B` = a scratch auth-cms binary on a FREE port (`PORT=8092 AUTH_RUNTIME_MODE=development
AUTH_DEBUG=1 go run ./cmd/server`; never 8082, the owner's server; `lsof -i :8092` first), authmem, console mailer; events are
read from `GET /debug/security-events` as a platform admin. Codes are computed by a scratch Go program (`/private/tmp/…/
scratchpad`, never committed) importing `pockets/authentication/logic/authentication/totp` with the secret from the page or
JSON; `oathtool --totp -b <secret>` cross-checks when installed.

- **§1 — PR-2a, the real binary over HTTP.** (1) Boot line `mfa_required=admins source=default sources=totp`; register,
  verify, password login; `GET /demo/mfa` → **401** `insufficient_user_authentication`; `GET /auth/methods` → `assurance:aal1`,
  `totp.enrolled:false`. (2) `POST /auth/totp/enrollment` within 5 min → **200** with `secret` and `otpauth_uri`; a second POST
  replaces the pending row (the first secret now fails). (3) Confirm with a wrong code → **401**, `totp_failed reason=invalid`,
  the pending row survives; with the right code → **201**, ten codes, `totp_enrolled tier=bootstrap`, `recovery_codes_generated
  count=10`, a `mfa_change_notice` on the console mailer; the same code on `/auth/step-up/totp` → **401** (`reused`, BE10).
  (4) The next step's code → **200** `assurance: aal2`, a new cookie, the old refresh token refused, `ExpiresAt` unchanged;
  `/demo/mfa` **200**; `methods` claim decoded from the access token = `["password","totp"]`. (5) Five wrong codes → the fifth
  **429** with `Retry-After: 300`, `totp_locked`; a correct code while locked → **429**; after the clock moves → **200**.
  (6) A fresh password login → `POST /auth/totp/enrollment` **409** `totp_exists`; `DELETE /auth/totp` **403**; after a TOTP
  step-up **200**: every session revoked, `totp_removed`, a notice; `passkeys/registration/options` for a TOTP holder at AAL1 →
  **403** (BE2). (7) Re-enroll; a recovery code on `/auth/step-up/recovery-code` → **200** `aal2`, `recovery_code_used
  remaining=9`, a notice; the same code again → **401**; `POST /auth/recovery-codes` with the stale `expected_generated_at` →
  **409**; with the current → **201**, ten new codes, the old ones refused. (8) `WithMFA{Methods: [passkey]}` (a second binary):
  a TOTP AAL2 token on `/demo/mfa` → **401**, `mfa_required_denied reason=method method=totp`; a token minted before the
  restriction (no `methods`) → **401** until refresh. (9) An AAL1 platform admin on `/auth/admin/users` → **401**; after a TOTP
  step-up **200** (B-2 widened); the admin resets user X → **200** `{removed:true}`, `totp_reset`, X's sessions revoked, a
  notice; X re-enrolls at `tier=bootstrap`. (10) Erase X → `EraseCounts` report `totp=1 recovery_codes=10`; rows gone. (11) No
  log line, row or notice holds a secret, code, digest, URI or ciphertext; the ring WARN prints for a fabricated orphan key id.
- **§2 — the browser leg (PR-3 branch; from the tags in §3).** A scratch CommonJS script requires `@playwright/test` and
  `@axe-core/playwright` by absolute path from the MAIN checkout's `examples/goth-showcase/e2e/node_modules`, spawns the binary
  on 8092, drives Chromium AND WebKit at `$B`, listens on `console` and `securitypolicyviolation`, and computes codes by
  shelling out to the scratch Go program. **Steps → expected:** (1) Register, verify, sign in; `/demo/mfa/page` → the chooser
  offers only "Set up an authenticator app" (no passkey held). (2) The manage page → "Set up" → PRG to the enroll page: an
  inline `<svg>` with a `<path>`, the grouped secret, the `otpauth://` link (`href` intact), the confirm form; a reload keeps
  the same secret; a wrong code re-renders the same secret with the error. (3) The right code → the recovery-codes page with
  ten codes and the "I saved these" link; refresh → the "already set up" manage page. (4) Sign out, sign in; `/demo/mfa/page`
  → the chooser shows the TOTP form; a code → lands on `/demo/mfa/page` **200**; `fetch('/demo/mfa')` → `aal2`. (5) Five wrong
  codes → the 429 copy with the wait. (6) A recovery code on the chooser → `aal2`; the manage page shows 9 remaining;
  regenerate → new codes; Back → the browser may show the old page (noted). (7) Add a UV passkey (CDP virtual authenticator) —
  the registration options now need the AAL2 step-up (BE2); the chooser then lists the passkey control FIRST and the TOTP form
  below. (8) Disable TOTP after a step-up → every session gone, landing on sign-in. (9) CSP `script-src 'self'`, zero
  violations (the inline SVG is not script); `make generate` leaves no diff; axe on manage, enroll, recovery-codes, chooser and
  the 409 re-render: zero serious/critical; both themes screenshot the QR for the record.
- **§3 — from the tags (PR-4).** §2 against a `GOWORK=off` auth-cms binary (`go version -m`: authentication v0.28.0,
  views/goth v0.8.0, rsc-qr v0.1.0); production-baseline boots from the tags on a fresh PG17 with ledger 0033 and, with
  `recovery_codes` dropped, refuses to boot naming 0026.
- **§4 — the owner-run real-device check (PRECONDITION of PR-4).** On the Mac against the owner's own build: enroll on the
  manage page by (a) scanning the QR with the iPhone camera into Apple Passwords, (b) scanning with Google Authenticator or
  1Password, (c) clicking the `otpauth://` link in Safari so Apple Passwords on the Mac takes it; in light AND dark theme.
  Expected per app: the entry shows the issuer "Gopernicus CMS" and the account; its code confirms enrollment; a later code
  steps `/demo/mfa/page` up to `aal2`; one recovery code steps up; TOTP disables; no console error. Pass/fail per app and theme
  goes into the RELEASING section; a failure becomes a views/goth or core patch first.

## Open rulings — YOUR CALL
- **R-1 — one slice or two?** (a) THIS plan ships TOTP + `Methods` + the `methods` claim + recovery codes; native `all`, the
  enrollment grace (`max(EnrollmentStart, user.CreatedAt) + EnrollmentGrace`; 403 `mfa_enrollment_required`; refresh
  re-checks; the admin-reopened per-user marker) ship next as `s4c2-native-all.md` with its own store train (the marker
  migration). (b) One slice: a PR-2d for `all` + grace + a 0027 marker table, one more core tag before the views tag, ~100 more
  plan lines. **Recommend (a).** The hard rule binds `Methods` to TOTP, not to `all`; `all` touches every tier and the refresh
  path and is a lockout-class change whose drill (an admin re-opening grace) is a different owner leg from scanning a QR; the
  500-line cap holds only for one; recovery codes shipping first is what makes `all` humane. Cost: one extra store train later.
- **R-2 — QR rendering.** (a) views/goth hook `WithQR(QRModules)` + the NEW adapter module `integrations/encoding/rsc-qr`
  over rsc.io/qr v0.2.0 (BSD-3, zero deps, 2018, frozen spec); nil hook → key + `otpauth://` link only; SPAs render from
  `otpauth_uri`. (b) A stdlib QR encoder in sdk (`sdk/pkg/qr`, ~500 kernel lines, goldens only, an sdk tag). (c) No QR.
  **Recommend (a):** no sdk change, the library stays behind a module named for its package, and the owner's device check is
  the proof either way; (b) is a kernel encoder with no in-repo decoder to test it against.
- **R-3 — the assurance a TOTP/recovery step-up yields.** (a) AAL2 on every live first-party session; "AAL2-capable" = a
  confirmed TOTP, one predicate for every gate; the strict-800-63B residual (a passwordless or OAuth primary + a possession code)
  is named in the control map, and a host that rejects it sets `Methods: [passkey, idp]`. (b) BE6's allowlist: AAL2 only beside
  a `password` primary or an already-AAL2 session; then TOTP is AAL2-capable only for users WITH a password, `/auth/methods`
  gains a per-method `yields`, the manage page warns passwordless users, and OAuth-only users cannot use TOTP at all.
  **Recommend (a).** (b) is more honest but creates FE1's dead ends unless the predicate goes conditional, and it strands the
  owner's OAuth-first React apps; password + TOTP, the canonical AAL2 pair, behaves identically under both.
- **R-4 — the TOTP library.** (a) Stdlib RFC 6238 in the pocket (`logic/authentication/totp`, ~150 lines, RFC vectors); no
  adapter, no sdk tag. (b) An sdk port + `integrations/cryptids/pquerna-otp` (v1.5.0, 2024-12, `go 1.12`, pulls
  boombuler/barcode + testify; its `Validate` reports no step, so replay tracking needs our loop anyway). **Recommend (a).**
- **R-5 — removing TOTP signs the user out everywhere** (the passkey precedent; the fenced `Remove` bumps R). (a) Yes.
  (b) Revoke only sessions whose `methods` carry `totp`/`recovery_code`. **Recommend (a):** one rule for every factor removal.

## Open questions
_None._ The lead questions are answered above (BE: the aggregate owns the fenced delete; FE: R-3).

## Recommended reviews
**product-manager:** R-1 and R-3 before ratification (scope and the honesty trade-off). **platform-sre:** 0026's boot
failure, the new key ring and its production refusal, the pepper-retirement WARN, a new module's first tag. **data-integration-
reviewer:** the aggregate store's lock order and the atomic lockout increment on both dialects. **architecture-steward:** the
no-port adapter module (R-2) and `totp` living in the pocket (BE8).

## Notes
- **S4c-2 handoff (if R-1 = (a)).** Native `all`: deny below AAL2 on every first-party route but an allowlist (`/auth/step-up/*`,
  `/auth/totp/*`, `/auth/passkeys/registration*`, `/auth/recovery-codes`, `/auth/methods`, `/auth/me`, `/auth/logout`,
  `/auth/refresh`); the grace deadline `max(EnrollmentStart, user.CreatedAt) + EnrollmentGrace` (`EnrollmentStart` a required
  literal date; 7-day default, 30-day maximum); before it an unenrolled user signs in at AAL1 and gates still deny; after it
  403 `mfa_enrollment_required` + `mfa_required_denied gate=enrollment`; refresh re-checks; `POST /auth/admin/users/{id}/mfa/
  grace` re-opens via a 0027 per-user marker; the `holdsAAL2Factor` predicate from §3 is the enrollment test. **S4d handoff:**
  API keys under MFA gates; `aal`/`methods` on delegated tokens; re-sealing TOTP secrets on ring rotation (`KeyIDOf`).
- **Follow-ups.** SHA-256 TOTP once Google Authenticator honours it; an escalating lockout (M8) replacing the fixed 5 × 5 min;
  a retention job for abandoned pending enrollments (`expires_at`); a committed Playwright spec.
- **Cut for the cap:** per-test case lists beyond the suite; the column list of the memory store; a `totp.templ` sketch; the
  handoff prose (PR-4 writes it); the `principal.go` diff for the chooser redirect.
