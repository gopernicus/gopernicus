# integrations/cryptids/bcrypt

A password-hashing connector wrapping exactly one third-party library —
`golang.org/x/crypto/bcrypt`. Its `Hasher` structurally satisfies the
feature-owned `PasswordHasher` port (mirrors `auth.PasswordHasher` in the auth
feature module) with zero import in either direction: the port lives with its
consumer, this integration knows only bcrypt.

It owns "how to hash with bcrypt," never any feature's policy. A different
algorithm (argon2, scrypt) would be a sibling connector, swapped at the
composition root.

## Surface

| member | shape |
|---|---|
| `New(opts ...Option) *Hasher` | builds a hasher; defaults to `bcrypt.DefaultCost` |
| `WithCost(cost int) Option` | sets the cost factor; out-of-range values fall back to `bcrypt.DefaultCost` |
| `Hasher.HashPassword(password) (string, error)` | self-describing bcrypt hash; `ErrPasswordTooLong` for input over 72 bytes |
| `Hasher.VerifyPassword(hash, password) error` | nil on match, non-nil otherwise; constant-time compare |
| `ErrPasswordTooLong` | returned instead of silently truncating over-long input |

## Why reject over-72-byte input

bcrypt truncates silently at 72 bytes, which would let distinct passwords verify
against the same hash. `HashPassword` returns `ErrPasswordTooLong` rather than
truncate.

## Error contract

The port promises only a self-describing hash and a non-nil error on mismatch
with a constant-time compare — all satisfied here with plain, stable errors, so
this module takes no `sdk` dependency.

## Testing

Unit tests are hermetic and run with a plain `go test ./...` — roundtrip, wrong
password, salt uniqueness, cost option, the 72-byte boundary, and a compile-time
structural-satisfaction assertion against a locally-mirrored copy of the port
interface (no import of the auth feature).
