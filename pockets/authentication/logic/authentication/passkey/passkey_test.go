package passkey

import (
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"  MacBook Touch ID  ", "MacBook Touch ID", true},
		{strings.Repeat("é", MaxNameRunes), strings.Repeat("é", MaxNameRunes), true},
		{strings.Repeat("a", MaxNameRunes+1), "", false},
		{"", "", false},
		{"   ", "", false},
		{"key\x00one", "", false},
		{"key\u0085one", "", false},
		{"\xff", "", false},
	} {
		got, ok := NormalizeName(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormalizeName(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDefaultName(t *testing.T) {
	if got := DefaultName(0); got != "Passkey 1" {
		t.Errorf("DefaultName(0) = %q", got)
	}
	if got := DefaultName(4); got != "Passkey 5" {
		t.Errorf("DefaultName(4) = %q", got)
	}
}
