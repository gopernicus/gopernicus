# integrations/cryptids/golang-jwt

HMAC JWT signing and verification using `github.com/golang-jwt/jwt/v5`.
`Signer` implements `sdk/pkg/cryptids.JWTSigner`. Hosts construct
it and pass the inward interface to their services or pockets.

The integration module path and SDK crypto namespace remain `cryptids`. ID
generation lives separately in `sdk`.

## Surface

| Member | Behavior |
|---|---|
| `New(secret string, opts ...Option) (*Signer, error)` | Constructs an HS256 signer by default; validates the selected method and key after options. |
| `WithMethod(method *jwt.SigningMethodHMAC) Option` | Selects HS256, HS384, or HS512. A nil method leaves the current selection unchanged. The option snapshots the method when created; the signer keeps its own validated copy. |
| `Signer.Sign(claims map[string]any, expiresAt time.Time) (string, error)` | Copies claims, then sets `exp` from `expiresAt` and `iat` from the current time, overriding those two supplied claims. |
| `Signer.Verify(token string) (map[string]any, error)` | Verifies the signature, method, encoding, and time claims. Returned JSON numbers are `float64`. |
| `ErrSecretTooShort` | Key is shorter than the selected method requires; constructor errors wrap this sentinel. |
| `ErrEmptyToken` | An initialized signer's `Verify` received an empty token. |

Construct signers with `New`. Methods on a zero-value or nil signer return
errors. Sign does not mutate its input map. Options configure private construction
settings in order, with the last non-nil method selection winning. They cannot
change a live signer or bypass construction-time key checks. A nil `Option`
returns an error wrapping `sdk.ErrInvalidInput`; `WithMethod(nil)` remains valid.

## Keys and algorithms

The minimum key lengths are 32 bytes for HS256, 48 for HS384, and 64 for HS512,
matching the hash-output lengths required by
[RFC 7518 §3.2](https://www.rfc-editor.org/rfc/rfc7518#section-3.2).
Only those name/hash combinations are supported.

`New` uses the string's bytes directly: it does not trim, hex-decode, or
base64-decode them. Preserve the effective key bytes when migrating from another
signer. For an existing decoded `[]byte` key, pass `string(keyBytes)`. Replacing
that with its encoded representation changes the key and invalidates tokens.
Hosts own secure key generation, storage, and rotation.

Verification accepts only the configured method and checks the concrete HMAC
method before returning the key. Different HMAC variants, asymmetric methods,
and `alg=none` are rejected. Strict base64url decoding rejects padding and
noncanonical signature padding bits.

## Claims and time checks

Claims must be a JSON object containing numeric `exp`. This expiration
requirement belongs to the framework's expiring-token contract. Optional `nbf`
and `iat` must also be numeric when present; null, strings, arrays, and objects
are invalid values for these claims.

All three dates must fall in calendar years 0001–9999. The explicit range
rejects extreme numbers that could overflow the library's date conversion and
make a future `nbf` or `iat` appear to be in the past.

The library enforces expiration, not-before, and issued-at with 60 seconds of
clock tolerance. An expired token is accepted only within that tolerance;
not-before and issued-at can be at most 60 seconds ahead. Missing expiration,
zero expiration, and a null claims payload are rejected. Ordinary tokens do
not need `nbf` or `iat` to verify.

`Sign` always owns `exp` and `iat`. Callers can supply `nbf`; its validity is
checked during verification. The host or consuming service owns issuer,
audience, identity, session, and authorization requirements.

## Testing

Run `go test ./...`. Tests cover method/key boundaries, zero-value use,
authoritative signing times, malformed/null claims, clock tolerance, wrong keys,
algorithm confusion, strict encoding, and upstream interoperability for all
three supported methods.

A [saved legacy SDK token](testdata/README.md) proves compatibility with ordinary
HS256 tokens minted before the SDK implementation was retired. The fixture uses
public dummy data and retains its original captured bytes.
