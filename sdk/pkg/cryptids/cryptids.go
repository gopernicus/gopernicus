// Package cryptids ("cryptography tidbits") provides encryption and token-signing
// contracts, an AES-256-GCM implementation, and SHA-256 digests. Library-backed implementations
// such as JWT signing live in integrations; this package uses only the standard
// library.
package cryptids

// Encrypter encrypts and decrypts text, for example persisted OAuth tokens.
// Ciphertext must be safe to store as text. Its format is implementation-defined:
// replacing an implementation must account for already persisted ciphertext.
type Encrypter interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}
