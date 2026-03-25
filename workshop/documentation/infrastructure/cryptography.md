# infrastructure/cryptography -- Crypto Reference

Package `cryptids` provides ID generation, password hashing, JWT signing, SHA256 hashing, and symmetric encryption. The core package uses only the standard library; implementations with external dependencies live in subpackages.

**Import:** `github.com/gopernicus/gopernicus/infrastructure/cryptids`

## ID Generation

Generates cryptographically secure, URL-safe random identifiers suitable for database primary keys, API tokens, and session IDs.

```go
id, err := cryptids.GenerateID() // e.g. "V1StGXR8Z5jdHi6B4mT3p" (21 chars)
```

The default alphabet excludes visually confusing characters (O, I, o, i) and vowels (to avoid accidental words).

For custom requirements:

```go
id, err := cryptids.GenerateCustomID("0123456789abcdef", 32)
```

## PasswordHasher Interface

```go
type PasswordHasher interface {
    Hash(password string) (string, error)
    Compare(hash, password string) error // nil on match
}
```

### bcrypt Adapter

```go
import "github.com/gopernicus/gopernicus/infrastructure/cryptids/bcrypt"

hasher := bcrypt.NewHasher(10) // cost 10-12 recommended for production
hash, err := hasher.Hash("password")
err = hasher.Compare(hash, "password") // nil if match
```

From environment:

```go
hasher := bcrypt.New(bcrypt.Options{Cost: 10}) // env:"BCRYPT_COST" default:"10"
```

- Cost is clamped to valid range [4, 31].
- Rejects empty passwords and passwords exceeding 72 bytes (bcrypt maximum).

## SHA256Hasher

Fast, deterministic hashing. Suitable for API keys where speed matters and salting is unnecessary. NOT suitable for passwords.

```go
hasher := cryptids.NewSHA256Hasher()
hex, err := hasher.Hash("api-key-value") // returns hex-encoded SHA256
```

## JWTSigner Interface

```go
type JWTSigner interface {
    Sign(claims map[string]any, expiresAt time.Time) (string, error)
    Verify(token string) (map[string]any, error)
}
```

### golangjwt Adapter (HMAC-SHA256)

```go
import "github.com/gopernicus/gopernicus/infrastructure/cryptids/golangjwt"

signer, err := golangjwt.NewSigner("my-secret-at-least-32-chars-long!!")

token, err := signer.Sign(map[string]any{
    "user_id": "u_abc123",
    "role":    "admin",
}, time.Now().Add(24 * time.Hour))

claims, err := signer.Verify(token)
```

From environment:

```go
signer, err := golangjwt.New(golangjwt.Options{Secret: secret}) // env:"JWT_SECRET" required:"true"
```

- Secret must be at least 32 bytes (NIST minimum for HMAC-SHA256).
- `Sign` automatically adds `exp` and `iat` claims.
- `Verify` rejects expired tokens, malformed tokens, and tokens signed with a different secret or algorithm.

## Encrypter Interface

Symmetric encryption for sensitive data at rest (e.g., OAuth provider tokens).

```go
type Encrypter interface {
    Encrypt(plaintext string) (string, error)
    Decrypt(ciphertext string) (string, error)
}
```

### aesgcm Adapter (AES-256-GCM)

Authenticated encryption -- ciphertext cannot be tampered with without detection. Each encryption generates a random nonce, so the same plaintext produces different ciphertext.

```go
import "github.com/gopernicus/gopernicus/infrastructure/cryptids/aesgcm"

enc, err := aesgcm.New(key) // key must be exactly 32 bytes
ciphertext, err := enc.Encrypt("secret-token")
plaintext, err := enc.Decrypt(ciphertext)
```

Output is base64url-encoded (nonce + ciphertext). The key should be loaded from a secure source (environment variable, secret manager, KMS) -- never hardcoded.

## Compliance Test Suites

The `cryptidstest` package provides compliance tests for all interfaces. Use these when implementing new adapters.

```go
import "github.com/gopernicus/gopernicus/infrastructure/cryptids/cryptidstest"

func TestMyHasher(t *testing.T) {
    cryptidstest.RunHasherSuite(t, myHasher)
}

func TestMySigner(t *testing.T) {
    cryptidstest.RunSignerSuite(t, mySigner)
}

func TestMyEncrypter(t *testing.T) {
    cryptidstest.RunEncrypterSuite(t, myEncrypter)
}
```

Suites test: round-trip correctness, mismatch detection, empty input rejection, uniqueness of output (salted hashing, random nonces), and tamper detection (encrypter).

## Related

- [infrastructure/oauth](../infrastructure/oauth.md) -- Encrypter used to store OAuth tokens at rest
- [sdk/web](../sdk/web.md) -- JWT verification in authentication middleware
