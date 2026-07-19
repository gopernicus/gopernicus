package theme

import (
	"testing"
)

func TestTokensCoverFrozenSet(t *testing.T) {
	if got := len(Tokens()); got != 59 {
		t.Errorf("Tokens() count = %d, want 59 (frozen token set)", got)
	}
}

func TestTokensDeterministicAndUnique(t *testing.T) {
	a, b := Tokens(), Tokens()
	if len(a) != len(b) {
		t.Fatalf("Tokens() not stable: %d vs %d", len(a), len(b))
	}
	seen := map[Token]bool{}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("Tokens() order not deterministic at %d: %q vs %q", i, a[i], b[i])
		}
		if seen[a[i]] {
			t.Errorf("Tokens() has a duplicate: %q", a[i])
		}
		seen[a[i]] = true
	}
	// Sorted ascending.
	for i := 1; i < len(a); i++ {
		if a[i] < a[i-1] {
			t.Errorf("Tokens() not sorted: %q before %q", a[i-1], a[i])
		}
	}
}

func TestHTMLAttributes(t *testing.T) {
	tests := []struct {
		name     string
		app      Appearance
		dir      Direction
		wantDir  string
		wantData string // "" means data-theme absent
	}{
		{"defaults", "", "", "ltr", "light"},
		{"dark rtl", AppearanceDark, DirectionRTL, "rtl", "dark"},
		{"light ltr", AppearanceLight, DirectionLTR, "ltr", "light"},
		{"system omits data-theme", AppearanceSystem, DirectionLTR, "ltr", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := HTMLAttributes(tt.app, tt.dir)
			if got := attrs["dir"]; got != tt.wantDir {
				t.Errorf("dir = %v, want %v", got, tt.wantDir)
			}
			got, present := attrs["data-theme"]
			if tt.wantData == "" {
				if present {
					t.Errorf("data-theme present = %v, want absent", got)
				}
				return
			}
			if got != tt.wantData {
				t.Errorf("data-theme = %v, want %v", got, tt.wantData)
			}
		})
	}
}
