# auth: JWT sessions + refresh rotation as the default (amends AV6)

**Status: RATIFIED 2026-07-11.** Owner directive (2026-07-11, via segovia);
review gate run same day (lead-backend, architecture-steward,
data-integration, platform-sre, product-manager — three re-plan verdicts,
zero boundary violations; all findings folded in below). This reverses
ratified AV6's "no refresh" arm and supersedes §4.4's framing of JWTs as
a side convenience — the JWT becomes the primary access credential; the
session row becomes the revocation + refresh anchor.

Module naming note: the feature is `features/authentication` (not
`features/auth`); all paths below use the real tree.

**AMENDED 2026-07-12 (greenfield-migrations owner ruling):** D6's
"single destructive migration `0014_sessions_refresh.sql`" is superseded —
feature canonical migration sets are greenfield and carry no evolution
files. The re-keyed sessions schema now ships directly in
`0003_sessions.sql`'s CREATE (0014 deleted, 0012/0013 likewise folded into
their base files; the set is `0001…0011`). Upgrading hosts write their own
migration in their host tree (segovia's `0018_sessions_refresh.sql` is the
exemplar); the runbook language moved to the README/RELEASING host-upgrade
notes. See the NOTES.md 2026-07-12 entry.

## 0. Ratified decisions (were §4 open questions + gate calls)

| # | Decision |
| --- | --- |
| D1 | **`RequireLiveSession`** is a distinct named middleware (root-package re-export, matching RequireUser/RequirePrincipal/RequireServiceAccount), ratified **with** the per-principal matrix (§1.4) and a **fail-CLOSED** posture on repository error (RequirePermission precedent, D-D). |
| D2 | **Fixed refresh expiry.** `ExpiresAt` is set at mint and never touched by rotation. Sliding (with absolute cap + idle window) is deferred behind a concrete demand trigger; pure sliding is rejected (unbounded stolen-token lifetime). |
| D3 | **The opaque-session legacy mode is cut.** One model, one test matrix. `Config.TokenSigner == nil` → hard construction error `ErrTokenSignerRequired` (Hasher/Mailer precedent). The core never synthesizes an ephemeral key; that convenience lives in example hosts only. All `tokenSigner == nil` guards and the `AUTH_JWT_DISABLED` branch are deleted. |
| D4 | **Two cookies**, refresh cookie **path-scoped to `/auth`** (not `/auth/refresh` — logout needs it, §1.5), both HttpOnly, `SameSite=Lax` explicit on both. |
| D5 | **Reuse-detection bound: one generation** (the most recent rotated-away token), made real by a `previous_used` consumed-flag — see §1.3. The "rotated-away family" clause of the draft is struck as unimplementable with hash-only storage; a family-id column and a two-slot previous were considered and declined (schema cost > marginal detection gain). |
| D6 | **Single destructive migration** (`0014`, both dialects): drop + recreate sessions. The upgrade note forbids rolling deploys across it and states rollback = restore old schema + second forced logout. The two-phase add-then-drop path was considered and declined (no current multi-instance rolling-deploy host). |
| D7 | **A stdlib HS256 default signer ships in `sdk/foundation/cryptids`** (§5) — sanctioned by the sdk decision rule (stdlib-only, vendor-neutral impl of an sdk port). Restores zero-integration boot now that the signer is required. `integrations/cryptids/golang-jwt` remains for RS256/ES256. |
| D8 | **`TokenTTL` is removed, not repurposed.** New fields `AccessTokenTTL` (default 15m) and `RefreshTTL` (default 7d). The rename is a deliberate compile-time break so no host silently inherits a 1h access window. |

## 1. The target model (one mint path, two verification tiers)

### 1.1 Mint (login / register / OAuth callback / token endpoint — all through the one `mintSession` path)

- A **session row** — the revocable anchor: `{ID, UserID,
  RefreshTokenHash, PreviousRefreshTokenHash (nullable), PreviousUsed,
  RotationCount, CreatedAt, ExpiresAt}`. No access-token hash: the access
  credential is self-validating and never persisted.
- **`session.ID` is app-minted unconditionally** (the existing secrets
  generator in `domain/session`), NOT `Config.IDs`-governed — the JWT is
  signed with `session_id` before/independent of the insert, so a
  DB-generated key (RETURNING) cannot work. Sessions stay excluded from
  the `0012` id-default pattern. Document on the field.
- An **access JWT** — claims `{user_id, session_id, exp, iat}`, TTL
  `AccessTokenTTL` (default **15m**). Browser flows carry it in the
  existing session cookie (HttpOnly, `SameSite=Lax`); API flows get it in
  the JSON body.
- An **opaque refresh token** — hashed into the session row (SHA-256 at
  rest, design §7.3 posture), TTL `RefreshTTL` (default **7d**, fixed
  horizon per D2). Browser: second HttpOnly cookie, `Path=/auth`,
  `SameSite=Lax`. API: JSON body. The raw token is never persisted or
  logged anywhere.

### 1.2 Verify — the route-based seam

| Middleware | Cost | Semantics |
| --- | --- | --- |
| `RequireUser` / `RequirePrincipal` (default) | zero DB for JWT credentials | JWT signature + expiry only. Revocation honored within ≤ `AccessTokenTTL`. |
| `RequireLiveSession` (new) | one PK lookup | Per-principal matrix (§1.4): user JWTs get `sessions.Get(claims.session_id)` — deleted/expired row denies. Immediate revocation. For sensitive routes (password change, key minting, invitations, secret reads…). **Fails CLOSED on repository error.** |

API keys are untouched (opaque, always DB — the machine rail, per the
original AV6 finding, which stays correct).

JWT verification applies a small clock-skew leeway (30–60s) — document
on the Config field / signer.

### 1.3 Refresh (`POST /auth/refresh`) — the pinned rotation contract

The write path is **compare-and-swap, never blind** (three reviewers
independently: blind update lets racing refreshes corrupt the chain and
false-revoke honest clients).

Resolve `H = hash(presented token)` via ONE port call,
`GetByRefreshHash(H) → (Session, match ∈ {current, previous})`
(single `WHERE refresh_token_hash = ? OR previous_refresh_token_hash = ?`
scan; **never matches empty/NULL input**; returns the row **verbatim** —
expiry is a service branch, apikey.GetByHash precedent). Then:

1. **No row** → generic 401 invalid credential. (No family detection —
   D5. A token ≥2 rotations stale is indistinguishable from garbage;
   this bound is documented, not hidden.)
2. **Row expired** → 401, re-login (fixed horizon, D2).
3. **Matched `current`** → rotate: mint new refresh token T′; CAS
   `Rotate(id, expectedCurrentHash=H, newHash=hash(T′))` which atomically
   sets `previous ← H`, `previous_used ← false`, `rotation_count++`,
   **does not touch `ExpiresAt`** (D2). Rows-affected 0 →
   `ErrRotationConflict` → re-resolve once: if H now sits in `previous`,
   fall through to the grace path (benign race); if H matches nothing,
   treat as reuse (revoke + `refresh_reuse`). On success: respond with
   new access JWT + T′ (browser: both Set-Cookies).
4. **Matched `previous`, `previous_used == false`** → **single-use
   grace** (racing client / lost response): CAS
   `ConsumeGrace(id, previousHash=H)` flips `previous_used ← true`
   (0 rows → re-resolve, as above). Respond with a **new access JWT
   only** — no new refresh token, no refresh Set-Cookie (cookie clients
   keep the winning token from the concurrent rotation; that's the
   self-heal). Record `refresh` (success) with a `grace` detail.
5. **Matched `previous`, `previous_used == true`** → **reuse**: revoke
   the session (`Delete(id)`), record `refresh_reuse` (blocked), 401.

Theft collapses correctly under this contract: a thief on the stale
token gets at most one 15m access JWT (grace), and the second arrival on
the consumed slot — thief or victim — burns the session and forces
re-login, locking the thief out.

### 1.4 `RequireLiveSession` per-principal matrix

| Credential presented | Behavior |
| --- | --- |
| Session-cookie / bearer **user JWT** | verify JWT, then `sessions.Get(session_id)`; missing/expired row → deny |
| **API key** | already DB-checked by resolution; pass (no session row exists — a naive lookup would reject every machine caller on exactly the routes this targets) |
| No/invalid credential | deny (as RequirePrincipal) |
| Repository error | **deny (fail CLOSED)** — never harmonize toward the limiter's fail-open |

### 1.5 Revoke

- **Logout** deletes the session row AND **clears both cookies**
  browser-side (otherwise "I logged out and I'm still in" for up to
  15m on stateless routes — shared-computer hazard). Session resolution
  for logout: primary lane is the refresh cookie (`Path=/auth` covers
  the logout route, D4) → resolve by hash → `Delete(id)`; fallback lane
  (API bearer, no refresh cookie) parses the access JWT
  **ignoring expiry** solely to read `session_id` for the delete. An
  expired access JWT must never make logout a no-op.
- **Password change / reset** deletes all the user's sessions — but this
  is no longer instantly global as in v1: stateless routes honor
  outstanding access JWTs for ≤15m. **Security-relevant behavior change
  from v1; documented**, and the mitigation is routing sensitive
  surfaces through `RequireLiveSession`.
- DB-checked routes see revocation immediately; stateless routes within
  the access TTL. The §4.4 revocation-asymmetry note survives but is now
  *bounded and chosen per route* rather than global.

### 1.6 Signing-key operational truth (replaces the draft's "ephemeral key is painless" claim)

- **Multi-instance hosts MUST share `AUTH_JWT_SECRET`** — per-instance
  ephemeral keys cannot cross-verify; behind a load balancer that is a
  continuous auth-flap storm on every request, not a restart event, and
  `/auth/refresh` round-robins into the same wall. Stated on the Config
  field and in the upgrade note.
- Ephemeral keys are a **single-instance dev convenience only** (example
  hosts), where the recovery story is real: restart kills access JWTs,
  clients recover via `/auth/refresh` without re-login — **for API
  clients**. Browser flows recover only if something drives the refresh:
  the auth-cms proof (§7) pins the browser lane (on 401-from-expired-JWT
  with a refresh cookie present, the client/page calls `/auth/refresh`
  and retries; no transparent middleware re-mint in this milestone).
- **Key rotation**: single-key HS256, no `kid`, deliberately no
  zero-downtime dual-key path in this milestone. Rotating the secret
  mass-invalidates access JWTs (recovered via refresh — refresh tokens
  are opaque/DB-backed and survive); named as a deferred option
  (kid-based dual-verify), not scope.

## 2. Contracts (the spec storetest pins BEFORE any adapter is written)

### 2.1 Repository port (`features/authentication/domain/session/repository.go`)

```
Create(ctx, Session) error                     // unchanged posture; MapError-routed
Get(ctx, id) (Session, error)                  // ErrNotFound; ErrExpired at read (existing posture) — backs RequireLiveSession
GetByRefreshHash(ctx, hash) (Session, RefreshMatch, error)
                                               // ONE method, current-or-previous; verbatim row (no expiry filter);
                                               // empty/NULL hash never matches; ErrNotFound otherwise
Rotate(ctx, id, expectedCurrentHash, newHash) error
                                               // CAS; 0 rows → ErrRotationConflict; sets previous←expected,
                                               // previous_used←false, rotation_count++; never touches expires_at
ConsumeGrace(ctx, id, previousHash) error      // CAS on (previous==hash AND !previous_used); 0 rows → ErrRotationConflict
Delete(ctx, id) error                          // unknown id → ErrNotFound
DeleteByUser(ctx, userID) error                // bulk, idempotent (nil on zero rows)
```

`RefreshMatch` is a small domain enum (`RefreshMatchCurrent`,
`RefreshMatchPrevious`). New sentinel `ErrRotationConflict` joins the
extended sentinel-contract doc block (which also records the
verbatim-return rule, mirroring the apikey.GetByHash wording).

### 2.2 Schema (`0014_sessions_refresh.sql`, BOTH `stores/pgx/migrations` and `stores/turso/migrations`; NEVER edit `0003` — the runner checksums applied migrations)

- `id` TEXT PRIMARY KEY (app-minted)
- `user_id` NOT NULL + **`idx_sessions_user_id`** (DeleteByUser full-scans today; the table is being rebuilt anyway)
- `refresh_token_hash` NOT NULL + **UNIQUE index** (live credential; collisions surface via MapError as conflict)
- `previous_refresh_token_hash` **nullable** — never `TEXT NOT NULL DEFAULT ''` (every fresh session would share `''` and `GetByRefreshHash` could match an arbitrary fresh row: cross-session bleed). Partial index `WHERE previous_refresh_token_hash IS NOT NULL` (0011 precedent; both dialects support it). Turso scans via `sql.NullString`.
- `previous_used` boolean NOT NULL DEFAULT false
- `rotation_count` INTEGER NOT NULL DEFAULT 0
- `created_at`, `expires_at` per existing dialect conventions
- Body: `DROP TABLE IF EXISTS sessions;` + CREATE + indexes (IF EXISTS so squashed/renumbered host trees stay order-independent). No `token` column survives.
- **All session write paths route driver errors through `MapError`** — turso `sessions.Create` currently returns raw errors; that gap becomes load-bearing with the unique hash index. Audit pgx twins.

### 2.3 storetest conformance cases (the sub-runner re-keyed to `id`; existing CreateGetDelete / ExpiredAtRead / DeleteByUser rewritten)

1. `Get(id)` round-trip; unknown → ErrNotFound; expired → ErrExpired.
2. `GetByRefreshHash`: current-match round-trip; unknown → ErrNotFound; **expired row returned verbatim** (service branches).
3. Rotation: hashA→hashB sets previous=hashA, previous_used=false, rotation_count++; resolve(hashB)=(row, current); resolve(hashA)=(row, previous).
4. Single-slot grace: second rotation hashB→hashC → resolve(hashA) → ErrNotFound.
5. CAS conflict: `Rotate` with stale expectedCurrentHash → ErrRotationConflict, row unchanged.
6. `ConsumeGrace`: flips previous_used once; second call → ErrRotationConflict.
7. Empty-previous guard: fresh session (NULL previous) round-trips; `GetByRefreshHash("")` → ErrNotFound, never a fresh row.
8. Rotation does not modify expires_at (D2).
9. `Delete(unknown)` → ErrNotFound; `DeleteByUser` idempotent, other users untouched.

### 2.4 Implementations to re-key — there are FOUR, not two

`stores/turso`, `stores/pgx`, **and two memory impls**:
`examples/auth-cms/internal/authmem/authmem.go` and the storetest
reference impl (`features/authentication/storetest/reference_test.go`) —
both currently key sessions by `s.Token` in one map. Both need: primary
map by ID, secondary hash lookups, the uniqueness invariant, single-slot
grace + consumed flag, CAS conflict on stale expected-hash, and the
empty-previous guard.

## 3. Change inventory

| # | Where | What |
| --- | --- | --- |
| 1 | `domain/session` | Struct per §1.1; `NewSession` mints ID (app-owned, D-note §1.1) + refresh material; access token no longer persisted. `RefreshMatch`, `ErrRotationConflict`. |
| 2 | `domain/session/repository.go` | Port per §2.1; sentinel doc block extended; **storetest updated first** (it is the spec, §2.3). |
| 3 | Stores ×4 (§2.4) | New shape + `0014` migrations (§2.2). UPGRADE NOTE per §6. |
| 4 | `authsvc` | `mintSession` → mint-pair; `Refresh` per §1.3; `ValidateSession` becomes the RequireLiveSession lookup; cookie path of `resolveUserID`/`resolvePrincipal` verifies the JWT statelessly (no store hit); logout lanes per §1.5. **Signature-change inventory (public surface):** `Service.Login`, `ChangePassword`, `IssueToken` return the pair; inbound `authService` interface follows; `OAuthResult` gains the refresh token; all six `mintSession` call sites (service.go:421/496, oauth.go:175/228/307, token path) updated; **every `SetSessionCookie` site sets BOTH cookies** — enumerate at implementation, or OAuth paths ship half-wired. `verifyBearer` stays user_id-only; a separate session_id claim reader serves RequireLiveSession + the logout fallback (ignore-expiry parse). |
| 5 | Config | `TokenSigner` **required** → `ErrTokenSignerRequired` on nil (D3). `TokenTTL` **removed**; `AccessTokenTTL` (15m) + `RefreshTTL` (7d) added (D8). Field docs carry: multi-instance shared-key requirement, ephemeral = dev/single-instance only, clock-skew leeway, revocation-asymmetry bound. |
| 6 | Routes | `POST /auth/refresh` — rate-limited **by IP and by session family** (per-process memory limiter is N× budget across N instances and NAT users share an IP bucket; multi-instance hosts must wire a shared limiter — documented). `POST /auth/token` returns the session-backed pair — **breaking response-contract change**, named in the upgrade note; auth-cms leg + README transcripts updated in §7. |
| 7 | Security events | `refresh` (success; `grace` detail on the grace lane), `refresh_reuse` (blocked). Details carry `session_id`, `rotation_count`, IP, UA — **never the raw token**. **`refresh_reuse` additionally emits an unconditional WARN via `Config.Logger`** regardless of whether SecurityEvents is wired (a nil-audit host must not be blind to token theft). Event vocabulary is schema-free (TEXT column) — no migration. |
| 8 | sdk | HS256 default signer per §5 (D7). |
| 9 | Docs | feature README §-rewrite; design doc §4.4 amendment recorded; middleware table gains the two-tier row; **RELEASING.md entries for `features/authentication` AND both store-module tags** (0013-entry precedent), runbook per §6. |

## 4. Middleware & routing notes

- `RequireLiveSession`: root-package re-export of an `internal/`
  implementation, alongside the existing gates; matrix §1.4; fail CLOSED.
  Its only dependency is `Repositories.Sessions` (already held) — no new
  edge, no guard changes anywhere in this plan (TokenSigner is an sdk
  port type; feature core stays sdk-only; `examples/minimal` wires no
  authentication and is untouched).
- CSRF: the refresh endpoint is cookie-driven and state-changing —
  `SameSite=Lax` explicit on the refresh cookie (D4).

## 5. sdk HS256 default signer (D7 — scoped sub-task)

`sdk/foundation/cryptids`: a stdlib HS256 implementation of `JWTSigner`
(crypto/hmac + base64url + encoding/json). Constraints:

- Hardcoded HS256; **reject every other `alg`** on verify (alg-confusion
  class); constant-time MAC comparison; exp/iat validation with the
  §1.2 leeway.
- Cross-verification tests live in `integrations/cryptids/golang-jwt`
  (sdk must stay dependency-free even in tests): golang-jwt verifies
  sdk-minted tokens and vice versa, plus static test vectors.
- `jwt.go`'s "signing requires a third-party library" comment corrected.
- `guard-sdk-stdlib` already covers it structurally.

## 6. Migration + release runbook (D6)

- `0014_sessions_refresh.sql` in both store modules, identical filenames;
  ledger keys `(source, version)` so the drop lands once per environment.
- Upgrade note (RELEASING.md + feature README, hash-cutover precedent
  expanded for a *shape* change): (1) all live sessions invalidate —
  every user re-logs-in; (2) **do not roll-deploy across this migration**
  — old binaries SELECT a dropped column and error, not flap; stop old,
  migrate, start new; (3) rollback requires restoring the old schema and
  forces a second logout; (4) `AUTH_JWT_SECRET` now required-shared for
  multi-instance (§1.6); (5) `TokenTTL` → `AccessTokenTTL`/`RefreshTTL`
  compile-time rename; (6) `/auth/token` response contract change; (7)
  port re-key = breaking version bump for the feature and both nested
  store-module tags.
- segovia re-export/renumber: `IF EXISTS` keeps the renumbered copy
  order-independent (accepted R2 cost).

## 7. Proof phase — `examples/auth-cms` (upstream, BEFORE the segovia carry)

auth-cms is the in-repo proof host and breaks under this change; it gets
an explicit phase mirroring auth-v2's A9:

- Re-wire: signer required (env `AUTH_JWT_SECRET`, ephemeral fallback
  stays **here**, with the multi-instance WARN), `AccessTokenTTL`/
  `RefreshTTL` env, two-cookie handling, authmem re-key (§2.4).
- Mount `RequireLiveSession` on a real sensitive route (password change
  or API-key mint).
- Executable legs (README curl transcripts, the existing leg set):
  refresh-rotation happy path; grace lane (double refresh);
  reuse-after-grace → revocation + `refresh_reuse`;
  logout-then-stateless-window (RequireUser 200 within TTL) vs
  logout-then-live-deny (RequireLiveSession 401 immediately);
  ephemeral-restart recovery (API lane).
- Browser denial UX: host-rendered pages redirect revoked-but-valid-JWT
  users to login on RequireLiveSession denial (401 for API accepts) —
  the pattern segovia authpages copies.
- Docs phase ships the "one signer, ephemeral dev key, defaults"
  quickstart (D3 condition).

**Verification gate for every phase: `make check` (per-module
build/vet/test + layering guards) + dual-dialect storetest green.**

## 8. Host carry (segovia, after upstream lands)

- Migration re-export + renumber into `primary/` (plan 08 R2 cost, accepted).
- `.env`: `AUTH_JWT_SECRET` (shared across instances), access + refresh TTLs.
- Route audit: pick the `RequireLiveSession` set (secrets values,
  invitations, grants are the obvious candidates) — audit confirms
  deployment topology against §1.6.
- `workshop/dev/proof.sh` legs mirroring §7's set.
- authpages: login/register unchanged (cookie mint is server-side);
  logout unchanged besides the double cookie clear.

## 9. Scope rationale (recorded so the next review doesn't re-litigate)

Rotation + reuse detection is deliberately in scope even though the
segovia driver is satisfied by stateless-JWT + RequireLiveSession alone:
a non-rotating 7-day refresh secret is a strictly worse long-lived
bearer credential, and the v1 pattern is proven. (PM finding #7.)

## EXECUTION LOG — 2026-07-11

Ratified and executed the same day (five-reviewer gate; three re-plan
verdicts folded in above). All phases green under `make check` (per-module
build/vet/test + layering guards) with no guard changes and no new sdk
dependency; upstream only — the segovia host carry (§8) remains open.

**Phases A–E (all COMPLETE):**

- **A — sdk HS256 default signer (§5, D7).** `sdk/foundation/cryptids.NewHS256`:
  a stdlib HS256 `JWTSigner` (crypto/hmac + base64url + encoding/json),
  alg-hardened (rejects every non-HS256 `alg`), constant-time MAC compare, 60s
  clock-skew leeway on exp/iat. Cross-verification tests live in
  `integrations/cryptids/golang-jwt` (sdk stays dependency-free even in tests):
  golang-jwt verifies sdk-minted tokens and vice versa. `jwt.go`'s stale
  "signing requires a third-party library" comment corrected.
- **B — domain + port + spec.** `domain/session` re-keyed to
  `{ID, UserID, RefreshTokenHash, PreviousRefreshTokenHash, PreviousUsed,
  RotationCount, CreatedAt, ExpiresAt}` (app-minted ID, access token never
  persisted); port re-keyed to `Get`, `GetByRefreshHash → (Session, RefreshMatch,
  error)`, CAS `Rotate`/`ConsumeGrace`, `Delete`, `DeleteByUser`, `Create`; new
  `ErrRotationConflict`; `RefreshMatch` enum. The nine storetest conformance
  cases (§2.3) written FIRST as the spec, and both memory impls (storetest
  reference + `examples/auth-cms/internal/authmem`) re-keyed to id-primary with
  secondary hash lookups, uniqueness invariant, single-slot grace + consumed
  flag, CAS conflict, and the empty-previous guard.
- **C — stores + migrations.** `stores/turso` and `stores/pgx` rewritten to the
  new shape; `0014_sessions_refresh.sql` in both dialect migration trees
  (destructive `DROP + CREATE`, UNIQUE `refresh_token_hash`, partial
  `previous_refresh_token_hash` index, `user_id` index). turso `Create` is now
  `MapError`-routed (the gap that became load-bearing under the unique index).
- **D — service, config, routes, middleware.** `mintSession` → mint-pair;
  `Service.Refresh` (§1.3 CAS contract); `Service.RequireLiveSession`
  (fail-closed, per-principal matrix); `Login`/`ChangePassword`/`IssueToken`
  return `TokenPair`; `Logout(ctx, refreshToken, accessToken)` two lanes;
  `SetSessionCookies`/`ClearSessionCookies` (access cookie `Path=/`, refresh
  cookie `Path=/auth` `SameSite=Lax`). Config: `TokenSigner` required
  (`ErrTokenSignerRequired`); `TokenTTL` removed → `AccessTokenTTL` (15m) +
  `RefreshTTL` (7d). Routes: `POST /auth/refresh` (IP + per-session rate limits);
  `POST /auth/token` returns `{access_token, expires_at, refresh_token}`;
  `/auth/password/change` `RequireLiveSession`-gated; `/auth/logout` ungated.
  securityevent vocabulary gains `refresh` / `refresh_reuse` (the latter always
  WARNs via `Config.Logger` even with nil SecurityEvents).
- **E — proof host (`examples/auth-cms`).** Re-wired to the sdk stdlib HS256
  signer (golang-jwt dependency dropped), `AUTH_ACCESS_TOKEN_TTL` /
  `AUTH_REFRESH_TTL`, two-cookie handling, `RequireLiveSession` on
  `POST /demo/admin/bootstrap`. README legs a–f (refresh happy path; grace;
  reuse-after-grace → revocation + `refresh_reuse`; logout-then-stateless-window
  vs logout-then-live-deny; ephemeral-restart recovery) added.

**Live-proof artifacts.** All nine session storetest conformance cases green
against BOTH live dialects — Postgres 17 and libsql-server containers. auth-cms
legs a–f drive the HTTP surface end-to-end; the curl transcripts live in the
`examples/auth-cms` README.

**Recorded deviations:**

- **(a) `/auth/logout` is ungated.** Gating it on a live credential would make
  logout a no-op exactly when an expired access JWT needs to log out
  (shared-computer hazard). Ungating keeps BOTH documented resolution lanes
  (refresh cookie primary, access-JWT fallback) reachable.
- **(b) The logout fallback parses the access JWT WITHOUT signature verification**
  to read `session_id` for the delete. The `JWTSigner` port has no
  verify-ignoring-expiry method, so `sessionIDIgnoringExpiry` base64url-decodes
  the payload directly. **Risk assessed LOW:** session IDs are unguessable
  high-entropy secrets, the value is used SOLELY to target a `Delete` (it
  authorizes nothing), and possession of the JWT already implies the ability to
  log out. A signature-verified variant needs an sdk port addition
  (verify-ignoring-expiry) — **deferred**.
- **(c) leg-f (store-backed restart recovery) is unobservable against the
  in-memory `authmem`** proof store: a process restart clears the map, so the
  cross-restart survival of refresh tokens cannot be shown on auth-cms. Noted in
  the auth-cms README; it is provable only on a persistent store.
- **(d) Pre-existing pgx pagination-collision conformance flakiness** (unrelated
  to this change) was observed and flagged for a separate follow-up; it does not
  touch the session suite.
