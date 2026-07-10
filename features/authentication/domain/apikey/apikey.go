// Package apikey is the machine-credential domain: a high-entropy secret that
// authenticates a service account (logic/serviceaccount). The plaintext key is
// returned exactly once at mint; only its SHA-256 hash is persisted (KeyHash).
// KeyPrefix is stored plain for display. There are no per-key scopes (design
// §4.1) — a key authenticates as its owning service account and the host's
// authorizer governs the rest — and no last_used_ip column (the audit row
// carries IP).
package apikey

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

// APIKey is a hashed machine credential. ExpiresAt zero means the key never
// expires (a NULL/absent expiry at rest); RevokedAt zero means the key is not
// revoked; LastUsedAt zero means it has never authenticated. Revocation and
// expiry are SERVICE-layer branches, not storage filters — GetByHash returns
// the record regardless (design §4.1, the pinned contract).
type APIKey struct {
	ID               string
	ServiceAccountID string
	Name             string
	KeyPrefix        string
	KeyHash          string
	ExpiresAt        time.Time // zero → never expires
	RevokedAt        time.Time // zero → not revoked
	LastUsedAt       time.Time // zero → never used
	CreatedAt        time.Time
}

// New builds an APIKey for serviceAccountID from an already-minted keyPrefix and
// keyHash (the mint lives in the auth service, which is the only holder of the
// plaintext), minting its record ID from ids (empty under cryptids.Database —
// the store then assigns the key). A zero expiresAt means the key never expires.
// A blank serviceAccountID or keyHash wraps sdk.ErrInvalidInput.
func New(ids cryptids.IDGenerator, serviceAccountID, name, keyPrefix, keyHash string, expiresAt, now time.Time) (APIKey, error) {
	serviceAccountID = strings.TrimSpace(serviceAccountID)
	keyHash = strings.TrimSpace(keyHash)
	if serviceAccountID == "" {
		return APIKey{}, fmt.Errorf("service account id is required: %w", sdk.ErrInvalidInput)
	}
	if keyHash == "" {
		return APIKey{}, fmt.Errorf("key hash is required: %w", sdk.ErrInvalidInput)
	}
	if !expiresAt.IsZero() {
		expiresAt = expiresAt.UTC()
	}
	return APIKey{
		ID:               ids.MustGenerate(),
		ServiceAccountID: serviceAccountID,
		Name:             strings.TrimSpace(name),
		KeyPrefix:        strings.TrimSpace(keyPrefix),
		KeyHash:          keyHash,
		ExpiresAt:        expiresAt,
		CreatedAt:        now.UTC(),
	}, nil
}

// Revoked reports whether the key has been revoked.
func (k APIKey) Revoked() bool { return !k.RevokedAt.IsZero() }

// Expired reports whether the key has a set expiry at or before now. A zero
// ExpiresAt (never-expires) is never expired.
func (k APIKey) Expired(now time.Time) bool {
	return !k.ExpiresAt.IsZero() && !now.Before(k.ExpiresAt)
}
