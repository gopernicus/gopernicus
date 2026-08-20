package invitation

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

var testIDs = cryptids.IDGenerator{}

// TestNewWithMetadataRoundTrip: a valid map is stored, and the stored copy is
// defensive — mutating the caller's map after construction does not change it.
func TestNewWithMetadataRoundTrip(t *testing.T) {
	in := map[string]string{"vendor_org_id": "org-1", "plan": ""}
	inv, err := NewWithMetadata(testIDs, "project", "p1", "member", "a@x.com", "", "inviter", "hash", false, time.Hour, time.Now(), in)
	if err != nil {
		t.Fatalf("NewWithMetadata: %v", err)
	}
	if inv.Metadata["vendor_org_id"] != "org-1" || len(inv.Metadata) != 2 {
		t.Fatalf("stored metadata = %+v", inv.Metadata)
	}
	in["vendor_org_id"] = "tampered"
	in["extra"] = "y"
	if inv.Metadata["vendor_org_id"] != "org-1" || len(inv.Metadata) != 2 {
		t.Errorf("metadata not a defensive copy: %+v", inv.Metadata)
	}
}

// TestNewWithMetadataEmptyIsNil: nil and empty input both yield a nil-metadata
// invitation (the no-metadata case).
func TestNewWithMetadataEmptyIsNil(t *testing.T) {
	for _, in := range []map[string]string{nil, {}} {
		inv, err := NewWithMetadata(testIDs, "project", "p1", "member", "a@x.com", "", "inviter", "hash", false, time.Hour, time.Now(), in)
		if err != nil {
			t.Fatalf("NewWithMetadata(%v): %v", in, err)
		}
		if inv.Metadata != nil {
			t.Errorf("NewWithMetadata(%v) metadata = %#v, want nil", in, inv.Metadata)
		}
	}
}

func TestValidateMetadataBounds(t *testing.T) {
	tooMany := make(map[string]string, MetadataMaxEntries+1)
	for i := range MetadataMaxEntries + 1 {
		tooMany[strings.Repeat("k", 1)+string(rune('a'+i%26))+string(rune('0'+i/26))] = "v"
	}
	// Twenty 256-byte values encode to > 4 KiB while each entry stays in bounds.
	big := map[string]string{}
	for i := range 20 {
		big["key"+string(rune('a'+i))] = strings.Repeat("x", MetadataMaxValueBytes)
	}

	cases := []struct {
		name string
		in   map[string]string
		ok   bool
	}{
		{"nil", nil, true},
		{"empty", map[string]string{}, true},
		{"valid", map[string]string{"k": "v", "empty-value-ok": ""}, true},
		{"too many entries", tooMany, false},
		{"empty key", map[string]string{"": "v"}, false},
		{"key too long", map[string]string{strings.Repeat("k", MetadataMaxKeyBytes+1): "v"}, false},
		{"value too long", map[string]string{"k": strings.Repeat("v", MetadataMaxValueBytes+1)}, false},
		{"invalid utf8 key", map[string]string{"\xff": "v"}, false},
		{"invalid utf8 value", map[string]string{"k": "\xff"}, false},
		{"total too big", big, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateMetadata(tc.in)
			if tc.ok {
				if err != nil {
					t.Fatalf("ValidateMetadata: unexpected err %v", err)
				}
				return
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("ValidateMetadata: err=%v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestValidateMetadataReturnsCopy: the returned map is independent of the input.
func TestValidateMetadataReturnsCopy(t *testing.T) {
	in := map[string]string{"k": "v"}
	out, err := ValidateMetadata(in)
	if err != nil {
		t.Fatalf("ValidateMetadata: %v", err)
	}
	in["k"] = "changed"
	if out["k"] != "v" {
		t.Errorf("ValidateMetadata did not return an independent copy: %+v", out)
	}
}

// TestCloneMetadata: the clone is always non-nil and independent of the source.
func TestCloneMetadata(t *testing.T) {
	if got := CloneMetadata(nil); got == nil || len(got) != 0 {
		t.Errorf("CloneMetadata(nil) = %#v, want non-nil empty map", got)
	}
	src := map[string]string{"k": "v"}
	clone := CloneMetadata(src)
	clone["k"] = "changed"
	if src["k"] != "v" {
		t.Errorf("CloneMetadata shares state with source: %+v", src)
	}
}
