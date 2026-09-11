# SDK audit S4: identity and cryptids

Naming update (2026-09-10): the crypto package's final name is cryptids, restored
by owner choice; the reviewed cryptography rename is superseded. See
[cryptids-name-and-sdk-shape.md](cryptids-name-and-sdk-shape.md). Other S4 fixes
and simplifications remain in effect.

Status: IMPLEMENTED AND VERIFIED — 2026-09-09. Reviewed baseline: branch firestore-authentication,
HEAD 82aa19f7. Parent: [framework-audit.md](framework-audit.md).

Implementation: [identity-cryptography-cleanup.md](identity-cryptography-cleanup.md).
The findings below describe the reviewed baseline. The owner approved the fixes
and package/API simplifications and clarified host ownership of password policy;
AUDIT-004 records the implemented migrations.

## Scope and work plan

Audit all source/tests in foundation/identity and foundation/cryptids, trace
framework and representative consumer contracts, and follow relevant cryptids
integrations far enough to assess interchangeability. Review correctness,
security assumptions, error behavior, human/machine clarity, names, and whether
the packages and public abstractions earn their place.

1. Read contracts/tests and trace actual wiring and callers, including aliases.
2. Use the named platform-sre for read-only JWT/adapter verification semantics
   and the lead-backend-engineer for read-only identity and package-boundary
   critique. Parent audits IDs, AES-GCM, hashing, and consumer behavior.
3. Validate security/protocol claims against primary documentation and run
   temporary dummy-data probes plus fresh scoped build/test/vet. No real
   credentials, dotenv files, live services, consumer edits, or migrations.
4. Record defects separately from documented policy, missing features, and
   structural alternatives. Preserve ciphertext/identifier/token compatibility
   unless a justified proposed change explicitly accounts for migration.
5. Update durable findings and the shared handoff. This is review only;
   AUDIT.md remains for implemented breaking changes, not proposals.

## Preconditions and ownership

The branch advanced independently from S3 to firestore-authentication/82aa19f7.
Prior S2/S3 code/docs and audit plans remain dirty/untracked. Preserve them and
concurrent Firestore work. Identity/cryptids source is clean at start. This
slice owns this record and the parent audit plan only. Go 1.26.1; no root
go.mod; use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache for module checks.

## Outcome

Keep identity's vocabulary/resolver and the configurable ID-generation seam.
The highest-value simplification is retiring the handwritten SDK JWT
implementation in favor of the existing integration, with an explicit verified
token contract. Fix password-length and input-validation inconsistencies. AES
and SHA-256 do useful shared work but can have simpler implementations/APIs.

The review itself changed no production code or tests. Existing tests passed
despite the demonstrated edge cases; the linked implementation records the fixes.

## Correctness findings

### S4-01 — JWT implementations have different acceptance rules (high priority)

Evidence: `sdk/foundation/cryptids/hs256.go:122` and
`integrations/cryptids/golang-jwt/golangjwt.go:108`; reproduced with correctly
signed dummy tokens by `/tmp/gopernicus-s4-jwt-probe.go`:

| Input | SDK HS256 accepts | golang-jwt integration accepts |
|---|---|---|
| Ordinary future expiration | Yes | Yes |
| `nbf` one hour ahead | Yes | No |
| `exp: "invalid"` | Yes | No |
| `iat: "invalid"` | Yes | Yes |
| Expired 30 seconds ago | Yes | No |
| `iat` one hour ahead | No | Yes |
| `exp: 0` | No | Yes |
| Missing `exp` | Yes | Yes |
| JSON `null` claims payload | Yes | Yes |
| Noncanonical signature padding bits | Yes | No |
| Header `ALG` instead of `alg` | Yes | No |

The SDK ignores `nbf` and skips nonnumeric time claims. The adapter has no
issued-at check or clock tolerance configured; its pinned `MapClaims` treats
numeric zero expiration as absent. SDK's 60-second tolerance is a documented
policy, not itself a defect. Missing expiration is allowed by generic JWT;
requiring it would be a framework policy. A JWT claims set is a JSON object;
accepting `null` does not meet that shape. SDK's typed header decoding accepts
case-insensitive field names, unlike the adapter's map lookup.

Present expiration/not-before claims require NumericDates and time enforcement;
issued-at also requires a NumericDate. See
[RFC 7519 §§4.1.4–4.1.6](https://www.rfc-editor.org/rfc/rfc7519#section-4.1.4),
[validation options](https://golang-jwt.github.io/jwt/usage/parse/), and
[pinned v5.3.1 MapClaims](https://github.com/golang-jwt/jwt/blob/v5.3.1/map_claims.go).
SDK's comment that wrong claim types match golang-jwt behavior is inaccurate.

Changing unused signature padding bits produces another accepted token string
with the same claims and MAC, not a forged identity. No inspected consumer uses
raw JWT strings as a revocation key. Both implementations pin algorithms and
reject tampered MAC bytes/wrong keys; preserve that behavior. Authentication
signs only `user_id` and `session_id`, checks its required claims, and denies
verification errors. No current malformed-claims exploit or arbitrary forgery
was established. The differing rules still defeat the promise of a simple swap.

Recommendation: keep `JWTSigner` inward and use one library-backed integration.
The SDK remains stdlib-only and hosts gain a Go dependency, not an external
service. Specify an object claims payload, required numeric expiration for
this expiring-token port, numeric optional `nbf`/`iat`, enforcement of both,
strict decoding/algorithm matching, and 60-second time tolerance to preserve
normal SDK-host behavior. Required expiration is an explicit proposed contract,
not a claim about the RFC's universal requirements. Test the matrix and ordinary
cross-verification before removal; library defaults alone do not implement it.

Alternative: retain SDK HS256, fix it, and maintain shared behavioral conformance
for both implementations. This preserves integration-free JWT construction but
retains a protocol parser and ongoing equivalence work; no demonstrated host
constraint requires that cost. Do not introduce a registered-claims framework
or expand the SDK error hierarchy without actual callers needing it.

### S4-02 — Zero-value SDK signer succeeds with an empty key (high priority)

`NewHS256` rejects fewer than 32 bytes, but `Sign`/`Verify` never check that
precondition. The probe's `var s cryptids.HS256` successfully signs and verifies;
independent HMAC computation confirms the key is empty. The comment says the
zero value is unusable, but the actual failure mode is unsafe success.

No sampled production caller bypasses the constructor. Remove this implementation
with S4-01, or reject uninitialized use in both methods if retaining it.

### S4-03 — Bcrypt verifies overlong candidates (medium priority)

`integrations/cryptids/bcrypt/bcrypt.go:83` delegates verification without the
72-byte limit enforced during hashing. The probe hashes 72 ASCII `a` bytes,
then successfully verifies those bytes plus `x`; hashing that 73-byte candidate
is rejected. This contradicts the wrapper's deliberate no-truncation policy.

Apply the same byte-length check to verification and document the error on
both methods. Existing hashes remain valid; previously accepted overlong
candidates stop working. This requires knowledge of the original 72-byte prefix,
not an account takeover with an unrelated password. Add a regression covering
the exact boundary (bytes, including multibyte inputs). Also correct the
zero-value comment: the probe confirms upstream default-cost behavior makes
`var Hasher` usable. Keep documented cost fallback/empty-password domain policy
unless a separate finding justifies changing them.

### S4-04 — Adapter method selection bypasses required key size (medium priority)

`integrations/cryptids/golang-jwt/golangjwt.go:68` checks 32 bytes before applying
`WithMethod`. The probe confirms HS384 and HS512 accept that same key size.
[RFC 7518 §3.2](https://www.rfc-editor.org/rfc/rfc7518#section-3.2) requires a
key at least as long as the hash output: 32/48/64 bytes for HS256/384/512.
Existing HS512 tests use the shorter shared test secret and encode the mistake.

Validate the selected supported method and its key length after options. No
inspected production host selects HS384/512. Existing callers of those options
may need a longer key and token rotation; do not imply those old keys have been
compromised. Keep algorithm pinning and correct docs promising RS256/ES256:
the existing adapter only supports HMAC.

### S4-05 — Two signing inputs control expiration (medium priority; API policy)

Both `Sign` implementations set `exp`/`iat`, then copy arbitrary claims over
them. The probe passes an expired `expiresAt` and a future `claims["exp"]`;
both produce an accepted token. SDK documents the override; the adapter does
not. This is an ambiguous API contract rather than a hidden SDK implementation
bug. Reserve `exp`/`iat` for the signer, making the explicit expiration argument
authoritative and `iat` the signing time. No sampled auth caller overrides them.

### S4-06 — NanoID accepts text it cannot preserve (medium priority)

`sdk/foundation/cryptids/id.go:109,156` validates/indexes bytes while promising
characters. `NanoID("é", 1)` succeeds; its output is always an invalid UTF-8 byte.
The probe's JSON encoding replaces it with `\ufffd`. Either possible output
collapses to the same replacement character, so text transport can destroy
identifier distinctness. Existing tests and sampled callers use ASCII defaults.

Require/document an ASCII alphabet; retain duplicate/minimum-size checks and
the existing default alphabet, length, and unbiased rejection sampling. Supporting
Unicode runes is an alternative, but no caller demonstrates that need. Do not
change default IDs or optimize random-buffer sizing as part of the fix.

### S4-07 — Context lookup accepts an incomplete principal (medium priority)

`sdk/foundation/identity/identity.go:108` only checks ID. Reproduced:
`Principal{ID:"u1"}` reports true without a type; `Principal{Type:"user"}`
returns that nonzero struct with false, contradicting its zero-result comment.
Require both fields and return `Principal{}, false` for absent/incomplete values.
Valid authentication output is unchanged. Authorization already validates both
fields in `authorizersvc/model.go:51`; no authorization bypass was established.

## Simplification and package decisions

| Area | Recommendation | Why / compatibility |
|---|---|---|
| `identity` | Keep vocabulary, context helpers, and `Resolver` together | Real display/contact consumers; splitting the port creates import concepts without helping callers. |
| Identity comments | Describe mechanism and contracts directly | Remove AV5/history/future-seam narration and universal anonymous-denial policy. Public endpoints can legitimately permit no principal. Protected entrypoints own denial. |
| `ResolveAll` | Remove | No production use found in framework or three apps. It is a sequential loop; no generic partial-result/batch abstraction is needed. |
| ID generation | Keep configurable function and default-capable generator | Deterministic tests, host UUID choices, and database-generated IDs are real seams. Keep generation errors for custom adapters. |
| `Database` | Prefer a named function over an exported mutable function variable | Preserves ordinary `GenerateFunc` assignment/call behavior; intentionally removes global reassignment. Confirm no assignments during implementation. |
| AES-GCM | Keep port/default; use `cipher.NewGCMWithRandomNonce` | Standard library owns nonce generation/prefix handling. Dummy-data bidirectional compatibility is proven. |
| SHA-256 | Keep shared digest behavior as a function, e.g. `SHA256(string) (string, error)` | Empty struct, constructor, and stored service pointers carry no state. Preserve lowercase hex and empty-input `sdk.ErrInvalidInput`. |
| Google UUID integration | Keep | Small meaningful V4/V7 generator adapter; no correctness defect found in this slice. |

**Package names:** favor `foundation/id` for generation and
`foundation/cryptography` for encryption/digests/the JWT port. `cryptids` groups
database-ID delegation with security mechanisms under an opaque inherited name.
Keep `identity` separate: a caller's actor reference is not an ID-generation
strategy. Two coherent packages are sufficient; four algorithm-sized packages
would add navigation without a useful boundary.

This naming move is optional and independent of correctness fixes. It affects
at least 43 framework/example production imports plus tests/docs/adapters and
the three apps. If selected, idiomatic `id.Generator` can replace
`cryptids.IDGenerator`; account for every public rename in the implementation
plan and AUDIT.md. Integration module-path renames need a separate release/tag
decision; do not assume an SDK rename authorizes moving published modules.

AES compatibility evidence: current SDK encryption decrypts through Go's
random-nonce AEAD, and its output decrypts through the current SDK. Wrong-key
and tampered ciphertexts fail. Preserve raw base64url of
`12-byte nonce || ciphertext || 16-byte tag`, the exact key bytes, and existing
empty-input behavior. Keep malformed-input error classes intentional when
removing manual slicing. Document Go's maximum of 2^32 encryptions per key for
random nonces; a process-local counter would not enforce that across hosts.
See [Go's helper](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce) and local
Go 1.26.1 `crypto/cipher/gcm.go`. No current encryption defect was established.

SHA-256 is actually used: auth session/API-key hashes, invitations, and GPS360
testauth/devcred. Auth also uses it for a development identifier digest fallback;
therefore a narrowly named `HashToken` would misdescribe existing behavior.
Do not remove the shared digest function as unused. Remove the claim that it
provides constant-time database lookup; hashing does not guarantee that.

The original framework already put JWT behind an adapter and contains the same
stateless hasher/manual AES pattern (`infrastructure/cryptids`). It is useful
lineage, not a reason to retain those shapes or move stdlib AES into a module.

## Consumer compatibility and deferred pocket findings

- All three sampled hosts use SDK HS256. Segovia decodes hex before construction;
  Coordination Hub and GPS360 pass the environment string's bytes. A migration
  must preserve each host's exact effective bytes; changing decoding rotates keys.
  Auth-cms also needs constructor/import wiring changes. No production JWT-error
  sentinel branching was found; auth treats Verify errors as denial.
- Preserve stored AES envelopes and SHA-256 digests. Interface satisfaction
  alone does not make a different Encrypter's persisted format interchangeable.
- Preserve the separation of entity-ID configuration and bearer-secret
  generation. Authentication session, invitation, OAuth-state, and machine-key
  generators deliberately do not follow `Config.IDs=Database`. Their apparent
  duplication protects a real invariant; constructors/signing may need IDs
  before persistence as well.
- Authentication's resolver projects all active verified identifiers, including
  `NotificationEnabled=false` (`authsvc/resolver.go:89`, existing projection
  test). Segovia `cmd/server/jobs.go:139` selects the first projected email for
  notifications. Coordination Hub explicitly checks notification eligibility;
  GPS360 uses projection emails for legacy ownership scoping. Clarify
  `Info.Addresses` is identity information, then review delivery selection during
  authentication/notify audits. Globally filtering the resolver is unsafe for
  its other consumers. This is a consumer/contract mismatch, not a violation of
  the resolver's current projection specification.
- Review issuer/audience separation during authentication's token-profile review
  if hosts share keys/issuers across applications. No present cross-app acceptance
  incident was established; do not grow this slice into a new token framework.
- Auth's development identifier digest comments claim SHA-256 makes predictable
  email/phone values non-reversible/PII-free. Carry that wording and the existing
  production HMAC-keyer requirement into the pocket review; this audit does not
  change identifier-keying policy.

Consumer pins checked read-only: Segovia SDK v0.8.0/authentication v0.10.0;
Coordination Hub v0.7.0/v0.9.0; GPS360 SDK v0.7.1 with a local SDK replacement
and authentication v0.9.0. These are source findings, not deployed-app audits.

## Verification and handoff

Passed with `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache`:

- From `sdk/`: `go build ./...`, `go test -count=1 ./...`, `go vet ./...`.
  Initial full test run hit sandbox loopback denial in web's httptest server;
  approved rerun with loopback allowed passed. Vet ran successfully afterward.
- From each `integrations/cryptids/{bcrypt,golang-jwt,google-uuid}` module:
  `go build ./...`, `go test -count=1 ./...`, `go vet ./...`.
- Root: `make guard` passed all current guards, including stdlib-only SDK.
- From bcrypt module: `go run /tmp/gopernicus-s4-crypto-probe.go` demonstrated
  S4-03/06/07, zero-value bcrypt behavior, and AES bidirectional compatibility.
- From golang-jwt module: `go run /tmp/gopernicus-s4-jwt-probe.go` demonstrated
  the acceptance matrix, zero-key signing, expiration override, and method-key
  length acceptance. Only disposable dummy secrets were used; no tokens printed.

Named platform-sre reviewed JWT/adapter semantics; lead-backend-engineer reviewed
identity and package boundaries. Both were read-only; parent inspected primitive
code/tests, consumer calls, primary sources, and ran all probes/checks.

Not run: live stores/services, browser, consumer builds/migrations, full workspace
`make check`, docs build, new race/32-bit suites. This slice changes only audit
Markdown. The earlier S3 workspace/docs/runtime evidence remains in its record;
it is not represented as rerun here. No unresolved verification failure remains.

S4 changed files: this file and `plans/framework-audit.md` only. Temporary probe
programs live under `/tmp` and may disappear; the observed cases above are durable.
Prior audit implementation and concurrent Firestore changes were preserved.
No generated files, dependency pins, releases, or consumer apps were changed.

Next: select JWT consolidation plus the correctness fixes, then optionally the
package/API simplifications. Write the implementation plan before source edits;
add AUDIT-004 only for implemented breaking changes, with token-policy/key-byte
and import migrations. S5 (`foundation/crud`) is the next unreviewed SDK slice.
