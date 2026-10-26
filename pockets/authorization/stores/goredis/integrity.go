package goredis

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
)

const (
	// MinIntegrityKeyBytes is the shortest key WithIntegrityKeys accepts.
	MinIntegrityKeyBytes = 32

	factContext   = "gopernicus.tuplecache.fact.v1"
	headerContext = "gopernicus.tuplecache.header.v1"
	// factTagBytes truncates the fact HMAC; base64url encodes it in 22 chars.
	factTagBytes = 16
	factTagChars = 22
)

// ErrIntegrity reports a keyed mirror whose header MAC or a fact tag failed
// verification. It matches tuplecache.ErrUnavailable, so decisions fall back to
// the durable source.
var ErrIntegrity = fmt.Errorf("tuple cache integrity verification failed: %w", tuplecache.ErrUnavailable)

// keyRing signs with keys[0] and verifies against every key. An epoch (one full
// publication and the deltas on it) is signed with a single key: the key that
// verifies the header also signs that epoch's deltas and verifies its tags.
type keyRing struct {
	keys [][]byte
}

// header is a mirror's metadata as stored. Every value is the raw Redis string.
// location is the mirror's Redis key, so a signed mirror copied to another
// namespace or binding fails verification. Facts need no location: they are
// bound to the epoch, which only a verified header vouches for.
type header struct {
	location, binding, receipt, until, epoch, epochAt, mac string
}

// sign returns the header MAC under key.
func (h header) sign(key []byte) string {
	m := hmac.New(sha256.New, key)
	writeParts(m, headerContext, h.location, h.binding, h.receipt, h.epoch, h.epochAt, h.until)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// verify returns the ring key whose MAC matches the stored one.
func (r *keyRing) verify(h header) ([]byte, bool) {
	if h.mac == "" {
		return nil, false
	}
	for _, key := range r.keys {
		if hmac.Equal([]byte(h.sign(key)), []byte(h.mac)) {
			return key, true
		}
	}
	return nil, false
}

// factSigner computes fact tags for one binding and epoch under one key.
type factSigner struct {
	mac            hash.Hash
	binding, epoch string
	sum            []byte
	encoded        []byte
}

func newFactSigner(key []byte, binding, epoch string) *factSigner {
	return &factSigner{mac: hmac.New(sha256.New, key), binding: binding, epoch: epoch, encoded: make([]byte, factTagChars)}
}

// tag returns base64url(HMAC-SHA256(key, context ‖ binding ‖ epoch ‖ f1…f7)[:16]).
func (s *factSigner) tag(w wireTuple) string {
	s.mac.Reset()
	writeParts(s.mac, factContext, s.binding, s.epoch)
	writeParts(s.mac, w[:]...)
	s.sum = s.mac.Sum(s.sum[:0])
	base64.RawURLEncoding.Encode(s.encoded, s.sum[:factTagBytes])
	return string(s.encoded)
}

func (s *factSigner) valid(w wireTuple, tag string) bool {
	return len(tag) == factTagChars && hmac.Equal([]byte(s.tag(w)), []byte(tag))
}

// writeParts length-prefixes every part so no two part lists share an input.
func writeParts(h hash.Hash, parts ...string) {
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
}
