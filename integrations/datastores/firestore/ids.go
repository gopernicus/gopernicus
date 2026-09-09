package firestore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// idAlphabet and idLength reproduce the vendor's auto-id shape
// (CollectionRef.NewDoc → 20 characters of [A-Za-z0-9], collref.go:136-147):
// URL-safe, no separator, well inside every document-id limit.
const (
	idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	idLength   = 20
)

// keyHashLengthBytes is the width of the big-endian length prefix KeyHash puts
// in front of every component.
const keyHashLengthBytes = 8

// NewID returns a database-style document id: 20 characters of [A-Za-z0-9] from
// crypto/rand, the same shape CollectionRef.NewDoc generates — but without
// needing a collection reference, so a store can mint an id before it decides
// where the document goes (the "DB-generated id" storetest cases).
//
// It differs from the vendor's generator in one invisible way: the vendor maps
// each random byte with a modulo, which favors the first four characters of the
// alphabet slightly; this draws 6 bits at a time and rejects the two
// out-of-range values, so the distribution is uniform. Same alphabet, same
// length, ~119 bits of entropy either way.
//
// It panics if the system entropy source fails, which is what crypto/rand does
// internally anyway: an id generator that returns a predictable value on error
// is worse than a crash.
func NewID() string {
	id := make([]byte, 0, idLength)
	buf := make([]byte, idLength)
	for len(id) < idLength {
		if _, err := rand.Read(buf); err != nil {
			panic(fmt.Sprintf("firestore: crypto/rand.Read: %v", err))
		}
		for _, b := range buf {
			if v := b & 0x3f; int(v) < len(idAlphabet) {
				id = append(id, idAlphabet[v])
				if len(id) == idLength {
					break
				}
			}
		}
	}
	return string(id)
}

// KeyHash returns the document id for a natural key: the lowercase hex SHA-256
// (64 characters) of an unambiguous encoding of parts.
//
// Encoding: for each part, in order, eight bytes of its LENGTH in bytes,
// big-endian, followed by the part's raw bytes. Nothing else — no separator, no
// terminator. Length-prefixing is what makes the tuple unambiguous:
// ["ab", "c"] encodes as 00…02 "ab" 00…01 "c" and ["a", "bc"] as 00…01 "a"
// 00…02 "bc", so no two distinct tuples share an encoding, and empty components
// are distinguishable from absent ones (KeyHash() ≠ KeyHash("") ≠
// KeyHash("", "")).
//
// This is not a style choice (compatibility note N1). A port-legal natural key
// in either pocket can exceed Firestore's 1500-byte document-id limit, contain
// a "/", or collide with the reserved __.*__ / "." / ".." forms; the six-part
// authorization relationship tuple alone reaches 1536 bytes. A hash is
// fixed-width, slash-free and never reserved. The original components stay in
// document FIELDS and in returned values — a KeyHash is an identity, never a
// projection and never a sort key (ordering follows the port's order fields).
func KeyHash(parts ...string) string {
	sum := sha256.New()
	var length [keyHashLengthBytes]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		sum.Write(length[:])
		sum.Write([]byte(part))
	}
	return hex.EncodeToString(sum.Sum(nil))
}
