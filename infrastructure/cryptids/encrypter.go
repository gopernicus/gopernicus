package cryptids

// Encrypter defines the interface for symmetric encryption and decryption.
// Implementations (AES-GCM, etc.) live in cryptids/aesgcm/, etc.
//
// Used for encrypting sensitive data at rest, such as OAuth provider tokens.
// The ciphertext format is implementation-defined but must be a valid string
// (typically base64-encoded).
//
//	type TokenStore struct {
//	    encrypter cryptids.Encrypter
//	}
type Encrypter interface {
	// Encrypt takes a plaintext string and returns an encrypted representation.
	Encrypt(plaintext string) (string, error)

	// Decrypt takes an encrypted string and returns the original plaintext.
	Decrypt(ciphertext string) (string, error)
}
