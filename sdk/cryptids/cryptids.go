// Package cryptids is the facility port for symmetric encryption, fast
// deterministic hashing, and token signing. It is stdlib-only and ships one
// vendor-neutral default in-package: AESGCM, an authenticated symmetric
// Encrypter. Ports follow the -er naming rule (Encrypter, JWTSigner); the
// default is named for its technology and lives next to its port, mirroring
// cacher.Memory.
//
// A signer or encrypter that needs a third-party library is an integration
// module, not a default here — the same rule that keeps golang-jwt out of the
// kernel (see JWTSigner) and bcrypt in integrations/cryptids/bcrypt.
package cryptids

// Encrypter defines symmetric encryption and decryption. The ciphertext format
// is implementation-defined but must be a valid string (typically
// base64-encoded), so it can be persisted or transported as text.
//
// Used to protect sensitive data at rest — for example OAuth provider tokens.
// The in-package default is AESGCM.
type Encrypter interface {
	// Encrypt takes a plaintext string and returns an encrypted representation.
	Encrypt(plaintext string) (string, error)

	// Decrypt takes an encrypted string and returns the original plaintext.
	Decrypt(ciphertext string) (string, error)
}
