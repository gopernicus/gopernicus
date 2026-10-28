# integrations/cryptids/bcrypt

Password hashing and verification using `golang.org/x/crypto/bcrypt`.
`Hasher` structurally implements authentication logic's consumer-owned `Hasher` interface;
this module imports no pocket. It uses the SDK's input-error classification.

The host selects the hasher, password policy, and cost. This adapter enforces
bcrypt's algorithm limit without adding minimum-length or complexity rules.

## Surface

| Member | Behavior |
|---|---|
| `DefaultCost` | `12`, the cost `New()` and the zero-value hasher hash at. |
| `New(opts ...Option) *Hasher` | Constructs a hasher using `DefaultCost`. |
| `WithCost(cost int) Option` | Sets hashing cost; values outside the library's supported range fall back to `DefaultCost`. |
| `Hasher.Cost() int` | Reports the cost new hashes are generated at (authentication's optional `CostReporter` shape). |
| `Hasher.HashPassword(password) (string, error)` | Returns a self-describing bcrypt hash. |
| `Hasher.VerifyPassword(hash, password) error` | Returns nil on a match; uses the hash's stored cost and the library's constant-time comparison. |
| `ErrPasswordTooLong` | Returned by both methods for passwords over 72 bytes; wraps `sdk.ErrInvalidInput`. |

The zero-value hasher uses `DefaultCost`. Changing configured cost affects new
hashes; existing hashes remain verifiable using their stored cost.

## Cost

Since v0.3.0 the default cost is **12** (it was the library default, 10). Cost 12
takes roughly 250 ms per hash on a 2024 server core, about four times cost 10.
Registration, password set, change and reset, and logins against hashes minted at
12 pay that latency; logins against existing cost-10 hashes are unchanged because
verification uses the cost stored in the hash. There is no rehash on login:
existing hashes stay at their stored cost until the password is next set.

A host that needs the old latency opts back with `bcrypt.New(bcrypt.WithCost(10))`.
`Cost()` lets authentication report a production `password_hash_cost_low` posture
warning for a cost below 12.

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
configured/default cost (12, `Cost()`, a cost-10 hash still verifying), and the 72-byte boundary for hashing and verification,
including multibyte passwords and input-error classification. A local interface
assertion checks the consumer's method set without importing a pocket.

`Option` values configure private construction settings and apply in order; the
last cost wins. `WithCost` is safe to reuse concurrently, including its invalid-cost
fallback. A nil option panics with `bcrypt: nil Option`. Options cannot change a
constructed hasher.
