# Phase A4 — JWT bearer mode

Status: RATIFIED (cut from design §4.4)
Executor model: opus
Depends on: A3 (the middleware trio + bearer classing exist).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §4.4
(ratified AV6 — stateless short-TTL *user* tokens, NO refresh tokens,
machine clients stay on API keys; the revocation-asymmetry doc
requirement), §7.1 (the verified-email gate extends to `/auth/token`).
Consumed, not modified: `sdk/cryptids.JWTSigner` (port) and
`integrations/cryptids/golang-jwt` (host-wired only — G2; this phase's
tests use an in-package fake signer, cut refinement 10).

## Work items

1. **Config**: `TokenSigner cryptids.JWTSigner` (nil → mode off — bearer
   JWTs are NEVER parsed, `/auth/token` not registered) +
   `TokenTTL time.Duration` (0 → 1h). The revocation asymmetry (a JWT
   outlives password change/logout until expiry; short TTL is the
   mitigation) goes in the field's doc comment verbatim from design §4.4.
2. **`POST /auth/token`**: login-shaped `{email, password}`, strict
   decode, rate-limited with the SAME pre-credential-work discipline and
   key shape as `/auth/login`, honors `RequireVerifiedEmail` (403), and
   on success returns `{token, expires_at}` — claims `{user_id}` with
   expiry via `Sign(claims, expiresAt)`. **Audit (plan-cut amendment): if
   A5 has landed, record `token_issued` via A5's `recordSecurityEvent`
   helper; otherwise A5 wires it when it lands** (the dependency table
   permits either order; A9 leg 5 asserts `token_issued` rows exist).
3. **Bearer verification**: the two-dot JWT arm of A3's classing now
   resolves — `RequireUser` and `RequirePrincipal` accept
   `Authorization: Bearer <jwt>` when the signer is wired, mapping
   `user_id` to the same user identity/`Principal{Type: "user"}` the
   session path produces. Signer nil → the JWT arm stays inert (A3's
   behavior unchanged).
4. **Tests** (fake signer in-package). **Fake-signer contract (plan-cut
   amendment): the fake must genuinely verify — reject expired tokens
   (checks the encoded expiry against the clock) and reject
   tampered/badly-signed tokens (e.g. an HMAC over the claims with a
   test secret), so the 401 assertions below are real checks, not rubber
   stamps.** Cases: issuance round-trip; expired token → 401;
   malformed/garbage bearer → 401 without signer panic; tampered
   signature → 401; signer-nil → `/auth/token` absent (404) and bearer
   JWTs unparsed; verified-email gating on `/auth/token`; rate-limit
   denial → 429.

## Acceptance

```sh
cd features/auth && go build ./... && go vet ./... && go test ./...
make check
```

`features/auth/go.mod` still requires exactly `sdk` (the golang-jwt
integration must NOT appear — grep `go.mod` for `golang-jwt` → empty).

## Real-interaction check

Standing check (a); check (b) unchanged. Deny-by-absence proof: boot
`examples/auth-cms` (signer unwired) →
`curl -s -o /dev/null -w '%{http_code}' -X POST localhost:8082/auth/token`
→ **404**. The wired JWT leg (real golang-jwt signer, expired-token and
absent-signer variants) is A9's protocol.

## Execution log

(append dated entries here)

### A4 — 2026-07-07 — PASS

JWT bearer mode landed in `features/auth` (core stays sdk-only; the golang-jwt
integration is host-wired in A9, not here — go.mod grep for `golang-jwt` empty).

Work items, all four:

1. **Config** — `auth.Config.TokenSigner cryptids.JWTSigner` (nil → mode off:
   bearer JWTs never parsed, `/auth/token` not registered) + `TokenTTL
   time.Duration` (≤0 → `defaultTokenTTL` = 1h, resolved in `NewService`). The
   revocation-asymmetry doc comment carries the design §4.4 sentence verbatim.
2. **`POST /auth/token`** — `authsvc.IssueToken`: strict-decoded `{email,
   password}`, rate-limited FIRST on the SAME `loginKey(email, clientIP)` shape
   as `/auth/login` (shared budget), honors `RequireVerifiedEmail` (403 after
   password verify), success → `{token, expires_at}` with claims `{user_id}`
   via `Sign(claims, expiresAt)`. `A5:` comment left at the `token_issued`
   audit site (deferred to A5 per the plan-cut amendment).
3. **Bearer verification** — the two-dot arm now resolves: `RequireUser`
   (refactored to `resolveUserID`) AND `RequirePrincipal` accept `Bearer <jwt>`
   when the signer is wired, mapping `user_id` → `Principal{Type:"user"}` / the
   same user identity the session path produces. Signer nil → the arm stays
   inert (A3 behavior unchanged); a JWT bearer never falls through to the
   cookie.
4. **Tests** — honest in-package fake `JWTSigner` in both authsvc and http test
   packages (HMAC-SHA256 over base64url JSON claims with a test secret; Verify
   checks the encoded `exp` against the clock and the MAC in constant time).
   Cases green: issuance round-trip + custom TTL; expired → 401; garbage
   (`aaa.bbb.ccc`) → 401 no panic; tampered signature → 401; wrong-secret forge
   → 401; signer-nil → `/auth/token` 404 and bearer JWTs unparsed (RequireUser
   + RequirePrincipal both 401); verified-email gate (403); rate-limit (429);
   pre-credential short-circuit asserted (users.calls unchanged).

Files: `features/auth/auth.go` (Config fields + Deps wiring + doc updates),
`internal/logic/authsvc/{service.go,token.go,machine.go}`,
`internal/inbound/http/http.go`, plus new tests
`internal/logic/authsvc/token_test.go` and `internal/inbound/http/token_test.go`.

**Acceptance:** `cd features/auth && go build ./... && go vet ./... && go test
./...` → all pass. `make check` → **all checks passed** (26 modules + 4 guards).
`grep golang-jwt features/auth/go.mod` → empty; go.mod still requires exactly
`sdk`.

**Real-interaction checks (all mandatory):**
- (a) Standing: `make check` green. `examples/minimal` :8081 → `GET /` 200,
  `GET /products/widget-3000` 200; killed; port free.
- (b) Auth flow unchanged: `examples/auth-cms` :8082 cookie-jar →
  `GET /articles` 401 → register 201 → login 200+cookie → `GET /articles` 200 →
  logout 200 → `GET /articles` 401. Exact.
- (c) Deny-by-absence: same booted host (signer unwired) → `POST /auth/token`
  → **404**. Killed; port free.
