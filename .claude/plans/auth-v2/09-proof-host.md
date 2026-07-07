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
