package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// dekBytes is the owner data key length (AES-256).
const dekBytes = 32

// ErrSealedValueCorrupt is a stored ciphertext the owner key rejects: the
// value was tampered with, moved to another reference, or sealed under a key
// other than the one its row names. It matches sdk.ErrConflict.
var ErrSealedValueCorrupt = fmt.Errorf("secrets: sealed value failed authentication: %w", sdk.ErrConflict)

var _ Wrapper = (*RingWrapper)(nil)

// RingWrapper is the bundled Wrapper: owner data keys wrapped by a
// cryptids.KeyedEncrypter, normally an AESGCMRing over keys the host reads
// from its environment.
type RingWrapper struct {
	enc cryptids.KeyedEncrypter
}

// NewRingWrapper wraps data keys with enc.
func NewRingWrapper(enc cryptids.KeyedEncrypter) (*RingWrapper, error) {
	if enc == nil {
		return nil, fmt.Errorf("secrets: nil keyed encrypter: %w", sdk.ErrInvalidInput)
	}
	return &RingWrapper{enc: enc}, nil
}

// Wrap seals dek under the ring's active key.
func (w *RingWrapper) Wrap(_ context.Context, dek []byte) (string, string, error) {
	if len(dek) != dekBytes {
		return "", "", fmt.Errorf("secrets: data key must be %d bytes: %w", dekBytes, sdk.ErrInvalidInput)
	}
	blob, err := w.enc.Encrypt(hex.EncodeToString(dek))
	if err != nil {
		return "", "", fmt.Errorf("secrets: wrap data key: %w", err)
	}
	return blob, w.enc.ActiveKeyID(), nil
}

// Unwrap opens blob under whichever ring key it names.
func (w *RingWrapper) Unwrap(_ context.Context, blob string) ([]byte, error) {
	encoded, err := w.enc.Decrypt(blob)
	if err != nil {
		return nil, fmt.Errorf("secrets: unwrap data key: %w", err)
	}
	dek, err := hex.DecodeString(encoded)
	if err != nil || len(dek) != dekBytes {
		return nil, fmt.Errorf("secrets: unwrapped data key malformed: %w", sdk.ErrInvalidInput)
	}
	return dek, nil
}

// ActiveKeyID is the ring key Wrap uses.
func (w *RingWrapper) ActiveKeyID() string { return w.enc.ActiveKeyID() }

// Seal encrypts value under dek with ref as additional authenticated data, so
// a ciphertext copied to another row fails to open. The result is base64url
// of nonce||ciphertext||tag. Exported for the rotation service.
func Seal(dek []byte, ref Ref, value []byte) (string, error) {
	gcm, err := dataKeyCipher(dek)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nil, nil, value, []byte(ref.String()))), nil
}

// Open reverses Seal. A ciphertext the key or the reference rejects is
// ErrSealedValueCorrupt. Exported for the rotation service.
func Open(dek []byte, ref Ref, ciphertext string) ([]byte, error) {
	gcm, err := dataKeyCipher(dek)
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("secrets: sealed value is not base64url: %w", sdk.ErrInvalidInput)
	}
	value, err := gcm.Open(nil, nil, raw, []byte(ref.String()))
	if err != nil {
		return nil, ErrSealedValueCorrupt
	}
	return value, nil
}

func dataKeyCipher(dek []byte) (cipher.AEAD, error) {
	if len(dek) != dekBytes {
		return nil, fmt.Errorf("secrets: data key must be %d bytes: %w", dekBytes, sdk.ErrInvalidInput)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("secrets: data key cipher: %w", err)
	}
	return cipher.NewGCMWithRandomNonce(block)
}

// NewDataKey returns a fresh random owner data key. Exported for the rotation
// service.
func NewDataKey() ([]byte, error) {
	dek := make([]byte, dekBytes)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("secrets: generate data key: %w", err)
	}
	return dek, nil
}

// NewOwnerKeyID returns a fresh owner key version id. Exported for the
// rotation service.
func NewOwnerKeyID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("secrets: generate key id: %w", err)
	}
	return "k_" + hex.EncodeToString(b[:]), nil
}

// NewAuditRecord builds a record with a time-ordered id. Source must already
// be validated. Exported for the rotation service.
func NewAuditRecord(now time.Time, action Action, owner Owner, name string, version int, keyID string, source Source) (AuditRecord, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return AuditRecord{}, fmt.Errorf("secrets: generate audit id: %w", err)
	}
	now = now.UTC().Truncate(time.Microsecond)
	return AuditRecord{
		ID:         fmt.Sprintf("%016x%s", now.UnixNano(), hex.EncodeToString(b[:])),
		OccurredAt: now,
		Action:     action,
		Owner:      owner,
		Name:       name,
		Version:    version,
		KeyID:      keyID,
		Source:     source,
	}, nil
}
