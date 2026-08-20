package pgx

import "testing"

// TestMarshalMetadata proves nil/empty encode to '{}' (never JSON null, which
// would bypass the jsonb column DEFAULT) and a populated map encodes to its JSON
// object. The read side decodes jsonb through pgx's own JSON codec into the
// row-struct map[string]string field, which surfaces malformed stored JSON as a
// scan error (verified end-to-end by the live conformance suite).
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
