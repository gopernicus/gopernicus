---
sidebar_position: 4
title: Cryptids
---

# Cryptids

Cryptids ("crypto tidbits") provides cryptographic primitives: ID generation, password hashing, symmetric encryption, and JWT signing. The package root is stdlib-only — external dependencies live in sub-packages.

<img src="/img/cryptid.jpg" alt="Gopernicus Cryptid" style="width: 100%; border-radius: 15px;" />

## ID Generation

`GenerateID` produces a 21-character, URL-safe, cryptographically secure random string using a vowel-free alphabet (no accidental words, no visually confusing characters):

```go
id, err := cryptids.GenerateID() // "V1StGXR8Z5jdHi6B4mT3p"
```

Used throughout Gopernicus for database primary keys, session tokens, and API keys. For custom alphabet or length:

```go
id, err := cryptids.GenerateCustomID("abcdef0123456789", 32)
```

## Interfaces

### PasswordHasher

For password storage — slow by design, salted:

```go
type PasswordHasher interface {
    Hash(password string) (string, error)
    Compare(hash, password string) error
}
```

### Encrypter

For symmetric encryption of sensitive data at rest (e.g., OAuth tokens):

```go
type Encrypter interface {
    Encrypt(plaintext string) (string, error)
    Decrypt(ciphertext string) (string, error)
}
```

### JWTSigner

For signing and verifying tokens:

```go
type JWTSigner interface {
    Sign(claims map[string]any, expiresAt time.Time) (string, error)
    Verify(token string) (map[string]any, error)
}
```

## SHA256Hasher

A fast, deterministic hasher for API keys and tokens — not for passwords:

```go
hasher := cryptids.NewSHA256Hasher()
hash, err := hasher.Hash(apiKey)
```

Suitable where speed matters and salting isn't needed. Use `bcrypt/` for user passwords.

## Implementations

| Package      | Interface        | Notes                                                 |
| ------------ | ---------------- | ----------------------------------------------------- |
| `bcrypt/`    | `PasswordHasher` | golang.org/x/crypto/bcrypt, cost 10-12 for production |
| `aesgcm/`    | `Encrypter`      | AES-256-GCM, authenticated encryption, stdlib only    |
| `golangjwt/` | `JWTSigner`      | golang-jwt/jwt                                        |

### bcrypt

```go
hasher := bcrypt.NewHasher(12) // cost factor
hash, err := hasher.Hash("user-password")
err = hasher.Compare(hash, "user-password") // nil if match
```

### aesgcm

Key must be exactly 32 bytes. Each encryption generates a random nonce — encrypting the same plaintext twice produces different ciphertext:

```go
enc, err := aesgcm.New([]byte("32-byte-secret-key-goes-here!!!"))
ciphertext, err := enc.Encrypt("oauth-token")
plaintext, err := enc.Decrypt(ciphertext)
```

### golangjwt

```go
signer, err := golangjwt.NewSigner("secret-key-at-least-32-chars!")
token, err := signer.Sign(map[string]any{"user_id": userID}, time.Now().Add(24*time.Hour))
claims, err := signer.Verify(token)
```

## Compliance Suite

`cryptidstest` provides separate suites for each interface:

```go
func TestHasherCompliance(t *testing.T) {
    cryptidstest.RunHasherSuite(t, bcrypt.NewHasher(10))
}

func TestSignerCompliance(t *testing.T) {
    signer, _ := golangjwt.NewSigner("my-secret-key-at-least-32-chars!")
    cryptidstest.RunSignerSuite(t, signer)
}
```

## Custom Implementations

```
gopernicus new adapter hasher argon2hasher
gopernicus new adapter token myhmacjwt
```
