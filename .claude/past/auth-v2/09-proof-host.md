# Phase A9 — proof host: `examples/auth-cms` extension + the full protocol

Status: RATIFIED (cut from design §13 A9, as amended by the review gate)
Executor model: opus
Depends on: A2–A6 (A4 included — the JWT leg needs `/auth/token`).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §13 A9 (the
protocol legs, verbatim), §3/§4/§5.1/§6 (the behaviors each leg proves),
§6's milestone-stranding resolution (the toy membership Granter IS the
demonstration of the ruling — invitations provably work with no ReBAC in
the module graph). **Green tests alone do not close this phase.**

## Work items

1. **`internal/authmem`** grows in-memory implementations of the new
   ports the protocol needs: oauth accounts + states, service accounts +
   api keys, security events, invitations (uniqueness and sentinel
   honesty per the ports — the memstore-honest lesson).
2. **Host wiring in `cmd/server/main.go`**: a host-local **fake OAuth
   provider** (a type implementing `sdk/oauth.Provider` against a local
   stub — no external vendor); `integrations/cryptids/golang-jwt` as
   `Config.TokenSigner` — secret from env, ≥32 bytes; **absent env
   secret → an EPHEMERAL RANDOM key generated at boot (plan-cut
   amendment, SRE) — NEVER a hardcoded demo constant (this host lands on
   public GitHub; a committed signing key is a leak). Ephemeral preserves
   zero-infra boot; tokens just don't survive restart (documented in the
   host README)**; a **toy membership Granter** (in-memory map
   `resource→subject→relation`) + one host-local demo route gated on
   that membership; `RequireVerifiedEmail: true` — **heads-up (plan-cut
   amendment): this may break existing auth-cms example tests that
   assert login-without-verify; fixing them is in-scope for this
   phase**; security-events repo wired; an env flag
   (`AUTH_JWT_DISABLED=1`) that boots the signer-nil variant for the
   absent-signer leg; **the debug route `GET /debug/security-events` is
   env-gated DEFAULT-OFF (`AUTH_DEBUG=1`) AND session-gated
   (`RequireUser`) — plan-cut amendment, SRE: it dumps IP/UA/emails and
   this host is public** (host code, not feature surface).
3. **README**: the full protocol below as copy-pasteable curls.
4. **`.env.example`** (plan-cut amendment, SRE): add the new keys as
   SECRET-FREE placeholders with comments — the JWT signing secret,
   `AUTH_JWT_DISABLED`, `AUTH_DEBUG`, OAuth client-id/secret, and the
   token-encrypter key. No real value ever lands in the file.

## The protocol (run-and-look; record exact commands + codes per leg)

0. **Amended five-step (verified-email gate ON)**: `GET /articles` → 401;
   `POST /auth/register` → 201; `POST /auth/login` BEFORE verify → 403
   (the gate, demonstrably on); read the code from the console-mailer
   log → `POST /auth/verify` → 200; login → 200 + cookie; `GET /articles`
   → 200; logout → 200; repeat → 401.
1. **OAuth (fake provider)**: `GET /auth/oauth/fake/start` → 302 (note
   the state + PKCE params in the Location); drive the callback
   `GET /auth/oauth/fake/callback?code=…&state=…` → new-user path mints a
   session (assert with a gated GET → 200); `GET /auth/oauth/linked` →
   200 listing the link. Re-run start/callback for the SAME provider
   identity → login path (no duplicate account).
2. **API key machine call**: with a session — create a service account,
   mint a key (response carries plaintext once); then WITHOUT any cookie:
   `curl -H "Authorization: Bearer <key>" localhost:8082/<RequirePrincipal-gated demo route>`
   → 200; revoke the key → same call → 401.
3. **JWT bearer**: `POST /auth/token` `{email, password}` → 200
   `{token, expires_at}`; `Bearer <jwt>` against the RequirePrincipal
   route → 200; a token minted with TTL≈1s, after sleep → 401
   (expired); reboot with `AUTH_JWT_DISABLED=1` → the SAME valid-looking
   JWT → 401 and `/auth/token` → 404 (absent-signer path: bearer JWTs
   never parsed).
4. **Invitations (toy Granter)**: as user A create an invitation for
   user B's email on the demo resource → mail logged with the token;
   as B (registered + verified) `POST /auth/invitations/accept` → 200;
   the toy membership map now grants B → B hits the membership-gated demo
   route → 200 (and a third user → 403/404); decline path on a second
   invitation → status declined, no grant.
5. **Audit rows visible**: with `AUTH_DEBUG=1` and a valid session,
   `GET /debug/security-events` shows the rows the legs above produced
   (register, blocked login, verify, login, oauth_register/login,
   apikey_auth success+failure, token_issued, invitation events) — paste
   a trimmed dump into the execution log; without `AUTH_DEBUG` → 404,
   without a session → 401.

Plus: `go list -m all | grep -i libsql` → empty (still no datastore
driver); rule-6 greps clean both directions (import-anchored, plan-cut
form):
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
and
`grep -rn --include='*.go' '"github.com/gopernicus/gopernicus/features/auth' features/cms/`
both empty.

## Acceptance

```sh
cd examples/auth-cms && go build ./... && go vet ./... && go test ./...
make check
```

Plus the full protocol above, all legs, exact codes in the execution log.

## Real-interaction check

The protocol IS the check. Also run standing check (a).

## Execution log

(append dated entries here)

### A9 — 2026-07-07 — PASS

Executor: opus. All work items landed; the full run-and-look protocol passed
live against the booted host (`HOST=localhost PORT=8082`, env pinned per boot).
Green tests alone did not close it — every leg was driven with curl and the exact
HTTP codes recorded below.

**Files changed**

- `examples/auth-cms/internal/authmem/authmem.go` — package doc + `data` fields +
  `New`/`Repositories` now wire all twelve auth ports (v1 + the six v2 ports).
- `examples/auth-cms/internal/authmem/ports_v2.go` — NEW: honest in-memory impls
  of oauthaccount/oauthstate/serviceaccount/apikey/securityevent/invitation
  (uniqueness + sentinel honesty mirroring the storetest reference; shared keyset
  `page` helper, the jobs-memstore precedent). `storetest.Run` against authmem now
  exercises the new sub-runners (0 skips, was 6 skipped groups).
- `examples/auth-cms/cmd/server/main.go` — host wiring: RequireVerifiedEmail=true,
  fake provider in Providers, golang-jwt TokenSigner (env/ephemeral/nil), toy
  Granter, TokenEncrypter (optional), demo + debug route registration.
- `examples/auth-cms/cmd/server/oauthfake.go` — NEW: host-local fake
  `oauth.Provider` (no vendor; identity derived from the code).
- `examples/auth-cms/cmd/server/membership.go` — NEW: toy membership `Granter`
  (in-memory `resource→relation→subject`) + `requireMembership` middleware.
- `examples/auth-cms/cmd/server/demo.go` — NEW: the two demo routes, the
  DEFAULT-OFF session-gated debug route, and the env-driven signer/encrypter/TTL
  builders.
- `examples/auth-cms/go.mod` / `go.sum` — added golang-jwt (require + replace).
- `examples/auth-cms/.env.example` — NEW: secret-free placeholders (JWT secret,
  AUTH_JWT_DISABLED, AUTH_TOKEN_TTL, AUTH_TOKEN_ENCRYPTER_KEY, AUTH_DEBUG, OAuth
  client id/secret).
- `examples/auth-cms/README.md` — full protocol as copy-pasteable curls + the
  nil-semantics table.

**Per-leg transcript (exact codes)**

Leg 0 (verify gate ON, `AUTH_DEBUG=1`, fixed `AUTH_JWT_SECRET`):
`GET /articles` 401 → register 201 → login-before-verify **403** → verify 200
(code read from console-mailer log) → login 200+cookie → `GET /articles` 200 →
logout 200 → `GET /articles` 401. (Re-login 200 to hold an admin session.)

Leg 1 (OAuth fake provider): `GET /auth/oauth/fake/start` **302** — Location
`https://oauth.fake.local/authorize?code=…&code_challenge=…&code_challenge_method=S256&redirect_uri=…&state=…`
(state + PKCE visible). Callback (new-user path) **302** + session cookie;
`GET /demo/whoami` **200** (`principal_type:user`); `GET /auth/oauth/linked`
**200** `[{"provider":"fake","provider_email":"oauth-user@fake.local",…}]`.
Re-run start+callback, same identity → **302** login path; linked still a single
entry (no duplicate account).

Leg 2 (API key): session create SA 201, mint key 201 (plaintext once);
no-cookie `Bearer <key>` on `/demo/whoami` **200** (`principal_type:service_account`);
revoke 200; same call **401**.

Leg 3 (JWT): `POST /auth/token` **200** `{token, expires_at}`; no-cookie
`Bearer <jwt>` on `/demo/whoami` **200** (`principal_type:user`). Expired path
(reboot `AUTH_TOKEN_TTL=1s`): fresh token 200, after `sleep 2` **401**.
Absent-signer path (reboot `AUTH_JWT_DISABLED=1`, same secret): the SAME valid
JWT → **401** (never parsed); `POST /auth/token` **404** (route absent).

Leg 4 (invitations, toy Granter): B pre-grant `GET /demo/members-only` **403**;
A invites B on `project/demo` **201** pending (token from mail log); B accepts
**200**; B `GET /demo/members-only` **200** (granted); C `GET /demo/members-only`
**403** (non-member). Second invitation (D) 201; `POST …/decline` **200** →
status `declined`, no grant.

Leg 5 (audit): `AUTH_DEBUG=1` + admin session `GET /debug/security-events`
**200**; no session **401**; reboot without `AUTH_DEBUG` → **404**.

**Trimmed audit dump (22 rows the legs produced):**

```
3 register/success        4 login/success         1 login/failure (login-before-verify gate)
3 email_verified/success  1 logout/success
1 oauth_register/success  1 oauth_login/success   (re-run same identity)
2 apikey_auth/success     1 apikey_auth/blocked   (revoked-key denial)
1 token_issued/success
2 invitation_created      1 invitation_granted/success  1 invitation_declined/success
```

**Extra checks:** `GOWORK=off go list -m all | grep -i libsql` → empty; both
rule-6 greps (auth↛cms/jobs/authorization; cms↛auth, import-anchored) → empty.
Ephemeral-key sanity boot (no `AUTH_JWT_SECRET`): WARN logged, `POST /auth/token`
still 200 that boot. Standing check (a): `examples/minimal` (:8081) `GET /` 200,
`GET /products/widget-3000` 200; killed, port free. Port 8082 confirmed free
after every boot variant and at the end.

**Acceptance:** `cd examples/auth-cms && go build ./… && go vet ./… && go test
./…` all green (no existing host test asserted login-without-verify, so the
RequireVerifiedEmail flip broke none); root `make check` → **all checks passed**.

**Divergences / interpretation notes (none blocking):**

1. **Two demo routes, not one.** The phase protocol names a
   "<RequirePrincipal-gated demo route>" (legs 2/3) and a distinct
   "membership-gated demo route" (leg 4). A single membership-gated route cannot
   return 200 for leg 2's freshly-minted service account (never a member), so the
   host ships `GET /demo/whoami` (RequirePrincipal only) and `GET /demo/members-only`
   (RequirePrincipal + toy membership). Both are host-local demo routes; the
   literal reading of the two protocol descriptors is honored.
2. **apikey_auth success + blocked** (not "success + failure"): the leg-2 denial
   is a REVOKED key, which design §4.1 records as `blocked` (service-account
   attributed). The `failure` status is the expired-key branch, not exercised by
   the revoke leg — a faithful mapping, not a gap.
3. **"blocked login"** in the leg-5 list is the login-before-verify 403, recorded
   as `login/failure` (the verified-email gate denial); `login/blocked` is the
   rate-limit branch, not exercised.
