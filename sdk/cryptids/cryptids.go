// Package cryptids — the original gopernicus's portmanteau of "crypto
// tidbits" — is the facility for identifier generation, symmetric
// encryption, fast deterministic hashing, and token signing. It is
// stdlib-only; anything needing a third-party library is an
// integrations/cryptids/<package> module satisfying the same port — the rule
// that keeps golang-jwt out of the kernel (see JWTSigner) and bcrypt in
// integrations/cryptids/bcrypt.
//
// Identifier generation is one port: a configured, zero-argument
// GenerateFunc. An application decides its ID strategy once, at wiring —
// nanoid-shaped strings (the stdlib default; see NanoID), database-generated
// keys (see Database and the empty-ID store convention), or an integration's
// generator — and everything downstream holds an IDGenerator and never
// thinks about the choice again. There is no uuid or integer generator here:
// the database or an integration owns those kinds.
//
// Encryption ships one vendor-neutral default in-package: AESGCM, an
// authenticated symmetric Encrypter. Ports follow the -er naming rule
// (Encrypter, JWTSigner); defaults are named for their technology and live
// next to their port, mirroring cacher.Memory.
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
