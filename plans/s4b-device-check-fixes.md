# S4b addendum — device-check fixes (development CSRF cookie, biometric enrollment)

**Status:** EXECUTED 2026-10-25 (PR-3d #197 → authentication v0.27.0; PR-3e #198 → v0.27.1, a passkey step-up always requires user verification — found by the owner's Safari re-check, approved "do it"; #196 amended twice → views/goth v0.7.0; Safari + Touch ID PASS). RATIFIED 2026-10-25 (owner: "ratified"). Addendum to `s4b-passkeys.md` (RATIFIED); it inserts one core
patch before tag 5 and amends PR-4 (#196, open, held). Evidence: `s4b-passkeys` deviations log §6.

## Why
The owner's device check (Safari 26.3 + Chrome 155 on the Mac, auth-cms over `http://localhost:8082`) failed twice:
- **F1 — Safari 403 on every JSON CSRF mutation.** The double-submit cookie is hard-coded `__Host-auth_csrf` + `Secure`
  (`inbound/http/security.go:32,248-256`). WebKit drops `Secure` cookies on `http://localhost`; Chromium keeps them
  (reproduced: Playwright WebKit 26.4 stored nothing, Chromium 148 stored the cookie). Owner log: `POST
  /auth/step-up/passkey/options` 403 ×8 in Safari, 200 in Chrome, same user. Pre-existing since S5; passkeys are the first
  browser JSON-CSRF path the owner exercised in Safari. Production (HTTPS) is unaffected. It matters beyond auth-cms: a
  React app embedded in the Go binary (same origin) uses the same cookie + `X-CSRF-Token` pattern.
- **F2 — no biometric prompt.** With `UserVerification` preferred (the zero value), macOS saved the passkey without Touch
  ID; it is stored presence-only, signs in at AAL1, and can never satisfy `RequireMFA()` or the `admins` default.

## Owner rulings (2026-10-25)
- **F1:** relax the CSRF cookie in **development only**; production must **hard-fail** if the relaxed configuration is
  ever present.
- **F2:** do NOT force `required` as the default; keep it a host toggle; a user must be able to add a new passkey — one or
  several — **with a biometric prompt**.

## Design
### F1 — the CSRF cookie follows the session cookie's `Secure` (pockets/authentication)
- One resolved `csrfCookie{name string; secure bool}` on the handler config, computed once at construction:
  `Secure` session cookie → `__Host-auth_csrf` + `Secure` (today's behavior, unchanged); non-`Secure` session cookie in
  **development** → `auth_csrf`, no `Secure`, `Path=/`, no `Domain`, `SameSite=Lax`, not HttpOnly (unchanged otherwise).
- **Hard failure in production:** the existing refusals already reject a non-`Secure` session cookie in production
  (`configuration.go:57`, `inbound/http/authenticator.go:186`). Add a second, independent constructor check: a resolved
  relaxed CSRF cookie with runtime mode production → error (`ErrInvalidInput`, names the cookie). Belt and braces: the
  relaxed name is never reachable in production even if the first check is refactored away. Tests pin both.
- The nine `csrfCookieName` reads (`security.go`, `account_forms.go`, `oauth2.go`, `authenticator.go` collision set) read
  the resolved name. A development boot line notes the relaxed cookie (`csrf_cookie=auth_csrf secure=false`).
- **No behavior change** for any host with a `Secure` session cookie (all production hosts). README: the CSRF section
  names both cookies and when each applies; SPAs keep reading `csrf_token` from the `GET /auth/csrf` body (never the
  cookie), so React clients need no change.
- **Out of scope (noted):** a decoupled React app on another origin. Cookie-based CSRF does not cross origins; those
  clients use bearer tokens (`isBearerOnly` skips the gate). Its own plan if the owner wants cross-origin cookie auth.

### F2 — biometric enrollment on request (pockets/authentication + views/goth)
- `PasskeyConfig.UserVerification` stays the host toggle (preferred default, `required` opt-in, `discouraged` refused).
- **Per-ceremony upgrade:** `POST /auth/passkeys/registration/options` accepts an optional
  `{"user_verification":"required"}` (the strict decoder gains one optional field; `{}` keeps today's behavior). The
  service passes it to `RegistrationStart.UserVerification`, so the browser must prompt for Touch ID / Face ID / Windows
  Hello / a PIN, and the adapter's deferred check refuses a response without UV (`ErrUserVerificationRequired` → 401
  `passkey_rejected`). A request can only raise the requirement, never lower a host's `required`.
- **Upgrade in place:** when a ceremony requires UV, the exclude list OMITS the user's presence-only passkeys, so the same
  device's keychain can create a verified passkey (Apple/Google replace a same-handle passkey). Without this, the owner's
  Mac answers InvalidStateError ("already has a passkey"). The new row is AAL2-capable; the old presence-only row stays
  until removed (listed as "Doesn't confirm a PIN or biometric", removable as today). Multiple passkeys are already
  supported (cap 10).
- **Views (#196 amended):** the manage page's add control gains a checkbox "Require Face ID, Touch ID or a PIN" (checked
  by default when the user holds no AAL2-capable passkey); the step-up page's "Add a passkey" link (shown when
  `CanReachAAL2` is false) lands with it checked; `passkey.js` sends the field. Copy for the new 401: "That passkey
  didn't confirm a PIN or biometric. Try again and use Face ID, Touch ID or your PIN."

## Tasks as PRs
- **PR-3d — authentication: development CSRF cookie + per-ceremony UV (`authentication/device-check-fixes`).** Core
  only. **BREAKING:** none (development-only rename; additive field). **Tag:** `pockets/authentication/v0.27.0` (additive
  API under v0: minor). Tests: cookie resolution matrix (Secure/non-Secure × dev/prod), the production double refusal,
  every CSRF reader on the relaxed name, the options field (absent / required / lowering refused / unknown value 400),
  the exclude-list omission, UV-required refusal through `webauthnstub`. auth-cms: no config change needed.
  **Verify:** module build/test/vet, auth-cms tests, `make guard`, `make check`, Verification §2 re-run.
- **PR-4 (#196) amended:** views go.mod → v0.27.0; the checkbox, step-up link, script field and copy; tests.
  **Verify:** as PR-4, plus the browser leg in **Chromium AND Playwright WebKit** (WebKit proves F1 end to end:
  enroll, sign in, step up to `aal2` over `http://localhost`).
- Then: tag 5 (views/goth v0.7.0, requires core v0.27.0; cold-verify set C), the owner's Safari re-check, PR-5.

## Verification (owner-facing)
Safari + Chrome on the Mac, fresh auth-cms: sign up, add a passkey **with the checkbox on** → Touch ID prompt; sign out;
usernameless sign-in; `/demo/mfa/page` → step up with Touch ID → `/demo/mfa` `aal2`; no console errors. Also: an
existing presence-only passkey user can add a verified one on the same Mac.

## Risks
- Relaxed cookie reaching production → two independent refusals + tests (F1).
- Keychain replace-in-place differs by platform; if a platform refuses, the user removes the presence-only key first
  (copy says so). Recorded per platform in the RELEASING section.
