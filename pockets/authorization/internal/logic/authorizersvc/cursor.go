package authorizersvc

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

// LookupCursorVersion is the wire version of an encoded lookup cursor. A cursor
// carrying any other version is refused with ErrInvalidCursor rather than
// interpreted, so a future encoding never silently mis-pages an old client.
const LookupCursorVersion = 1

// LookupCursorEncodingVersion identifies the canonical encoding hashed into a
// cursor FINGERPRINT. It is a prefix of the hashed bytes, so bumping it changes
// every fingerprint — a deliberate, visible break that invalidates in-flight
// cursors.
const LookupCursorEncodingVersion = "gopernicus.authorization.lookupcursor/1"

// lookupCursor is the decoded continuation: the last ID of the previous page
// plus the fingerprint of the query it was produced for. The fingerprint is
// QUERY BINDING, not authentication — LastID stays untrusted opaque client
// input and is validated like any resource id.
type lookupCursor struct {
	V           int    `json:"v"`
	LastID      string `json:"id"`
	Fingerprint string `json:"fp"`
}

// LookupFingerprint digests the identity of one paged enumeration: the owning
// kind, that kind's model digest, the principal, the permission, and the
// resource type. Presenting a cursor against a different query — another
// principal, another permission or type, the other kind, or a model that has
// changed since — is refused, because none of those enumerations share an id
// order with the one the cursor came from.
//
// The encoding is length-prefixed so opaque values (which may legally contain
// any non-control rune) cannot alias across field boundaries. The result is the
// hex of the first 16 digest bytes: enough to make an accidental collision
// across queries implausible, short enough to keep a cursor small.
func LookupFingerprint(kind, modelDigest string, principal PrincipalRef, permission, resourceType string) string {
	h := sha256.New()
	io.WriteString(h, LookupCursorEncodingVersion)
	h.Write([]byte{0})
	for _, field := range []string{kind, modelDigest, principal.Type, principal.ID, permission, resourceType} {
		writeString(h, field)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// EncodeLookupCursor returns the opaque continuation for a page whose last ID is
// lastID under fingerprint. It is base64url (unpadded) JSON: opaque to the
// client, debuggable by the operator.
func EncodeLookupCursor(lastID, fingerprint string) string {
	payload, err := json.Marshal(lookupCursor{V: LookupCursorVersion, LastID: lastID, Fingerprint: fingerprint})
	if err != nil {
		// lookupCursor is three plain fields; json.Marshal cannot fail on it.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeLookupCursor returns the last ID of the previous page, or
// ErrInvalidCursor when the cursor is malformed, carries an unknown version,
// was minted for a different query (fingerprint mismatch), or holds an id that
// is not a structurally valid reference. Every failure is the SAME sentinel: a
// caller learns only that it must restart from page one.
func DecodeLookupCursor(cursor, wantFingerprint string) (string, error) {
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", ErrInvalidCursor
	}
	var decoded lookupCursor
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", ErrInvalidCursor
	}
	if decoded.V != LookupCursorVersion {
		return "", ErrInvalidCursor
	}
	if decoded.Fingerprint == "" || decoded.Fingerprint != wantFingerprint {
		return "", ErrInvalidCursor
	}
	if err := relationship.ValidateRefField("cursor id", decoded.LastID); err != nil {
		return "", ErrInvalidCursor
	}
	return decoded.LastID, nil
}
