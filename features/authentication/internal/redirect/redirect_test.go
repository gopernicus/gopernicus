package redirect

import "testing"

func TestAllowedExactMatch(t *testing.T) {
	a := New([]string{"https://app.example.com/home", ""})

	cases := []struct {
		target string
		want   bool
	}{
		{"https://app.example.com/home", true},   // exact match
		{"/", true},                              // same-origin default always allowed
		{"https://app.example.com/home/", false}, // trailing slash is a different string
		{"https://app.example.com", false},       // prefix is not a match
		{"https://evil.example.com", false},      // not listed
		{"", false},                              // blank entries are never allowlisted
	}
	for _, c := range cases {
		if got := a.Allowed(c.target); got != c.want {
			t.Errorf("Allowed(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}

func TestResolveFallsBackToSafeDefault(t *testing.T) {
	a := New([]string{"https://app.example.com/home"})

	if got := a.Resolve(""); got != "/" {
		t.Errorf("Resolve(empty) = %q, want /", got)
	}
	if got := a.Resolve("https://app.example.com/home"); got != "https://app.example.com/home" {
		t.Errorf("Resolve(allowlisted) = %q, want it honored", got)
	}
	if got := a.Resolve("https://evil.example.com"); got != "/" {
		t.Errorf("Resolve(open-redirect attempt) = %q, want the safe default /", got)
	}
}

func TestEmptyAllowlistPermitsOnlyDefault(t *testing.T) {
	a := New(nil)
	if !a.Allowed("/") {
		t.Error("empty allowlist must still allow the same-origin default")
	}
	if a.Allowed("https://app.example.com/home") {
		t.Error("empty allowlist must reject any absolute target")
	}
}
