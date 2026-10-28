---
title: Keys and rotation
description: The six authentication keys, how NewKeys decodes and loads them, what rotating each one costs, and the secrets-pocket key procedures.
---

# Keys and rotation

Current state as of authentication v0.35.0, golang-jwt v0.4.0 and sdk v0.23.0 (SOC 2 S4d
part 3). The authentication pocket never generates a
key in production: every signer, encrypter and keyer is a required collaborator, and
`authentication.NewKeys(mode, KeysInput{…}, log, need…)` is the one loader that decodes them.
The host fills `KeysInput` from its own environment (hex, every key), names the keys it wires
in `need`, and gets back the collaborators. A needed key that is unset is `ErrKeyRequired` in
production (one joined error naming each variable) and a per-boot `crypto/rand` key with one
WARN in development; a malformed value (not hex, too short) is refused in every mode. The
reference host (`examples/auth-cms/cmd/server/demo.go`) and the `init` scaffold use it.

## The six authentication keys

| Key (single variable → ring form) | Protects | Encoding (`NewKeys`) | Minimum | Rotation |
|---|---|---|---|---|
| `AUTH_JWT_SECRET` → `AUTH_JWT_KEYS` + `AUTH_JWT_ACTIVE_KEY` | access-JWT signing (HS256; delegated OAuth2 tokens too) | hex; the ring is `id:hex,id:hex` | 32 bytes per key for the HS256 ring; the single `Signer` keeps 48 for HS384 and 64 for HS512 | `golangjwt.NewRing` stamps `kid` and verifies under any ring key; `AUTH_JWT_SECRET` kept beside the ring is the `legacy` key for tokens with no `kid`, for one window (`max(AccessTokenTTL, OAuth2 AccessTTL, ConsentTTL) + 60 s`) |
| `AUTH_CHALLENGE_PEPPER` → `AUTH_CHALLENGE_PEPPERS` + `AUTH_CHALLENGE_ACTIVE_PEPPER` | HMAC over OTP codes and recovery codes | hex; the ring names its ids itself (both forms set refuses boot) | 32 bytes per key | the first ring entry reuses the current id (`dev` on the reference host); unexpired codes verify under every ring id; regenerate recovery codes before dropping an id |
| `AUTH_IDENTIFIER_KEY` | PII-free rate-limit and idempotency digests; `identifier_digest` on invitation and delivery audit rows (correlation only) | hex | 32 bytes | single key, rotated in one step: budgets and idempotency windows reset once, and audit digests before the cut no longer match recomputed ones (nothing is looked up by them) |
| `AUTH_DELIVERY_ENCRYPTER_KEY` → `AUTH_DELIVERY_ENCRYPTER_KEYS` + `…_ACTIVE_KEY` | AES-256-GCM over queued delivery envelopes (the rendered secret and destination) | hex | exactly 32 bytes | `cryptids.NewAESGCMRing` with `WithLegacyKeyID` opens envelopes the single key sealed; drop the legacy key once the queue holds none |
| `AUTH_TOKEN_ENCRYPTER_KEY` → `AUTH_TOKEN_ENCRYPTER_KEYS` + `…_ACTIVE_KEY` (optional in every mode; unset → provider tokens are not persisted) | AES-256-GCM over stored OAuth provider tokens | hex | exactly 32 bytes | the ring with the legacy id reads old rows; a legacy row persists until the user unlinks and relinks or the host re-seals it (a returning login never rewrites it), so drop the legacy key only at a zero count |
| `AUTH_TOTP_KEYS` + `AUTH_TOTP_ACTIVE_KEY` (`KeyTOTPRing`, since authentication v0.28.0) | AES-256-GCM over stored TOTP secrets, each sealed with its user id | a ring `id:hex,id:hex` parsed by `cryptids.ParseKeyRing`; needed only by a host that wires `WithTOTP` | exactly 32 bytes per key | unchanged: add, activate, keep the old id while any secret is sealed under it (secrets are not re-sealed); dropping it means those users' codes stop verifying until an admin TOTP reset and re-enrollment |

Two things to know before you repin a host that copied the pre-S1 reference host:

- **Unset keys are refused in production.** Before S1 the reference host minted a random key
  at boot with a WARN in every mode, so a multi-instance deployment flapped authentication
  and lost sealed delivery rows on restart. `NewKeys` fails closed; the development mint is
  what a host that still minted its own keys may keep seeing in `guard --posture` P6's
  informational `rand` lines.
- **Continuity across the encoding change.** `NewKeys` decodes every key as hex. If your host
  used the hex *text* of `AUTH_JWT_SECRET` as the key bytes, or raw 32-character strings for
  the two encrypter keys, set each new value to the hex of the OLD bytes
  (`printf %s "$OLD" | xxd -p | tr -d '\n'`) so outstanding access tokens, in-flight sealed
  outbox rows and stored provider tokens stay valid. Never mint a new value at the upgrade;
  the consequences are the last column above.

**The TOTP ring and the recovery-code pepper (S4c).** A host that wires TOTP builds the
encrypter with `cryptids.NewAESGCMRing(keys.TOTPRing)` after naming `KeyTOTPRing` in `need`.
Development mints a one-key `ephemeral` ring with the usual WARN, so enrolled apps stop working
on restart; production refuses the ring unset only when `KeyTOTPRing` is needed. Recovery codes
are not under the TOTP ring: they are digested under `AUTH_CHALLENGE_PEPPER`'s active key, with
the key id stored beside each digest, and verify under every ring key. **Regenerate recovery
codes before dropping a pepper key:** a code digested under a dropped key never verifies again.
While any remain, `New` WARNs `auth: recovery codes sealed under retired pepper keys
key_ids=[…]`; those users regenerate at their next AAL2 step-up.

There is deliberately no separate CSRF or magic-link key: CSRF is a per-render double-submit
token, and magic-link, reset and invitation tokens are 256-bit random values digested with
unkeyed SHA-256 (`DigestToken`), not under the pepper.

**Key rings (S4d part 3).** The JWT, pepper, delivery and provider-token keys each take a ring
form beside the single variable. The ring alone fills `Keys.JWTRing`, `ChallengeRing`,
`DeliveryEncrypterRing` or `TokenEncrypterRing`. For the JWT and the two encrypters, the single
variable set beside its ring joins it under the reserved id `legacy`
(`authentication.LegacyKeyID`) and sets `JWTLegacyKeyID`, `DeliveryLegacyKeyID` or
`TokenLegacyKeyID`. The host builds the ring adapters with that id:
`golangjwt.NewRing(keys.JWTRing, golangjwt.WithLegacyKeyID(keys.JWTLegacyKeyID))` and
`cryptids.NewAESGCMRing(ring, cryptids.WithLegacyKeyID(id))` (an empty id is no option). While
`legacy` is the active id the adapters write the single-key shape, so a rolling fleet rotates
in three phases (*Operations runbooks*, "Rotate a key"): ring beside the single key with
`legacy` active, then the new id active, then the single key dropped. Both reference hosts
(`examples/auth-cms`, `examples/production-baseline`) wire the ring when it is set and the
single key otherwise; `examples/auth-cms/cmd/server/key_rotation_test.go` walks every phase.

`New` logs one `auth: keys` line naming each role's kind (`ring`, `single`, `opaque`, `none`)
and, for a ring, its active id, ids and legacy state, never a key. In production a role with a
legacy id logs `posture_warning=legacy_key_read_enabled key=<role>` until the single variable is
unset. **That WARN is never acceptable during a compromise:** an emergency rotation drops the
compromised key at once with no legacy id.

## Revoking every session at once

**Rotating `AUTH_JWT_SECRET` does NOT end sessions.** It invalidates every outstanding access
JWT at once, but the session rows survive: every client refreshes and carries on. To end
sessions, use `RevokeAllSessions` (authentication v0.31.0, SOC 2 S4d):

1. Call `Service.RevokeAllSessions(ctx, actor)`, or `POST /auth/admin/sessions/revoke-all`
   on a host that sets `AdministrationConfig.GlobalSessionRevocationRoute` (the runbook is in
   *Operations runbooks*). It bumps every active user's `auth_revision` and deletes their
   sessions, step-up grants and `password_reset` challenges, so every refresh, `Live()` route,
   pending magic link and delegated OAuth2 connection is dead at once. It records
   `all_sessions_revoked_global` (`started`, then `success` or `failure` with the counts).
2. Outstanding access JWTs keep working for up to `AccessTokenTTL` (default 15 minutes) on
   safe reads under the default liveness tier and on `Stateless()` routes. If that window is
   too long, or the signing key itself may be compromised, ALSO run the JWT key's emergency
   rotation on every instance (a fresh ring key, no legacy id; *Operations runbooks*, "Rotate a
   key"): together they end everything native at once. A planned rotation with a legacy id
   keeps old access tokens verifying through its window and ends nothing.
3. What survives: API keys (revoke them, or disable their service accounts) and external IdP
   sessions (the provider's own; locally, deactivate the user).

**Idle expiry is the standing control beside it** (authentication v0.32.0, SOC 2 S4d part 2).
Revoke-all ends every session once, on demand; `SessionsConfig.IdleTimeout`
(`AUTH_IDLE_TIMEOUT`, zero is off) ends each first-party session nobody has refreshed within
the timeout, at its next refresh, with `session_idle_expired`. It needs migration 0029 and a
store implementing `session.ActivityRepository` (pgx v0.16.0 / Turso v0.15.0). Neither control
ends an external IdP session; for that, set `ExternalProvider.MaxAuthenticationAge` on a provider
that asserts `auth_time` (Firebase), which caps how long ago its user signed in. *Operations
runbooks*, "Enable an idle timeout on a live fleet".

The old manual recipe (`DELETE FROM sessions; DELETE FROM authentication_grants;`) is
superseded: it never bumped `auth_revision` and wrote no security event.

## API-key lifetime

API keys are not under any of the keys above: each is a random secret stored as a digest, and
it lives until it expires or is revoked. Since authentication v0.31.0,
`AdministrationConfig.APIKeyMaxTTL` (`AUTH_API_KEY_MAX_TTL`, a Go duration) bounds the life of
every newly minted key. A zero value is 365 days in production (`DefaultAPIKeyMaxTTL`) and
unlimited in development. **90 days (`2160h`) is the recommended setting** for a host whose
clients can rotate keys. Under a bound, a mint without `expires_at` gets now plus the bound
(the 201 echoes it and `api_key_minted` records `expires_at_defaulted: true`); one beyond the
bound is 400 `api_key_ttl_exceeded`. Keys minted before the upgrade keep their expiry, or
none. Nothing warns before a key expires, so schedule rotation ahead of `expires_at` and use
the never-expiring-keys query in *Operations runbooks* to find what is left.
`APIKeyUnlimitedTTLAcknowledged` keeps production unlimited, with a
`posture_waiver=APIKeyUnlimitedTTLAcknowledged` WARN.

## Secrets pocket (`pockets/secrets`)

Owner-scoped secrets are sealed under a per-owner data key wrapped by a master key ring
(`APP_SECRETS_KEYS`, `APP_SECRETS_ACTIVE_KEY`). The two procedures are library calls:

- **Master key:** add the new key, deploy; make it active, deploy; `Rotation.RewrapOwnerKeys`
  until `Report.Failed` is empty; remove the old key from the ring **and from every other
  place it exists**. A backup of `secrets_owner_keys` taken before the rewrap stays readable
  for as long as the old key exists anywhere.
- **Owner key:** `Rotation.RotateOwnerKey` reseals one owner's secrets under a fresh data key
  in one transaction. Use it on a schedule or when someone with database read access leaves.
- **Offboarding:** `Vault.DeleteOwner` deletes the owner's secrets and destroys its data key
  in one transaction. Backups of the value rows alone are unreadable afterwards; a backup that
  also holds the wrapped owner key is readable until the ring key is retired.

Every read, write, delete and key operation is recorded in `secrets_audit` in the same
transaction (successful reads record `secret_read`; a failed open or a missing owner key
records nothing today).
