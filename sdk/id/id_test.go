package id

import (
	"regexp"
	"testing"
)

func TestNew_FormatAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^[a-z2-7]{26}$`)
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		v := New()
		if !re.MatchString(v) {
			t.Fatalf("id %q does not match expected base32 format", v)
		}
		if seen[v] {
			t.Fatalf("duplicate id generated: %q", v)
		}
		seen[v] = true
	}
}
