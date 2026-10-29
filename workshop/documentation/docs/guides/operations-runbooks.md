---
title: Operations runbooks
description: Deactivate or erase a user, revoke sessions, rotate a key, handle an abusive IP, read the audit rails, and run retention.
---

# Operations runbooks

Procedures a host operator runs with what the framework ships on `main` at 2026-10-23.
Each names the bundled route or service call, what it records, and what is missing.

## Deactivate or reactivate a user

- **Bundled routes** (mounted only when the host wires `UserAdminCheck` plus the
  `UserAdmin` and `ActiveSessions` repositories): `POST /auth/admin/users/{id}/deactivate`,
  `POST /auth/admin/users/{id}/reactivate`, `GET /auth/admin/users`,
  `GET /auth/admin/users/{id}`. Each runs the authenticator, the browser-safe gate and the
  host check before any target lookup.
- **Service:** `Service.DeactivateUser(ctx, actor, id)` / `ReactivateUser` /
  `SetUserStatus(ctx, actor, id, status)`.
- **What happens:** one store transaction sets the status, bumps `auth_revision`, and deletes
  every session and grant; a concurrent login cannot mint a session for a deactivated user.
  Afterwards the account is indistinguishable from bad credentials on public endpoints.
- **Recorded:** `user_deactivated` / `user_reactivated` with the administrator as `Actor`.
  Admin listings and reads record nothing.
- **Erasure** is a separate, irreversible procedure — *Erase a person* below. Deactivation
  keeps every identifier and credential row.

## Revoke sessions for one user

- **Self-service HTML** (when `SessionManagement` is wired): `GET /auth/sessions`,
  `POST /auth/sessions/{id}/revoke`, `POST /auth/sessions/revoke-all`.
- **Service:** `ListUserSessions`, `RevokeUserSession(ctx, userID, sessionID)`,
  `RevokeAllUserSessions(ctx, userID)` for a host admin route.
- **Recorded:** `session_revoked`, `all_sessions_revoked`.
- **Lag:** outstanding access JWTs live up to `AccessTokenTTL` on the default tier
  (*Production posture*, "Revocation SLA"). For every user at once, see *Keys and rotation*.

## Revoke all sessions

Authentication v0.31.0 (SOC 2 S4d). For a suspected fleet-wide session compromise.

- **Route** (opt-in: `AdministrationConfig.GlobalSessionRevocationRoute`, beside
  `Repositories.GlobalSessions` and a `UserAdminCheck`): `POST /auth/admin/sessions/revoke-all`
  with the body `{}`. The caller presents a first-party access token or an external token
  (never an API key or a delegated token: 401); the host check sees the action
  `UserAdminRevokeAllSessions` (`revoke-all-sessions`); under `MFARequired: admins` the
  caller must be at **AAL2** (an AAL1 admin gets 401 with the RFC 9470 challenge: step up
  and retry).
- **Budget:** one call a minute per actor; a second is **429**. Re-running later is safe.
- **Answer:** 200 `{"users":n,"sessions":n,"grants":n,"batches":n,"revoked_at":"…"}`, and
  the session cookies are cleared: the caller's own native session ends with the rest, so
  sign in again. The run is detached from the request (10-minute limit); if the client
  times out first, read the outcome from the rail.
- **Service:** `Service.RevokeAllSessions(ctx, actor)` for a host CLI, when the admin path
  itself (an IdP, an admin sign-in) is what is compromised. The pocket README has the recipe.
- **Recorded:** `all_sessions_revoked_global` twice: `started` (`batch_size`), then
  `success` with `users`, `sessions`, `grants`, `batches`, `duration_ms`, or `failure` with
  the partial counts and `error_kind` (re-run it). Read them with
  `GET /auth/admin/security-events?event_type=all_sessions_revoked_global`.
- **What survives:** outstanding access JWTs on safe reads under the default tier, for up
  to `AccessTokenTTL` (to end them at once too, run the JWT key's emergency rotation in
  *Rotate a key*: a planned rotation keeps them verifying through its window); API keys (next runbook); external IdP sessions (an external admin's next
  provider token still signs in; deactivate the user locally to stop one).

## Disable a service account

Authentication v0.31.0 (SOC 2 S4d). A reversible pause of a machine identity.

- **Prerequisite:** migration 0027 (`service_account_status`, baseline ledger 0034)
  applied pre-boot, then `AdministrationConfig.ServiceAccountStatus`
  (`AUTH_SERVICE_ACCOUNT_STATUS`). Set without the table, the host refuses to boot naming
  0027; unset, the table is never read.
- **Routes** (behind the machine routes' gate): `POST /auth/service-accounts/{id}/disable`
  and `…/enable`, body `{}`. Each answers
  `{"service_account":{…,"status":…},"changed":bool,"keys_revoked":n}`; a repeat is
  `changed: false`. **Service:** `DisableServiceAccount` / `EnableServiceAccount`.
- **What happens:** disable marks the account disabled AND revokes every unrevoked key in
  one transaction; its keys, act-as-user ones included, are refused from then on, and a
  mint is 409 `service_account_disabled`. Enable restores minting only: **the old keys stay
  revoked**, so mint and distribute new ones. A compromised account: revoke its keys or
  delete it; disable is the reversible pause. Un-wiring the flag or rolling the binary back
  resumes mints on a disabled account (the revoked keys stay revoked).
- **Recorded:** `service_account_disabled` (`keys_revoked`), `service_account_enabled`;
  a refused key writes `apikey_auth blocked reason=service_account_disabled`.
- **Find keys that never expire or expire soon** (the listing is per account,
  `GET /auth/service-accounts/{id}/keys?status=active&never_expires=true`); across accounts,
  in SQL:
  `select service_account_id, key_prefix, expires_at from api_keys where revoked_at is null and (expires_at is null or expires_at < now() + interval '30 days')`.

## Enable an idle timeout on a live fleet

Authentication v0.32.0, stores pgx v0.16.0 / Turso v0.15.0 (SOC 2 S4d part 2). Ends a
first-party session nobody has refreshed within `IdleTimeout`.

1. **Repin** core v0.32.0 and the store (pgx v0.16.0 or Turso v0.15.0); a host with its own
   `SessionRepository` implements `session.ActivityRepository` or leaves idle off.
2. **Apply migration 0029** (`sessions.last_rotated_at`, nullable; the baseline ledger copy is
   0035) with `make migrate`, pre-boot. It is required only once idle is set: unset, the
   column is never read and an unmigrated database boots.
3. **Set `AUTH_IDLE_TIMEOUT`** (`SessionsConfig.IdleTimeout`) above `AccessTokenTTL` (at or
   below refuses boot; under twice WARNs `idle_granularity`) and below `RefreshTTL` (at or
   above WARNs `idle_never_fires`). Set without 0029, the host refuses to boot naming
   `0029_session_activity.sql`.
4. **Roll the fleet.** The boot line reads `idle_timeout=<d> idle_source=config`.
- **Expected logout wave: zero.** Under the grandfather rule a session rotated while idle was
  off (rotations, no stamp) is not judged until its first refresh after the switch stamps
  it; only a session never refreshed is judged on `created_at`, and only once it is really
  that old. `RefreshTTL` still bounds every session.
- **Recorded:** `session_idle_expired` once per deleted row; the client sees the same generic
  401 as any refresh denial. Read them with
  `GET /auth/admin/security-events?event_type=session_idle_expired`.
- **What it does not cover:** delegated OAuth2 connections (`OAuth2Config.SessionTTL`); an
  outstanding access JWT, which lives up to `AccessTokenTTL` on `Stateless()` routes and
  default-tier safe reads; a client that refreshes on a timer, which is never idle.
- **Turning it off:** unset `AUTH_IDLE_TIMEOUT`; rotation stops stamping and nothing is judged.
  Before turning it on again, read the next runbook.

## Re-enable an idle timeout after a gap

While idle is off, rotation leaves `last_rotated_at` as it was, so the stamps age. Re-enabling
judges those old stamps: every session refreshed during the gap is logged out once at its next
refresh. To avoid that wave, reset the stamps BEFORE setting `AUTH_IDLE_TIMEOUT` again, so the
grandfather rule applies afresh:

```sql
UPDATE sessions SET last_rotated_at = NULL WHERE session_profile = 'first_party';
```

**The NULL reset is the only supported manual write to `last_rotated_at`.** Never set it to a
time: the column is the idle clock, and the store writes it only through the compare-and-swap
rotation.

## Migration 0029 lock timeout: re-run

0029 takes an `ACCESS EXCLUSIVE` lock on `sessions`, the busiest authentication table, and on
pgx opens with `SET LOCAL lock_timeout = '5s'`. Behind a long reader it fails after about 5 s
(`55P03`, lock not available) and records nothing in the ledger. **Re-run `make migrate`**
(at a quieter moment if it repeats); the file is idempotent (`ADD COLUMN IF NOT EXISTS`). On
Turso the `ALTER` briefly holds the database write lock. The only supported manual write to
the new column is the NULL reset above.

## Enable delegated assurance

Authentication v0.37.0, stores pgx v0.19.0 / Turso v0.18.0 (SOC 2 S4d part 4). With
`OAuth2Config.DelegatedAssurance` an MCP grant carries the approving session's `aal`/`methods`,
so a grant approved at AAL2 passes `RequireMFA()` on a route that admits its audience. The order
matters: a flag-on issuer beside an older verifier 401s delegated routes.

1. **Every verifier on v0.37.0 or later.** Every process that verifies delegated tokens runs
   authentication v0.37.0 or later before the flag moves: the issuing binary, and any separate
   resource server or sidecar that parses those tokens rather than introspecting them. An older
   verifier refuses a delegated token carrying `aal`. Confirm there is no such process before
   treating this step as one deploy.
2. **Apply migration 0030** (`0030_oauth_code_authentication.sql`: three nullable columns on
   `oauth_authorization_codes`, NULL on every existing row; the baseline ledger copy is 0036)
   with `make migrate`, pre-boot. On pgx it opens with `SET LOCAL lock_timeout = '5s'`; on a
   lock timeout re-run `make migrate` (the file is idempotent). A store that does not implement
   `oauth2.CodeAuthenticationRepository` cannot take the flag.
3. **Set the flag** (`OAuth2Config{DelegatedAssurance: true}`, a literal in `cmd`) and roll the
   fleet. Boot refuses without the interface (`ErrDelegatedAssuranceRequires`) or without 0030
   (`ErrDelegatedAssuranceSchemaMissing`, naming the file), and logs
   `auth: oauth2 delegated_assurance=on`. During the roll a code approved on a flag-on instance
   and redeemed on a flag-off one (or the reverse) carries no level: it reads
   `AssuranceUnknown` and fails closed.
4. **Re-consent.** Grants approved before the flag carry no level until the user connects
   again from a session at the level the route needs. There is no bulk tool; a user who must
   reach an AAL2 route disconnects and reconnects after stepping up.

- **Verify:** introspect a new grant's access token (`POST /auth/oauth2/introspect`, the
  confidential client): it reports `aal` and `methods`. A token from an AAL2 approval passes
  the `RequireMFA()` route; one from an AAL1 approval gets 401 with the RFC 9470
  `insufficient_user_authentication` challenge and one `mfa_required_denied reason=assurance`
  row.
- **What it does not change:** the bundled admin and machine groups name no audience, so they
  refuse every delegated token at admission, AAL2 or not.
- **A delegated AAL2 is fixed at approval.** Refresh never re-reads the level and exchange never
  raises it, so AAL2 lives as long as the grant, `OAuth2Config.Server.SessionTTL` (thirty days by
  default), however long ago the person stepped up. There is no separate age bound yet; to
  shorten the window, shorten `SessionTTL` or revoke the grant (`/auth/sessions`).

**Rollback.** Turn the flag off and roll the fleet; signing is gated on the flag, so no new
token carries a level. Wait one `AccessTTL` (five minutes by default) so every token that
carries one has expired, then downgrade below v0.37.0. Downgrading with the flag still on, or
before the wait, 401s delegated routes for up to `AccessTTL`, because an older verifier refuses
the `aal` claim. Migration 0030 stays: its columns are never read with the flag off.

## Turn on MFA for everyone

Authentication v0.38.0 (SOC 2 S4c-2). Native `all` (`MFAConfig.Required: all` beside
`WithPasskeys` or `WithTOTP`) requires AAL2 of every first-party session; a user without a
second factor may use the app at AAL1 until their enrollment grace closes at
`max(EnrollmentStart, user created at) + EnrollmentGrace`, then only the enrollment surface
answers them. No migration.

1. **Wire a native factor.** `WithPasskeys` and/or `WithTOTP`, with their migrations (0025,
   0026) applied pre-boot. Leave `MFAConfig.Methods` empty, or let it admit `passkey` or
   `totp`.
2. **Ship the client handling.** An SPA routes 403 `mfa_enrollment_required` (body
   `enroll_by`) to its enrollment UI, 401 with the RFC 9470 challenge to step-up, and a plain
   401 to `/auth/refresh`. Its shell route carries `AllowMFAEnrollment()` or is served
   unauthenticated. Bundled browser pages need nothing.
3. **Set the policy** as literals in `cmd` and roll the fleet: `Required: MFARequiredAll`,
   `EnrollmentStart` = the rollout date, `EnrollmentGrace` (zero is 7 days; at most 30). Every
   instance carries the same `EnrollmentStart`.
4. **Watch.** The boot line reads `mfa_required=all gates=first_party_routes,…
   enrollment_start=<date> enrollment_grace=<duration>`, beside the single-factor WARN. A
   window-closed WARN (`auth: enrollment window closed for every user created before
   <start>…`) on this deploy means the date is wrong: fix it before users are walled. Then
   watch `mfa_required_denied` rows with `gate=enrollment` (*Read the authentication audit
   rail*): `reason=enrollment` is a user past the window, `reason=assurance` an enrolled user
   who must step up. Each is recorded once per session per process.

- **Tokens minted before the flip** carry no claim: under the floor they get a plain 401 (JSON)
  or the login page's session recovery (`recover=1`, browser) until their first refresh, at
  most `AccessTokenTTL`.
- **A lost factor.** An administrator resets the user's TOTP or passkeys
  (`/auth/admin/users/<id>/totp/reset`, `…/passkeys/reset`), which revokes their sessions. Their
  next sign-in is walled to the enrollment surface at once if the window has closed, and they
  enrol a new factor there at AAL1. There is no per-user re-open of the window.

**Break-glass.** The surface stays reachable: a walled sole administrator signs in and enrols
(the enrollment pages are allowlisted), then steps up for the admin routes. To reopen the
window for the whole fleet, bump `EnrollmentStart` and redeploy — a deliberate change, never an
environment value. To leave native `all`, redeploy with `Required: admins`. Never redeploy
production with `MFARequiredNone`: beside native single-factor login it needs
`SingleFactorAcknowledged` and is refused without it.

**Rollback.** Set `Required: admins` FIRST and roll the fleet, then repin below v0.38.0. A
v0.37.0 binary with `all` beside a native factor refuses to boot; it ignores the
`mfa_enroll_by`/`mfa_enrolled` claims on tokens already issued.

## Move a PostgreSQL server from 17 to 18

pgxdb v0.17.0 and the pgx store tags that pin it require PostgreSQL 18: `RunMigrations`
refuses an older server with `ErrServerVersion` before touching the ledger, even with nothing
pending, and `Open` logs one `WARN` (attrs `server_version_num`, `required=180000`,
`server_version`). There is no maintenance line for older servers. Either upgrade, or hold pgxdb at
v0.16.x and the pgx stores at their previous tags.

- **An image-tag change never upgrades a data directory.** The official `postgres:18` image
  keeps `PGDATA` in a versioned subdirectory under `/var/lib/postgresql`, so a compose volume
  mounted at `…/data` does not carry over; a disposable dev database is simply recreated.
- **`pg_upgrade`:** PostgreSQL 18's `initdb` enables data checksums by default. Upgrading a
  cluster created without them needs `--no-data-checksums` on the new `initdb`, or checksums
  enabled on the old cluster first.
- **Dump and restore:** `pg_dump` / `pg_restore` clients must be 18 or later. An 18 dump does not
  restore onto 17 (the canonical migrations use `uuidv7()` defaults).
- **Replicas and DR targets** must be 18 before the first migrate under the new tags.
- **Verify:** `SHOW server_version_num;` reads `180000` or higher, then run the migrator.

## Rotate a key

Authentication v0.35.0, golang-jwt v0.4.0, sdk v0.23.0 (SOC 2 S4d part 3). One subsection
per key in the inventory (*Keys and rotation*). Generate every new key with
`openssl rand -hex 32`; never reuse one across roles. Key ids are lower-case
(`^[a-z0-9][a-z0-9_-]{0,31}$`), are configuration and not secrets, and appear in logs; key
material never does.

**The three phases.** Every ring rotation (JWT, delivery, provider tokens) runs in three
phases, so a rolling fleet never refuses a token or dead-letters an envelope part-way through
a deploy:

- **(a) Ring beside the single key, legacy active.** Deploy code on the three tags above with
  the ring variables set, the single variable KEPT and the active id `legacy`. The ring reads
  both shapes and writes the old one (no `kid`, no `v1.` prefix), so instances still on the
  single key read everything the new ones write.
- **(b) Activate the new id.** Once EVERY instance runs (a), set the active id to the new key.
  New output carries the new id; the legacy key still verifies or opens the old output.
- **(c) Drop the legacy key.** After the window below, unset the single variable. The boot
  line then reads `…_legacy=no`, and the production
  `posture_warning=legacy_key_read_enabled key=<role>` WARN stops.

**Before each phase:**

- before (a): `go version -m <binary>` shows authentication v0.35.0 or later, golang-jwt
  v0.4.0 or later and sdk v0.23.0 or later. An older `AESGCMRing` treats `legacy` as an
  ordinary key and cannot open single-key ciphertext.
- before (b): every instance's boot line shows phase (a) (`…_active_key_id=legacy`). An
  instance still on the single key cannot read what (b) writes.
- before (c): the key's window has passed since the last instance reached (b), and the key's
  own gate below is met.

**Boot-line evidence.** `New` logs one `auth: keys` line (no key material). Record it at each
phase: it is the rotation's evidence.

```
phase (a):  msg="auth: keys" jwt_signer=ring jwt_active_key_id=legacy jwt_key_ids=k2,legacy jwt_legacy=yes …
phase (b):  msg="auth: keys" jwt_signer=ring jwt_active_key_id=k2 jwt_key_ids=k2,legacy jwt_legacy=yes …
phase (c):  msg="auth: keys" jwt_signer=ring jwt_active_key_id=k2 jwt_key_ids=k2 jwt_legacy=no …
```

The delivery and provider-token roles print `delivery_…` and `token_…` the same way, and the
challenge protector prints `challenge_active_key_id` and `challenge_key_ids`. A role printed
`single` is on its single variable; `opaque` is a collaborator that does not report its keys
(a third-party signer), which gets no legacy WARN.

**Rollback.** Before (c), re-activating the old id (`…_ACTIVE_KEY=legacy`) is a KEY rollback
and loses nothing. A CODE rollback below the three tags after phase (b) is lossy: the old code
cannot read anything written under the new id. Every `kid`-stamped access token is refused
until its client refreshes, every `v1.` delivery envelope dead-letters, and every `v1.`
provider token is unreadable until the user re-links. To roll code back, go back to phase (a)
first and wait out the window.

**Emergency rotation.** When a key may be compromised, skip the overlap: set the ring to a
fresh key with no legacy id and drop the compromised key everywhere at once, then accept the
cost listed under each key. **The `legacy_key_read_enabled` WARN is never acceptable during a
compromise**: a legacy id keeps the compromised key verifying.

### JWT signing key (`AUTH_JWT_SECRET` → `AUTH_JWT_KEYS`)

The host wires `golangjwt.NewRing(keys.JWTRing, golangjwt.WithLegacyKeyID(keys.JWTLegacyKeyID))`
when `keys.JWTRing` has keys, else `golangjwt.New(string(keys.JWTSecret))` (the reference
hosts do both).

```sh
# (a) every instance: ring + secret kept, legacy active
AUTH_JWT_SECRET=<current hex>          # kept: joins the ring as id "legacy"
AUTH_JWT_KEYS=k2:<new hex>
AUTH_JWT_ACTIVE_KEY=legacy
# (b) after the whole fleet runs (a)
AUTH_JWT_ACTIVE_KEY=k2
# (c) after the window
#   unset AUTH_JWT_SECRET
# next rotation: AUTH_JWT_KEYS=k2:<hex>,k3:<new hex>, AUTH_JWT_ACTIVE_KEY=k3, then drop k2
```

- **Window:** `max(AccessTokenTTL, OAuth2Config.AccessTTL, OAuth2Config.ConsentTTL) + 60 s`.
  The same signer mints delegated OAuth2 tokens. With the defaults that is 16 minutes.
- **Evidence:** a fresh token's header carries `kid` from (b) on
  (`cut -d. -f1 <<<"$TOKEN" | base64 -d` shows `"kid":"k2"`). In (a) it carries none.
- **What a token without `kid` does:** it verifies under `legacy` while that id is in the
  ring and is refused once it is gone. A `kid` the ring does not hold is always refused.
  Sessions survive every phase (refresh never consults the signer), so a refused access
  token costs one refresh, never a sign-in.
- **HS384/HS512:** a host whose single `Signer` used `WithMethod(HS384|HS512)` cannot read its
  old tokens through the HS256 ring. It stays on `Signer`, or rotates as an emergency.
- **Emergency:** `AUTH_JWT_KEYS=k3:<new hex>`, `AUTH_JWT_ACTIVE_KEY=k3`, every old id and
  `AUTH_JWT_SECRET` removed. Every outstanding access token (and any forged one) is 401 at
  once and clients refresh. If refresh tokens may also be exposed, run *Revoke all sessions*.

### Challenge pepper (`AUTH_CHALLENGE_PEPPER` → `AUTH_CHALLENGE_PEPPERS`)

Challenge rows and recovery codes store the id of the key that digested them, so the pepper
ring names its ids itself and the single variable is never joined as `legacy`: setting both
forms refuses boot (`ErrKeyRequired` naming both). The first ring entry MUST reuse the id and
value the host uses today (`dev` on the reference host). The host builds the protector with
`protection.NewHMACChallengeProtector(protection.HMACKeyRing(keys.ChallengeRing))`.

```sh
# (a) the ring with the current id and value, still active; AUTH_CHALLENGE_PEPPER unset
AUTH_CHALLENGE_PEPPERS=dev:<current hex>,p2:<new hex>
AUTH_CHALLENGE_ACTIVE_PEPPER=dev
# (b) after the whole fleet runs (a)
AUTH_CHALLENGE_ACTIVE_PEPPER=p2
# (c) after the window, and the recovery-code gate
AUTH_CHALLENGE_PEPPERS=p2:<new hex>
```

The single variable and the ring with the same id and value digest identically, so (a) can
also be a single step on a small fleet. Only (b) needs the whole fleet to hold `p2` first.

- **Window:** the longest code-challenge TTL: 15 minutes for the bundled purposes (5 for login
  and step-up codes). Magic-link, reset and invitation tokens are unkeyed SHA-256
  (`DigestToken`): the pepper does not touch them.
- **Gate (recovery codes, the long tail):** a recovery code stays digested under the id that
  was active when it was generated, until the user regenerates. While any remain under an id
  that left the ring, `New` WARNs `auth: recovery codes sealed under retired pepper keys
  key_ids=[…]`. Keep the old id while that matters, or accept the loss and record the WARN's
  `key_ids` as evidence. Those users regenerate codes at their next AAL2 step-up, or an
  administrator resets their TOTP.
- **Evidence:** `challenge_active_key_id=p2 challenge_key_ids=p2`.
- **Code rollback:** code below v0.35.0 reads only `AUTH_CHALLENGE_PEPPER` under the host's
  hard-coded id. Codes and recovery codes under `p2` stop verifying.
- **Emergency:** drop the compromised id at once. Pending codes fail (users request a new
  one), and recovery codes under it are orphaned (the WARN above lists them).

### Identifier key (`AUTH_IDENTIFIER_KEY`)

A single key, rotated in one step: no ring, no legacy id. Set the new value on every instance.

- **Effect:** rate-limit budgets and outbox idempotency windows reset once (they are minutes
  long). `identifier_digest` on invitation and delivery audit rows minted before the cut no
  longer matches digests recomputed afterwards: the digest is correlation only, and nothing is
  looked up by it.
- **Window:** `max(budget window)`, the longest `AuthenticationLimits` window you configured
  (1 minute by default). During a rolling deploy two instances key the same identifier
  differently, so a budget can briefly allow up to twice its limit.
- **Evidence:** record the cut time and keep the retired key in the evidence store if audit
  digests before the cut must stay checkable.
- **Emergency:** the same procedure. The only cost is the budget reset.

### Delivery envelope key (`AUTH_DELIVERY_ENCRYPTER_KEY` → `AUTH_DELIVERY_ENCRYPTER_KEYS`)

The host wires `cryptids.NewAESGCMRing(keys.DeliveryEncrypterRing,
cryptids.WithLegacyKeyID(keys.DeliveryLegacyKeyID))` when the ring is set, else
`cryptids.NewAESGCM(keys.DeliveryEncrypterKey)`.

```sh
# (a)
AUTH_DELIVERY_ENCRYPTER_KEY=<current hex>     # kept: joins as "legacy"
AUTH_DELIVERY_ENCRYPTER_KEYS=d2:<new hex>
AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY=legacy
# (b)
AUTH_DELIVERY_ENCRYPTER_ACTIVE_KEY=d2
# (c) once the queue holds no pre-(b) envelope
#   unset AUTH_DELIVERY_ENCRYPTER_KEY
```

- **Window:** the drain time: until no envelope sealed before (b) is still queued. On the
  generic jobs rail the (c) gate is a zero from
  `SELECT count(*) FROM job_queue WHERE kind = 'authentication.delivery' AND status IN
  ('pending', 'running', 'failed')` taken after the last instance reached (b) (`failed` still
  has retries left). Delayed retries can hold an envelope for its whole retry schedule. In
  `in_process` mode nothing persists, so the window is one drain of the in-memory pool.
- **Evidence:** `delivery_encrypter=ring delivery_active_key_id=d2 delivery_key_ids=d2
  delivery_legacy=no`. An envelope the ring cannot open fails as `ErrUnsealedPayload` (no bytes
  in the error) and dead-letters.
- **Emergency:** drop the compromised key. In-flight envelopes dead-letter, and their users
  request the code or link again.

### Provider-token key (`AUTH_TOKEN_ENCRYPTER_KEY` → `AUTH_TOKEN_ENCRYPTER_KEYS`)

Optional: only a host that persists OAuth provider tokens has it. The wiring matches
delivery: `NewAESGCMRing(keys.TokenEncrypterRing, WithLegacyKeyID(keys.TokenLegacyKeyID))`.
A host that branches only on `keys.TokenEncrypterKey != nil` silently stops wiring the
encrypter once the ring is set, so branch on `len(keys.TokenEncrypterRing.Keys) > 0` first.
A host that decrypts these columns itself moves its reader to the same ring with the legacy
id at the same time.

```sh
# (a)
AUTH_TOKEN_ENCRYPTER_KEY=<current hex>        # kept: joins as "legacy"
AUTH_TOKEN_ENCRYPTER_KEYS=t2:<new hex>
AUTH_TOKEN_ENCRYPTER_ACTIVE_KEY=legacy
# (b)
AUTH_TOKEN_ENCRYPTER_ACTIVE_KEY=t2
# (c) only at a zero count (below)
#   unset AUTH_TOKEN_ENCRYPTER_KEY
```

- **Window: until no row remains under the legacy key.** A stored row is never rewritten by a
  returning login, so legacy rows persist until the user unlinks and relinks or the host
  re-seals them. The (c) gate is a zero from this count (PostgreSQL and SQLite/Turso; legacy
  values are base64url and never contain a `.`):

  ```sql
  SELECT count(*) FROM oauth_accounts
   WHERE (access_token <> '' AND access_token NOT LIKE 'v1.%')
      OR (refresh_token <> '' AND refresh_token NOT LIKE 'v1.%');
  ```

  `refresh_token` is counted too: providers such as Google omit it on a repeat consent, so a
  legacy refresh token can outlive the access-token check.
- **Re-sealing (host Go, never SQL alone):** for each counted row, decrypt each legacy column
  with the ring (it opens legacy values), encrypt the plaintext with the ring (it writes the
  active id), and write it back compare-and-swap so a concurrent relink is never overwritten:
  `UPDATE oauth_accounts SET access_token = $new WHERE provider = $p AND provider_user_id = $u
  AND access_token = $old`, and the same for `refresh_token`. Re-run the count until it is zero.
- **Evidence:** the zero count, then `token_encrypter=ring token_active_key_id=t2
  token_key_ids=t2 token_legacy=no`.
- **Emergency:** drop the compromised key. Every row under it is unreadable: revoke the tokens
  at the provider, and users re-link.

### TOTP secret key (`AUTH_TOTP_KEYS`)

Already a ring and unchanged by this release; the other rings copy it. Secrets are sealed with
the user id inside the envelope as `v1.<id>.…` and are never re-sealed on rotation.

```sh
# add, then activate (on a fleet: add everywhere first, then activate)
AUTH_TOTP_KEYS=t1:<current hex>,t2:<new hex>
AUTH_TOTP_ACTIVE_KEY=t2
# drop t1 only at a zero count:
#   SELECT count(*) FROM totp_credentials WHERE secret LIKE 'v1.t1.%';
```

- **Window:** until no `totp_credentials` row names the old id. Rows move only when users
  re-enroll.
- **Evidence:** the zero count, then `totp_active_key_id=t2 totp_key_ids=t2`.
- **Emergency:** drop the compromised id. Those users' codes stop verifying until an
  administrator TOTP reset and re-enrollment; their recovery codes (under the pepper) still
  work.

### Raise the password hash cost (rehash on login)

bcrypt v0.3.0 `New()` hashes at cost 12 (`WithCost(10)` is the hatch, and below 12 production
WARNs `posture_warning=password_hash_cost_low`). Existing hashes verify at their stored cost.
With the store tags pgx v0.18.0 / Turso v0.17.0 (or a store implementing
`user.PasswordHashUpgrader`), a successful login or password step-up re-hashes a lower-cost
hash at the configured cost through a compare-and-swap that bumps no revision and revokes no
session. A failure logs one `password_rehash_failed` WARN and never fails the login.

- **The timing residual closes as users log in.** A login miss path verifies against a dummy
  hash at the configured cost. Until a user's stored hash is re-hashed, a wrong password for
  that user answers faster than a miss. Track it with
  `SELECT count(*) FROM user_passwords WHERE substr(hash, 5, 2) < '12';`
  (bcrypt hashes are `$2a$<cost>$…`; both dialects). Accounts that never log in keep their old
  hashes until the host forces a reset.
- **Breach checks.** No example wires one. A password host wires
  `integrations/passwords/hibp` (the keyless Pwned Passwords range API; fail-closed, 503 on an
  outage unless `CompromisedPasswordFailOpen`) or `protection.NewBlocklistChecker` over a list
  file as `PasswordConfig.CompromisedPasswordChecker`. In production a password host with
  neither logs `posture_warning=password_breach_check_none`.

## An abusive IP

There is no bundled block list. What exists:

- Per-IP budgets on login and token issuance (30/min), reset starts (20/min), sensitive codes
  (30/min), refresh (30/min) and invitation decline (10/min), keyed on the
  `web.TrustProxies`-resolved client IP. Behind a proxy without `TrustProxies` every client
  shares the proxy's budget.
- The budgets fail closed on a limiter outage, except the HTTP refresh and decline budgets,
  which fail open.
- Blocking is the edge's job (WAF, load balancer). Register, verify, reset, token and the
  native OAuth routes have no HTTP-level per-IP throttle today (planned, S5).

## Read the authentication audit rail

`security_events` is written by the authentication service when `Repositories.SecurityEvents`
is wired. Read it through the repository:

```go
page, err := repos.SecurityEvents.List(ctx, securityevent.ListFilter{
    UserID: id,            // optional
    EventType: "login",    // optional
    EventStatus: "failure",// optional
    Since: from, Until: to,// optional, Until exclusive
}, list.Request{})
// ordered created_at DESC, id DESC; cursor-paginated
```

Rows carry `user_id`, `actor_type`/`actor_id`, `event_type`, `event_status`, `details`
(identifiers and key prefixes, never secrets), `ip_address`, `user_agent`, `created_at`
(the application clock, UTC). Since S2, rows also carry `request_id` and `trace_id`, which
join a row to the request's logs and trace (blank when the request carried none, or when the
recorder refused an id that was not printable ASCII of at most 128 bytes).

**Over HTTP (since authentication v0.30.0).** With `AdministrationConfig.SecurityEventsRoute`
on, `GET /auth/admin/security-events` serves the same listing to an admin the host's
`UserAdminCheck` admits for the `read-audit` action (switch on `Action` before opting in), at
AAL2 under `MFARequired: admins`:

```sh
curl -s "$HOST/auth/admin/security-events?event_type=login&event_status=failure&since=2026-10-01T00:00:00Z&limit=100" -H "$AUTH"
```

Filters `user_id`, `event_type`, `event_status`, `since` (inclusive), `until` (exclusive);
cursor paging only. **Export is paging:** keep the filters fixed, follow `next_cursor` at
`limit=100` until `has_more` is false. Every page read leaves one `audit_read` row naming the
reader and the filter keys used, so the export is itself on the rail.

`examples/auth-cms` also has `GET /debug/security-events` behind `AUTH_DEBUG=1` (refused
outside development); it requires a live session holding the platform-admin permission
(`admin` on `platform:main`) and is a harness feature — do not copy it into a host.

## Read authorization change history

With `WithAudit()` on the store and `Repositories.Audit` wired:

```go
page, err := repos.Audit.List(ctx, audit.Filter{
    ResourceType: "project", ResourceID: pid, // both or neither
    SubjectType: "user", SubjectID: uid,      // both or neither
    ActorType: "user", ActorID: adminID,      // both or neither
}, list.Request{})
// ordered occurred_at DESC then id
```

Each record is one committed fact delta (`added` or `removed`) with its source (actor pair or
system, optional reason) and time. Refusals and denials are not in this history: since S2 the
decision observer reports them (`authorization_denied`, `role_write_refused`), and a host that
wires the audit bridge records them on the security-event rail (above).

**Over HTTP (since authorization v0.31.0).** With `RoleRoutes.AuditGate` wired (a
global-history gate: platform admin or auditor), `GET /authorization/audit` takes the same
three filter pairs and pages by cursor; export it the same way (`limit=100`, follow
`next_cursor` until `has_more` is false). With `WithAuditReadObserver` bridged into the
security-event rail, each read leaves an `audit_read` row with `rail: authorization`.

```sh
curl -s "$HOST/authorization/audit?resource_type=project&resource_id=$ID&limit=100" -H "$AUTH"
```

The listing recipes for a periodic access review are in `pockets/authorization/README.md`,
"Access review".

## Retention

The pockets ship bounded disposal calls and start none of them; the host runs each from its own
schedule, one runner instance per database. The lifetimes, defaults and the floor are in
*Production posture*, "Retention".

| Data | Call | Schedule |
|---|---|---|
| sessions, OAuth states, grants, contact changes, invitations, challenges, OAuth2 rows, `security_events` | `Components.Maintenance.Run(ctx)` (blocks; a jittered first pass, then one an hour), or `Components.Maintenance.Pass(ctx)` from the host's own ticker or a jobs schedule — the three-line handler in `pockets/authentication/README.md` | hourly; the rail step runs once a day (`SecurityEventStepInterval`) and reports `not_due` between |
| `iam_operations` | `Components.Mutations.ExpireOperations(ctx, limit)` until it returns less than `limit` | hourly |
| ordinary `job_queue` | `Components.Queue.PurgeTerminalJobs(ctx, before, limit)` | hourly; `before` behind the scheduler's longest retry window |
| fenced job rows (terminal, incl. sealed delivery envelopes) | `Components.Queue.PurgeTerminal(ctx, before, limit)` | host scheduler |
| published events (`event_outbox`) | `outbox.PurgePublished(ctx, before)` — unbounded: its own step, after a one-off manual drain | host scheduler |
| cms `inquiries` | `PurgeBefore(ctx, before, limit)` on the `messaging.InquiryEraser` the host wired as `Repositories.InquiryMaintenance` | host scheduler |
| `ratelimit_windows` (pgx limiter) | `DELETE FROM ratelimit_windows WHERE expires_at < now();` | host cron |
| `iam_audit`, `secrets_audit` | the host's definer function (*Production database posture*, "Definer functions") | host cron |

Set `MaintenanceRunnerAcknowledged: true` once the authentication runner runs; production WARNs
while it is false. `Pass` never cancels a running statement: it checks `PassBudget` (5m) between
batches, and a budget that runs out pass after pass WARNs `maintenance_budget_exhausted` — the
first enablement on an old host drains its backlog over many passes, so watch WAL and replica lag.
Two concurrent passes are safe, not cheap; a multi-replica host runs the runner on one replica.

**Where the evidence is.**

- One `msg=maintenance_step step=<name> count=<n> skipped=<bool> error_kind=<kind>` line per step
  per pass, and `LastReport()` for a health surface.
- `security_events_purged` rows (`actor_type='system'`, `actor_id='maintenance'`; Details
  `cutoff`, `count`, `floor_days`, `max_rows`, and on pgx `session_user`): one per purge call, a
  zero count included — the evidence that the control ran on a day with nothing to purge.
- `security_events_archived` rows (Details `object`, `sha256`, `rows`, `first_created_at`,
  `first_id`, `last_created_at`, `last_id`, `archive_location`): one per archive object, written
  before its chunk is purged.
- The archive objects themselves, NDJSON at `<ArchivePrefix><cutoff>/<id>.ndjson`.
- On a jobs schedule, the schedule's `last_run_at`.

Dashboards filter `actor_type='system'`. The canonical alert: **no `security_events_purged` row
in 48 h**.

**Verify the archive.** Fetch each object an archived row names, check its SHA-256 and line count
against `sha256` and `rows`, and compare, per cutoff, the sum of `rows` over the archived rows
with the sum of `count` over the purged rows. The archive's own lifecycle — retention, backup,
access control, object lock on a real sink — is host policy, and archived rows are outside
erasure's reach. A failed archive purges nothing that pass (`maintenance.ErrArchiveFailed`); an
object already written is left for reconciliation.

**After a restore.** `pg_restore -x` drops the grants: revoke `EXECUTE` on
`security_events_purge_before` from `PUBLIC` and grant it to the runtime role again (the boot
refuses a PUBLIC-executable definer). After a restore into a renamed schema, re-run the pin `DO`
block of `0024_security_events_retention.sql`.

## Erase a person

Erasure is irreversible and composes across pockets; run every step and record each.

1. **Settle any accepting invitation claim** for the person first: the erase refuses with 409
   (`user.ErrEraseBlockedByClaim`) while one is held, and writes nothing.
2. **Erase the account**: `POST /auth/admin/users/{id}/erase` (mounted only when
   `Repositories.UserErasure` is wired and `AdministrationConfig.UserErasureRoute` is true; the
   host check receives `UserAdminErase`; 400 on self) or `Service.EraseUser(ctx, actor, id)`. One
   transaction marks the user erased, blanks and retires every identifier, deletes credentials,
   sessions, grants, challenges and the act-as-user service accounts with their keys, removes or
   blanks the invitations addressed to the person, and appends `user_erased` with the counts. A
   retry is a 200 with `changed:false`. The `users` row and every pseudonymous id survive.
3. **Remove the authorization facts** for `user:<id>`. A host that wires
   `AdministrationConfig.BeforeErase` (authentication v0.36.0) has this done server-side by step
   2: the hook runs before the erase transaction and revokes the person's role assignments in
   every scope, protected relations first, with the administrator as audit actor and reason
   `user_erase`. A **409** with the roles intact is the host's integrity rule (the last owner or
   admin): add another, re-run. A **409** after the roles vanished means the hook ran and the
   store refused (a held claim; logged `auth: erase refused after BeforeErase ran`): settle the
   claim (step 1), re-run. Re-running the erase is idempotent and sweeps a grant that raced in.
   Without the hook, list them with `Roles.ListRoleAssignmentsBySubject` and remove each through
   the bundled role routes or `Mutations.UnassignRole`/`RevokeRelationship` — an executed step,
   not a note.
4. **Delete the person's inquiries**: `DeleteByEmail(ctx, email)` on the wired cms eraser (ASCII
   case-fold equality), for each address the person used.
5. **Purge sealed delivery rows**: `Queue.PurgeTerminal` for the terminal fenced jobs that carried
   the person's messages.
6. **Out of reach, by design**: archive objects already written; `iam_operations` fingerprints,
   which expire under `ExpireOperations`; `security_events` rows, which keep their IP and
   User-Agent and age out at the rail's retention.

Then **sweep**: as the migrator, count the rows of every table whose text or JSON columns contain
the person's email or last IP:

```sql
DO $sweep$
DECLARE
  needles pg_catalog.text[] := ARRAY['<email>', '<last ip>'];
  needle pg_catalog.text;
  col record;
  n pg_catalog.int8;
BEGIN
  FOR col IN
    SELECT c.table_schema, c.table_name, c.column_name
      FROM information_schema.columns c
      JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
     WHERE t.table_type = 'BASE TABLE'
       AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
       AND c.data_type IN ('text', 'character varying', 'json', 'jsonb')
  LOOP
    FOREACH needle IN ARRAY needles LOOP
      EXECUTE pg_catalog.format('SELECT count(*) FROM %I.%I WHERE %I::text ILIKE %L',
        col.table_schema, col.table_name, col.column_name, '%' || needle || '%') INTO n;
      IF n > 0 THEN
        RAISE NOTICE '%.%.%: % rows contain %', col.table_schema, col.table_name, col.column_name, n, needle;
      END IF;
    END LOOP;
  END LOOP;
END
$sweep$;
```

Expect no notice except on `security_events`, whose rows within retention keep the IP (and, on
rows older than AUDIT-054, the raw address). The other documented carve-outs hold no plaintext
match: `oauth_refresh_history` keeps hashes, `iam_operations` keeps fingerprints, and archive
objects live outside the database. Erase is not a ban: the next external login provisions a fresh
user; deactivate to keep someone out.

## Tenants and offboarding

Tenant isolation is host-owned: one database or schema per tenant, never a row-level
predicate the framework enforces. Offboarding a tenant means dropping its database and
destroying the vault owner that held its credentials (`Vault.DeleteOwner`), then rotating the
secrets master ring if backups must be defeated (*Keys and rotation*). Ambient transactions
are per `*DB` (pgxdb v0.15.0, datastores/turso v0.10.0): a repository bound to one database,
called inside another database's transaction, runs on its own database's pool. The two never
share a transaction, so there is no atomicity between control-plane and tenant work; see
*Persistence*, "Two databases never share a transaction". Symptom of nesting two `*DB` over
ONE database: requests stall in pool acquire, or sit `idle in transaction` on the outer
instance while the inner pool saturates. Build those stores over one `*DB`, or set
`DB_LOCK_TIMEOUT` and do not nest the pair.
