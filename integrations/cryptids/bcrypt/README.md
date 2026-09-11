# integrations/cryptids/bcrypt

Password hashing and verification using `golang.org/x/crypto/bcrypt`.
`Hasher` structurally implements authentication logic's consumer-owned `Hasher` interface;
this module imports no pocket. It uses the SDK's input-error classification.

The host selects the hasher, password policy, and cost. This adapter enforces
bcrypt's algorithm limit without adding minimum-length or complexity rules.

## Surface

| Member | Behavior |
|---|---|
| `New(opts ...Option) *Hasher` | Constructs a hasher using `bcrypt.DefaultCost`. |
| `WithCost(cost int) Option` | Sets hashing cost; values outside the library's supported range fall back to `bcrypt.DefaultCost`. |
| `Hasher.HashPassword(password) (string, error)` | Returns a self-describing bcrypt hash. |
| `Hasher.VerifyPassword(hash, password) error` | Returns nil on a match; uses the hash's stored cost and the library's constant-time comparison. |
| `ErrPasswordTooLong` | Returned by both methods for passwords over 72 bytes; wraps `sdk.ErrInvalidInput`. |

The zero-value hasher uses the library's default cost. Changing configured cost
affects new hashes; existing hashes remain verifiable using their stored cost.

## Password length and errors

The limit is **72 bytes**, not 72 characters. Both hashing and verification
reject longer strings. This prevents an overlong candidate from matching a
hash solely because its first 72 bytes match. Existing hashes are unchanged;
previously accepted overlong candidates now fail.

Callers can use `errors.Is(err, bcrypt.ErrPasswordTooLong)` for the specific
algorithm limit or `errors.Is(err, sdk.ErrInvalidInput)` for an input error.
Other errors wrap the library's hash/verification causes. The SDK classification
lets standard HTTP handling report unsupported password input as a client error.

## Testing

Run `go test ./...`. Tests cover round trips, mismatches, randomized salts,
configured/default cost, and the 72-byte boundary for hashing and verification,
including multibyte passwords and input-error classification. A local interface
assertion checks the consumer's method set without importing a pocket.

`Option` values configure private construction settings and apply in order; the
last cost wins. `WithCost` is safe to reuse concurrently, including its invalid-cost
fallback. A nil option panics with `bcrypt: nil Option`. Options cannot change a
constructed hasher.
