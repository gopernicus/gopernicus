package command

import (
	"encoding/json"

	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Seal validates env and encrypts it through enc into the opaque ciphertext a queue
// persists as its payload. A command is validated BEFORE sealing so a durable
// payload is always a well-formed envelope — an invalid command never reaches the
// store. A nil enc is ErrEncrypterRequired. A marshal or encrypt failure returns a
// STATIC sentinel that carries no envelope bytes.
func Seal(enc cryptids.Encrypter, env Envelope) ([]byte, error) {
	if enc == nil {
		return nil, ErrEncrypterRequired
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(env)
	if err != nil {
		// Unreachable for these field types; guarded with a static message so a
		// marshal error can never carry envelope content.
		return nil, ErrMalformedPayload
	}
	ciphertext, err := enc.Encrypt(string(plaintext))
	if err != nil {
		return nil, ErrUnsealedPayload
	}
	return []byte(ciphertext), nil
}

// Open decrypts payload through enc, decodes the envelope, and re-validates it. It
// is the sole way to read a durable payload, and it fails closed:
//
//   - a nil enc is ErrEncrypterRequired;
//   - a payload that is not valid sealed ciphertext is ErrUnsealedPayload (the
//     decrypt error is NOT wrapped, so no bytes leak);
//   - decrypted bytes that are not a well-formed envelope are ErrMalformedPayload
//     (the unmarshal error is NOT wrapped, because it would echo the decrypted
//     plaintext — which carries the secret — into the error string);
//   - an unknown version, missing purpose, or malformed stage combination is the
//     corresponding static Validate error.
//
// No branch interpolates payload bytes into its error, so parsing failures are
// observable and classifiable without ever leaking a destination or secret.
func Open(enc cryptids.Encrypter, payload []byte) (Envelope, error) {
	if enc == nil {
		return Envelope{}, ErrEncrypterRequired
	}
	plaintext, err := enc.Decrypt(string(payload))
	if err != nil {
		return Envelope{}, ErrUnsealedPayload
	}
	var env Envelope
	if err := json.Unmarshal([]byte(plaintext), &env); err != nil {
		return Envelope{}, ErrMalformedPayload
	}
	if err := env.Validate(); err != nil {
		return Envelope{}, err
	}
	return env, nil
}
