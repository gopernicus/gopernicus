---
title: Keys and rotation
description: The six authentication keys, how NewKeys decodes and loads them, what rotating each one costs, and the secrets-pocket key procedures.
---

# Keys and rotation

Current state on `main` at 2026-10-22, after S1. The authentication pocket never generates a
key in production: every signer, encrypter and keyer is a required collaborator, and
`authentication.NewKeys(mode, KeysInput{…}, log, need…)` is the one loader that decodes them.
The host fills `KeysInput` from its own environment (hex, every key), names the keys it wires
in `need`, and gets back the collaborators. A needed key that is unset is `ErrKeyRequired` in
production (one joined error naming each variable) and a per-boot `crypto/rand` key with one
WARN in development; a malformed value (not hex, too short) is refused in every mode. The
reference host (`examples/auth-cms/cmd/server/demo.go`) and the `init` scaffold use it.

## The six authentication keys

| Env (reference host) | Protects | Encoding (`NewKeys`) | Minimum | Rotation consequence |
|---|---|---|---|---|
| `AUTH_JWT_SECRET` | access-JWT signing (HS256 by default) | hex, decoded to bytes (before S1 the reference host used the hex *text* as the key — see continuity below) | 32 bytes for HS256, 48 for HS384, 64 for HS512 (enforced by `integrations/cryptids/golang-jwt`) | single key: every outstanding access JWT becomes invalid at once; sessions survive and clients refresh |
| `AUTH_CHALLENGE_PEPPER` | HMAC over OTP codes; digests of reset and magic-link tokens | hex, decoded to bytes | 32 bytes per ring key | the only key with overlap: `HMACKeyRing` keeps older keys so unexpired codes still verify until the old key is dropped |
| `AUTH_IDENTIFIER_KEY` | PII-free rate-limit and idempotency digests | hex, decoded to bytes | 32 bytes | single key: every budget and idempotency bucket resets once (transient) |
| `AUTH_DELIVERY_ENCRYPTER_KEY` | AES-256-GCM over queued delivery envelopes (the rendered secret and destination) | hex, decoded to bytes (before S1: a raw 32-byte string) | 32 bytes | single key: drain the queue first; in-flight sealed rows dead-letter under a new key |
| `AUTH_TOKEN_ENCRYPTER_KEY` | AES-256-GCM over stored OAuth provider tokens | hex, decoded to bytes (before S1: a raw 32-byte string); optional in every mode — unset → provider tokens are not persisted | 32 bytes | single key: stored provider tokens become unreadable; users re-link |
| `AUTH_TOTP_KEYS` + `AUTH_TOTP_ACTIVE_KEY` (`KeyTOTPRing`, since authentication v0.28.0) | AES-256-GCM over stored TOTP secrets, each sealed with its user id | a ring `id:hex,id:hex` parsed by `cryptids.ParseKeyRing`; the active variable names the sealing id; needed only by a host that wires `WithTOTP` | 32 bytes per ring key | a ring: add the new key, make it active, and keep the old id in the ring while any secret is sealed under it (secrets are not re-sealed on rotation); dropping it means those users' codes stop verifying until an admin TOTP reset and re-enrollment |

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
token, and link tokens are digested under the challenge pepper.

The sdk ships `cryptids.AESGCMRing` (key-id-tagged ciphertext, overlapping rotation) and the
secrets pocket uses it; the authentication pocket accepts any `cryptids.Encrypter`, so a host
may wire the ring for the delivery and provider-token keys today. The reference host wires
the single-key `cryptids.NewAESGCM`.

## Revoking every session at once

There is no bundled operation. Today:

1. `DELETE FROM sessions; DELETE FROM authentication_grants;` on the authentication database
   ends every refresh immediately. Outstanding access JWTs keep working for up to
   `AccessTokenTTL` (default 15 minutes) on the default liveness tier, and die at once on
   routes using the `Live()` tier.
2. If the signing key itself may be compromised, also rotate `AUTH_JWT_SECRET` on every
   instance: every outstanding access JWT is invalid immediately. On its own this does not end
   sessions — clients refresh and continue — so pair it with step 1.
3. Record what you did: the deletion writes no security event; add the operator's own note.

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
