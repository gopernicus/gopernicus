# S4d part 3 — Key management and password policy: key rings with `kid`, legacy-read encrypters, `iss`/`aud`, a breach checker, bcrypt cost (SOC 2 readiness, slice 4d part 3)

**Status:** EXECUTED 2026-10-28 (tags: sdk v0.23.0 @ d3fc9dcf, #258 + #260; bcrypt v0.3.0 + hibp v0.1.0 @ d403f6a1, #259; authentication v0.35.0 @ 409e030e, #261; golang-jwt v0.4.0 @ 6d0acbe1, #262; stores pgx v0.18.0 / turso v0.17.0 @ 82fd7c13, #265 (PR-3b, added during the build); examples + runbooks #266 @ 4b654e0f, no tags; record PR-6). See **As built** at the end; the tag table, MVS sets, upgrade notes and deviations are in `RELEASING.md` "Key management and password policy (SOC 2 slice S4d part 3)"; audit entry AUDIT-059. Was RATIFIED 2026-10-28 (owner: "all recs" — R-1 (a) HS256 ring now, R-2 (a) WARN, R-3 (a) docs-only identifier rotation, R-4 (b) no lockout, R-5 (a) INFO for single-key, R-6 (a) hibp + list-less blocklist, R-7 (a) bcrypt 12 with WithCost(10) hatch, R-8 (b) WARN + runbook); build-time owner rulings (rehash on login pulled in and made live; HIBP wired into no example) are the late sections below. Was DRAFT 2026-10-28 (planner; the four-reviewer gate — lead-backend-engineer, platform-sre, architecture-steward,
data-integration-reviewer — run on the first draft and folded under "Decided by gate").
Closes review.md **M7** (FAC-12, FAF-12, FSK-7, FAC-15) and **M8** (FAC-11, FAC-14); control-map rows "Key management &
rotation" (GAP → DEFAULT), "Rotate each key" (PARTIAL → DEFAULT), "Password policy" (OPT-IN breach → shipped checker +
production WARN), "Provider OAuth tokens at rest" (single key → ring).

- **Size:** M — six PRs, five tags on five modules (sdk; golang-jwt; authentication core; bcrypt; a new `hibp` module); no
  migration, no store tag, no views tag, no workshop module tag (the docs site changes ship with the record). Every feature
  is host opt-in; the single-key constructors and `KeysInput` fields all keep working.
- **Anchors.** `main` @ e65713a2 (#255, the #232 record): sdk v0.22.0; authentication v0.34.0 (go.mod pins sdk
  v0.21.0 — BE-10); stores pgx v0.17.0 / turso v0.16.0 (both pin sdk v0.21.0); examples pin sdk v0.22.0; views/goth
  v0.9.0; golang-jwt v0.3.0 (go.mod pins sdk v0.18.0); bcrypt v0.2.0; pgxdb
  v0.17.0; datastores/turso v0.10.0; workshop v0.10.2 (on origin); secrets v0.2.0. Sources: `review.md:284-291`,
  `passes/01-auth-core.md:357-430` (FAC-11/12/14/15), `passes/02-auth-federation.md:143-148` (FAF-12),
  `passes/06-sdk-kernel.md:137-143` (FSK-7), `control-map.md:16,18,22,23,119`, `roadmap.md:195-198`,
  `s4d2-session-lifetime.md` Notes (the part-3 handoff), `pockets/authentication/README.md:3159-3186`,
  `workshop/documentation/docs/guides/keys-and-rotation.md:17-57`.
- **Owner rulings (binding, 2026-10-28).** M7 first; every ring accepts a one-key ring (opt-in, non-breaking); existing
  tokens and ciphertexts keep working through a rotation; no secret in logs; production WARNs over refusals unless
  argued; migrations additive-only (none here); hosts own their schema; `make guard && make check` per PR; one worktree
  per PR at `../gopernicus-s4d3-<n>`; never `go work sync`; never ports 8082 or 55432; nothing exploit-shaped;
  authentication/authorization naming; vars and consts first, then interfaces; accept interfaces, return structs. The
  owner's hosts are passwordless: M8 serves password hosts and the SOC 2 story and stays small.
- **What `main` already does.** `sdk/pkg/cryptids`: `AESGCM` (raw base64url, no prefix), `KeyRing`/`ParseKeyRing`
  (`id:hex,…` + active; exactly 32 bytes; ids `^[a-z0-9][a-z0-9_-]{0,31}$`), `AESGCMRing` (`v1.<id>.<payload>`;
  `splitKeyed` makes a pre-ring value `ErrCiphertextMalformed`). `golangjwt.Signer`: one HMAC secret, method pinned, no
  `kid`. `NewKeys` (`keys.go:17-45`): five single hex keys + the TOTP ring. The challenge protector is a ring in code
  (`protector_key_id` on `challenges`; key id beside each recovery-code digest) but every host builds it from ONE
  `AUTH_CHALLENGE_PEPPER` under a hard-coded id (`"dev"`, `demo.go:282-286`): no environment shape for overlap.
  `parseAccessClaims` REJECTS a first-party token carrying `iss`/`aud`/`client_id`/`origin_client_id`/`act`
  (`token.go:127-131`); delegated tokens require `iss` = `OAuth2Config.Issuer`. `CompromisedPasswordChecker` is nil by
  default with no WARN (`config.go:211-224`, `service.go:1326`, fail-closed unless `CompromisedPasswordFailOpen`);
  bcrypt defaults to cost 10 and exposes none; the login per-subject budget is 5/min (`abuse.go:36`), no lockout.

## Key inventory (authentication pocket, as of v0.34.0)

| # | Key (env, `KeyName`) | Type today | Where its output lives | Rotating it today | After this slice |
|---|---|---|---|---|---|
| 1 | `AUTH_JWT_SECRET` (`KeyJWT`) | one HMAC secret, HS256 pinned; `golangjwt.New(string(keys.JWTSecret))` | the access JWT (cookie value or bearer), ≤ `AccessTokenTTL` (15 m) | every outstanding access JWT fails at once (401 until refresh); sessions survive (refresh never consults the signer) | `AUTH_JWT_KEYS` + `AUTH_JWT_ACTIVE_KEY` ring; `golangjwt.NewRing` stamps `kid`, verifies under any ring key; `AUTH_JWT_SECRET` beside the ring = the legacy key for `kid`-less tokens for one `AccessTokenTTL` |
| 2 | `AUTH_CHALLENGE_PEPPER` (`KeyChallengePepper`) | HMAC-SHA-256 ring in code, ONE key from env under a host-chosen id | `challenges.code_digest` + `protector_key_id` (minutes); recovery-code digests + key id (long-lived); link tokens are unkeyed SHA-256 | overlap works in code only if the host hand-builds a multi-key `HMACKeyRing`; dropping a key orphans recovery codes (`New` WARNs `recovery codes sealed under retired pepper keys`) | `AUTH_CHALLENGE_PEPPERS` + `AUTH_CHALLENGE_ACTIVE_PEPPER` ring form (ids explicit so the current id is preserved); runbook: regenerate recovery codes before dropping a key |
| 3 | `AUTH_IDENTIFIER_KEY` (`KeyIdentifier`) | one HMAC key (`HMACIdentifierKeyer`) | rate-limit and outbox idempotency keys (≤ minutes); `identifier_digest` on invitation/delivery audit rows (AUDIT-054, persisted, correlation only) | budgets and idempotency windows reset once; audit digests before the rotation no longer match recomputed ones | unchanged type; documented rotation (no dual-digest window — nothing durable is LOOKED UP by this digest) — R-3 |
| 4 | `AUTH_DELIVERY_ENCRYPTER_KEY` (`KeyDeliveryEncrypter`) | `cryptids.AESGCM`, single key | sealed command envelopes on the jobs rail / in-process queue (`command.Seal`/`Open`), seconds to minutes | drain first; in-flight rows dead-letter under a new key | `AUTH_DELIVERY_ENCRYPTER_KEYS` + `…_ACTIVE_KEY` ring; `AESGCMRing` with `WithLegacyKeyID` opens pre-ring rows |
| 5 | `AUTH_TOKEN_ENCRYPTER_KEY` (`KeyTokenEncrypter`, optional) | `cryptids.AESGCM`, single key | `oauth_accounts.access_token` / `refresh_token` (TEXT, pgx + turso); the pocket only ENCRYPTS (`oauth.go:818-830`), hosts decrypt | stored provider tokens unreadable; users re-link | `AUTH_TOKEN_ENCRYPTER_KEYS` + `…_ACTIVE_KEY` ring with legacy read; a legacy row PERSISTS until the user unlinks and relinks or the host runs the runbook sweep (a returning login never rewrites `oauth_accounts`, `oauth.go:374-381`; `Link` is insert-only; there is no token-rewrite port — DI-1) |
| 6 | `AUTH_TOTP_KEYS` + `AUTH_TOTP_ACTIVE_KEY` (`KeyTOTPRing`) | `AESGCMRing` (`KeyedEncrypter`), user id inside the envelope | `totp_credentials` sealed secret, `v1.<id>.` prefixed | already overlapping: add → activate → keep the old id while any secret is sealed under it | unchanged; runbook formalised; the model the other rings copy |
| — | not keys | CSRF (per-render random), refresh tokens / API keys (unkeyed SHA-256 at rest), passkeys (public keys), `oauth_states.payload` (opaque, short-lived, unencrypted PKCE/OIDC state), the secrets pocket's `APP_SECRETS_KEYS` ring (already a ring; its runbook exists in `keys-and-rotation.md`) | | | documented in the inventory, no change |

## Goal
A host can rotate every authentication key on a schedule with overlap and without logging anyone out, losing a queued
message or a stored provider token: the JWT signer and both encrypters become key rings that stamp a key id, keep reading
what the old single key wrote, and show their state on one boot line; first-party tokens can carry a verifiable
`iss`/`aud`; and a password host gets a shipped breach checker, a production WARN when it has none, and bcrypt cost 12.

## Scope and non-goals
**In scope:** `cryptids.AESGCMRing` legacy read (FSK-7 (a)); `golangjwt.NewRing` with `kid` (FAC-12); ring forms for the
JWT, pepper, delivery and provider-token keys in `NewKeys` (FAF-12); the `auth: keys` boot line; `SessionsConfig.Issuer`/
`Audience` (FAC-15); `integrations/passwords/hibp` + `protection.BlocklistChecker` + the production WARN (FAC-11); bcrypt
default cost 12 + `Cost()` + the cost WARN; an optional account lockout (FAC-14, R-4); a rotation runbook per key; the
record. **Non-goals:** asymmetric signing / JWKS (R-1 (b)); `jti` (the session id is in the token); AAD on `Encrypter`
(FSK-7 (b): a port change; TOTP already binds the owner in its envelope); a pocket-side re-seal port for
`oauth_accounts` (it would force a store tag; the runbook ships a host-Go sweep, DI-1); password history/expiry (NIST);
`aal`/`methods` on delegated tokens and `Identity.AuthTimeAsserted` (the other part-3 carries, deferred to a part 4 so
this slice stays on keys and passwords; NEXT.md); any store, views or scaffold change; a workshop P-rule for rings.

## Decided by gate
Reviews 2026-10-28 on this draft: data-integration-reviewer (DI, ship with edits), lead-backend-engineer (BE),
platform-sre (SRE), architecture-steward (AS). Folded unless marked.
- DI-1 (HIGH, the draft said a legacy `oauth_accounts` row re-seals at the next login; it does not — a returning login
  takes `GetByProvider` + `oauthSession` (`oauth.go:374-381`) and never writes the row; tokens are encrypted only in
  `newAccount` on link/register/adopt; `Link` is insert-only on pgx, turso and authmem; no token-rewrite port) → the
  inventory, §5 and Verification §2 now say a legacy row persists until unlink/relink or the host sweep; the runbook's
  drop gate is a zero count; the sweep is host Go (decrypt under the legacy id, encrypt active, compare-and-swap
  `UPDATE`), never SQL alone, and never a new pocket port (it would force a store tag).
- DI-2 (MED) + SRE-1 → the count SQL covers `refresh_token` too (Google omits it on repeat consent, so a legacy refresh
  token outlives the access-token check). DI-7 (LOW) → the count is the GATE for dropping the legacy id (runbook, Risks).
- DI-3/4/5/6 (OK) → both token columns are unbounded `TEXT` on both dialects (no index, CHECK or collation); the jobs
  payload is `BYTEA`/`BLOB`; authmem stores the struct as-is; storetest round-trips opaque ciphertext
  (`storetest.go:772-787`); no store tag. Optional: a 36-char-prefixed storetest value (neutral suite, no store change).
- SRE-2 (HIGH, a rolling deploy with the new key active at once: old instances on `Signer(secret)` reject `kid`-signed
  tokens — in fact they ignore `kid`, but old `AESGCM` instances cannot open `v1.` envelopes and dead-letter them) →
  **three-phase rotation** (§1, §2, §3a, §5): the legacy id may be ACTIVE; while it is, the ring writes the single-key
  shape (no `kid`, no prefix) and reads both; activation of the new id is phase (b), after the whole fleet runs (a).
- SRE-3 (HIGH, `*url.Error` prints the request URL and so the five-character hash prefix; the service wraps the checker
  error with `%w`) → hibp builds fresh errors over `sdk.ErrUnavailable`, never wraps the transport error; a test asserts no
  5-hex run or URL path in any error (§4).
- SRE-4 (MED) → every runbook subsection states that a code rollback below the three new tags after phase (b) is lossy.
  SRE-5 (MED) → an "Emergency rotation" paragraph per key; the legacy WARN is never acceptable during a compromise;
  `operations-runbooks.md:61` corrected to `RevokeAllSessions`. SRE-6 (MED) → R-8. SRE-7 (MED) → `KeyRing.Validate()`
  exported in sdk v0.23.0; golang-jwt repins and PR-2 follows tag 1. SRE-8 (MED, the docs say link tokens are digested
  under the pepper; `DigestToken` is unkeyed SHA-256) → the inventory wins, the two docs sentences are corrected, the
  pepper window is the longest challenge TTL with recovery codes as the documented long tail. SRE-9 (MED) → the fail-closed
  refusal is **503**, a `password_breach_check_unavailable` WARN on both branches, a 1 MiB body cap, no redirects.
  SRE-10 (MED) → the dummy verify on the miss paths; row 9's "login ×4" corrected to new hashes only. SRE-11 (LOW) →
  `kid` is pattern-checked before it is echoed. SRE-12 (LOW) → the baseline asserts the positive boot line and its
  `.env.example` drops `AUTH_JWT_SECRET`. SRE-13 (LOW) → the auth-cms-shape cold set proves the MVS lift (Release step 4).
- AS-1/2/4/6 (aligned) → `WithLegacyKeyID` lives in sdk beside `AESGCMRing` (the owner of the wire format); `RingSigner`
  reuses `cryptids.KeyRing`, NO signing-ring type (if ever needed it is sdk vocabulary, never golang-jwt's);
  `BlocklistChecker` in `protection` beside the other bundled defaults, structural (importing `logic/authentication`
  would cycle); `Issuer`/`Audience` on `SessionsConfig`, enforced in `token.go` (precedent `OAuth2Config.Issuer`).
- AS-3 (MED) → hibp is an integration: "an external vendor's live API contract" is a valid single dependency and
  "vendor-specific connectors are never sdk defaults even when stdlib-implementable" (precedent `integrations/oauth/
  github`); `passwords/` is a NEW category ratified in ARCHITECTURE.md's layout tree in PR-4 (module count → 48); there
  is no `integrations/README.md`.
- AS-5 (LOW) → the optional interfaces are NAMED exported types beside `Hasher` (`KeyRingReporter`, `CostReporter`), not
  anonymous asserts; not promoted to sdk (single consumer; `Cost` is algorithm-specific); the third-party-signer gap is
  documented.
- BE-1 (HIGH, ring keys are exactly 32 bytes but the JWT secret and pepper are "at least 32", so a 64-byte secret could
  never join a ring and a 64-byte pepper would orphan every recovery code) → `ParseKeyRingMinimum` / `ValidateMinimum`
  in sdk v0.23.0 (§1); `NewRing` validates at ≥ 32; the HS384/HS512 `Signer` case is named in row 2.
- BE-2 (HIGH, `unverifiedDelegatedShape` treats any `iss`/`aud` as delegated, so an expired-token first-party logout
  under an issuer would fail `ErrDelegatedCredential`) → the helper reads `session_profile` first (§3c); an expired-token
  logout test with `Issuer` set.
- BE-3 (HIGH, only under R-4 (a): the counter was unsound) → §3d rewritten (both event types, skip `blocked`, stop at
  `success`, invalid-credential failures only, refuse with a nil rail, dummy verify first); R-4's recommendation (b)
  strengthened.
- BE-4 (MED) → a set ring satisfies `need`; no ephemeral `legacy` beside a ring in development. BE-5 (MED) → `Issuer`/
  `Audience` refuse to collide with `OAuth2Config.Issuer`/`MCPResource`/`APIResource`; the values are threaded through
  `Service`. BE-6 (MED) → `opaque` instead of a guessed `single`; `HMACChallengeProtector.KeyIDs()`. BE-7 = DI-1.
  BE-8 (LOW) → a compile-time conversion pin in `keys_test.go`. BE-9 (LOW, "no pin bump needed") → NOT taken: SRE-7 and
  BE-1 both need sdk v0.23.0 in golang-jwt, so PR-2 repins and follows tag 1. BE-10 (LOW) → anchors corrected: core and
  stores pin sdk v0.21.0; set A wording fixed. BE-11 (LOW) → §1(2) moves to PR-5; the 503 was already fixed under SRE-9.
  BE-12 (LOW) → the JWT legacy window is `max(AccessTokenTTL, OAuth2 AccessTTL, ConsentTTL) + 60 s`.

## Design

### 1. `cryptids.AESGCMRing` reads pre-ring ciphertext (sdk v0.23.0; FSK-7 (a), FAF-12)
- `NewAESGCMRing(ring KeyRing, opts ...RingOption)` (additive variadic). `WithLegacyKeyID(id string)` names the ring key
  that opens single-key `AESGCM` output: `Decrypt` of a value with NO `.` (base64url never contains one) opens under
  that key; a dotted value takes the `v1.<id>.` path. Without the option a legacy value stays `ErrCiphertextMalformed`.
  The id must be in the ring (`ErrLegacyKeyMissing`, `sdk.ErrInvalidInput`). `KeyIDOf` of a legacy value returns the
  legacy id. `Encrypt` writes the prefixed form — EXCEPT while the active key IS the legacy id, when it writes the
  single-key shape (SRE-2, phase (a): not-yet-reconfigured instances holding a plain `AESGCM` must open what the
  reconfigured ones write). Once another key is active every write is prefixed; legacy rows shrink only by rewrite.
- `LegacyKeyID() string` on `*AESGCMRing` (`""` when none). `KeyedEncrypter` is unchanged (a new method would break
  every implementer); consumers detect structurally. `WithLegacyNotAfter(t)` is R-8.
- `KeyRing.Validate() error` exported (SRE-7) so `golangjwt.NewRing` validates through the sdk. **Key length (BE-1):**
  `ParseKeyRing`/`Validate` keep exact-32 (AES-256), but the JWT secret and pepper are "at least 32" today, so a 64-byte
  one could never join a ring: sdk adds `ParseKeyRingMinimum(keys, active string, min int)` + `KeyRing.ValidateMinimum
  (min int)`; `NewKeys` parses the JWT and pepper rings with the minimum form, the encrypter rings exact. `KeyRing` doc
  drops "AES-256" (it is the 32-byte ring AES-GCM, HMAC-SHA-256 and HS256 share). No other sdk change. Errors never carry
  key material or plaintext (the redaction assertions extend to the legacy path).

### 2. `golangjwt.NewRing`: an HS256 signer that stamps `kid` (golang-jwt v0.4.0; FAC-12)
- `NewRing(ring cryptids.KeyRing, opts ...RingOption) (*RingSigner, error)`; `RingSigner` satisfies `cryptids.JWTSigner`
  structurally (`var _ cryptids.JWTSigner = (*RingSigner)(nil)`). HS256 only, validated with `ring.ValidateMinimum(32)`
  (keys of any length ≥ 32 are fine for HMAC); HS384/HS512 rings are out of scope (documented; `Signer` keeps them), and
  a host whose legacy `Signer` was built `WithMethod(HS384|HS512)` CANNOT read its old tokens through an HS256 ring — it
  stays on `Signer` or accepts the 401 wave (BE-1, upgrade-note row 2).
- `Sign` sets the JOSE header `kid` = `ring.Active` and signs under it — EXCEPT while the active key is the legacy id,
  when no `kid` is written (SRE-2: a pre-ring `Signer` on a not-yet-reconfigured instance ignores `kid` anyway, but the
  symmetry with the encrypter keeps phase (a) "writes what the old code wrote" on every key). `Verify` reads `kid` from
  the header: a string matching `cryptids` key-id syntax and naming a ring key → that key; an unknown, malformed or
  non-string `kid` → reject, and the error echoes the id only when it matches the id pattern, else `invalid` (SRE-11); an
  ABSENT `kid` → the `WithLegacyKeyID(id)` key when set, else reject. The method pin (`*jwt.SigningMethodHMAC == HS256`, `WithValidMethods`, `WithStrictDecoding`,
  `WithExpirationRequired`, `WithIssuedAt`, 60 s leeway, the 0001–9999 NumericDate bound) is the SAME code path as
  `Signer.Verify` — extract the parse/validate tail into an unexported helper both signers call, so the two cannot drift.
- `ActiveKeyID() string`, `KeyIDs() []string` (sorted), `LegacyKeyID() string`. `Format`/`LogValue` print the method, the
  active id, the key count and whether a legacy id is set — never a key (`redaction_test.go` extends to `RingSigner`).
- `crossverify_test.go` gains: a `New(secret)` token verifies under `NewRing({legacy: secret}, WithLegacyKeyID("legacy"))`
  and fails without the option; `kid=k2` fails under a ring holding only `k1` (the error names the id, never the key);
  alg confusion (HS384, RS256, `none`) is still rejected before any MAC; the `testdata` legacy token still verifies.
- go.mod repins sdk v0.23.0 (explicit `GOWORK=off go get`) for `KeyRing.Validate()` (SRE-7), so PR-2 follows tag 1.

### 3. Authentication core (authentication v0.35.0)
**3a. `NewKeys` ring forms (FAF-12).** `KeysInput` gains, each a pair mirroring the TOTP ring: `JWTKeys`/`JWTActiveKey`
(`AUTH_JWT_KEYS`, `AUTH_JWT_ACTIVE_KEY`), `ChallengePeppers`/`ChallengeActivePepper` (`AUTH_CHALLENGE_PEPPERS`,
`AUTH_CHALLENGE_ACTIVE_PEPPER`), `DeliveryEncrypterKeys`/`DeliveryEncrypterActiveKey`, `TokenEncrypterKeys`/
`TokenEncrypterActiveKey` (`AUTH_DELIVERY_ENCRYPTER_KEYS`/`…_ACTIVE_KEY`, `AUTH_TOKEN_ENCRYPTER_KEYS`/`…_ACTIVE_KEY`).
`Keys` gains `JWTRing`, `ChallengeRing`, `DeliveryEncrypterRing`, `TokenEncrypterRing cryptids.KeyRing` and
`JWTLegacyKeyID`, `DeliveryLegacyKeyID`, `TokenLegacyKeyID string`. Resolution per key, same `need` names:
- single variable alone → today's field, unchanged (a v0.34.0 host repins with no change);
- ring pair alone → the ring field (JWT/pepper via `ParseKeyRingMinimum(…, 32)`, encrypters via `ParseKeyRing`);
- BOTH for JWT / delivery / token → the ring plus the single key joined under the reserved id `legacy`
  (`authentication.LegacyKeyID`; a ring entry already named `legacy` beside the single variable is `ErrKeyRequired`)
  and the `…LegacyKeyID` field set — the migration window. The join happens BEFORE validation, so
  `AUTH_JWT_ACTIVE_KEY=legacy` (and the encrypter equivalents) is legal: phase (a) of the rolling runbook (SRE-2);
- BOTH for the pepper → `ErrKeyRequired` naming both: challenge rows and recovery codes STORE the key id, so the host
  names its current id itself (`AUTH_CHALLENGE_PEPPERS="dev:<current hex>,p2:<new hex>"`); the ring converts by plain
  struct conversion `protection.HMACKeyRing(keys.ChallengeRing)` (identical underlying types; verified against `main`
  with a scratch test; PR-3 pins it with a compile-time conversion in `keys_test.go`, BE-8);
- a set ring SATISFIES `need` (BE-4): development mints an ephemeral single key only when NEITHER form is set (the loop
  at `keys.go:142-162` would otherwise mint a `legacy` beside a ring), the token-encrypter INFO fires only then, and
  production refuses a needed key unset in both forms. `Keys`/`KeysInput` `String`/`LogValue` list the new fields
  redacted; `keys_test.go` covers every role and branch.

**3b. The `auth: keys` boot line.** One INFO line at construction: `jwt_signer=ring|opaque active_key_id=k2
key_ids=[k1 k2] legacy=yes|no`, `delivery_encrypter=ring|single|opaque …`, `token_encrypter=ring|single|opaque|none …`,
`challenge_protector active_key_id=… key_ids=[…]`, `totp_encrypter=…`, `identifier_keyer=single`, `token_issuer=<iss>|none
token_audience=…`. `ring` = implements `KeyRingReporter`; `single` = the sdk's `*cryptids.AESGCM` by type assertion
(the core may name an sdk type); anything else is `opaque`, never a guessed `single` (BE-6: the core cannot see
`golangjwt.Signer`, and a wrapped signer is not single-key by inspection). `HMACChallengeProtector` gains `KeyIDs()`
so it satisfies `KeyRingReporter` too (its `LegacyKeyID()` returns `""`).
Read through two NAMED optional interfaces declared beside `Hasher` in `logic/authentication/service.go` (AS-5):
`KeyRingReporter{ActiveKeyID() string; KeyIDs() []string; LegacyKeyID() string}` and `CostReporter{Cost() int}`;
the protector's existing `ActiveKeyID()` is read as-is. The core imports nothing new; adapters and tests get a documented
contract. Documented gap: a third-party signer that does not implement `KeyRingReporter` gets no legacy WARN. Key ids
are configuration, not secrets. No WARN for a single-key signer or encrypter (R-5: rotation is a procedure, not a
misconfiguration); a legacy id set in production logs `posture_warning=legacy_key_read_enabled key=<role>` so the window
gets closed (one line per role) — and the runbook says in bold that this WARN is never "acceptable" during a compromise
(SRE-5).

**3c. `iss`/`aud` on first-party tokens (FAC-15).** `SessionsConfig.Issuer` (`AUTH_TOKEN_ISSUER`) and
`SessionsConfig.Audience` (`AUTH_TOKEN_AUDIENCE`), both optional strings. Set → `signAccessToken` adds `iss`/`aud`;
`parseAccessClaims` requires an exact match on a first-party token (`aud` as a string or a one-element array) and a
first-party token WITHOUT them is rejected (the pinned generic 401; the client refreshes — at most one refresh per client at
enablement, documented). Unset → today's rule (present claims rejected). The delegated branch (`iss` = `OAuth2Config.Issuer`)
is untouched; `session_profile` remains the discriminator. **Logout (BE-2):** `unverifiedDelegatedShape` (`token.go:278`)
today treats ANY `iss`/`aud` as delegated, and `firstPartyLogoutToken` calls it when `Verify` fails — the expired-token
logout is the common case — so with an issuer set every first-party logout would become `ErrDelegatedCredential`. It is
rewritten to read `session_profile` first and, for `first_party`, to treat only `client_id`/`origin_client_id`/`act` as
the delegated markers; an expired-token logout test with `Issuer` set is added. **Collision (BE-5):** the same signer
mints delegated tokens with `iss` = `OAuth2Config.Issuer` and `aud` = a resource, so construction refuses
`SessionsConfig.Issuer` equal to `OAuth2Config.Issuer` and `Audience` equal to `MCPResource` or `APIResource`
(`ErrTokenAudienceCollides`); the expected values are threaded through `Service` (parse becomes a method; the logout
helper takes them), not a free function. Validation: `Audience` without `Issuer` is allowed; either set to whitespace
refuses (`ErrTokenIssuerInvalid`). Mirrors in `config_env_test.go`.

**3d. M8 in the core (FAC-11, FAC-14).**
- Production WARN `posture_warning=password_breach_check_none` when password flows are enabled (`Hasher` wired, not
  `PasswordFlowsDisabled`) and `CompromisedPasswordChecker` is nil (R-2; the plan builds the WARN). Development: silent.
- `protection.BlocklistChecker`: `NewBlocklistChecker(r io.Reader)` — one entry per line, `strings.ToLower` +
  `TrimSpace` (NFKC is not stdlib; documented), a `map[string]struct{}`; structural. The pocket ships NO list
  (licensing, public repo, size); the docs name sources (HIBP offline corpus, SecLists top-100k) and the file shape.
- Hasher cost: a `CostReporter` hasher with `Cost()` < 12 in production → `posture_warning=password_hash_cost_low
  cost=10 recommended=12`. The core never imports bcrypt.
- Breach-check outage (SRE-9): on a checker error BOTH branches log one WARN `password_breach_check_unavailable
  outcome=refused|allowed` (no password, no checker error text); fail-closed surfaces `sdk.ErrUnavailable` → **503**.
- Timing oracle (SRE-10): at cost 12 an unknown-identifier login returns ~250 ms faster (`service.go:1179-1197` hashes
  nothing on the miss path); PR-3 adds a dummy `VerifyPassword` against a hash minted once at construction.
- Lockout (R-4, only if (a); the draft's counter was unsound, BE-3 — this is the corrected shape):
  `PasswordConfig.Lockout{Threshold int; Window, Duration time.Duration}` (`AUTH_LOGIN_LOCKOUT_*`; zero = off). Before
  `VerifyPassword`, list the user's events since `now − Window` across BOTH `login` and `token_issued` (`/auth/token`
  records the latter); skip `blocked` rows (they neither unlock nor extend); stop at the first `success`; count only
  invalid-credential failures (`ErrEmailNotVerified` records `failure` and must not count); `Threshold` reached and the
  newest within `Duration` → generic 401 + ONE `login` row `status=blocked reason=locked` per attempt, AFTER the dummy
  verify. Construction refuses `Lockout` with a nil `Repositories.SecurityEvents` (the rail is waivable). The read is
  `user_id` + time under the existing single-column indexes (`0008`), fine at `Threshold`-sized windows. No escalation
  (a lockout on a known username is a denial lever; the generic 401 and the per-IP budget are the controls).

### 4. bcrypt v0.3.0 and `integrations/passwords/hibp` v0.1.0 (FAC-11)
- **bcrypt:** `DefaultCost = 12` (exported const; `New()` uses it; `WithCost` unchanged, out-of-range falls back to 12);
  `(*Hasher).Cost() int`; README: cost guidance (12 ≈ 250 ms on a 2024 server core; registration/login latency ×4 vs 10;
  existing hashes verify at their stored cost, no rehash), the `WithCost(10)` hatch. `bcrypt_test.go` asserts the default
  and that a cost-10 hash still verifies.
- **hibp** (new module `integrations/passwords/hibp`, go.mod requires sdk v0.22.0 only — G13): `New(opts ...Option)
  (*Checker, error)`; `WithHTTPClient`, `WithBaseURL` (tests), `WithUserAgent` (HIBP requires one; default
  `gopernicus-hibp`), `WithTimeout` (default 3 s), `WithMinimumCount` (default 1). `IsCompromised(ctx, password)`: SHA-1
  upper-hex, `GET {base}/range/{first 5}` with `Add-Padding: true`, parse `SUFFIX:COUNT` lines, padding rows (count 0)
  ignored, the suffix compared against every line (no early exit), `count ≥ minimum` → compromised. Any transport, status
  or parse failure → a FRESH error built from `sdk.ErrUnavailable` plus a class (`hibp: upstream status 429`, `hibp:
  transport failure`, `hibp: malformed response`) — NEVER `%w` over the transport error, because `*url.Error` prints the
  request URL and with it the five-character hash prefix (SRE-3). The core's fail-closed default then refuses the
  password; `CompromisedPasswordFailOpen` is the host's hatch. The response body is capped (`io.LimitReader`, 1 MiB) and
  the client refuses redirects (`CheckRedirect` returns `http.ErrUseLastResponse`). No retries, no cache, no logging; the
  password, the hash and the prefix never appear in an error. Tests on `httptest`: hit, miss, padding-only, 429/500,
  timeout, malformed line, oversized body, a redirect, the UA header sent, and an assertion that no error string contains
  a 5-hex-character run or the base URL path. The module satisfies `authlogic.CompromisedPasswordChecker` structurally; a compile-time
  check lives in the auth-cms wiring, not here. Added to `go.work`, `Makefile MODULES`, the integrations README table.

### 5. Examples, runbooks, record
- **auth-cms** (`demo.go`): ring forms when the ring variables are set, else today's single-key constructors;
  `golangjwt.NewRing(keys.JWTRing, golangjwt.WithLegacyKeyID(keys.JWTLegacyKeyID))` (an empty id = no option);
  `cryptids.NewAESGCMRing(ring, cryptids.WithLegacyKeyID(id))` for delivery and token; the pepper ring by struct
  conversion; `AUTH_HIBP=1` wires the hibp checker (default off: no network in development); `.env.example` entries.
  **production-baseline:** the JWT ring form (its two keys). No scaffold change (the scaffold has no key wiring; P6 only).
- **Runbooks** (`operations-runbooks.md`, new "Rotate a key", one subsection per inventory row). Every ring rotation is
  THREE PHASES (SRE-2) so a rolling fleet never 401s or dead-letters: **(a)** ring-capable code + the ring env with the
  legacy id ACTIVE (`AUTH_JWT_KEYS=k1:<new>`, `AUTH_JWT_ACTIVE_KEY=legacy`, `AUTH_JWT_SECRET` kept) — reads both shapes,
  writes the old one; **(b)** once every instance runs (a), activate the new id; **(c)** after the window, drop the
  legacy id. Each subsection carries:
  - the **window** — JWT `max(AccessTokenTTL, OAuth2 AccessTTL, ConsentTTL) + 60 s` (the same signer mints delegated
    tokens, BE-12); pepper = the longest challenge TTL for codes (link tokens are unkeyed SHA-256, `DigestToken` — the
    docs sentence "reset and magic-link tokens are digested under the pepper" is corrected, SRE-8) with recovery codes as
    the long tail (a dormant user's codes stay under the old id until their next AAL2 step-up: keep the id, or accept
    the loss and record the WARN's `key_ids` as evidence); identifier `max(budget window)`; delivery = queue drained;
    provider tokens = a ZERO count from `SELECT count(*) FROM oauth_accounts WHERE (access_token <> '' AND access_token
    NOT LIKE 'v1.%') OR (refresh_token <> '' AND refresh_token NOT LIKE 'v1.%')` (legacy values are base64url and never
    contain `.`; valid on both dialects), reached by unlink/relink or the HOST-GO sweep recipe (decrypt under the legacy
    id, encrypt active, `UPDATE … SET access_token = $new WHERE provider = $p AND provider_user_id = $u AND
    access_token = $old` — compare-and-swap, same for `refresh_token`; SQL alone cannot re-seal); TOTP = no row names
    the old id;
  - the **evidence** (`legacy=no` on the boot line closes the window; `kid` in a fresh token's header);
  - **rollback**: re-activating the old id is a KEY rollback; a CODE rollback below the three new tags after phase (b)
    loses queued deliveries and stored tokens and logs everyone out (SRE-4);
  - **Emergency rotation** (SRE-5): on compromise drop the key at once with NO legacy id, and the cost (401 wave,
    dead-letters, re-link, orphaned recovery codes); the legacy WARN is never acceptable during a compromise;
    `operations-runbooks.md:61` ("rotate `AUTH_JWT_SECRET` to end them at once") is corrected to `RevokeAllSessions`.
  `keys-and-rotation.md` table rewritten to the inventory's after-column; `production-posture.md` password section
  (cost 12, the checker, the WARN, lockout); `SECURITY.md` rows.
- **Record:** `RELEASING.md` ONE section; `control-map.md` rows 16/18/22/23/119; `roadmap.md` S4 line; `NEXT.md`
  (part-4 carries); AUDIT entry; `plans/s4d3-keys-passwords.md` (executed copy).

## Upgrade notes
> **Read first:** `grep -rnE 'golangjwt\.New\(|cryptids\.NewAESGCM\(|NewHMACChallengeProtector|AUTH_JWT_SECRET|
> AUTH_CHALLENGE_PEPPER|AUTH_DELIVERY_ENCRYPTER_KEY|AUTH_TOKEN_ENCRYPTER_KEY|bcrypt\.New\(|CompromisedPasswordChecker'
> --include='*.go' --include='.env*'`.

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `cryptids.WithLegacyKeyID` on `NewAESGCMRing` | opt-in; nothing changes without the option | — | sdk@v0.23.0 |
| 2 | `golangjwt.NewRing`, `RingSigner`, `kid`; go.mod → sdk v0.23.0 | opt-in; `New` unchanged; an HS384/HS512 `Signer` host cannot read its old tokens through the HS256 ring (BE-1) | stay on `Signer`, or accept one 401 wave | golang-jwt@v0.4.0 |
| 3 | ring variables in `NewKeys`; `Keys` ring + legacy-id fields; `LegacyKeyID` | opt-in; a host setting only the single variables is unchanged; a ring key named `legacy` beside the single var refuses; pepper single + ring refuses | use one form | authentication@v0.35.0 |
| 4 | the `auth: keys` boot line; production `posture_warning=legacy_key_read_enabled` | one INFO line; one WARN per role while a legacy id is set | drop the single variable after the window | authentication@v0.35.0 |
| 5 | `SessionsConfig.Issuer`/`Audience` | opt-in; enabling → every outstanding access token refreshes once | leave empty | authentication@v0.35.0 |
| 6 | production `posture_warning=password_breach_check_none` | password hosts with no checker see one WARN | wire hibp or a blocklist (or R-2 (b)'s acknowledgement) | authentication@v0.35.0 |
| 7 | production `posture_warning=password_hash_cost_low` | a host on bcrypt v0.2.0 (`Cost()` absent) sees nothing; on v0.3.0 with `WithCost(10)` sees one WARN | `WithCost(12)` | authentication@v0.35.0 + bcrypt@v0.3.0 |
| 8 | `PasswordConfig.Lockout` (if R-4 (a)) | opt-in; zero = off | — | authentication@v0.35.0 |
| 9 | **bcrypt `New()` defaults to cost 12** | registration, password set/change/reset and logins against NEW hashes take ~4× longer (≈250 ms); logins against existing cost-10 hashes are unchanged (verified at the stored cost) | `WithCost(10)` | bcrypt@v0.3.0 |
| 10 | `integrations/passwords/hibp` | new module; fail-closed by the core's default | `CompromisedPasswordFailOpen` | hibp@v0.1.0 |
| 11 | auth-cms / production-baseline ring wiring; `.env.example`; runbooks | example behaviour; docs | — | examples (untagged), docs |

Row 9 is the only behaviour change a repin brings without opting in. Core stays pinned to sdk v0.21.0 (it uses nothing
from §1; the boot line reads named optional interfaces and `LegacyKeyID` is pocket-local — BE-10), so set A holds with
no store repin; a host that wires the ring forms lifts sdk to v0.23.0 through golang-jwt or its own `cryptids` call. **Not touched:** stores, views/goth,
`pockets`, workshop, ui/goth, pgxdb, any port's method set, any schema.

**By host shape:**
- **gps-three-sixty** (native Google OAuth → first-party sessions, bearer SPA, pgx + goredis): rows 3–5 at repin, in the
  three phases: (a) repin, add `AUTH_JWT_KEYS="k1:<new>"` + `AUTH_JWT_ACTIVE_KEY=legacy`, KEEP `AUTH_JWT_SECRET` → boot
  line `legacy=yes active_key_id=legacy`, nothing changes on the wire; (b) fleet on (a) → `AUTH_JWT_ACTIVE_KEY=k1`; (c)
  after the JWT window unset `AUTH_JWT_SECRET` → `legacy=no`. If it stores provider tokens, the same on
  `AUTH_TOKEN_ENCRYPTER_KEYS` with the count SQL at zero as the (c) gate. `AUTH_TOKEN_ISSUER`/`AUDIENCE` optional. No
  passwords → rows 6–10 do not apply.
- **battleboardhq** (magic link + Google, `invitation_only`, pooled PG under RLS, SPA): as above plus the delivery ring
  and the pepper ring — its first pepper entry MUST reuse the id its `HMACKeyRing` uses today (reference value `dev`) so
  recovery codes keep verifying. Lockout and breach rows do not apply.
- **gps-360-go** (old pins): nothing until it repins; then as gps-three-sixty.
- **password hosts** (auth-cms shape): rows 6–10 — hibp (fail-closed; decide `CompromisedPasswordFailOpen`) or a
  blocklist file; cost 12 or a conscious `WithCost(10)`; lockout optional.
- **examples:** auth-cms (ring forms behind env; `AUTH_HIBP`; `$B` drives §1–§3), production-baseline (JWT ring form,
  no legacy id, zero-WARN test kept), external-identity (no signer; nothing).

## Risks
(1) A legacy id is a verify-only downgrade surface while it is set; controls: the production WARN (row 4), phase (c)
and its gates (the count SQL, DI-7), the record's `legacy=no` evidence; the legacy form is written only in phase (a).
(2) bcrypt 12 raises new-hash latency ~4× on repin (row 9); one-option hatch. (3) hibp is fail-closed by default: an HIBP
outage refuses registrations and password changes (3 s timeout; `CompromisedPasswordFailOpen` documented beside it).
(4) Lockout, if built, is a denial lever on a known username; generic 401, per-IP budget, opt-in. (5) The pepper
"both forms refuse" rule is a boot failure naming both variables; no host sets the ring form today. (6) `iss`/`aud`
enablement costs every client one refresh; no grace built.

## Tasks as PRs
Worktrees `../gopernicus-s4d3-<pr>`; the owner merges; Claude cuts each tag after a green `live-stores` dispatch on `main`,
confirms it on origin, cold-verifies; every PR runs with model **opus**; live legs on a free port (`lsof -i :8096` first;
never 8082 or 55432); `make guard && make check` in every verify line.

- **PR-1 — sdk: `AESGCMRing` legacy read (`sdk/cryptids-legacy-read`).** Depends on nothing. **Tag:** `sdk/v0.23.0`.
  **Files:** `sdk/pkg/cryptids/{keyring.go (RingOption, WithLegacyKeyID, ErrLegacyKeyMissing, the no-dot branch in
  Decrypt/KeyIDOf, LegacyKeyID, the legacy-active write, Validate/ValidateMinimum, ParseKeyRingMinimum; doc),
  keyring_test.go (legacy opens / KeyIDOf / tampered = ErrCiphertextAuthFailed / no option = ErrCiphertextMalformed /
  missing legacy id refuses / legacy-active writes unprefixed / minimum-length parse / errors carry no bytes)}`,
  `sdk/README.md`, `workshop/documentation/docs/sdk/pkg.md` (`## cryptids`). **Verify:** `cd sdk && go build ./... &&
  go test -race ./... && go vet ./...`; `make guard`; `make check`.
- **PR-2 — golang-jwt: `NewRing` with `kid` (`golang-jwt/ring-signer`).** Depends on tag 1. **Tag:** `integrations/
  cryptids/golang-jwt/v0.4.0`. **Files:** `integrations/cryptids/golang-jwt/{ring.go (new), golangjwt.go (shared verify
  tail; no behaviour change), ring_test.go (new), crossverify_test.go, redaction_test.go, README.md ("Key rings and
  rotation"), go.mod, go.sum (sdk → v0.23.0, explicit `GOWORK=off go get`)}`. **Verify:** `cd integrations/cryptids/
  golang-jwt && go build ./... && go test -race ./... && go vet ./...`; `make guard`; `make check`.
- **PR-3 — authentication core (`authentication/s4d3-core`).** Depends on nothing at the module level (core tests use
  fakes implementing `KeyRingReporter`/`CostReporter`; the ring signer is exercised in PR-5). **Tag:** `pockets/
  authentication/v0.35.0`. **Files** (under `pockets/authentication/`): `keys.go`, `keys_test.go` (§3a + the conversion
  pin), `config.go` (`SessionsConfig.Issuer/Audience`; `PasswordConfig.Lockout` if R-4 (a); mirrors), `configuration.go`
  (the `auth: keys` and issuer lines), `security.go` (the posture WARNs), `configuration_errors.go`, `config_env_test.go`,
  `posture_test.go`, `configuration_hardening_test.go`, `logic/authentication/{token.go (iss/aud mint + parse; the
  logout helper), service.go (KeyRingReporter, CostReporter; Issuer/Audience threading; the dummy verify; the
  breach-unavailable WARN; lockout gate), options.go, password_lockout.go + _test (if R-4 (a)), token_issuer_test.go,
  protection/{hmac.go (KeyIDs), blocklist.go, blocklist_test.go}}`, `README.md` (keys section = the inventory; password
  policy). **Verify:** `cd pockets/authentication && go build ./... && go test -race ./... && go vet ./...`; `cd
  examples/auth-cms && go test ./...` (go.work; no wiring change yet); `make guard`; `make check`; Verification §1 except
  (2), which moves to PR-5 (BE-11).
- **PR-4 — bcrypt cost 12 + the hibp module (`integrations/passwords-hibp`).** Depends on nothing. **Tags:**
  `integrations/cryptids/bcrypt/v0.3.0`, `integrations/passwords/hibp/v0.1.0`. **Files:** `integrations/cryptids/bcrypt/
  {bcrypt.go, bcrypt_test.go, README.md}`; `integrations/passwords/hibp/{go.mod, go.sum, hibp.go, hibp_test.go,
  README.md}` (new); `go.work`; `Makefile` (MODULES); `ARCHITECTURE.md` (layout tree: the `passwords/` category; module
  count → 48); `workshop/documentation/docs/integrations/catalog.md`. The boundary guard globs `integrations/*/*/go.mod`
  (`integration_boundaries_test.go:68`): no guard edit. **Verify:** `cd integrations/cryptids/bcrypt && go test ./...`;
  `cd integrations/passwords/hibp && go build ./... && go test -race ./... && go vet ./... && govulncheck ./...`; `make
  guard` (G13); `make check`.
- **PR-5 — examples + runbooks (`docs/s4d3-rotation-runbooks`).** Depends on tags 1–5 (explicit `GOWORK=off go get`
  repins: auth-cms sdk v0.23.0, golang-jwt v0.4.0, core v0.35.0, bcrypt v0.3.0, hibp v0.1.0; baseline sdk v0.23.0,
  golang-jwt v0.4.0, core v0.35.0; external-identity core v0.35.0). **Files:** `examples/auth-cms/cmd/server/{demo.go,
  authentication.go, main.go (hibp behind AUTH_HIBP), key_rotation_test.go (new: §2), identity_crypto_test.go}`,
  `examples/auth-cms/{.env.example, go.mod, go.sum}`; `examples/production-baseline/cmd/server/{main.go,
  production_test.go}`, `examples/production-baseline/{.env.example, go.mod, go.sum}`; `examples/external-identity/{go.mod,
  go.sum}`; `workshop/documentation/docs/guides/{operations-runbooks.md, keys-and-rotation.md, production-posture.md}`,
  `workshop/documentation/docs/pockets/authentication.md` ("Added by S4d part 3"). **Verify:** per example `GOWORK=off
  go build ./... && go test ./...`; `make guard`; `make check`; `pnpm build` in `workshop/documentation`; §2–§3.
- **PR-6 — the record (`docs/s4d3-release-record`).** Depends on PR-5 merged and §3. **Files:** `RELEASING.md` (ONE
  section "Key management and password policy (SOC 2 slice S4d part 3)": tag table, Upgrade notes by host shape, the
  inventory, the review and verification records, as-built deviations), `SECURITY.md`, `AUDIT.md`,
  `.claude/plans/soc2-readiness/{control-map.md, roadmap.md, NEXT.md}`, `plans/s4d3-keys-passwords.md` (executed copy).
  **Verify:** `make check`; `make guard`.

## Sequencing
```
PR-1 sdk ──[tag sdk v0.23.0]── PR-2 golang-jwt ──[tag golang-jwt v0.4.0]──┐
PR-3 core ──[live-stores on main]──[tag auth v0.35.0]──────────────────────┼── PR-5 examples + runbooks ── PR-6 record
PR-4 bcrypt + hibp ──[tags bcrypt v0.3.0, hibp v0.1.0]─────────────────────┘
```
PR-1, PR-3 and PR-4 run in parallel worktrees; PR-2 follows tag 1 (SRE-7). PR-3's `live-stores` dispatch is the only
store leg (no store change; it proves the core tag against the released stores). PR-5 waits for all five tags.

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 1 | `sdk/v0.23.0` | PR-1 merge | sdk alone builds; a scratch consumer on authentication v0.34.0 + sdk v0.23.0 builds (MVS lift is safe) |
| 2 | `integrations/cryptids/golang-jwt/v0.4.0` | PR-2 merge (after tag 1) | golang-jwt v0.4.0 selects sdk v0.23.0 and builds |
| 3 | `pockets/authentication/v0.35.0` | PR-3 merge + `live-stores` dispatch | **set A:** core v0.35.0 beside RELEASED pgx v0.17.0 / turso v0.16.0 AND views v0.9.0 selects sdk v0.21.0 and builds; the gps-three-sixty shape (core + pgx + goredis v0.6.0) and the battleboardhq shape build |
| 4 | `integrations/cryptids/bcrypt/v0.3.0`, `integrations/passwords/hibp/v0.1.0` | PR-4 merge | each alone; **the auth-cms shape** — golang-jwt v0.4.0 (sdk v0.23.0) + core v0.35.0 + the released stores (sdk v0.22.0) + bcrypt v0.3.0 + hibp v0.1.0 — selects sdk v0.23.0 under the released stores and builds (SRE-13: the MVS lift proof) |

Annotated, unsigned tags, one per command, cut at the squash commit, confirmed on origin, THEN polled on
`proxy.golang.org/<mod>/@v/<ver>.info`; cold-verify with `GOWORK=off`, an empty `GOMODCACHE`, `go mod verify`, `go build
./...` on `go 1.26.10`. Never tidy before a tag resolves; never `go work sync`. **Not tagged:** stores, views/goth,
`pockets`, workshop, ui/goth, pgxdb, the examples.

## Verification
Real binaries in DEVELOPMENT on a FREE port (`lsof -i :8096` first). `$B` = a scratch auth-cms binary (`PORT=8096
AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 AUTH_ACCESS_TOKEN_TTL=1m go run ./cmd/server`, authmem, console mailer). authmem
loses sessions on restart, so cross-restart continuity is proved in-process (§2); the binary proves env parsing, the boot
line, `kid` and the legacy read within one boot (§1).
- **§1 — PR-3 on `$B` ((2) runs in PR-5 once the ring signer is wired; PR-3 tests use fakes).** (1) `AUTH_JWT_SECRET`
  only → `auth: keys jwt_signer=opaque … token_issuer=none`; a fresh JWT header (`cut -d. -f1 | base64 -d`) has no
  `kid`. (2) `AUTH_JWT_KEYS=k1:<hex>` + `AUTH_JWT_SECRET` kept: with `AUTH_JWT_ACTIVE_KEY=legacy` → `jwt_signer=ring
  active_key_id=legacy key_ids=[k1 legacy] legacy=yes` and a fresh token has NO `kid` (phase (a)); with `=k1` → `kid=k1`.
  (3) A production-shaped constructor with a legacy id → `posture_warning=legacy_key_read_enabled`. (4)
  `AUTH_TOKEN_ISSUER=https://auth.example AUTH_TOKEN_AUDIENCE=app` → the token carries both, `/auth/me` **200**; a same-key
  token without `iss` → **401**; mismatched `aud` → **401**; logout with an EXPIRED token → **204** (BE-2). (5)
  `AUTH_CHALLENGE_PEPPERS=dev:<hex>,p2:<hex2>` active `p2`: a magic link and a recovery code issued under `dev` still
  verify. (6) Password legs: `AUTH_HIBP=1` + `password123456789` → **400** `password is known to be compromised`; HIBP at
  a dead port, fail-closed → **503** + `password_breach_check_unavailable outcome=refused` (no URL, no hex), fail-open →
  accepted + `outcome=allowed`; production-shaped, no checker → the WARN; `WithCost(10)` → the cost WARN; unknown-
  identifier vs wrong-password logins differ by < 50 ms at cost 12; if R-4 (a): `Threshold=3 Window=10m Duration=5m` →
  three wrong then the RIGHT password → **401** + `login blocked reason=locked`; after 5 m → **200**. (7) No log line,
  response or event carries a key, token or password.
- **§2 — PR-5 `key_rotation_test.go` (in-process, real signers and HTTP adapters, one shared authmem).** A
  (`golangjwt.New(secret)`), B (`NewRing({legacy: secret, k2}, active k2, WithLegacyKeyID("legacy"))`), C (same ring, no
  legacy), D (`{k2}`), E (ring, active `legacy`). Login via A → T0 (no `kid`): `/auth/me` via B **200**, via C **401**;
  login via B → T1 (`kid=k2`): via D **200**, T0 via D **401**; `kid=k9` → **401**; A and E verify each other's tokens
  (phase (a)). Delivery: an envelope sealed by `AESGCM(old)` through the jobs-mode proof harness is delivered by a ring
  + legacy and dead-letters without it (`ErrUnsealedPayload`, no bytes logged); A and E open each other's envelopes.
  Provider tokens: a row sealed by `AESGCM(old)` reads through the ring (`KeyIDOf` = `legacy`); a returning login leaves
  it legacy (DI-1); unlink + relink writes `v1.k2.`; the runbook sweep, run as a helper, rewrites it CAS-style and the
  count SQL reaches zero. bcrypt: a cost-10 hash verifies under v0.3.0; `Cost()` = 12.
- **§3 — from the tags (PR-6).** §1(2) and §2 against a `GOWORK=off` auth-cms binary (`go version -m`: core v0.35.0,
  golang-jwt v0.4.0, sdk v0.23.0, bcrypt v0.3.0, hibp v0.1.0); the baseline production test logs zero WARNs AND asserts
  `auth: keys jwt_signer=ring … legacy=no` (its `.env.example` moves to `AUTH_JWT_KEYS`/`AUTH_JWT_ACTIVE_KEY` and drops
  `AUTH_JWT_SECRET`, SRE-12); `make guard` green; the docs site builds and the runbook renders every key.

## Open rulings — YOUR CALL
- **R-1 — signing algorithm.** (a) HS256 ring now; asymmetric (ES256/EdDSA + JWKS) later as a sibling signer when a
  host needs cross-service verification. (b) Both now: an `ES256` ring + `GET /auth/.well-known/jwks.json`.
  **Recommend (a):** every adopting host verifies in the process that signs; (b) adds a public route, a key-format story,
  `JWTVerifier` wiring and a second sdk ring type for no consumer; the lead leans the same way. FAC-15's "any service
  sharing the secret can mint" is answered by `iss`/`aud` + per-service rings.
- **R-2 — a production password host with no checker.** (a) One `posture_warning=password_breach_check_none`. (b) Refuse
  boot unless `CompromisedPasswordCheckAcknowledged`. **Recommend (a):** the owner rule; (b) makes a repin a boot outage
  for every password host; the control-map target says WARN.
- **R-3 — identifier-key rotation.** (a) Documented procedure only (rotate freely; budgets reset once; pre-cut audit
  digests no longer match recomputed ones — keep the retired key in the evidence notes). (b) A dual-digest window
  (`HMACIdentifierKeyer` ring). **Recommend (a):** nothing durable is LOOKED UP by this digest (`delivery.go:60-92`,
  `invitations/service.go:1109-1118`: audit rows only); (b) doubles limiter reads for a one-minute-window key.
- **R-4 — account lockout (FAC-14).** (a) Build the opt-in `PasswordConfig.Lockout` in the corrected shape (§3d; no
  schema; no escalation). (b) Out: document `AuthenticationLimits.Login.PerSubject` as the per-account control (an
  hourly budget, e.g. `Limit{Requests: 20, Window: time.Hour}` on goredis). **Recommend (b), with the lead's weight
  (BE-3):** the draft's counter was unsound five ways and the corrected one is a second failure counter with its own
  semantics; the budget already answers "N failures per account per T" on a persistent backend with no denial lever on
  usernames. If the auditor story needs the word "lockout", (a) as corrected is ready for PR-3.
- **R-5 — a single-key signer/encrypter in production.** (a) INFO on the boot line. (b) A `posture_warning` per role.
  **Recommend (a):** a one-key ring is a legitimate steady state; (b) fires on every host at repin and adds nothing.
- **R-6 — breach-checker shape.** (a) hibp k-anonymity module + the list-less `BlocklistChecker`, no list shipped. (b)
  An embedded top-N list too. (c) hibp only. **Recommend (a):** a list in a public repo is a licensing and
  "exploit-shaped" question for ~100 KB of value; the blocklist reads any file (HIBP offline corpus, SecLists).
- **R-7 — bcrypt default cost.** (a) `New()` → 12, `WithCost(10)` hatch, v0.3.0. (b) Keep 10; WARN only. **Recommend
  (a):** the control map's target; new hashes only; the README gives the number.
- **R-8 — a time bound on legacy reads (SRE-6).** (a) `WithLegacyNotAfter(t)` on both rings: after `t` legacy reads
  fail and the boot line says `legacy=expired`; no env. (b) The WARN + phase (c) only; evidence = `legacy=no`.
  **Recommend (b):** an unattended cut-off nobody re-checked is a scheduled outage for the provider-token ring (rows
  persist until the sweep, DI-1); the WARN names the open window on every boot. (a) is a one-option follow-up.

## Open questions
- Does gps-three-sixty persist provider tokens (`AUTH_TOKEN_ENCRYPTER_KEY` set)? It decides whether its upgrade has the
  token-ring step and the sweep.
- What id does battleboardhq's `HMACKeyRing` use today (`dev` if copied from the reference host)? Its first ring entry
  must reuse it.
- Does any host decrypt `oauth_accounts` tokens itself (`cryptids.NewAESGCM`)? That reader moves to the ring with
  legacy at the same time as the writer.

## Recommended reviews
**product-manager:** R-1, R-4, R-7 before ratification; the "by host shape" paragraph as the adopter contract. The
four-reviewer gate (BE, SRE, AS, DI) has run on the draft and is folded above; re-reads at execution: **lead-backend**
on PR-3's `NewKeys`/`token.go` diff, **platform-sre** on PR-5's runbook text, **data-integration** only if a store file
changes (none planned).

## Notes
- **Part 4 handoff (from s4d2 R-4/R-5):** `aal`/`methods` on delegated tokens under the four constraints; `Identity.
  AuthTimeAsserted` (an sdk minor + three adapters). Both recorded in NEXT.md by PR-6.
- **Follow-ups:** `ContextEncrypter` (AAD, FSK-7 (b)); rehash-on-login below the configured bcrypt cost; HS384/HS512
  rings; the asymmetric signer + JWKS (R-1 (b)); `WithLegacyNotAfter` (R-8 (a)); a `guard --posture` ring line.
- **Cut for the cap:** per-test case lists; the runbook text (PR-5 writes it from the inventory); the boot-line
  attribute order (PR-3 pins it in `configuration_test.go`).

## Lead answers to the open questions (2026-10-28, from reading the host repos)
- **gps-three-sixty** wires no `TokenEncrypter` and no challenge pepper. Its `cmd/server/main.go:386-394` says "Google owns the credential … no challenge pepper is needed", so it stores no encrypted provider tokens. Keys 4/5 rings don't apply to it; the JWT ring (key 1) is the one it would adopt.
- **battleboardhq** builds the challenge-pepper ring with id **`primary`** (`pockets/auth/outbound/authentication.go:40,160`), not `dev`. The `AUTH_CHALLENGE_PEPPERS` migration must keep `primary` as the current id. It wires no `TokenEncrypter`.
- **No host decrypts `oauth_accounts` tokens itself**: neither repo has a decrypt call site, and both have the `iam/0004` ledger copy with the ciphertext-when-wired note only.

## Owner ruling during execution (2026-10-28): rehash-on-login pulled INTO this slice
Trigger: the PR-3 code review (MED-3). The dummy-verify timing equalisation runs at the configured cost, so existing cost-10 hashes answer faster after a host moves to cost 12, which leaves a residual oracle. Owner: "yes" to pulling rehash-on-login forward.
- **PR-4 (bcrypt, #259):** `(*Hasher).NeedsRehash(hash string) bool` is true when the stored hash's cost is below the hasher's cost (via `golang.org/x/crypto/bcrypt.Cost`). A malformed hash returns false and never panics.
- **PR-3 (core):**
  - Add an optional `Rehasher` interface beside `Hasher`/`CostReporter`: `NeedsRehash(hash string) bool`.
  - After a SUCCESSFUL password verification (login and step-up), if the hasher implements it and it returns true, hash the just-verified password with the configured hasher and store it through the existing password-set port. Prefer a conditional update on the old hash if the store supports one; otherwise last-write-wins, which is safe because both writes encode the same password.
  - A failed rehash or store logs one WARN (`password_rehash_failed`, no password or hash) and NEVER fails the login.
  - Record no security event: it is not a credential change by the user. A debug-level log is optional.
  - It never runs on miss paths, so equalisation is unaffected.
  - Tests:
    - a cost-10 hash is upgraded on the first successful login, and the next login no longer rehashes;
    - a failing store doesn't fail login;
    - a hasher without `NeedsRehash` means no rehash.
  - The README and RELEASING (PR-6) say the timing residual closes as users log in; unused accounts keep old hashes until a host sweep.

## Owner ruling (2026-10-28): (a) make rehash-on-login live in this slice. Adds a stores PR
The implementer found that `PasswordRepository.Set`/`Change` bump `auth_revision` and revoke every session, so a rehash can't use them. PR-3 (#261) therefore adds an optional CAS port `user.PasswordHashUpgrader{UpgradeHash(ctx, userID, oldHash, newHash) (bool, error)}`, which bumps no revision and revokes nothing. The owner chose (a): make it live now.
- **#261 (core, before the v0.35.0 tag):** a storetest runner `RunPasswordHashUpgrade`.
  - Cases: a matching old hash swaps and returns true; a stale old hash returns false with no change; an unknown user returns false or `ErrNotFound` as the port doc says; `auth_revision` is unchanged and no sessions are revoked.
  - The reference store implements it.
  - It is a SEPARATE runner, not inside Run, so the auth-cms authmem keeps passing until it adopts it (the RunIDs precedent).
- **PR-3b, new (`authentication-stores/upgrade-hash`), after tag v0.35.0:**
  - pgx: `UPDATE user_passwords SET hash=@new WHERE user_id=@id AND hash=@old` on the pool path.
  - turso: the same.
  - Both run the new runner, and both repin core v0.35.0.
  - Tags: auth stores pgx **v0.18.0**, turso **v0.17.0**.
  - The authmem adoption rides PR-5.

## Owner ruling (2026-10-28): HIBP is NOT wired into any example
Owner: "i don't actually want to wire up HIBP … but its good to have that option available and the design layed out." So PR-5 wires no hibp `Checker` into auth-cms or production-baseline. The module stays published (v0.1.0) and documented as an option, and the README and runbooks describe how a host would wire it. Note for the record: the Pwned Passwords **range** API the module calls is keyless and free; the paid HIBP API is the breached-account search, which the module does not use. A password host with no checker keeps the production `password_breach_check_none` WARN (R-2). Neither example triggers it: production-baseline disables password flows, and auth-cms runs in development.

## As built

Executed 2026-10-28. Every PR passed `make guard && make check`; a green `live-stores`
dispatch on `main` preceded each tag; every tag is confirmed on origin and cold-verified.

| PR | Merged as | Tag(s) | `live-stores` |
|---|---|---|---|
| PR-1 | #258 (`f6ff28f3`) + follow-up #260 (`d3fc9dcf`) | `sdk/v0.23.0` @ `d3fc9dcf` | 40722442910 |
| PR-4 | #259 (`d403f6a1`) | `integrations/cryptids/bcrypt/v0.3.0`, `integrations/passwords/hibp/v0.1.0` | 40727926525 |
| PR-3 | #261 (`409e030e`) | `pockets/authentication/v0.35.0` | 40730117775 |
| PR-2 | #262 (`6d0acbe1`) | `integrations/cryptids/golang-jwt/v0.4.0` | 40731499863 |
| PR-3b (added) | #265 (`82fd7c13`) | `pockets/authentication/stores/pgx/v0.18.0`, `pockets/authentication/stores/turso/v0.17.0` | 40739145196 |
| PR-5 | #266 (`4b654e0f`) | none (examples + docs) | — |
| PR-6 | the record PR | none | — |

Cold sets: **set A** (authentication v0.35.0 + released stores pgx v0.17.0 / turso v0.16.0 +
views/goth v0.9.0; core on sdk v0.21.0); **the auth-cms shape** (golang-jwt v0.4.0 +
authentication v0.35.0 + bcrypt v0.3.0 + hibp v0.1.0); **the full set** (authentication
v0.35.0 + pgx v0.18.0 + turso v0.17.0 + golang-jwt v0.4.0 + bcrypt v0.3.0 + hibp v0.1.0 + sdk
v0.23.0 + pgxdb v0.17.0); **set C** (#266: auth-cms, production-baseline, external-identity).

Deviations from the task text:

- **PR-1:** #258 merged before its `KeyIDs()` commit was pushed; `KeyIDs()` followed as #260
  and the sdk tag was cut after it. `ParseKeyRingMinimum` / `ValidateMinimum` raise a `min`
  below 32 to 32.
- **PR-4:** bcrypt also gained `NeedsRehash` (the rehash ruling). hibp compares every line in
  constant time. CodeQL `go/weak-sensitive-data-hashing` flagged hibp's SHA-1, which the range
  protocol requires; the owner dismissed the alert and a code comment records why.
  `ARCHITECTURE.md` counts fifty modules, not 48.
- **PR-3:** the lead-backend review (ship-with-edits) folded HIGH-1 (logout across an
  Issuer/Audience change), MED-1 (one shape helper), MED-2 (the collision check covers
  external providers' issuers), MED-3 (the README's timing residual), MED-4 (the "On repin"
  notes: `ErrPasswordBreachCheckUnavailable` replaces the wrapped checker error; the fail-open
  WARN's message and keys changed; the miss-path dummy verify and the boot-time dummy hash),
  MED-5 (ring wiring docs) and L1–L6. MED-3 led to the rehash ruling: an optional
  `authlogic.Rehasher` and an optional compare-and-swap `user.PasswordHashUpgrader`
  (`Set`/`Change` bump `auth_revision` and revoke every session), with
  `storetest.RunPasswordHashUpgrade` as a separate runner. The boot line prefixes each
  attribute by role and joins ids with commas. A set ring leaves its single `Keys` field nil.
  CI failed `TestDescribeKeyRole` under `go.work` (sdk HEAD's `AESGCMRing` already had
  `KeyIDs()`); the test now uses a fake ring. No lockout (R-4 (b)).
- **PR-3b (new):** pgx and turso `UpgradeHash` on the pool path, repinned core v0.35.0, run
  `RunPasswordHashUpgrade`; rehash on login is live from these tags.
- **PR-5:** no example wires hibp (owner ruling); auth-cms's authmem implements
  `UpgradeHash`. production-baseline also repinned authorization v0.34.0 and authentication
  pgx v0.18.0, which fixed its `GOWORK=off` build (broken on `main` since #264).
- **Release step 3:** the gps-three-sixty and battleboardhq shapes were cold-verified after the
  last tag, against the final versions (authorization v0.34.0 / pgx v0.24.0 included). Both
  pass; the module lists are in RELEASING.
- **PR-6:** `NEXT.md` (the part-4 carries) is the lead's, not this PR's.

Pre-existing, not from this slice: the pgx bare-mode `TestInvitationAcceptanceUpgrade` and
`TestOAuth2SessionUpgradePreservesBrowserSession` fail locally with "oauth_clients table
missing", the same on `main`, and pass under `POSTGRES_TEST_SCHEMA` (follow-up).
