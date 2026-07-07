# integrations/cryptids/golang-jwt

A stateless-token connector wrapping exactly one third-party library —
`github.com/golang-jwt/jwt/v5`. Its `Signer` structurally satisfies the
sdk-owned `cryptids.JWTSigner` port, signing and verifying HMAC-SHA JSON Web
Tokens from a shared secret.

It owns "how to sign a token with golang-jwt," never any feature's
authentication policy. A different token scheme (asymmetric RS/ES, PASETO)
would be a sibling connector, swapped at the composition root.

## Surface

| member | shape |
|---|---|
| `New(secret string, opts ...Option) (*Signer, error)` | builds a signer; defaults to HS256; `ErrSecretTooShort` under 32 bytes |
| `WithMethod(method *jwt.SigningMethodHMAC) Option` | pins the HMAC method (HS256/HS384/HS512); default HS256; nil ignored |
| `Signer.Sign(claims map[string]any, expiresAt time.Time) (string, error)` | signed token with registered `exp`/`iat` added; caller-supplied `nbf` honored on verify |
| `Signer.Verify(token string) (map[string]any, error)` | claims when signature, method, and time claims all check out |
| `ErrSecretTooShort` | secret under 32 bytes (256-bit HMAC minimum) |
| `ErrEmptyToken` | empty token string passed to `Verify` |

## Algorithm-confusion guard

`Verify` pins the token's signing method to the one the `Signer` was built with
**before the secret is ever returned** to the parser. A token whose `alg` header
was swapped — to a different HMAC variant, an asymmetric algorithm, or `none` —
is rejected before any MAC is computed. This closes the classic JWT
algorithm-confusion hole where an attacker rewrites `alg` to steer verification
onto a weaker or key-mismatched path. `WithValidMethods` repeats the assertion
at the parser boundary; `WithStrictDecoding` rejects non-canonical base64url
that would otherwise admit padding-bit signature malleability.

## Why a 32-byte minimum

`New` rejects secrets under 32 bytes (256 bits), the NIST minimum for
HMAC-SHA256. A shorter key weakens the MAC below its design strength.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — round-trip
(HS256 and HS512), wrong-key rejection, expiry and not-yet-valid (`nbf`)
handling, tampered/malformed/empty rejection, and the algorithm-confusion cases:
a same-secret HS512 forgery rejected by an HS256 signer, an HS256 token rejected
when HS512 is configured, and an `alg=none` token rejected. A compile-time
assertion (`var _ cryptids.JWTSigner = (*Signer)(nil)`) proves `Signer`
satisfies the port.
