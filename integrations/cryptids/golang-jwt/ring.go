package golangjwt

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// ringMinKeyBytes is the HS256 floor; HMAC accepts longer keys.
const ringMinKeyBytes = 32

var (
	// ErrUnknownKeyID is returned by RingSigner.Verify for a token whose kid
	// header is malformed, not a string, or names a key not in the ring.
	ErrUnknownKeyID = errors.New("golangjwt: token names a key that is not in the ring")

	// ErrMissingKeyID is returned by RingSigner.Verify for a token without a
	// kid header when the ring has no legacy key.
	ErrMissingKeyID = errors.New("golangjwt: token has no key id")
)

// keyIDPattern mirrors the cryptids key-id syntax, so a kid is echoed in an
// error only when it has the shape of a configured id.
var keyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Compile-time proof that RingSigner satisfies the sdk-owned port.
var _ cryptids.JWTSigner = (*RingSigner)(nil)

// RingSigner signs HS256 JWTs under the active key of a cryptids.KeyRing,
// stamping its id in the kid header, and verifies a token under the ring key
// its kid names. Construct it with NewRing; the zero value is not usable.
type RingSigner struct {
	active string
	legacy string
	keys   map[string][]byte
	method *jwt.SigningMethodHMAC
}

// RingOption configures construction of a RingSigner. Options apply in order.
type RingOption func(*ringConfig)

type ringConfig struct {
	legacy string
}

// WithLegacyKeyID names the ring key that verifies a token with no kid header,
// the shape a single-key Signer writes, so tokens minted before a host moved
// to a ring keep verifying. While that key is also the active key, Sign writes
// no kid either, so instances still on a single-key Signer verify what the
// ring writes during a rolling deploy. An empty id is no option.
func WithLegacyKeyID(id string) RingOption {
	return func(cfg *ringConfig) { cfg.legacy = id }
}

// NewRing builds an HS256 RingSigner. The ring must validate with every key at
// least 32 bytes (cryptids.KeyRing.ValidateMinimum); keys are copied. A legacy
// key id not in the ring is cryptids.ErrLegacyKeyMissing. A nil option returns
// an error wrapping sdk.ErrInvalidInput. HS384 and HS512 rings are not
// supported; Signer keeps those methods.
func NewRing(ring cryptids.KeyRing, opts ...RingOption) (*RingSigner, error) {
	var cfg ringConfig
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("golangjwt: nil RingOption: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if err := ring.ValidateMinimum(ringMinKeyBytes); err != nil {
		return nil, fmt.Errorf("golangjwt: %w", err)
	}
	if _, ok := ring.Keys[cfg.legacy]; cfg.legacy != "" && !ok {
		return nil, fmt.Errorf("golangjwt: %w: %q", cryptids.ErrLegacyKeyMissing, cfg.legacy)
	}
	keys := make(map[string][]byte, len(ring.Keys))
	for id, raw := range ring.Keys {
		keys[id] = slices.Clone(raw)
	}
	method := *jwt.SigningMethodHS256
	return &RingSigner{active: ring.Active, legacy: cfg.legacy, keys: keys, method: &method}, nil
}

// ActiveKeyID is the id Sign signs under.
func (r *RingSigner) ActiveKeyID() string { return r.active }

// KeyIDs returns a sorted copy of the ring's key ids.
func (r *RingSigner) KeyIDs() []string { return slices.Sorted(maps.Keys(r.keys)) }

// LegacyKeyID is the key that verifies a token without a kid, or "" when
// every token must carry one.
func (r *RingSigner) LegacyKeyID() string { return r.legacy }

// Format writes a summary without any key for every verb, so %v, %+v and %#v
// of a RingSigner never print key material.
func (r *RingSigner) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprintf(f, "golangjwt.RingSigner{Method:%s Active:%s Keys:%d Legacy:%t Secret:%s}",
		r.alg(), r.active, len(r.keys), r.legacy != "", sdk.Redacted)
}

// LogValue returns the method, active id, key count and whether a legacy id
// is set, never a key.
func (r *RingSigner) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("method", r.alg()),
		slog.String("active_key_id", r.active),
		slog.Int("key_count", len(r.keys)),
		slog.Bool("legacy", r.legacy != ""),
	)
}

func (r *RingSigner) alg() string {
	if r == nil || r.method == nil {
		return ""
	}
	return r.method.Alg()
}

// Sign creates a signed token under the active key, with its id as the kid
// header, carrying claims plus registered exp and iat claims exactly as
// Signer.Sign does. While the active key is the legacy key no kid is written.
func (r *RingSigner) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	if r == nil || r.method == nil || len(r.keys[r.active]) == 0 {
		return "", errors.New("golangjwt: ring signer must be created with NewRing")
	}
	kid := r.active
	if kid == r.legacy {
		kid = ""
	}
	return sign(r.method, r.keys[r.active], kid, claims, expiresAt)
}

// Verify validates a token exactly as Signer.Verify does, under the ring key
// its kid header names. A token without a kid verifies under the legacy key
// when one is set and is ErrMissingKeyID otherwise. A kid that is malformed,
// not a string, or not in the ring is ErrUnknownKeyID; no other key is tried.
// The signing method is pinned to HS256 before the kid is read.
func (r *RingSigner) Verify(tokenString string) (map[string]any, error) {
	if r == nil || r.method == nil || len(r.keys) == 0 {
		return nil, errors.New("golangjwt: ring signer must be created with NewRing")
	}
	if tokenString == "" {
		return nil, ErrEmptyToken
	}
	return verify(tokenString, r.method, r.key)
}

func (r *RingSigner) key(token *jwt.Token) ([]byte, error) {
	raw, present := token.Header["kid"]
	if !present {
		if r.legacy == "" {
			return nil, ErrMissingKeyID
		}
		return r.keys[r.legacy], nil
	}
	kid, ok := raw.(string)
	if !ok || !keyIDPattern.MatchString(kid) {
		return nil, fmt.Errorf("%w: invalid", ErrUnknownKeyID)
	}
	key, ok := r.keys[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKeyID, kid)
	}
	return key, nil
}
