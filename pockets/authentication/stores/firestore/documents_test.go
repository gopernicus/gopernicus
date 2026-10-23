package firestore

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// awkwardKeys are the map keys a Firestore MAP field cannot carry as itself.
// Firestore map keys are FIELD PATHS: "" is rejected, "a.b" is split into nested
// fields, "__x__" is a reserved name, and "a/b"/"a[0]" collide with the path
// syntax. security_events.details and invitations.metadata are the two open bags
// in this store — their keys come from a HOST, so the store cannot know which of
// these it will be handed, and the SQL adapters accept all of them because JSON
// text has no key vocabulary at all.
//
// The bags are therefore JSON text here too (documents.go). These are the keys
// that would have made a native map quietly disagree with the other two
// families.
var awkwardKeys = []string{"__x__", "", "a.b", "a/b", "a[0]", "__name__", ".", "..", "a\x00b"}

// TestDetailsRoundTripEveryKey is the encoding half of the parity claim: every
// key survives, and a NUMBER comes back as JSON's float64 — the same type the
// pgx and turso adapters return, rather than Firestore's int64/float64 split.
func TestDetailsRoundTripEveryKey(t *testing.T) {
	in := map[string]any{}
	for i, key := range awkwardKeys {
		in[key] = map[bool]string{true: "value", false: ""}[i%2 == 0]
	}
	in["count"] = float64(7)
	in["nested"] = map[string]any{"a.b": "deep"}

	encoded, err := encodeDetails(in)
	if err != nil {
		t.Fatalf("encodeDetails: %v", err)
	}
	out, err := decodeDetails(encoded)
	if err != nil {
		t.Fatalf("decodeDetails: %v", err)
	}
	if !maps.EqualFunc(in, out, sameJSON) {
		t.Errorf("details round trip changed the bag:\n in = %#v\nout = %#v", in, out)
	}
	if _, ok := out["count"].(float64); !ok {
		t.Errorf("a number round-tripped as %T, want float64 — the SQL families return JSON's float64 and this store must agree", out["count"])
	}
}

// TestMetadataRoundTripEveryKey is the same property for the invitation bag,
// whose values are strings.
func TestMetadataRoundTripEveryKey(t *testing.T) {
	in := map[string]string{}
	for i, key := range awkwardKeys {
		in[key] = map[bool]string{true: "value", false: ""}[i%2 == 0]
	}

	encoded, err := encodeMetadata(in)
	if err != nil {
		t.Fatalf("encodeMetadata: %v", err)
	}
	out, err := decodeMetadata(encoded)
	if err != nil {
		t.Fatalf("decodeMetadata: %v", err)
	}
	if !maps.Equal(in, out) {
		t.Errorf("metadata round trip changed the bag:\n in = %#v\nout = %#v", in, out)
	}
}

// TestEmptyBagsAreStoredAsAnEmptyObject pins the uniform round-trip contract the
// three families share: nil in, non-nil empty out, and '{}' on the wire — never
// a null, which would bypass the SQL column's DEFAULT and read back as nil.
func TestEmptyBagsAreStoredAsAnEmptyObject(t *testing.T) {
	for _, in := range []map[string]any{nil, {}} {
		encoded, err := encodeDetails(in)
		if err != nil {
			t.Fatalf("encodeDetails(%v): %v", in, err)
		}
		if encoded != "{}" {
			t.Errorf("encodeDetails(%v) = %q, want %q", in, encoded, "{}")
		}
	}
	for _, in := range []map[string]string{nil, {}} {
		encoded, err := encodeMetadata(in)
		if err != nil {
			t.Fatalf("encodeMetadata(%v): %v", in, err)
		}
		if encoded != "{}" {
			t.Errorf("encodeMetadata(%v) = %q, want %q", in, encoded, "{}")
		}
	}

	// Absent, "null" and '{}' are one fact on the way back — the third is what a
	// document written before the field existed decodes as.
	for _, stored := range []string{"", "null", "{}"} {
		details, err := decodeDetails(stored)
		if err != nil {
			t.Fatalf("decodeDetails(%q): %v", stored, err)
		}
		if details == nil || len(details) != 0 {
			t.Errorf("decodeDetails(%q) = %#v, want a non-nil empty map", stored, details)
		}
		metadata, err := decodeMetadata(stored)
		if err != nil {
			t.Fatalf("decodeMetadata(%q): %v", stored, err)
		}
		if metadata == nil || len(metadata) != 0 {
			t.Errorf("decodeMetadata(%q) = %#v, want a non-nil empty map", stored, metadata)
		}
	}
}

// TestMalformedBagsAreRejected keeps a decode failure an ERROR rather than a
// silently empty bag: an audit record that lost its details without saying so is
// worse than one that fails to load.
func TestMalformedBagsAreRejected(t *testing.T) {
	if _, err := decodeDetails("{not json"); err == nil {
		t.Error("decodeDetails accepted malformed stored JSON")
	}
	if _, err := decodeMetadata("[1,2,3]"); err == nil {
		t.Error("decodeMetadata accepted a JSON array as a metadata bag")
	}
}

// sameJSON compares two decoded values by their JSON rendering, which is what
// "round-trips as itself" means for an open bag.
func sameJSON(a, b any) bool {
	left, err := encodeDetails(map[string]any{"v": a})
	if err != nil {
		return false
	}
	right, err := encodeDetails(map[string]any{"v": b})
	if err != nil {
		return false
	}
	return left == right
}

// TestSecurityEventDocCarriesCorrelation is the hermetic half of the
// correlation round-trip (the storetest case runs on the emulator): both ids
// reach the document and come back, a 129-byte id whole, and a document written
// before the fields existed reads them back empty.
func TestSecurityEventDocCarriesCorrelation(t *testing.T) {
	long := strings.Repeat("r", securityevent.MaxCorrelationIDLen+1)
	evt := securityevent.New(sdk.IDGenerator{}, securityevent.TypeLogin, securityevent.StatusSuccess, time.Now())
	evt.RequestID, evt.TraceID = "req-1", long
	doc, err := newSecurityEventDoc(evt)
	if err != nil {
		t.Fatal(err)
	}
	if doc.RequestID != "req-1" || doc.TraceID != long {
		t.Fatalf("document ids = %q, %q", doc.RequestID, doc.TraceID)
	}
	got, err := doc.toDomain()
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestID != "req-1" || got.TraceID != long {
		t.Fatalf("read back ids = %q, %q", got.RequestID, got.TraceID)
	}
	doc.RequestID, doc.TraceID = "", ""
	if legacy, err := doc.toDomain(); err != nil || legacy.RequestID != "" || legacy.TraceID != "" {
		t.Fatalf("legacy document = %+v, %v", legacy, err)
	}
}
