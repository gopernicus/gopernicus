package turso

import (
	"maps"
	"testing"
)

// TestUnmarshalMetadata proves the stored-JSON decoder yields a non-nil empty map
// for the empty/NULL/'{}' cases, round-trips a populated object, and returns an
// error (never a silent empty) for malformed stored JSON.
func TestUnmarshalMetadata(t *testing.T) {
	empties := []string{"", "null", "{}"}
	for _, s := range empties {
		got, err := unmarshalMetadata(s)
		if err != nil {
			t.Fatalf("unmarshalMetadata(%q): %v", s, err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("unmarshalMetadata(%q) = %#v, want non-nil empty map", s, got)
		}
	}

	want := map[string]string{"vendor_org_id": "org-1", "plan": "pro"}
	got, err := unmarshalMetadata(`{"vendor_org_id":"org-1","plan":"pro"}`)
	if err != nil {
		t.Fatalf("unmarshalMetadata(valid): %v", err)
	}
	if !maps.Equal(got, want) {
		t.Errorf("unmarshalMetadata(valid) = %+v, want %+v", got, want)
	}

	if _, err := unmarshalMetadata(`{"k":`); err == nil {
		t.Error("unmarshalMetadata(malformed): want error, got nil")
	}
}

// TestMetadataJSONScan proves the sql.Scanner decodes string and []byte sources,
// treats NULL as an empty map, rejects an unsupported source type, and surfaces a
// malformed-JSON error (rather than swallowing it) inside rows.Scan.
func TestMetadataJSONScan(t *testing.T) {
	var m metadataJSON
	if err := m.Scan([]byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if m["k"] != "v" {
		t.Errorf("Scan([]byte) = %+v", m)
	}

	m = nil
	if err := m.Scan(nil); err != nil || m == nil || len(m) != 0 {
		t.Errorf("Scan(nil) = %+v err=%v, want non-nil empty map", m, err)
	}

	m = nil
	if err := m.Scan(`{bad`); err == nil {
		t.Error("Scan(malformed): want error, got nil")
	}

	m = nil
	if err := m.Scan(42); err == nil {
		t.Error("Scan(int): want unsupported-type error, got nil")
	}
}

// TestMarshalMetadata proves nil/empty encode to '{}' (never JSON null) and a
// populated map encodes to its JSON object.
func TestMarshalMetadata(t *testing.T) {
	for _, in := range []map[string]string{nil, {}} {
		got, err := marshalMetadata(in)
		if err != nil || got != "{}" {
			t.Errorf("marshalMetadata(%v) = %q err=%v, want '{}'", in, got, err)
		}
	}
	got, err := marshalMetadata(map[string]string{"k": "v"})
	if err != nil || got != `{"k":"v"}` {
		t.Errorf("marshalMetadata(populated) = %q err=%v", got, err)
	}
}
