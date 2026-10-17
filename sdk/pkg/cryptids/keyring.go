package cryptids

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// Key ring and keyed-ciphertext errors. All but ErrCiphertextAuthFailed match
// sdk.ErrInvalidInput; it matches sdk.ErrConflict because the ciphertext and
// the key it names disagree. No error text carries key material or plaintext.
var (
	ErrKeyRingEmpty         = fmt.Errorf("cryptids: key ring has no keys: %w", sdk.ErrInvalidInput)
	ErrKeyRingActiveMissing = fmt.Errorf("cryptids: active key is not in the ring: %w", sdk.ErrInvalidInput)
	ErrKeyMalformed         = fmt.Errorf("cryptids: key malformed: %w", sdk.ErrInvalidInput)
	ErrUnknownScheme        = fmt.Errorf("cryptids: unknown ciphertext scheme: %w", sdk.ErrInvalidInput)
	ErrUnknownKeyID         = fmt.Errorf("cryptids: ciphertext names a key that is not in the ring: %w", sdk.ErrInvalidInput)
	ErrCiphertextMalformed  = fmt.Errorf("cryptids: ciphertext malformed: %w", sdk.ErrInvalidInput)
	ErrCiphertextAuthFailed = fmt.Errorf("cryptids: ciphertext authentication failed: %w", sdk.ErrConflict)
)

// ringScheme tags keyed ciphertext: ringScheme.<key id>.<AESGCM payload>.
const ringScheme = "v1"

// ringKeyBytes is the AES-256 key length every ring key must have.
const ringKeyBytes = 32

// keyIDPattern keeps key ids free of the separators the wire format uses.
var keyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// KeyedEncrypter is an Encrypter whose ciphertext records which key sealed it,
// so keys can be added, made active and retired without re-encrypting
// everything on one day. Decrypt accepts any key still in the ring.
type KeyedEncrypter interface {
	Encrypter
	// ActiveKeyID is the id Encrypt seals under.
	ActiveKeyID() string
	// KeyIDOf reports which key a ciphertext was sealed under, without
	// decrypting it, so a rotation can skip values already on the active key.
	KeyIDOf(ciphertext string) (string, error)
}

// KeyRing names the keys a KeyedEncrypter may use. Active encrypts; every key
// decrypts. Remove a retired key only after nothing is still sealed under it.
// Ids match ^[a-z0-9][a-z0-9_-]{0,31}$ and every key is exactly 32 bytes.
type KeyRing struct {
	Active string
	Keys   map[string][]byte
}

// ParseKeyRing reads keys as "id:hex,id:hex" and the active id, the shape a
// host puts in two environment variables such as APP_SECRETS_KEYS and
// APP_SECRETS_ACTIVE_KEY. Each hex value decodes to 32 bytes
// (`openssl rand -hex 32`). Errors name the key id, never its material.
func ParseKeyRing(keys, active string) (KeyRing, error) {
	ring := KeyRing{Active: strings.TrimSpace(active), Keys: map[string][]byte{}}
	for entry := range strings.SplitSeq(keys, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, material, ok := strings.Cut(entry, ":")
		id = strings.TrimSpace(id)
		if !ok || !keyIDPattern.MatchString(id) {
			return KeyRing{}, fmt.Errorf("%w: entry %d is not id:hex with a valid id", ErrKeyMalformed, len(ring.Keys)+1)
		}
		if _, dup := ring.Keys[id]; dup {
			return KeyRing{}, fmt.Errorf("%w: key %q appears twice", ErrKeyMalformed, id)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(material))
		if err != nil {
			return KeyRing{}, fmt.Errorf("%w: key %q is not valid hex", ErrKeyMalformed, id)
		}
		ring.Keys[id] = raw
	}
	if err := ring.validate(); err != nil {
		return KeyRing{}, err
	}
	return ring, nil
}

func (r KeyRing) validate() error {
	if len(r.Keys) == 0 {
		return ErrKeyRingEmpty
	}
	for id, raw := range r.Keys {
		if !keyIDPattern.MatchString(id) {
			return fmt.Errorf("%w: key id %q", ErrKeyMalformed, id)
		}
		if len(raw) != ringKeyBytes {
			return fmt.Errorf("%w: key %q must be %d bytes, got %d", ErrKeyMalformed, id, ringKeyBytes, len(raw))
		}
	}
	if _, ok := r.Keys[r.Active]; !ok {
		return fmt.Errorf("%w: %q", ErrKeyRingActiveMissing, r.Active)
	}
	return nil
}

var _ KeyedEncrypter = (*AESGCMRing)(nil)

// AESGCMRing is the in-package KeyedEncrypter: one AESGCM per ring key. Its
// ciphertext is "v1.<key id>.<payload>", where the payload is exactly what
// AESGCM produces, so each key keeps AESGCM's nonce and message-count limits.
type AESGCMRing struct {
	active string
	keys   map[string]*AESGCM
}

// NewAESGCMRing validates the ring and builds a cipher per key.
func NewAESGCMRing(ring KeyRing) (*AESGCMRing, error) {
	if err := ring.validate(); err != nil {
		return nil, err
	}
	r := &AESGCMRing{active: ring.Active, keys: make(map[string]*AESGCM, len(ring.Keys))}
	for id, raw := range ring.Keys {
		gcm, err := NewAESGCM(raw)
		if err != nil {
			return nil, fmt.Errorf("cryptids: key %q: %w", id, err)
		}
		r.keys[id] = gcm
	}
	return r, nil
}

// ActiveKeyID is the id Encrypt seals under.
func (r *AESGCMRing) ActiveKeyID() string { return r.active }

// Encrypt seals plaintext under the active key and prefixes the scheme and
// key id.
func (r *AESGCMRing) Encrypt(plaintext string) (string, error) {
	payload, err := r.keys[r.active].Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return ringScheme + "." + r.active + "." + payload, nil
}

// Decrypt opens ciphertext under the key it names. A key no longer in the ring
// is ErrUnknownKeyID; a payload the key rejects is ErrCiphertextAuthFailed.
func (r *AESGCMRing) Decrypt(ciphertext string) (string, error) {
	id, payload, err := splitKeyed(ciphertext)
	if err != nil {
		return "", err
	}
	gcm, ok := r.keys[id]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownKeyID, id)
	}
	// AESGCM reports a short payload as invalid input but a non-base64 one as a
	// plain error, so check the encoding here to keep malformed and tampered apart.
	if _, err := base64.RawURLEncoding.DecodeString(payload); err != nil {
		return "", fmt.Errorf("%w: payload is not base64url", ErrCiphertextMalformed)
	}
	plaintext, err := gcm.Decrypt(payload)
	if err != nil {
		if errors.Is(err, sdk.ErrInvalidInput) {
			return "", fmt.Errorf("%w: payload", ErrCiphertextMalformed)
		}
		return "", fmt.Errorf("%w: key %q", ErrCiphertextAuthFailed, id)
	}
	return plaintext, nil
}

// KeyIDOf reports the key id a ciphertext names.
func (r *AESGCMRing) KeyIDOf(ciphertext string) (string, error) {
	id, _, err := splitKeyed(ciphertext)
	return id, err
}

// splitKeyed parses "v1.<key id>.<payload>".
func splitKeyed(ciphertext string) (id, payload string, err error) {
	parts := strings.SplitN(ciphertext, ".", 3)
	if len(parts) != 3 || parts[2] == "" {
		return "", "", fmt.Errorf("%w: want scheme.key.payload", ErrCiphertextMalformed)
	}
	if parts[0] != ringScheme {
		return "", "", fmt.Errorf("%w: %q", ErrUnknownScheme, parts[0])
	}
	if !keyIDPattern.MatchString(parts[1]) {
		return "", "", fmt.Errorf("%w: key id", ErrCiphertextMalformed)
	}
	return parts[1], parts[2], nil
}
