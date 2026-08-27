package authentication

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
)

// minKeyBytes is the shortest HMAC key the bundled security helpers accept: 256
// bits, mirroring cryptids.NewHS256's floor (design §3.3). A six-digit code has
// only a million possibilities, so the pepper — not the code's entropy — is the
// defense; a key below its design strength is rejected at construction rather
// than used. The identifier keyer applies the same floor.
const minKeyBytes = 32

// Domain-separation labels. Each keyed digest is bound to a distinct label so a
// value digested for one purpose can never collide with another, even under the
// same key. The labels are versioned so a future scheme change is unambiguous.
const (
	challengeCodeDomain = "gopernicus.auth.challenge.code.v1"
	identifierKeyDomain = "gopernicus.auth.identifier-key.v1"
)

// Construction and use errors for the bundled security helpers. These fire at
// NewHMACChallengeProtector / NewHMACIdentifierKeyer construction (or, for
// ErrChallengeUnknownKeyID / ErrEmptyChallengeCode, on a misuse of an otherwise
// valid protector); they are distinct from the AV3-0.1 Config required-slot
// errors, which fire when a subsystem is enabled without a collaborator.
var (
	// ErrChallengeKeyRingEmpty is returned when HMACKeyRing.Keys is empty.
	ErrChallengeKeyRingEmpty = errors.New("auth: HMACKeyRing.Keys is empty")
	// ErrChallengeActiveKeyMissing is returned when HMACKeyRing.Active is empty
	// or names a key ID absent from HMACKeyRing.Keys.
	ErrChallengeActiveKeyMissing = errors.New("auth: HMACKeyRing.Active is empty or absent from Keys")
	// ErrChallengeKeyTooShort is returned when any key in the ring is under
	// minKeyBytes.
	ErrChallengeKeyTooShort = fmt.Errorf("auth: every challenge pepper key must be at least %d bytes", minKeyBytes)
	// ErrChallengeUnknownKeyID is returned by DigestCode when keyID is not an
	// accepted key ID in the ring.
	ErrChallengeUnknownKeyID = errors.New("auth: DigestCode called with an unknown key ID")
	// ErrEmptyChallengeCode is returned by DigestCode / CandidateCodeDigests when
	// the code is empty: an empty code can never be a legitimately issued secret.
	ErrEmptyChallengeCode = errors.New("auth: challenge code is empty")
	// ErrIdentifierKeyTooShort is returned when the identifier keyer key is under
	// minKeyBytes.
	ErrIdentifierKeyTooShort = fmt.Errorf("auth: identifier keyer key must be at least %d bytes", minKeyBytes)
)

// Compile-time proof the bundled helpers satisfy their public ports.
var (
	_ ChallengeProtector = (*HMACChallengeProtector)(nil)
	_ IdentifierKeyer    = (*HMACIdentifierKeyer)(nil)
)

// HMACKeyRing is the host-supplied key ring for the bundled challenge protector.
// Active names the key new issues are digested under; Keys holds every accepted
// key ID so an unexpired challenge issued under an old key remains verifiable
// during rotation (design §3.3). Every key must be at least 32 random bytes and
// distinct from the JWT signing key and the AES-GCM encryption key.
type HMACKeyRing struct {
	Active string
	Keys   map[string][]byte
}

// HMACChallengeProtector protects short authentication codes with a keyed HMAC
// and digests high-entropy tokens with SHA-256. It is stdlib-only (crypto/hmac
// + crypto/sha256) — there is no external pepper service and no network call
// (design §3.3). Construct with NewHMACChallengeProtector; the zero value is not
// usable.
type HMACChallengeProtector struct {
	activeKeyID string
	keys        map[string][]byte
	keyIDs      []string // accepted key IDs, sorted for deterministic candidates
}

// NewHMACChallengeProtector builds a protector from a key ring. It returns a
// loud construction error when the ring is empty, the active key is missing, or
// any key is under 32 bytes. Every key is copied so a later mutation of the
// caller's slice cannot swap the pepper.
func NewHMACChallengeProtector(ring HMACKeyRing) (*HMACChallengeProtector, error) {
	if len(ring.Keys) == 0 {
		return nil, ErrChallengeKeyRingEmpty
	}
	if ring.Active == "" {
		return nil, ErrChallengeActiveKeyMissing
	}
	if _, ok := ring.Keys[ring.Active]; !ok {
		return nil, ErrChallengeActiveKeyMissing
	}

	keys := make(map[string][]byte, len(ring.Keys))
	keyIDs := make([]string, 0, len(ring.Keys))
	for id, key := range ring.Keys {
		if len(key) < minKeyBytes {
			return nil, fmt.Errorf("%w: key %q is %d bytes", ErrChallengeKeyTooShort, id, len(key))
		}
		cp := make([]byte, len(key))
		copy(cp, key)
		keys[id] = cp
		keyIDs = append(keyIDs, id)
	}
	sort.Strings(keyIDs)

	return &HMACChallengeProtector{
		activeKeyID: ring.Active,
		keys:        keys,
		keyIDs:      keyIDs,
	}, nil
}

// ActiveKeyID reports the key ID new issues are digested under.
func (p *HMACChallengeProtector) ActiveKeyID() string { return p.activeKeyID }

// DigestCode returns the HMAC-SHA-256 digest of a code under keyID, domain-
// separated and bound to userID + purpose + code. keyID must name an accepted
// key; an empty code is rejected.
func (p *HMACChallengeProtector) DigestCode(keyID, userID, purpose, code string) (string, error) {
	if code == "" {
		return "", ErrEmptyChallengeCode
	}
	key, ok := p.keys[keyID]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrChallengeUnknownKeyID, keyID)
	}
	return codeDigest(key, userID, purpose, code), nil
}

// CandidateCodeDigests returns one DigestCandidate per accepted key ID (sorted
// by key ID) so an unexpired challenge issued under an old key stays verifiable
// during rotation. The atomic store selects the candidate whose KeyID matches
// the row's protector_key_id. An empty code is rejected.
func (p *HMACChallengeProtector) CandidateCodeDigests(userID, purpose, code string) ([]DigestCandidate, error) {
	if code == "" {
		return nil, ErrEmptyChallengeCode
	}
	candidates := make([]DigestCandidate, 0, len(p.keyIDs))
	for _, id := range p.keyIDs {
		candidates = append(candidates, DigestCandidate{
			KeyID:  id,
			Digest: codeDigest(p.keys[id], userID, purpose, code),
		})
	}
	return candidates, nil
}

// DigestToken returns the SHA-256 digest of a high-entropy URL token as a
// 64-character lowercase hex string, matching cryptids.SHA256Hasher. Entropy,
// not a pepper, protects tokens, so no key is applied. An empty token digests to
// the empty string so the store's empty-hash guard never matches it.
func (p *HMACChallengeProtector) DigestToken(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// HMACIdentifierKeyer derives a stable, non-reversible key from an identifier
// for rate-limiter and idempotency keys, so raw PII never enters limiter keys
// (design §4.4). Its key is distinct from the challenge pepper, the JWT signing
// key, and the delivery-encryption key. Construct with NewHMACIdentifierKeyer;
// the zero value is not usable.
type HMACIdentifierKeyer struct {
	key []byte
}

// NewHMACIdentifierKeyer builds an identifier keyer from a single key. It returns
// ErrIdentifierKeyTooShort when the key is under 32 bytes. The key is copied so a
// later mutation of the caller's slice cannot swap it.
func NewHMACIdentifierKeyer(key []byte) (*HMACIdentifierKeyer, error) {
	if len(key) < minKeyBytes {
		return nil, ErrIdentifierKeyTooShort
	}
	cp := make([]byte, len(key))
	copy(cp, key)
	return &HMACIdentifierKeyer{key: cp}, nil
}

// IdentifierKey returns the domain-separated HMAC-SHA-256 of kind + normalized
// value as a 64-character lowercase hex string. It never reveals the identifier
// and, using a separate key and domain label, never collides with a challenge
// code digest.
func (k *HMACIdentifierKeyer) IdentifierKey(kind, normalizedValue string) string {
	mac := hmac.New(sha256.New, k.key)
	writeField(mac, identifierKeyDomain)
	writeField(mac, kind)
	writeField(mac, normalizedValue)
	return hex.EncodeToString(mac.Sum(nil))
}

// ConstantTimeDigestEqual reports whether two digests are equal in constant time.
// Memory-backed challenge repositories (and authmem) compare a candidate digest
// against the stored digest in Go rather than in SQL; they route through this
// helper so the comparison never short-circuits on the first differing byte. An
// empty digest never matches (the empty-hash guard).
func ConstantTimeDigestEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// codeDigest computes the domain-separated HMAC-SHA-256 of the code fields under
// key, returned as lowercase hex. Fields are length-prefixed so no two distinct
// (userID, purpose, code) tuples can produce the same MAC input.
func codeDigest(key []byte, userID, purpose, code string) string {
	mac := hmac.New(sha256.New, key)
	writeField(mac, challengeCodeDomain)
	writeField(mac, userID)
	writeField(mac, purpose)
	writeField(mac, code)
	return hex.EncodeToString(mac.Sum(nil))
}

// writeField writes s to w with an 8-byte big-endian length prefix, so a
// sequence of writeField calls is an unambiguous encoding of its fields (no
// separator-injection or field-boundary ambiguity).
func writeField(w io.Writer, s string) {
	var lp [8]byte
	binary.BigEndian.PutUint64(lp[:], uint64(len(s)))
	_, _ = w.Write(lp[:])
	_, _ = io.WriteString(w, s)
}
