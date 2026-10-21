package authorization

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	cursorVersion = 1
	// maxCursorBytes bounds a presented cursor before any decoding work.
	maxCursorBytes = 8 << 10
)

type cursorEnvelope struct {
	V     int    `json:"v"`
	FP    string `json:"fp"`
	Inner string `json:"in"`
}

// EncodeCursor wraps an implementation's inner continuation in the shared
// envelope: version, a fingerprint of req's principal, permission and
// resource type, then inner. req.After and req.Limit do not enter the
// fingerprint, so a caller may change the page size between pages.
func EncodeCursor(req LookupRequest, inner string) string {
	raw, err := json.Marshal(cursorEnvelope{V: cursorVersion, FP: fingerprint(req), Inner: inner})
	if err != nil {
		// A struct of strings and an int always marshals.
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor returns the inner continuation carried by req.After, or "" when
// After is empty. A malformed envelope, another version, or one bound to
// another query is ErrInvalidCursor.
func DecodeCursor(req LookupRequest) (string, error) {
	if req.After == "" {
		return "", nil
	}
	if len(req.After) > maxCursorBytes {
		return "", fmt.Errorf("%w: exceeds %d bytes", ErrInvalidCursor, maxCursorBytes)
	}
	raw, err := base64.RawURLEncoding.DecodeString(req.After)
	if err != nil {
		return "", fmt.Errorf("%w: not base64url", ErrInvalidCursor)
	}
	var env cursorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("%w: not an envelope", ErrInvalidCursor)
	}
	if env.V != cursorVersion {
		return "", fmt.Errorf("%w: unknown version %d", ErrInvalidCursor, env.V)
	}
	if env.FP != fingerprint(req) {
		return "", fmt.Errorf("%w: bound to another query", ErrInvalidCursor)
	}
	if env.Inner == "" {
		return "", fmt.Errorf("%w: empty continuation", ErrInvalidCursor)
	}
	return env.Inner, nil
}

func fingerprint(req LookupRequest) string {
	h := sha256.New()
	for _, part := range []string{req.Principal.Type, req.Principal.ID, req.Permission, req.ResourceType} {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}
