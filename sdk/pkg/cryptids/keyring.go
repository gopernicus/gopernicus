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
	ErrLegacyKeyMissing     = fmt.Errorf("cryptids: legacy key is not in the ring: %w", sdk.ErrInvalidInput)
)

// ringScheme tags keyed ciphertext: ringScheme.<key id>.<AESGCM payload>.
const ringScheme = "v1"

// ringKeyBytes is the key length Validate requires and the floor
// ValidateMinimum never goes below.
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

// KeyRing names the keys a KeyedEncrypter or a signer may use. Active
// encrypts or signs; every key decrypts or verifies. Remove a retired key only
// after nothing is still sealed or signed under it. Ids match
// ^[a-z0-9][a-z0-9_-]{0,31}$. Validate requires every key to be exactly 32
// bytes (AES-256-GCM, HMAC-SHA-256 and HS256 share that shape);
// ValidateMinimum accepts longer keys for HMAC secrets.
type KeyRing struct {
	Active string
	Keys   map[string][]byte
}

// RingOption configures NewAESGCMRing.
type RingOption func(*AESGCMRing)

// WithLegacyKeyID names the ring key that opens single-key AESGCM output (raw
// base64url, no "v1." prefix), so values written before a host moved to a ring
// keep decrypting. While that key is also the active key, Encrypt writes the
// single-key shape too, so instances still on a plain AESGCM can open it
// during a rolling deploy. An empty id is no option.
func WithLegacyKeyID(id string) RingOption {
	return func(r *AESGCMRing) { r.legacy = id }
}

// ParseKeyRing reads keys as "id:hex,id:hex" and the active id, the shape a
// host puts in two environment variables such as APP_SECRETS_KEYS and
// APP_SECRETS_ACTIVE_KEY. Each hex value decodes to 32 bytes
// (`openssl rand -hex 32`). Errors name the key id, never its material.
func ParseKeyRing(keys, active string) (KeyRing, error) {
	ring, err := parseKeyRing(keys, active)
	if err != nil {
		return KeyRing{}, err
	}
	if err := ring.Validate(); err != nil {
		return KeyRing{}, err
	}
	return ring, nil
}

// ParseKeyRingMinimum is ParseKeyRing for keys of at least min bytes rather
// than exactly 32, the shape of an HMAC secret. min below 32 is raised to 32.
func ParseKeyRingMinimum(keys, active string, min int) (KeyRing, error) {
	ring, err := parseKeyRing(keys, active)
	if err != nil {
		return KeyRing{}, err
	}
	if err := ring.ValidateMinimum(min); err != nil {
		return KeyRing{}, err
	}
	return ring, nil
}

func parseKeyRing(keys, active string) (KeyRing, error) {
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
	return ring, nil
}

// Validate checks the ids, that the active key is present and that every key
// is exactly 32 bytes.
func (r KeyRing) Validate() error {
	return r.validate(func(n int) bool { return n == ringKeyBytes }, fmt.Sprintf("must be %d bytes", ringKeyBytes))
}

// ValidateMinimum is Validate for keys of at least min bytes. min below 32 is
// raised to 32.
func (r KeyRing) ValidateMinimum(min int) error {
	min = max(min, ringKeyBytes)
	return r.validate(func(n int) bool { return n >= min }, fmt.Sprintf("must be at least %d bytes", min))
}

func (r KeyRing) validate(lengthOK func(int) bool, want string) error {
	if len(r.Keys) == 0 {
		return ErrKeyRingEmpty
	}
	for id, raw := range r.Keys {
		if !keyIDPattern.MatchString(id) {
			return fmt.Errorf("%w: key id %q", ErrKeyMalformed, id)
		}
		if !lengthOK(len(raw)) {
			return fmt.Errorf("%w: key %q %s, got %d", ErrKeyMalformed, id, want, len(raw))
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
// With WithLegacyKeyID it also reads (and, while that key is active, writes)
// the unprefixed single-key form.
type AESGCMRing struct {
	active string
	legacy string
	keys   map[string]*AESGCM
}

// NewAESGCMRing validates the ring and builds a cipher per key. A legacy key
// id that is not in the ring is ErrLegacyKeyMissing.
func NewAESGCMRing(ring KeyRing, opts ...RingOption) (*AESGCMRing, error) {
	if err := ring.Validate(); err != nil {
		return nil, err
	}
	r := &AESGCMRing{active: ring.Active, keys: make(map[string]*AESGCM, len(ring.Keys))}
	for _, opt := range opts {
		opt(r)
	}
	if _, ok := ring.Keys[r.legacy]; r.legacy != "" && !ok {
		return nil, fmt.Errorf("%w: %q", ErrLegacyKeyMissing, r.legacy)
	}
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

// LegacyKeyID is the key that reads unprefixed single-key ciphertext, or ""
// when the ring reads only the v1 form.
func (r *AESGCMRing) LegacyKeyID() string { return r.legacy }

// Encrypt seals plaintext under the active key and prefixes the scheme and
// key id, except while the active key is the legacy key: then it writes the
// unprefixed single-key form.
func (r *AESGCMRing) Encrypt(plaintext string) (string, error) {
	payload, err := r.keys[r.active].Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	if r.active == r.legacy {
		return payload, nil
	}
	return ringScheme + "." + r.active + "." + payload, nil
}

// Decrypt opens ciphertext under the key it names, or under the legacy key
// when it has no "." (base64url never contains one). A key no longer in the
// ring is ErrUnknownKeyID; a payload the key rejects is ErrCiphertextAuthFailed.
func (r *AESGCMRing) Decrypt(ciphertext string) (string, error) {
	id, payload, err := r.split(ciphertext)
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

// KeyIDOf reports the key id a ciphertext names, or the legacy key id for an
// unprefixed value when the ring has one.
func (r *AESGCMRing) KeyIDOf(ciphertext string) (string, error) {
	id, _, err := r.split(ciphertext)
	return id, err
}

func (r *AESGCMRing) split(ciphertext string) (id, payload string, err error) {
	if r.legacy != "" && ciphertext != "" && !strings.Contains(ciphertext, ".") {
		return r.legacy, ciphertext, nil
	}
	return splitKeyed(ciphertext)
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
