package id

import (
	"regexp"
	"strings"
	"testing"
)

func TestNew_LengthAlphabetUniqueness(t *testing.T) {
	a := New()
	b := New()
	if len(a) != DefaultLength {
		t.Fatalf("New() length = %d, want %d", len(a), DefaultLength)
	}
	for i := 0; i < len(a); i++ {
		if strings.IndexByte(Alphabet, a[i]) < 0 {
			t.Fatalf("New() byte %q not in Alphabet", a[i])
		}
	}
	if a == b {
		t.Fatalf("two New() calls returned the same id: %q", a)
	}
}

func TestNewCustom_HonorsAlphabetAndSize(t *testing.T) {
	const custom = "abc"
	v, err := NewCustom(custom, 40)
	if err != nil {
		t.Fatalf("NewCustom returned error: %v", err)
	}
	if len(v) != 40 {
		t.Fatalf("NewCustom length = %d, want 40", len(v))
	}
	for i := 0; i < len(v); i++ {
		if strings.IndexByte(custom, v[i]) < 0 {
			t.Fatalf("NewCustom byte %q not in custom alphabet %q", v[i], custom)
		}
	}
}

func TestNewCustom_Errors(t *testing.T) {
	cases := []struct {
		name     string
		alphabet string
		size     int
	}{
		{"short alphabet", "a", 5},
		{"empty alphabet", "", 5},
		{"duplicate bytes", "aab", 5},
		{"size zero", "abc", 0},
		{"size negative", "abc", -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCustom(tc.alphabet, tc.size); err == nil {
				t.Fatalf("NewCustom(%q, %d) = nil error, want error", tc.alphabet, tc.size)
			}
		})
	}
}

func TestUUID_FormatAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a := UUID()
	b := UUID()
	if !re.MatchString(a) {
		t.Fatalf("UUID() %q does not match v4 format", a)
	}
	if !re.MatchString(b) {
		t.Fatalf("UUID() %q does not match v4 format", b)
	}
	if a == b {
		t.Fatalf("two UUID() calls returned the same value: %q", a)
	}
}

func TestNew_DistributionCoverage(t *testing.T) {
	var seen [256]bool
	for i := 0; i < 10_000; i++ {
		v := New()
		for j := 0; j < len(v); j++ {
			seen[v[j]] = true
		}
	}
	for i := 0; i < len(Alphabet); i++ {
		if !seen[Alphabet[i]] {
			t.Fatalf("Alphabet byte %q never appeared across 10k ids", Alphabet[i])
		}
	}
}

func TestAlphabet_UniqueAndSized(t *testing.T) {
	if len(Alphabet) != 52 {
		t.Fatalf("Alphabet length = %d, want 52", len(Alphabet))
	}
	var seen [256]bool
	for i := 0; i < len(Alphabet); i++ {
		if seen[Alphabet[i]] {
			t.Fatalf("Alphabet has duplicate byte %q (the fixed bug regressed)", Alphabet[i])
		}
		seen[Alphabet[i]] = true
	}
}
