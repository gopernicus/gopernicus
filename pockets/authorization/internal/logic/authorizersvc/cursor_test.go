package authorizersvc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func testFingerprint() string {
	return LookupFingerprint("relationship", "digest-1", PrincipalRef{Type: "user", ID: "u1"}, "view", "project")
}

// encodeRawCursor builds a cursor from an ARBITRARY payload, so a test can
// present shapes the encoder itself would never mint.
func encodeRawCursor(t *testing.T, c lookupCursor) string {
	t.Helper()
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// TestLookupCursorRoundTrip proves an encoded cursor decodes back to the exact
// last id under its own fingerprint.
func TestLookupCursorRoundTrip(t *testing.T) {
	fp := testFingerprint()
	for _, id := range []string{"p1", "a_child", "01J8Z9QK", "ünïcode-id"} {
		cursor := EncodeLookupCursor(id, fp)
		if cursor == "" {
			t.Fatalf("EncodeLookupCursor(%q) produced an empty cursor", id)
		}
		got, err := DecodeLookupCursor(cursor, fp)
		if err != nil {
			t.Fatalf("DecodeLookupCursor(%q): %v", id, err)
		}
		if got != id {
			t.Fatalf("round trip returned %q, want %q", got, id)
		}
	}
}

// TestDecodeLookupCursorRefusesEveryMalformedOrForeignCursor pins the ONE
// sentinel: a caller learns only that it must restart from page one, whether the
// cursor was corrupted in transit, minted under another version, bound to a
// different query, or carries an id that is not a valid reference.
func TestDecodeLookupCursorRefusesEveryMalformedOrForeignCursor(t *testing.T) {
	fp := testFingerprint()
	other := LookupFingerprint("role", "digest-1", PrincipalRef{Type: "user", ID: "u1"}, "view", "project")

	cases := map[string]string{
		"not base64":            "not base64!!",
		"base64 of non-JSON":    base64.RawURLEncoding.EncodeToString([]byte("{not json")),
		"empty cursor":          "",
		"unknown version":       encodeRawCursor(t, lookupCursor{V: 2, LastID: "p1", Fingerprint: fp}),
		"zero version":          encodeRawCursor(t, lookupCursor{LastID: "p1", Fingerprint: fp}),
		"foreign fingerprint":   encodeRawCursor(t, lookupCursor{V: LookupCursorVersion, LastID: "p1", Fingerprint: other}),
		"absent fingerprint":    encodeRawCursor(t, lookupCursor{V: LookupCursorVersion, LastID: "p1"}),
		"empty id":              encodeRawCursor(t, lookupCursor{V: LookupCursorVersion, Fingerprint: fp}),
		"control character id":  encodeRawCursor(t, lookupCursor{V: LookupCursorVersion, LastID: "p\x001", Fingerprint: fp}),
		"over-long id":          encodeRawCursor(t, lookupCursor{V: LookupCursorVersion, LastID: strings.Repeat("x", 257), Fingerprint: fp}),
		"tampered last byte":    EncodeLookupCursor("p1", fp)[:len(EncodeLookupCursor("p1", fp))-1] + "%",
		"truncated base64 body": EncodeLookupCursor("p1", fp)[:8],
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeLookupCursor(cursor, fp)
			if !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("want ErrInvalidCursor, got (%q, %v)", got, err)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("ErrInvalidCursor must wrap sdk.ErrInvalidInput, got %v", err)
			}
			if got != "" {
				t.Fatalf("a refused cursor must yield no id, got %q", got)
			}
		})
	}
}

// TestLookupFingerprintBindsEveryQueryComponent proves the fingerprint changes
// with EACH of the five things a cursor is bound to — the owning kind, that
// kind's model digest, the principal, the permission, and the resource type — so
// a continuation cannot cross into an enumeration with a different id order.
func TestLookupFingerprintBindsEveryQueryComponent(t *testing.T) {
	base := testFingerprint()
	if base == "" {
		t.Fatal("fingerprint must not be empty")
	}
	variants := map[string]string{
		"other kind":         LookupFingerprint("role", "digest-1", PrincipalRef{Type: "user", ID: "u1"}, "view", "project"),
		"other model digest": LookupFingerprint("relationship", "digest-2", PrincipalRef{Type: "user", ID: "u1"}, "view", "project"),
		"other principal id": LookupFingerprint("relationship", "digest-1", PrincipalRef{Type: "user", ID: "u2"}, "view", "project"),
		"other principal type": LookupFingerprint("relationship", "digest-1",
			PrincipalRef{Type: "service_account", ID: "u1"}, "view", "project"),
		"other permission":    LookupFingerprint("relationship", "digest-1", PrincipalRef{Type: "user", ID: "u1"}, "edit", "project"),
		"other resource type": LookupFingerprint("relationship", "digest-1", PrincipalRef{Type: "user", ID: "u1"}, "view", "dashboard"),
	}
	for name, fp := range variants {
		if fp == base {
			t.Fatalf("%s: fingerprint must differ from the base query", name)
		}
	}

	// The encoding is length-prefixed, so no two distinct field splits can alias.
	if LookupFingerprint("relationship", "digest-1", PrincipalRef{Type: "user", ID: "u1"}, "view", "project") !=
		LookupFingerprint("relationship", "digest-1", PrincipalRef{Type: "user", ID: "u1"}, "view", "project") {
		t.Fatal("the fingerprint must be deterministic")
	}
	if LookupFingerprint("relationship", "digest-1", PrincipalRef{Type: "us", ID: "eru1"}, "view", "project") == base {
		t.Fatal("adjacent fields must not alias across the boundary")
	}
}
