package googleuuid

import (
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/cryptids"
)

// canonical asserts the 8-4-4-4-12 lowercase text form and returns the version
// nibble for the caller to pin.
func canonical(t *testing.T, s string) byte {
	t.Helper()
	if len(s) != 36 {
		t.Fatalf("len = %d, want 36: %q", len(s), s)
	}
	for _, i := range []int{8, 13, 18, 23} {
		if s[i] != '-' {
			t.Fatalf("missing dash at %d: %q", i, s)
		}
	}
	if s != strings.ToLower(s) {
		t.Fatalf("not lowercase: %q", s)
	}
	return s[14] // the version nibble
}

func TestV4Shape(t *testing.T) {
	s, err := V4()()
	if err != nil {
		t.Fatalf("V4: %v", err)
	}
	if v := canonical(t, s); v != '4' {
		t.Errorf("version nibble = %c, want 4 (%q)", v, s)
	}
}

func TestV7Shape(t *testing.T) {
	s, err := V7()()
	if err != nil {
		t.Fatalf("V7: %v", err)
	}
	if v := canonical(t, s); v != '7' {
		t.Errorf("version nibble = %c, want 7 (%q)", v, s)
	}
}

func TestUniqueness(t *testing.T) {
	gen := V4()
	seen := map[string]bool{}
	for range 1000 {
		s, err := gen()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if seen[s] {
			t.Fatalf("duplicate uuid: %q", s)
		}
		seen[s] = true
	}
}

// TestWiresAsIDGenerator proves the decided-once wiring shape a host uses:
// cryptids.NewGenerator(googleuuid.V7()) on a feature's Config.IDs.
func TestWiresAsIDGenerator(t *testing.T) {
	gen := cryptids.NewGenerator(V7())
	s := gen.MustGenerate()
	if v := canonical(t, s); v != '7' {
		t.Errorf("version nibble = %c, want 7 (%q)", v, s)
	}
}

// TestV7TextOrder pins the property V7 is chosen for: later mints sort later
// as text.
func TestV7TextOrder(t *testing.T) {
	gen := V7()
	prev, err := gen()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for range 100 {
		s, err := gen()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if s < prev {
			t.Fatalf("text order regressed: %q then %q", prev, s)
		}
		prev = s
	}
}
